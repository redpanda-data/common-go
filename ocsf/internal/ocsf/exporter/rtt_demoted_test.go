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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/exporter"
	samplev1 "github.com/redpanda-data/common-go/ocsf/internal/ocsf/exporter/testdata/sample"
)

// The --iceberg-compat generator demotes un-representable structural fields to
// proto3 strings holding the value's OCSF JSON. These tests are the
// round-trip-through-time (RTT) matrix proving that a demotion-aware
// exporter/importer re-inlines those strings on export and re-stringifies them
// on import, so an iceberg-compat message round-trips to spec OCSF JSON
// losslessly in BOTH directions.
//
// SampleEvent stands in for a demoted OCSF message: message_text (singular
// string), tags (repeated string) and note (optional string) play the role of
// demoted fields carrying JSON; the remaining typed fields (enum, int64, nested
// message, a real google.protobuf.Value) verify demotion coexists with normal
// marshaling.

const sampleFullName = "ocsf.exporter.sample.v1.SampleEvent"

// demoted is the demoted-field set for SampleEvent: message_text, tags, note
// carry JSON text.
var demoted = exporter.DemotedFields{
	sampleFullName: {"message_text": true, "tags": true, "note": true},
}

// jsonEqual asserts two JSON byte slices are semantically equal (key order and
// insignificant whitespace ignored).
func jsonEqual(t *testing.T, want, got []byte) {
	t.Helper()
	var w, g any
	require.NoError(t, json.Unmarshal(want, &w), "want is not JSON: %s", want)
	require.NoError(t, json.Unmarshal(got, &g), "got is not JSON: %s", got)
	require.Equal(t, w, g, "JSON differs\nwant: %s\ngot:  %s", want, got)
}

// TestRTT_ProtoJSONProto proves proto → OCSF JSON → proto is the identity for
// an iceberg-compat message across a matrix of demoted-payload shapes. The
// demoted strings hold COMPACT JSON (what the producer writes), so the compact
// re-stringify on import reproduces them byte-for-byte.
func TestRTT_ProtoJSONProto(t *testing.T) {
	cases := []struct {
		name string
		msg  *samplev1.SampleEvent
	}{
		{
			name: "singular nested object",
			msg: &samplev1.SampleEvent{
				SeverityId:  samplev1.SampleEvent_SEVERITY_ID_CRITICAL,
				Time:        1685403212834,
				MessageText: `{"team":"platform","tags":{"itar":true,"levels":[1,2,3]}}`,
			},
		},
		{
			name: "demoted holds a JSON array",
			msg:  &samplev1.SampleEvent{MessageText: `[{"a":1},{"b":2},[3,4]]`},
		},
		{
			name: "demoted holds JSON scalars",
			msg:  &samplev1.SampleEvent{MessageText: `42`, Note: proto.String(`"just a string"`)},
		},
		{
			name: "demoted holds JSON null",
			msg:  &samplev1.SampleEvent{MessageText: `null`},
		},
		{
			name: "repeated demoted: array of JSON objects",
			msg: &samplev1.SampleEvent{
				Tags: []string{`{"k":"v1"}`, `{"k":"v2","n":[1,2]}`, `"scalar"`, `null`},
			},
		},
		{
			name: "unicode and escapes in the payload",
			msg:  &samplev1.SampleEvent{MessageText: `{"name":"Ada \"Lovelace\"","emoji":"🧮","path":"a\\b\\c","nl":"x\ny"}`},
		},
		{
			name: "deeply nested payload",
			msg:  &samplev1.SampleEvent{MessageText: deepJSON(40)},
		},
		{
			name: "demotion coexists with typed fields + a real Value",
			msg: &samplev1.SampleEvent{
				SeverityId:  samplev1.SampleEvent_SEVERITY_ID_OTHER,
				Time:        99,
				Count:       7,
				MessageText: `{"why":"deny"}`,
				IsAlert:     true,
				Tags:        []string{`{"policy":"p1"}`},
				Actor:       &samplev1.Actor{Name: "dev@local.com", Uid: 5},
				Metadata:    mustStruct(t, `{"real":"value","structured":[1,2,3]}`),
				Note:        proto.String(`{"n":1}`),
			},
		},
		{
			name: "absent demoted fields stay unset",
			msg:  &samplev1.SampleEvent{SeverityId: samplev1.SampleEvent_SEVERITY_ID_CRITICAL, Time: 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := exporter.ToOCSFJSONDemoted(tc.msg, demoted)
			require.NoError(t, err)
			require.True(t, json.Valid(b), "export is not valid JSON: %s", b)

			got := &samplev1.SampleEvent{}
			require.NoError(t, exporter.FromOCSFJSONDemoted(b, got, demoted))
			require.True(t, proto.Equal(tc.msg, got),
				"proto → JSON → proto not identity\njson: %s\nwant: %v\ngot:  %v", b, tc.msg, got)
		})
	}
}

