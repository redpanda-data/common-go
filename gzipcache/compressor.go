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
	"compress/gzip"
	"io"

	"connectrpc.com/connect"
)

// compressor is the per-send connect.Compressor. Exactly one of e and gw is
// set once the first non-empty Write has run.
type compressor struct {
	c      *Cache
	dst    io.Writer
	e      *entry       // cached frame for the first Write
	gw     *gzip.Writer // plain gzip streaming to dst
	closed bool
}

func (c *Cache) newCompressor() connect.Compressor {
	return &compressor{c: c, dst: io.Discard}
}

func (z *compressor) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if z.gw != nil {
		return z.gw.Write(p)
	}
	if z.e != nil {
		// The cached frame covers only the first Write. Its input equals
		// that Write, so replay it into a plain stream.
		z.c.fallbacks.Add(1)
		first := z.e.input
		z.e = nil
		z.gw = z.c.getWriter(z.dst)
		if _, err := z.gw.Write(first); err != nil {
			return 0, err
		}
		return z.gw.Write(p)
	}
	if !z.c.cacheable(len(p)) {
		z.c.bypasses.Add(1)
	} else if z.e = z.c.acquire(p); z.e != nil {
		return len(p), nil
	}
	z.gw = z.c.getWriter(z.dst)
	return z.gw.Write(p)
}

func (z *compressor) Close() error {
	if z.closed {
		return nil
	}
	z.closed = true
	switch {
	case z.e != nil:
		_, err := z.dst.Write(z.e.gz)
		z.e = nil
		return err
	case z.gw == nil:
		// No payload: emit an empty gzip stream.
		z.gw = z.c.getWriter(z.dst)
	}
	err := z.gw.Close()
	z.c.putWriter(z.gw)
	z.gw = nil
	return err
}

func (z *compressor) Reset(w io.Writer) {
	if z.gw != nil {
		z.c.putWriter(z.gw)
	}
	*z = compressor{c: z.c, dst: w}
}
