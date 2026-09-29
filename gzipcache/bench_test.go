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
	"encoding/binary"
	"fmt"
	"io"
	"math/rand/v2"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// subscribers matches the policy-materializer fanout this package was built for.
const subscribers = 52

// cedarPayload returns n bytes of Cedar-like policy text.
func cedarPayload(n int) []byte {
	rng := rand.New(rand.NewPCG(1, 2))
	var b bytes.Buffer
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "@id(\"policy-%d\")\npermit(principal == User::\"u%d\", action in [Action::\"agent_%d\"], resource in Agent::\"ag%d\") when { resource has tags && resource.tags.hasTag(\"team-%d\") };\n",
			i, rng.IntN(5000), rng.IntN(40), rng.IntN(800), rng.IntN(30))
	}
	return b.Bytes()[:n]
}

// BenchmarkFanout compresses one payload for every subscriber per iteration.
// Each iteration uses a new payload, so gzipcache pays one miss and serves
// the other sends from the cache.
func BenchmarkFanout(b *testing.B) {
	base := cedarPayload(2200 << 10)

	b.Run("stock", func(b *testing.B) {
		pool := sync.Pool{New: func() any {
			w, err := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
			require.NoError(b, err)
			return w
		}}
		fanout(b, base, func(dst *bytes.Buffer, p []byte) {
			gw := pool.Get().(*gzip.Writer)
			defer pool.Put(gw)
			gw.Reset(dst)
			_, err := gw.Write(p)
			assert.NoError(b, err)
			assert.NoError(b, gw.Close())
		})
	})

	b.Run("gzipcache", func(b *testing.B) {
		c, err := New()
		require.NoError(b, err)
		pool := sync.Pool{New: func() any { return c.newCompressor() }}
		fanout(b, base, func(dst *bytes.Buffer, p []byte) {
			z := pool.Get().(connect.Compressor)
			defer pool.Put(z)
			z.Reset(dst)
			_, err := z.Write(p)
			assert.NoError(b, err)
			assert.NoError(b, z.Close())
			z.Reset(io.Discard)
		})
	})
}

func fanout(b *testing.B, p []byte, compress func(*bytes.Buffer, []byte)) {
	b.SetBytes(int64(len(p)) * subscribers)
	b.ReportAllocs()
	var iter uint64
	for b.Loop() {
		iter++
		binary.LittleEndian.PutUint64(p, iter)
		var wg sync.WaitGroup
		for range subscribers {
			wg.Go(func() {
				var dst bytes.Buffer
				compress(&dst, p)
			})
		}
		wg.Wait()
	}
}
