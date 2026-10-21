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

package log

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestConvertValue(t *testing.T) {
	for name, tc := range map[string]struct {
		in   any
		want attribute.Value
	}{
		"bool":        {in: true, want: attribute.BoolValue(true)},
		"string":      {in: "s", want: attribute.StringValue("s")},
		"int":         {in: 42, want: attribute.Int64Value(42)},
		"uint8":       {in: uint8(7), want: attribute.Int64Value(7)},
		"uint64 max":  {in: uint64(math.MaxUint64), want: attribute.StringValue("18446744073709551615")},
		"float32":     {in: float32(1.5), want: attribute.Float64Value(1.5)},
		"duration":    {in: time.Second, want: attribute.Int64Value(int64(time.Second))},
		"bytes":       {in: []byte("ab"), want: attribute.ByteSliceValue([]byte("ab"))},
		"error":       {in: errors.New("boom"), want: attribute.StringValue("boom")},
		"complex":     {in: complex(1, 2), want: attribute.MapValue(attribute.Float64("r", 1), attribute.Float64("i", 2))},
		"slice":       {in: []int{1, 2}, want: attribute.SliceValue(attribute.Int64Value(1), attribute.Int64Value(2))},
		"map":         {in: map[string]int{"a": 1}, want: attribute.MapValue(attribute.Int64("a", 1))},
		"nil":         {in: nil, want: attribute.Value{}},
		"nil pointer": {in: (*int)(nil), want: attribute.Value{}},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, convertValue(tc.in))
		})
	}
}

func TestConvertKVs(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "v")

	gotCtx, kvs := convertKVs(context.Background(), "a", 1, 2, "b", "ctx", ctx, "odd")

	require.Equal(t, ctx, gotCtx)
	require.Equal(t, []attribute.KeyValue{
		attribute.Int64("a", 1),
		attribute.String("2", "b"),
		{Key: "odd"},
	}, kvs)
}
