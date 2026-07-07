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
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/exporter"
	samplev1 "github.com/redpanda-data/common-go/ocsf/internal/ocsf/exporter/testdata/sample"
)

// sampleMsg builds a fully-populated SampleEvent for use in tests.
func sampleMsg(t *testing.T) *samplev1.SampleEvent {
	t.Helper()

	note := "some note"
	metaVal, err := structpb.NewValue(map[string]any{
		"src_ip": "192.0.2.1",
		"port":   float64(443),
	})
	require.NoError(t, err)

	return &samplev1.SampleEvent{
		SeverityId:  samplev1.SampleEvent_SEVERITY_ID_CRITICAL, // 5
		Time:        1685403212834,
		Count:       42,
		MessageText: "login failed",
		IsAlert:     true,
		Score:       3.14,
		Metadata:    metaVal,
		Tags:        []string{"alpha", "beta"},
		Actor: &samplev1.Actor{
			Name: "alice",
			Uid:  9876543210,
		},
		Note: &note,
	}
}

// TestToOCSFJSON_EnumIsNumber asserts that an enum field is serialised as a
// JSON number equal to the integer value (5 for SEVERITY_ID_CRITICAL), not a
// string like "SEVERITY_ID_CRITICAL".
func TestToOCSFJSON_EnumIsNumber(t *testing.T) {
	msg := sampleMsg(t)
	out, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))

	v, ok := m["severity_id"]
	require.True(t, ok, "severity_id must be present")

	// After json.Unmarshal into map[string]any, numbers become float64.
	f, isFloat := v.(float64)
	require.True(t, isFloat, "severity_id must unmarshal as float64 (JSON number), got %T", v)
	require.Equal(t, float64(5), f)
}

// TestToOCSFJSON_Int64IsNumber asserts that int64 fields are emitted as
// unquoted JSON numbers, not quoted strings. We verify both at the structural
// level (correct numeric value) and at the byte level (no surrounding quotes
// around the number in the raw JSON).
func TestToOCSFJSON_Int64IsNumber(t *testing.T) {
	msg := sampleMsg(t)
	out, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)

	// Byte-level assertion: the raw JSON must contain `"time":1685403212834`
	// (no surrounding quotes on the value).
	require.True(t, bytes.Contains(out, []byte(`"time":1685403212834`)),
		"time must be an unquoted JSON number; raw JSON: %s", out)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))

	v, ok := m["time"]
	require.True(t, ok, "time must be present")
	f, isFloat := v.(float64)
	require.True(t, isFloat, "time must be a JSON number, got %T", v)
	require.Equal(t, float64(1685403212834), f)
}

// TestToOCSFJSON_SnakeCaseKeys asserts that JSON keys are snake_case, matching
// the proto field names, not camelCase.
func TestToOCSFJSON_SnakeCaseKeys(t *testing.T) {
	msg := sampleMsg(t)
	out, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))

	// snake_case keys must be present.
	require.Contains(t, m, "severity_id", "severity_id (snake_case) must be a key")
	require.Contains(t, m, "message_text", "message_text (snake_case) must be a key")
	require.Contains(t, m, "is_alert", "is_alert (snake_case) must be a key")

	// camelCase keys must NOT appear.
	require.NotContains(t, m, "severityId", "camelCase key severityId must not appear")
	require.NotContains(t, m, "messageText", "camelCase key messageText must not appear")
	require.NotContains(t, m, "isAlert", "camelCase key isAlert must not appear")
}

// TestToOCSFJSON_UnsetOptionalAbsent asserts that an explicit optional field
// that was not set is omitted from the output entirely.
func TestToOCSFJSON_UnsetOptionalAbsent(t *testing.T) {
	// Build a message WITHOUT the optional note field.
	msg := &samplev1.SampleEvent{
		SeverityId:  samplev1.SampleEvent_SEVERITY_ID_CRITICAL,
		Time:        1685403212834,
		MessageText: "test",
	}

	out, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))

	_, present := m["note"]
	require.False(t, present, "unset optional field 'note' must not appear in output")
}

