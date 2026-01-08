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

package kvstore

import (
	"context"
	"iter"
)

// QueryOptions configures queries.
type QueryOptions struct {
	Limit int
}

// KeyConstraint defines bounds for a key range scan.
type KeyConstraint struct {
	Start []byte // inclusive, nil = beginning
	End   []byte // exclusive, nil = end
}

// Prefix creates a KeyConstraint that matches all keys with the given prefix.
func Prefix(p string) KeyConstraint {
	start := []byte(p)
	if len(start) == 0 {
		return KeyConstraint{}
	}
	// Calculate end key by incrementing the last byte
	// e.g., "user:" -> "user;" (0x3a + 1 = 0x3b)
	end := make([]byte, len(start))
	copy(end, start)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] < 0xff {
			end[i]++
			return KeyConstraint{Start: start, End: end}
		}
		end = end[:i] // truncate if 0xff, continue to next byte
	}
	// All bytes were 0xff, no upper bound needed
	return KeyConstraint{Start: start}
}

// Storage is the interface for key-value storage backends.
// All methods accept a context to support cancellation and timeouts,
// particularly important for remote storage implementations.
type Storage interface {
	Get(ctx context.Context, key []byte) ([]byte, error)
	Set(ctx context.Context, key []byte, value []byte) error
	Delete(ctx context.Context, key []byte) error
	Range(ctx context.Context, c KeyConstraint, opts QueryOptions) iter.Seq2[KeyValue, error]
}

// BatchStorage is an optional interface for storage backends that support
// batch operations. Implementations can provide this for better performance
// during bulk inserts (e.g., bootstrap/restore).
type BatchStorage interface {
	Storage
	// ApplyBatch applies a batch of set and delete operations atomically.
	// For each item: nil Value means delete, non-nil means set.
	ApplyBatch(ctx context.Context, items []KeyValue) error
}