// TestRTT_JSONProtoJSON proves OCSF JSON → proto → OCSF JSON is the identity on
// compact spec JSON, i.e. the demoted fields are re-inlined as native JSON, not
// re-emitted as quoted strings.
func TestRTT_JSONProtoJSON(t *testing.T) {
	inputs := []string{
		`{"severity_id":5,"time":1685403212834,"message_text":{"team":"platform","n":[1,2]}}`,
		`{"message_text":[{"a":1},{"b":2}]}`,
		`{"tags":[{"k":"v1"},{"k":"v2"}],"note":{"x":true}}`,
		`{"message_text":{"deep":{"deeper":{"deepest":{"v":1}}}},"count":3}`,
		`{"message_text":{"u":"🧮","q":"a\"b","bs":"a\\b"}}`,
	}
	for _, in := range inputs {
		t.Run(in[:min(len(in), 40)], func(t *testing.T) {
			msg := &samplev1.SampleEvent{}
			require.NoError(t, exporter.FromOCSFJSONDemoted([]byte(in), msg, demoted))
			out, err := exporter.ToOCSFJSONDemoted(msg, demoted)
			require.NoError(t, err)
			jsonEqual(t, []byte(in), out)
		})
	}
}

// TestRTT_DemotedIsNativeJSON pins that a demoted string is emitted as native
// JSON at its key, NOT as a quoted string — the whole point of demotion-aware
// export.
func TestRTT_DemotedIsNativeJSON(t *testing.T) {
	msg := &samplev1.SampleEvent{MessageText: `{"x":1}`}

	demotedOut, err := exporter.ToOCSFJSONDemoted(msg, demoted)
	require.NoError(t, err)
	require.Contains(t, string(demotedOut), `"message_text":{"x":1}`, "demoted field must inline as an object")
	require.NotContains(t, string(demotedOut), `"message_text":"{`, "demoted field must not be a quoted string")

	// Without the demoted set, the same field is a plain (quoted) string —
	// proving the behavior is opt-in and localized.
	plainOut, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)
	require.Contains(t, string(plainOut), `"message_text":"{\"x\":1}"`, "non-demoted export must quote the string")
}

// TestRTT_WhitespaceCompaction verifies that JSON with insignificant whitespace
// is compacted into the demoted string on import, so a second round trip is
// byte-stable (idempotent after the first import).
func TestRTT_WhitespaceCompaction(t *testing.T) {
	spaced := `{"message_text":  { "a" : 1 ,  "b" : [ 1, 2 ] } }`
	msg := &samplev1.SampleEvent{}
	require.NoError(t, exporter.FromOCSFJSONDemoted([]byte(spaced), msg, demoted))
	require.Equal(t, `{"a":1,"b":[1,2]}`, msg.MessageText, "import must compact the demoted JSON")

	out, err := exporter.ToOCSFJSONDemoted(msg, demoted)
	require.NoError(t, err)

	msg2 := &samplev1.SampleEvent{}
	require.NoError(t, exporter.FromOCSFJSONDemoted(out, msg2, demoted))
	require.True(t, proto.Equal(msg, msg2), "second round trip must be byte-stable")
}

// TestRTT_Deterministic pins that export is deterministic.
func TestRTT_Deterministic(t *testing.T) {
	msg := &samplev1.SampleEvent{
		SeverityId:  samplev1.SampleEvent_SEVERITY_ID_CRITICAL,
		MessageText: `{"b":2,"a":1}`,
		Tags:        []string{`{"z":1}`, `{"y":2}`},
	}
	first, err := exporter.ToOCSFJSONDemoted(msg, demoted)
	require.NoError(t, err)
	for range 20 {
		again, err := exporter.ToOCSFJSONDemoted(msg, demoted)
		require.NoError(t, err)
		require.Equal(t, first, again)
	}
}

// TestRTT_InvalidDemotedJSON pins that a demoted string that is not valid JSON
// is a loud export error, not silent corruption.
func TestRTT_InvalidDemotedJSON(t *testing.T) {
	msg := &samplev1.SampleEvent{MessageText: `{not valid json`}
	_, err := exporter.ToOCSFJSONDemoted(msg, demoted)
	require.ErrorContains(t, err, "valid JSON")
}

// TestRTT_NilDemotedUnchanged pins that a nil demoted set is exactly the
// original behavior (demotion is purely additive).
func TestRTT_NilDemotedUnchanged(t *testing.T) {
	msg := &samplev1.SampleEvent{MessageText: `{"x":1}`, Time: 5}
	withNil, err := exporter.ToOCSFJSONDemoted(msg, nil)
	require.NoError(t, err)
	plain, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)
	require.Equal(t, plain, withNil)
}

func deepJSON(depth int) string {
	var sb strings.Builder
	for range depth {
		sb.WriteString(`{"n":`)
	}
	sb.WriteString(`1`)
	for range depth {
		sb.WriteString(`}`)
	}
	return sb.String()
}

func mustStruct(t *testing.T, js string) *structpb.Value {
	t.Helper()
	v := &structpb.Value{}
	require.NoError(t, v.UnmarshalJSON([]byte(js)))
	return v
}
