// Copyright 2026 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gzipcache

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// payload returns n compressible bytes whose first byte is tag, so payloads
// of equal length differ by tag.
func payload(n int, tag byte) []byte {
	line := []byte("permit(principal in Group::\"g\", action == Action::\"read\", resource);\n")
	b := bytes.Repeat(line, n/len(line)+1)[:n]
	if n > 0 {
		b[0] = tag
	}
	return b
}

// send drives a compressor the way connect does and returns the frame.
func send(t *testing.T, c *Cache, writes ...[]byte) []byte {
	t.Helper()
	var dst bytes.Buffer
	z := c.newCompressor()
	z.Reset(&dst)
	for _, w := range writes {
		_, err := z.Write(w)
		require.NoError(t, err)
	}
	require.NoError(t, z.Close())
	return dst.Bytes()
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(b))
	require.NoError(t, err)
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return out
}

func TestSendRoundTrip(t *testing.T) {
	const minBytes, maxBytes = 1 << 10, 1 << 20
	tests := []struct {
		name  string
		input []byte
		want  Stats
	}{
		{name: "empty", want: Stats{}},
		{name: "below min bytes", input: payload(minBytes-1, 'a'), want: Stats{Bypasses: 1}},
		{name: "exactly min bytes", input: payload(minBytes, 'a'), want: Stats{Misses: 1, Entries: 1}},
		// Input plus frame exceed MaxBytes, so the entry is evicted on completion.
		{name: "exactly max bytes", input: payload(maxBytes, 'a'), want: Stats{Misses: 1, Evictions: 1}},
		{name: "above max bytes", input: payload(maxBytes+1, 'a'), want: Stats{Bypasses: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(WithMinBytes(minBytes), WithMaxBytes(maxBytes))
			require.NoError(t, err)
			var writes [][]byte
			if tt.input != nil {
				writes = append(writes, tt.input)
			}
			assert.Equal(t, string(tt.input), string(gunzip(t, send(t, c, writes...))))
			got := c.Stats()
			got.Bytes = 0
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHitServesCachedFrame(t *testing.T) {
	c, err := New(WithMinBytes(0))
	require.NoError(t, err)
	p := payload(256<<10, 'a')
	first := send(t, c, p)
	second := send(t, c, p)
	assert.Equal(t, first, second)
	assert.Equal(t, p, gunzip(t, second))
	s := c.Stats()
	assert.Equal(t, uint64(1), s.Misses)
	assert.Equal(t, uint64(1), s.Hits)
	assert.Equal(t, int64(len(p)+len(first)), s.Bytes)
}

// connect recycles the buffer it passes to Write, so the cache must keep its
// own copy of the payload.
func TestCacheOwnsItsCopy(t *testing.T) {
	c, err := New(WithMinBytes(0))
	require.NoError(t, err)
	p := payload(64<<10, 'a')
	want := bytes.Clone(p)
	send(t, c, p)
	for i := range p {
		p[i] = 'x'
	}
	assert.Equal(t, want, gunzip(t, send(t, c, want)))
	assert.Equal(t, uint64(1), c.Stats().Hits)
}

func TestSingleFlight(t *testing.T) {
	c, err := New(WithMinBytes(0))
	require.NoError(t, err)
	p := payload(1<<20, 'a')
	const senders = 64
	outs := make([][]byte, senders)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range senders {
		wg.Go(func() {
			<-start
			outs[i] = send(t, c, p)
		})
	}
	close(start)
	wg.Wait()
	for _, out := range outs {
		assert.Equal(t, outs[0], out)
	}
	s := c.Stats()
	assert.Equal(t, uint64(1), s.Misses)
	assert.Equal(t, uint64(senders-1), s.Hits+s.Waits)
}

func TestLeaderFailure(t *testing.T) {
	tests := []struct {
		name      string
		compress  func([]byte) ([]byte, error)
		wantPanic bool
	}{
		{name: "error", compress: func([]byte) ([]byte, error) { return nil, errors.New("boom") }},
		{name: "panic", compress: func([]byte) ([]byte, error) { panic("boom") }, wantPanic: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(WithMinBytes(0))
			require.NoError(t, err)
			c.compress = tt.compress
			p := payload(64<<10, 'a')
			if tt.wantPanic {
				assert.PanicsWithValue(t, "boom", func() { send(t, c, p) })
			} else {
				assert.Equal(t, p, gunzip(t, send(t, c, p)))
			}
			s := c.Stats()
			assert.Equal(t, 0, s.Entries)
			assert.Equal(t, int64(0), s.Bytes)
		})
	}
}

func TestWaiterOnFailedLeader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, err := New(WithMinBytes(0))
		require.NoError(t, err)
		release := make(chan struct{})
		var calls int
		c.compress = func([]byte) ([]byte, error) {
			calls++
			<-release
			return nil, errors.New("boom")
		}
		p := payload(64<<10, 'a')
		var leader, waiter []byte
		var wg sync.WaitGroup
		wg.Go(func() { leader = send(t, c, p) })
		synctest.Wait() // leader is inside compress
		wg.Go(func() { waiter = send(t, c, p) })
		synctest.Wait() // waiter is blocked on the pending entry
		close(release)
		wg.Wait()

		assert.Equal(t, 1, calls)
		assert.Equal(t, p, gunzip(t, leader))
		assert.Equal(t, p, gunzip(t, waiter))
		assert.Equal(t, Stats{Fallbacks: 2}, c.Stats())
	})
}

