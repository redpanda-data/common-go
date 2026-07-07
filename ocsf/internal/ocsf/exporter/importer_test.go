// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package exporter_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/exporter"
	samplev1 "github.com/redpanda-data/common-go/ocsf/internal/ocsf/exporter/testdata/sample"
)

// fullSample builds a SampleEvent exercising every field kind the importer
// must invert: enum, int64, int32, string, bool, double, Value, repeated
// string, nested message, optional string, repeated enum.
func fullSample(t *testing.T) *samplev1.SampleEvent {
	t.Helper()
	metadata, err := structpb.NewValue(map[string]any{
		"labels": []any{"a", "b"},
		"depth":  2.5,
		"nested": map[string]any{"k": "v"},
	})
	require.NoError(t, err)
	note := "a note"
	return &samplev1.SampleEvent{
		SeverityId:  samplev1.SampleEvent_SEVERITY_ID_CRITICAL,
		Time:        1751313600000,
		Count:       42,
		MessageText: "hello",
		IsAlert:     true,
		Score:       99.5,
		Metadata:    metadata,
		Tags:        []string{"x", "y"},
		Actor:       &samplev1.Actor{Name: "alice", Uid: 9007199254740993}, // > 2^53
		Note:        &note,
		Severities: []samplev1.SampleEvent_SeverityId{
			samplev1.SampleEvent_SEVERITY_ID_CRITICAL,
			samplev1.SampleEvent_SEVERITY_ID_OTHER,
		},
		LabelsMap: map[string]string{"env": "prod", "tëam": "adp", "": "empty-key"},
		Raw:       []byte{0x00, 0xff, 0x10, 0x20},
		Ratio:     1.5, // exactly representable in float32
		Counter:   18446744073709551615,
		Checksum:  4294967295,
		Deltas:    -9007199254740993, // < -2^53
	}
}

// TestFromOCSFJSON_RoundTripProtoJSONProto proves proto → JSON → proto is
// lossless: FromOCSFJSON(ToOCSFJSON(evt)) equals evt.
func TestFromOCSFJSON_RoundTripProtoJSONProto(t *testing.T) {
	original := fullSample(t)

	b, err := exporter.ToOCSFJSON(original)
	require.NoError(t, err)

	var decoded samplev1.SampleEvent
	require.NoError(t, exporter.FromOCSFJSON(b, &decoded))
	require.True(t, proto.Equal(original, &decoded),
		"proto → JSON → proto must be lossless\njson: %s", b)
}

// TestFromOCSFJSON_RoundTripJSONProtoJSON proves JSON → proto → JSON is
// byte-identical for conformant input (ToOCSFJSON output is deterministic).
func TestFromOCSFJSON_RoundTripJSONProtoJSON(t *testing.T) {
	b, err := exporter.ToOCSFJSON(fullSample(t))
	require.NoError(t, err)

	var decoded samplev1.SampleEvent
	require.NoError(t, exporter.FromOCSFJSON(b, &decoded))

	b2, err := exporter.ToOCSFJSON(&decoded)
	require.NoError(t, err)
	require.Equal(t, string(b), string(b2), "JSON → proto → JSON must be byte-identical")
}

// TestFromOCSFJSON_IntegerEnums verifies enums decode from OCSF integer
// values, including values with no named constant (open-enum forward compat).
func TestFromOCSFJSON_IntegerEnums(t *testing.T) {
	var evt samplev1.SampleEvent
	require.NoError(t, exporter.FromOCSFJSON([]byte(`{"severity_id":5,"severities":[99,7]}`), &evt))
	require.Equal(t, samplev1.SampleEvent_SEVERITY_ID_CRITICAL, evt.SeverityId)
	require.Len(t, evt.Severities, 2)
	require.Equal(t, samplev1.SampleEvent_SEVERITY_ID_OTHER, evt.Severities[0])
	// 7 has no named constant; the raw number must be preserved.
	require.Equal(t, samplev1.SampleEvent_SeverityId(7), evt.Severities[1])
}