// TestToOCSFJSON_ZeroImplicitScalarAbsent asserts that a proto3 implicit-
// presence scalar at its zero value is omitted.
func TestToOCSFJSON_ZeroImplicitScalarAbsent(t *testing.T) {
	msg := &samplev1.SampleEvent{
		MessageText: "non-zero",
		// count, time, score, is_alert left at zero values
	}

	out, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))

	_, hasCount := m["count"]
	require.False(t, hasCount, "zero count must be omitted")
	_, hasTime := m["time"]
	require.False(t, hasTime, "zero time must be omitted")
	_, hasScore := m["score"]
	require.False(t, hasScore, "zero score must be omitted")
	_, hasIsAlert := m["is_alert"]
	require.False(t, hasIsAlert, "false is_alert must be omitted")
}

// TestToOCSFJSON_RepeatedField asserts that repeated fields become JSON arrays.
func TestToOCSFJSON_RepeatedField(t *testing.T) {
	msg := sampleMsg(t)
	out, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))

	tagsRaw, ok := m["tags"]
	require.True(t, ok, "tags must be present")
	tags, ok := tagsRaw.([]any)
	require.True(t, ok, "tags must be a JSON array, got %T", tagsRaw)
	require.Len(t, tags, 2)
	require.Equal(t, "alpha", tags[0])
	require.Equal(t, "beta", tags[1])
}

// TestToOCSFJSON_EmptyRepeatedAbsent asserts that an empty repeated field is
// omitted from the output.
func TestToOCSFJSON_EmptyRepeatedAbsent(t *testing.T) {
	msg := &samplev1.SampleEvent{MessageText: "x"}
	out, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))

	_, present := m["tags"]
	require.False(t, present, "empty repeated field must be omitted")
}

// TestToOCSFJSON_NestedMessage asserts that nested messages are serialised as
// JSON objects applying the same rules (snake_case keys, numeric int64).
func TestToOCSFJSON_NestedMessage(t *testing.T) {
	msg := sampleMsg(t)
	out, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))

	actorRaw, ok := m["actor"]
	require.True(t, ok, "actor must be present")
	actor, ok := actorRaw.(map[string]any)
	require.True(t, ok, "actor must be a JSON object, got %T", actorRaw)

	require.Equal(t, "alice", actor["name"])

	// uid is an int64 — must be a JSON number, not a string.
	uidRaw, ok := actor["uid"]
	require.True(t, ok, "uid must be present in actor")
	_, isFloat := uidRaw.(float64)
	require.True(t, isFloat, "actor.uid must be a JSON number, got %T", uidRaw)

	// Nested key must also be snake_case (uid is already snake, but verify no camelCase leaks).
	require.NotContains(t, actor, "unknownFields")
}

// TestToOCSFJSON_WellKnownValue asserts that google.protobuf.Value fields
// round-trip to their natural JSON representation.
func TestToOCSFJSON_WellKnownValue(t *testing.T) {
	msg := sampleMsg(t)
	out, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))

	metaRaw, ok := m["metadata"]
	require.True(t, ok, "metadata must be present")
	meta, ok := metaRaw.(map[string]any)
	require.True(t, ok, "metadata must be a JSON object, got %T", metaRaw)

	require.Equal(t, "192.0.2.1", meta["src_ip"])
	require.Equal(t, float64(443), meta["port"])
}

// TestToOCSFJSON_Deterministic asserts that two calls on the same message
// produce byte-identical output.
func TestToOCSFJSON_Deterministic(t *testing.T) {
	msg := sampleMsg(t)

	out1, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)
	out2, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)

	require.Equal(t, string(out1), string(out2),
		"ToOCSFJSON must be deterministic: two calls on same message must be byte-identical")
}

