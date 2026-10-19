// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package redpandaotelexporter

import (
	"testing"

	pb "buf.build/gen/go/redpandadata/otel/protocolbuffers/go/redpanda/otel/v1"
	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestAttributeValueToProto(t *testing.T) {
	str := func(s string) *pb.AnyValue {
		return &pb.AnyValue{Value: &pb.AnyValue_StringValue{StringValue: s}}
	}
	i64 := func(i int64) *pb.AnyValue {
		return &pb.AnyValue{Value: &pb.AnyValue_IntValue{IntValue: i}}
	}

	tests := []struct {
		name string
		in   attribute.Value
		want *pb.AnyValue
	}{
		{
			name: "empty",
			in:   attribute.Value{},
			want: &pb.AnyValue{},
		},
		{
			name: "string",
			in:   attribute.StringValue("v"),
			want: str("v"),
		},
		{
			name: "bytes",
			in:   attribute.ByteSliceValue([]byte{0x01, 0x02}),
			want: &pb.AnyValue{Value: &pb.AnyValue_BytesValue{BytesValue: []byte{0x01, 0x02}}},
		},
		{
			name: "int64 slice",
			in:   attribute.Int64SliceValue([]int64{1, 2}),
			want: &pb.AnyValue{Value: &pb.AnyValue_ArrayValue{ArrayValue: &pb.ArrayValue{
				Values: []*pb.AnyValue{i64(1), i64(2)},
			}}},
		},
		{
			name: "heterogeneous slice",
			in:   attribute.SliceValue(attribute.StringValue("a"), attribute.Int64Value(1)),
			want: &pb.AnyValue{Value: &pb.AnyValue_ArrayValue{ArrayValue: &pb.ArrayValue{
				Values: []*pb.AnyValue{str("a"), i64(1)},
			}}},
		},
		{
			name: "nested map",
			in: attribute.MapValue(
				attribute.String("k", "v"),
				attribute.Map("inner", attribute.Int64("n", 2)),
			),
			want: &pb.AnyValue{Value: &pb.AnyValue_KvlistValue{KvlistValue: &pb.KeyValueList{
				Values: []*pb.KeyValue{
					{Key: "inner", Value: &pb.AnyValue{Value: &pb.AnyValue_KvlistValue{KvlistValue: &pb.KeyValueList{
						Values: []*pb.KeyValue{{Key: "n", Value: i64(2)}},
					}}}},
					{Key: "k", Value: str("v")},
				},
			}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, attributeValueToProto(tt.in), protocmp.Transform()); diff != "" {
				t.Errorf("attributeValueToProto() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