// TestFromOCSFJSON_Int64Precision verifies int64 decodes beyond 2^53 without
// float64 truncation, from both unquoted (OCSF) and quoted (protojson) forms.
func TestFromOCSFJSON_Int64Precision(t *testing.T) {
	const big = int64(9007199254740993) // 2^53 + 1

	var evt samplev1.SampleEvent
	require.NoError(t, exporter.FromOCSFJSON([]byte(`{"time":9007199254740993}`), &evt))
	require.Equal(t, big, evt.Time)

	var quoted samplev1.SampleEvent
	require.NoError(t, exporter.FromOCSFJSON([]byte(`{"time":"9007199254740993"}`), &quoted))
	require.Equal(t, big, quoted.Time)
}

// TestFromOCSFJSON_UnknownKeysGoToUnmapped verifies unknown top-level keys
// land in the message's unmapped-style Value field ("metadata" on SampleEvent
// has the json_t role but is named differently, so this test uses a payload
// with the field absent to check the error path, and the merge path is
// covered by the conformance suite on real classes with "unmapped").
func TestFromOCSFJSON_UnknownKeysError(t *testing.T) {
	// SampleEvent has no "unmapped" field: unknown keys must error, naming them.
	var evt samplev1.SampleEvent
	err := exporter.FromOCSFJSON([]byte(`{"count":1,"never_heard_of_it":true}`), &evt)
	require.ErrorContains(t, err, "never_heard_of_it")
	require.ErrorContains(t, err, "unmapped")
}

// TestFromOCSFJSON_NullAndAbsent verifies JSON null and absent keys both leave
// fields unset.
func TestFromOCSFJSON_NullAndAbsent(t *testing.T) {
	var evt samplev1.SampleEvent
	require.NoError(t, exporter.FromOCSFJSON([]byte(`{"note":null,"count":3}`), &evt))
	require.Nil(t, evt.Note)
	require.Equal(t, int32(3), evt.Count)
	require.Zero(t, evt.Time)
}

// TestFromOCSFJSON_ValueField verifies google.protobuf.Value fields take
// arbitrary JSON.
func TestFromOCSFJSON_ValueField(t *testing.T) {
	payload := []byte(`{"metadata":{"a":[1,2,{"b":"c"}],"d":null}}`)
	var evt samplev1.SampleEvent
	require.NoError(t, exporter.FromOCSFJSON(payload, &evt))

	got, err := evt.Metadata.MarshalJSON()
	require.NoError(t, err)

	var want, gotAny any
	require.NoError(t, json.Unmarshal([]byte(`{"a":[1,2,{"b":"c"}],"d":null}`), &want))
	require.NoError(t, json.Unmarshal(got, &gotAny))
	require.Equal(t, want, gotAny)
}

// TestFromOCSFJSON_ResetsTarget verifies the target message is reset before
// decoding, so reuse does not leak fields between events.
func TestFromOCSFJSON_ResetsTarget(t *testing.T) {
	evt := fullSample(t)
	require.NoError(t, exporter.FromOCSFJSON([]byte(`{"count":1}`), evt))
	require.Equal(t, int32(1), evt.Count)
	require.Empty(t, evt.MessageText, "previous contents must be cleared")
	require.Nil(t, evt.Actor)
}

// TestFromOCSFJSON_TypeMismatchErrors verifies a JSON type mismatch surfaces
// as an error naming the field.
func TestFromOCSFJSON_TypeMismatchErrors(t *testing.T) {
	var evt samplev1.SampleEvent
	err := exporter.FromOCSFJSON([]byte(`{"count":"not-a-number"}`), &evt)
	require.ErrorContains(t, err, `field "count"`)

	err = exporter.FromOCSFJSON([]byte(`{"tags":"not-an-array"}`), &evt)
	require.ErrorContains(t, err, `field "tags"`)

	err = exporter.FromOCSFJSON([]byte(`{"actor":[1,2]}`), &evt)
	require.ErrorContains(t, err, `field "actor"`)
}

// TestFromOCSFJSON_EmptyObject verifies {} decodes to the zero message.
func TestFromOCSFJSON_EmptyObject(t *testing.T) {
	var evt samplev1.SampleEvent
	require.NoError(t, exporter.FromOCSFJSON([]byte(`{}`), &evt))
	require.True(t, proto.Equal(&samplev1.SampleEvent{}, &evt))
}
