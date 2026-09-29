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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func FuzzRoundTrip(f *testing.F) {
	f.Add([]byte(nil), uint(0))
	f.Add([]byte("hello"), uint(2))
	f.Add(payload(4096, 'a'), uint(1000))
	f.Fuzz(func(t *testing.T, data []byte, split uint) {
		c, err := New(WithMinBytes(0))
		require.NoError(t, err)
		i := int(split % uint(len(data)+1))
		for range 2 { // miss, then hit
			assert.Equal(t, string(data), string(gunzip(t, send(t, c, data))))
		}
		assert.Equal(t, string(data), string(gunzip(t, send(t, c, data[:i], data[i:]))))
	})
}