func TestCollisionFallsBack(t *testing.T) {
	c, err := New(WithMinBytes(0))
	require.NoError(t, err)
	c.hash = func([]byte) uint64 { return 42 }
	a, b := payload(64<<10, 'a'), payload(64<<10, 'b')
	send(t, c, a)
	assert.Equal(t, b, gunzip(t, send(t, c, b)))
	assert.Equal(t, a, gunzip(t, send(t, c, a)))
	s := c.Stats()
	assert.Equal(t, Stats{Hits: 1, Misses: 1, Fallbacks: 1, Entries: 1, Bytes: s.Bytes}, s)
}

func TestMultiWrite(t *testing.T) {
	c, err := New(WithMinBytes(0))
	require.NoError(t, err)
	p := payload(128<<10, 'a')
	assert.Equal(t, p, gunzip(t, send(t, c, p[:1000], p[1000:]))) // miss on p[:1000], then fallback
	assert.Equal(t, p, gunzip(t, send(t, c, p[:1000], p[1000:]))) // hit on p[:1000], then fallback
	assert.Equal(t, p, gunzip(t, send(t, c, p[:1000], nil, p[1000:5000], p[5000:])))
	s := c.Stats()
	assert.Equal(t, uint64(1), s.Misses)
	assert.Equal(t, uint64(2), s.Hits)
	assert.Equal(t, uint64(3), s.Fallbacks)
}

func TestResetAfterAbandonedSend(t *testing.T) {
	tests := []struct {
		name     string
		minBytes int
	}{
		{name: "plain gzip stream", minBytes: 1 << 20}, // abandoned write bypasses the cache
		{name: "cached frame", minBytes: 0},            // abandoned write holds an entry
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(WithMinBytes(tt.minBytes))
			require.NoError(t, err)
			z := c.newCompressor()
			var abandoned bytes.Buffer
			z.Reset(&abandoned)
			_, err = z.Write(payload(1000, 'a')) // never closed
			require.NoError(t, err)

			abandonedLen := abandoned.Len()
			var dst bytes.Buffer
			z.Reset(&dst)
			p := payload(2000, 'b')
			_, err = z.Write(p)
			require.NoError(t, err)
			require.NoError(t, z.Close())
			require.NoError(t, z.Close())
			assert.Equal(t, p, gunzip(t, dst.Bytes()))
			assert.Equal(t, abandonedLen, abandoned.Len(), "abandoned sink written after Reset")
		})
	}
}

func TestEvictsByEntries(t *testing.T) {
	c, err := New(WithMinBytes(0), WithMaxEntries(2))
	require.NoError(t, err)
	a, b, d := payload(64<<10, 'a'), payload(64<<10, 'b'), payload(64<<10, 'd')
	send(t, c, a)
	send(t, c, b)
	send(t, c, a) // a is now the most recent
	send(t, c, d) // evicts b
	send(t, c, a)
	send(t, c, b) // evicts d
	s := c.Stats()
	assert.Equal(t, uint64(4), s.Misses)
	assert.Equal(t, uint64(2), s.Hits)
	assert.Equal(t, uint64(2), s.Evictions)
	assert.Equal(t, 2, s.Entries)
	assert.Equal(t, a, gunzip(t, send(t, c, a)))
	assert.Equal(t, uint64(3), c.Stats().Hits)
}

func TestEvictsByBytes(t *testing.T) {
	a, b := payload(64<<10, 'a'), payload(64<<10, 'b')
	maxBytes := int64(len(a)) + 32<<10
	c, err := New(WithMinBytes(0), WithMaxBytes(maxBytes))
	require.NoError(t, err)
	send(t, c, a)
	send(t, c, b)
	s := c.Stats()
	assert.Equal(t, 1, s.Entries)
	assert.Equal(t, uint64(1), s.Evictions)
	assert.LessOrEqual(t, s.Bytes, maxBytes)
	assert.Equal(t, b, gunzip(t, send(t, c, b)))
	assert.Equal(t, uint64(1), c.Stats().Hits)
}
