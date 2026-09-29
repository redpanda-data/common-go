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
	"fmt"
)

const (
	defaultMinBytes   = 64 << 10
	defaultMaxEntries = 4
	defaultMaxBytes   = 64 << 20
)

type config struct {
	level      int
	minBytes   int
	maxEntries int
	maxBytes   int64
}

// Option configures a Cache.
type Option func(*config)

// WithLevel sets the gzip compression level. Defaults to
// gzip.DefaultCompression. Since each payload is compressed once, a higher
// level costs little.
func WithLevel(level int) Option { return func(c *config) { c.level = level } }

// WithMinBytes sets the smallest payload that goes through the cache. Smaller
// payloads are compressed on every send. Defaults to 64 KiB.
func WithMinBytes(n int) Option { return func(c *config) { c.minBytes = n } }

// WithMaxEntries caps the number of cached payloads. Defaults to 4.
func WithMaxEntries(n int) Option { return func(c *config) { c.maxEntries = n } }

// WithMaxBytes caps the input plus compressed bytes the cache holds. Larger
// payloads are compressed on every send. Defaults to 64 MiB.
func WithMaxBytes(n int64) Option { return func(c *config) { c.maxBytes = n } }

func defaultConfig() config {
	return config{
		level:      gzip.DefaultCompression,
		minBytes:   defaultMinBytes,
		maxEntries: defaultMaxEntries,
		maxBytes:   defaultMaxBytes,
	}
}

func (c config) validate() error {
	switch {
	case c.level < gzip.HuffmanOnly || c.level > gzip.BestCompression:
		return fmt.Errorf("gzipcache: invalid compression level %d", c.level)
	case c.minBytes < 0:
		return fmt.Errorf("gzipcache: MinBytes must not be negative, got %d", c.minBytes)
	case c.maxEntries < 1:
		return fmt.Errorf("gzipcache: MaxEntries must be positive, got %d", c.maxEntries)
	case c.maxBytes < 0:
		return fmt.Errorf("gzipcache: MaxBytes must not be negative, got %d", c.maxBytes)
	}
	return nil
}
