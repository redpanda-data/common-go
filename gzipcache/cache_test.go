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
	"io"
	"testing"

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
