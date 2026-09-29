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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		opts    []Option
		wantErr string
	}{
		{name: "defaults"},
		{name: "best compression", opts: []Option{WithLevel(gzip.BestCompression)}},
		{name: "huffman only", opts: []Option{WithLevel(gzip.HuffmanOnly)}},
		{name: "min bytes zero", opts: []Option{WithMinBytes(0)}},
		{name: "level too high", opts: []Option{WithLevel(10)}, wantErr: "gzipcache: invalid compression level 10"},
		{name: "level too low", opts: []Option{WithLevel(-3)}, wantErr: "gzipcache: invalid compression level -3"},
		{name: "negative min bytes", opts: []Option{WithMinBytes(-1)}, wantErr: "gzipcache: MinBytes must not be negative, got -1"},
		{name: "zero max entries", opts: []Option{WithMaxEntries(0)}, wantErr: "gzipcache: MaxEntries must be positive, got 0"},
		{name: "negative max bytes", opts: []Option{WithMaxBytes(-1)}, wantErr: "gzipcache: MaxBytes must not be negative, got -1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			for _, o := range tt.opts {
				o(&cfg)
			}
			err := cfg.validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}
