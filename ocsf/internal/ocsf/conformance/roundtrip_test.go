// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package conformance_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	ocsfv1 "github.com/redpanda-data/common-go/ocsf/internal/ocsf/conformance/genpb/ocsf/v1"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/exporter"
)

// TestJSONRoundTrip_ProtoJSONProto proves proto → OCSF JSON → proto is
// lossless on the real generated classes: FromOCSFJSON(ToOCSFJSON(evt))
// equals evt for every conformance event.
func TestJSONRoundTrip_ProtoJSONProto(t *testing.T) {
	cases := []struct {
		name    string
		event   proto.Message
		decoded proto.Message
	}{
		{"ApiActivity/Allowed", buildApiActivityAllowed(), &ocsfv1.ApiActivity{}},
		{"ApiActivity/Unauthorized", buildApiActivityUnauthorized(), &ocsfv1.ApiActivity{}},
		{"EntityManagement/Create", buildEntityManagement(), &ocsfv1.EntityManagement{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := exporter.ToOCSFJSON(tc.event)
			require.NoError(t, err)

			require.NoError(t, exporter.FromOCSFJSON(b, tc.decoded))
			require.True(t, proto.Equal(tc.event, tc.decoded),
				"proto → JSON → proto must be lossless\njson: %s", b)
		})
	}
}

// TestJSONRoundTrip_JSONProtoJSON proves OCSF JSON → proto → OCSF JSON is
// byte-identical: an event ingested from JSON re-exports to the same bytes.
// This is the property that lets external OCSF JSON (from other producers)
// pass through the proto representation without loss.
func TestJSONRoundTrip_JSONProtoJSON(t *testing.T) {
	cases := []struct {
		name    string
		event   proto.Message
		decoded proto.Message
	}{
		{"ApiActivity/Allowed", buildApiActivityAllowed(), &ocsfv1.ApiActivity{}},
		{"ApiActivity/Unauthorized", buildApiActivityUnauthorized(), &ocsfv1.ApiActivity{}},
		{"EntityManagement/Create", buildEntityManagement(), &ocsfv1.EntityManagement{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := exporter.ToOCSFJSON(tc.event)
			require.NoError(t, err)

			require.NoError(t, exporter.FromOCSFJSON(b, tc.decoded))
			b2, err := exporter.ToOCSFJSON(tc.decoded)
			require.NoError(t, err)

			require.Equal(t, string(b), string(b2),
				"JSON → proto → JSON must be byte-identical")
		})
	}
}

// TestJSONRoundTrip_UnknownKeysToUnmapped verifies OCSF's forward-compat
// model: JSON keys not in the proto schema land in the class's unmapped field
// and survive re-export.
func TestJSONRoundTrip_UnknownKeysToUnmapped(t *testing.T) {
	b, err := exporter.ToOCSFJSON(buildApiActivityAllowed())
	require.NoError(t, err)

	// Splice two unknown keys into the event JSON, as a producer on a newer
	// OCSF revision would.
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	m["future_attribute"] = "hello"
	m["future_block"] = map[string]any{"a": float64(1)}
	spliced, err := json.Marshal(m)
	require.NoError(t, err)

	var evt ocsfv1.ApiActivity
	require.NoError(t, exporter.FromOCSFJSON(spliced, &evt))

	require.NotNil(t, evt.Unmapped, "unknown keys must land in unmapped")
	unmapped := evt.Unmapped.GetStructValue()
	require.NotNil(t, unmapped)
	require.Equal(t, "hello", unmapped.Fields["future_attribute"].GetStringValue())
	require.Equal(t, float64(1), unmapped.Fields["future_block"].GetStructValue().Fields["a"].GetNumberValue())

	// Known fields are unaffected.
	require.Equal(t, ocsfv1.ApiActivity_CLASS_UID_API_ACTIVITY, evt.ClassUid)

	// Re-export keeps the data (under unmapped, per OCSF).
	b2, err := exporter.ToOCSFJSON(&evt)
	require.NoError(t, err)
	var m2 map[string]any
	require.NoError(t, json.Unmarshal(b2, &m2))
	un, ok := m2["unmapped"].(map[string]any)
	require.True(t, ok, "unmapped must re-export as a JSON object")
	require.Equal(t, "hello", un["future_attribute"])
}
