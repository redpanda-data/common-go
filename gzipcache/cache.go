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
	"container/list"
	"errors"
	"fmt"
	"hash/maphash"
	"io"
	"sync"
	"sync/atomic"
)

// Cache holds compressed frames keyed by payload content. It is safe for
// concurrent use; share one Cache across every handler that sends the same
// payloads.
type Cache struct {
	cfg    config
	gzPool sync.Pool

	// Test hooks.
	hash     func([]byte) uint64
	compress func([]byte) ([]byte, error)

	mu      sync.Mutex
	entries map[key]*entry
	lru     list.List // of *entry, most recently used first
	bytes   int64

	hits, waits, misses, bypasses, fallbacks, evictions atomic.Uint64
}

// Stats is a snapshot of cache counters.
type Stats struct {
	Hits      uint64 // sends served from a ready entry
	Waits     uint64 // sends served after waiting on another send's compression
	Misses    uint64 // sends that compressed and populated an entry
	Bypasses  uint64 // sends outside [MinBytes, MaxBytes], compressed per send
	Fallbacks uint64 // collisions, failed leaders and multi-write sends, compressed per send
	Evictions uint64
	Entries   int
	Bytes     int64 // input plus compressed bytes held
}

type key struct {
	sum uint64
	n   int
}

// entry is immutable once done is closed.
type entry struct {
	key   key
	elem  *list.Element
	ready bool // guarded by Cache.mu
	done  chan struct{}
	input []byte
	gz    []byte
	err   error
}

func (e *entry) size() int64 { return int64(e.key.n + len(e.gz)) }

var errLeaderPanicked = errors.New("gzipcache: compression panicked")

// New returns a Cache configured by opts.
func New(opts ...Option) (*Cache, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	c := &Cache{cfg: cfg, entries: make(map[key]*entry)}
	seed := maphash.MakeSeed()
	c.hash = func(p []byte) uint64 { return maphash.Bytes(seed, p) }
	c.compress = c.gzipBytes
	return c, nil
}

// Stats returns the current counters.
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	entries, held := len(c.entries), c.bytes
	c.mu.Unlock()
	return Stats{
		Hits:      c.hits.Load(),
		Waits:     c.waits.Load(),
		Misses:    c.misses.Load(),
		Bypasses:  c.bypasses.Load(),
		Fallbacks: c.fallbacks.Load(),
		Evictions: c.evictions.Load(),
		Entries:   entries,
		Bytes:     held,
	}
}

func (c *Cache) getWriter(w io.Writer) *gzip.Writer {
	if gw, ok := c.gzPool.Get().(*gzip.Writer); ok {
		gw.Reset(w)
		return gw
	}
	gw, _ := gzip.NewWriterLevel(w, c.cfg.level) // level validated by New
	return gw
}

func (c *Cache) putWriter(gw *gzip.Writer) {
	gw.Reset(io.Discard)
	c.gzPool.Put(gw)
}

func (c *Cache) gzipBytes(p []byte) ([]byte, error) {
	var buf bytes.Buffer
	gw := c.getWriter(&buf)
	defer c.putWriter(gw)
	if _, err := gw.Write(p); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return bytes.Clone(buf.Bytes()), nil
}

func (c *Cache) cacheable(n int) bool {
	return n >= c.cfg.minBytes && int64(n) <= c.cfg.maxBytes
}

// acquire returns a completed entry whose input equals p, or nil when the
// caller must compress p itself.
func (c *Cache) acquire(p []byte) *entry {
	k := key{sum: c.hash(p), n: len(p)}

	c.mu.Lock()
	if e, ok := c.entries[k]; ok {
		c.lru.MoveToFront(e.elem)
		c.mu.Unlock()
		waited := false
		select {
		case <-e.done:
		default:
			waited = true
			<-e.done
		}
		if e.err != nil || !bytes.Equal(e.input, p) {
			c.fallbacks.Add(1)
			return nil
		}
		if waited {
			c.waits.Add(1)
		} else {
			c.hits.Add(1)
		}
		return e
	}
	e := &entry{key: k, done: make(chan struct{})}
	e.elem = c.lru.PushFront(e)
	c.entries[k] = e
	c.bytes += e.size()
	c.evictLocked()
	c.mu.Unlock()

	c.fill(e, p)
	if e.err != nil {
		c.fallbacks.Add(1)
		return nil
	}
	c.misses.Add(1)
	return e
}

// fill compresses p into e and releases its waiters, also when compression
// panics.
func (c *Cache) fill(e *entry, p []byte) {
	defer func() {
		r := recover()
		if r != nil {
			e.err = fmt.Errorf("%w: %v", errLeaderPanicked, r)
		}
		c.finish(e)
		close(e.done)
		if r != nil {
			panic(r)
		}
	}()
	e.input = bytes.Clone(p)
	e.gz, e.err = c.compress(p)
}

func (c *Cache) finish(e *entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e.err != nil {
		e.gz = nil
		c.removeLocked(e)
		return
	}
	e.ready = true
	c.bytes += int64(len(e.gz))
	c.evictLocked()
}

func (c *Cache) evictLocked() {
	for el := c.lru.Back(); el != nil && (len(c.entries) > c.cfg.maxEntries || c.bytes > c.cfg.maxBytes); {
		prev := el.Prev()
		if e, ok := el.Value.(*entry); ok && e.ready {
			c.removeLocked(e)
			c.evictions.Add(1)
		}
		el = prev
	}
}

func (c *Cache) removeLocked(e *entry) {
	if c.entries[e.key] != e {
		return
	}
	c.lru.Remove(e.elem)
	delete(c.entries, e.key)
	c.bytes -= e.size()
}