// TestToOCSFJSON_DivergenceFromProtojson proves that ToOCSFJSON is necessary by
// showing stock protojson.Marshal diverges on at least three points:
//
// (a) enum: stock emits the name string; ToOCSFJSON emits the integer.
// (b) int64: stock emits a quoted string; ToOCSFJSON emits an unquoted number.
// (c) key casing: stock emits camelCase; ToOCSFJSON emits snake_case.
func TestToOCSFJSON_DivergenceFromProtojson(t *testing.T) {
	msg := sampleMsg(t)

	// Stock protojson output.
	stockBytes, err := protojson.Marshal(msg)
	require.NoError(t, err)
	stockStr := string(stockBytes)

	// Our exporter output.
	ourBytes, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)
	ourStr := string(ourBytes)

	// (a) Enum: stock emits name string; we emit integer.
	require.True(t, strings.Contains(stockStr, "SEVERITY_ID_CRITICAL"),
		"stock protojson must emit the enum name string, got: %s", stockStr)
	require.False(t, strings.Contains(ourStr, "SEVERITY_ID_CRITICAL"),
		"ToOCSFJSON must NOT emit enum name strings, got: %s", ourStr)
	require.True(t, strings.Contains(ourStr, `"severity_id":5`),
		"ToOCSFJSON must emit severity_id as integer 5, got: %s", ourStr)

	// (b) int64: stock emits quoted string; we emit unquoted number.
	require.True(t, strings.Contains(stockStr, `"1685403212834"`),
		"stock protojson must emit int64 as a quoted string, got: %s", stockStr)
	require.True(t, bytes.Contains(ourBytes, []byte(`"time":1685403212834`)),
		"ToOCSFJSON must emit time as unquoted number, got: %s", ourStr)

	// (c) Key casing: stock uses camelCase; we use snake_case.
	// stock must NOT have snake_case severity_id directly (it uses severityId)
	// and must NOT have snake_case message_text (it uses messageText).
	require.True(t, strings.Contains(stockStr, "severityId") || strings.Contains(stockStr, "messageText"),
		"stock protojson must emit camelCase keys, got: %s", stockStr)
	require.False(t, strings.Contains(ourStr, "severityId"),
		"ToOCSFJSON must not emit camelCase severityId, got: %s", ourStr)
	require.False(t, strings.Contains(ourStr, "messageText"),
		"ToOCSFJSON must not emit camelCase messageText, got: %s", ourStr)
}

// TestToOCSFJSON_RepeatedEnumIsIntArray asserts that a repeated enum field
// marshals to a JSON array of integers, not name strings. This directly
// validates the headline requirement: enums are always integers, including in
// the repeated position (e.g. [5,99], never ["SEVERITY_ID_CRITICAL",...]).
func TestToOCSFJSON_RepeatedEnumIsIntArray(t *testing.T) {
	msg := &samplev1.SampleEvent{
		MessageText: "repeated enum test",
		Severities: []samplev1.SampleEvent_SeverityId{
			samplev1.SampleEvent_SEVERITY_ID_CRITICAL, // 5
			samplev1.SampleEvent_SEVERITY_ID_OTHER,    // 99
		},
	}

	out, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))

	raw, ok := m["severities"]
	require.True(t, ok, "severities must be present")

	arr, ok := raw.([]any)
	require.True(t, ok, "severities must be a JSON array, got %T", raw)
	require.Len(t, arr, 2)

	// Each element must be a JSON number (float64 after unmarshal), not a string.
	f0, ok := arr[0].(float64)
	require.True(t, ok, "severities[0] must be a JSON number, got %T", arr[0])
	require.Equal(t, float64(5), f0, "severities[0] must be 5 (SEVERITY_ID_CRITICAL)")

	f1, ok := arr[1].(float64)
	require.True(t, ok, "severities[1] must be a JSON number, got %T", arr[1])
	require.Equal(t, float64(99), f1, "severities[1] must be 99 (SEVERITY_ID_OTHER)")

	// Byte-level check: no enum name strings in the output.
	require.False(t, strings.Contains(string(out), "SEVERITY_ID"),
		"ToOCSFJSON must not emit enum name strings in repeated field, got: %s", out)
}

// TestToOCSFJSON_OptionalSetPresent asserts that an optional field that IS set
// appears in the output.
func TestToOCSFJSON_OptionalSetPresent(t *testing.T) {
	msg := sampleMsg(t) // note is set to "some note" in sampleMsg
	out, err := exporter.ToOCSFJSON(msg)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))

	v, present := m["note"]
	require.True(t, present, "set optional field 'note' must appear in output")
	require.Equal(t, "some note", v)
}
