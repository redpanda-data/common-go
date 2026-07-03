// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Merged-event (AuditEvent) conformance: the single flat message that unions
// api_activity + entity_management for a one-topic audit log.
//
// The tests prove four properties:
//
//  1. Wire round-trip on the generated AuditEvent bindings.
//  2. JSON equivalence: an AuditEvent exports to OCSF JSON that is deeply
//     equal to the same event built on the per-class message, so a single
//     topic loses nothing relative to per-class topics.
//  3. JSON round-trip in both directions (proto → JSON → proto lossless,
//     JSON → proto → JSON byte-identical).
//  4. protovalidate enforces the class-aware CEL rules: type_uid consistency,
//     class-field ownership, and per-class conditional requiredness.
package conformance_test

import (
	"encoding/json"
	"testing"

	protovalidate "buf.build/go/protovalidate"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	ocsfv1 "github.com/redpanda-data/common-go/ocsf/internal/ocsf/conformance/genpb/ocsf/v1"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/exporter"
)

// buildMergedApiActivityAllowed mirrors buildApiActivityAllowed on the merged
// AuditEvent message. Field values are identical; only the Go type differs.
func buildMergedApiActivityAllowed() *ocsfv1.AuditEvent {
	return &ocsfv1.AuditEvent{
		ClassUid:    ocsfv1.AuditEvent_CLASS_UID_API_ACTIVITY, // 6003
		CategoryUid: ocsfv1.AuditEvent_CATEGORY_UID_APPLICATION_ACTIVITY,
		TypeUid:     ocsfv1.AuditEvent_TYPE_UID_API_ACTIVITY_READ,
		ActivityId:  2, // Read — plain int32 on the merged message (class-scoped enum)
		SeverityId:  ocsfv1.AuditEvent_SEVERITY_ID_INFORMATIONAL,
		Time:        1751313600000,

		Actor: &ocsfv1.Actor{
			User: &ocsfv1.User{
				Uid:  "user:alice@example.com",
				Name: "alice",
			},
		},
		Api: &ocsfv1.Api{
			Operation: "DescribeTopics",
			Service: &ocsfv1.Service{
				Name: "redpanda-cloud",
			},
		},
		Cloud: &ocsfv1.Cloud{
			Provider: "AWS",
			Region:   "us-east-1",
		},
		Metadata: &ocsfv1.Metadata{
			Version: "1.8.0",
			Product: &ocsfv1.Product{
				Name:       "Redpanda Cloud",
				VendorName: "Redpanda Data",
			},
			Profiles: []string{"cloud", "security_control"},
		},
		SrcEndpoint: &ocsfv1.NetworkEndpoint{
			Ip: "10.0.1.42",
		},
		DispositionId: ocsfv1.AuditEvent_DISPOSITION_ID_ALLOWED,
		StatusId:      ocsfv1.AuditEvent_STATUS_ID_SUCCESS,
		Authorizations: []*ocsfv1.Authorization{
			{
				Decision: "Permit",
				Policy: &ocsfv1.Policy{
					Uid:  "policy:cluster-readonly-v1",
					Name: "ClusterReadOnly",
				},
			},
		},
		Resources: []*ocsfv1.ResourceDetails{
			{
				Uid:  "rn:redpanda:cluster:prod-cluster-1",
				Name: "prod-cluster-1",
				Type: "cluster",
			},
		},
		Message: "DescribeTopics authorised for alice",
	}
}

// buildMergedEntityManagement mirrors buildEntityManagement on AuditEvent.
func buildMergedEntityManagement() *ocsfv1.AuditEvent {
	return &ocsfv1.AuditEvent{
		ClassUid:    ocsfv1.AuditEvent_CLASS_UID_ENTITY_MANAGEMENT, // 3004
		CategoryUid: ocsfv1.AuditEvent_CATEGORY_UID_IDENTITY_ACCESS_MANAGEMENT,
		TypeUid:     ocsfv1.AuditEvent_TYPE_UID_ENTITY_MANAGEMENT_CREATE,
		ActivityId:  1, // Create
		SeverityId:  ocsfv1.AuditEvent_SEVERITY_ID_INFORMATIONAL,
		Time:        1751313600000,

		Entity: &ocsfv1.ManagedEntity{
			TypeId: ocsfv1.ManagedEntity_TYPE_ID_POLICY,
			Type:   "policy",
			Name:   "cluster-readonly-v1",
			Uid:    "policy:cluster-readonly-v1",
			Policy: &ocsfv1.Policy{
				Uid:  "policy:cluster-readonly-v1",
				Name: "ClusterReadOnly",
			},
		},
		Cloud: &ocsfv1.Cloud{
			Provider: "AWS",
			Region:   "us-east-1",
		},
		Metadata: &ocsfv1.Metadata{
			Version: "1.8.0",
			Product: &ocsfv1.Product{
				Name:       "Redpanda Cloud",
				VendorName: "Redpanda Data",
			},
			Profiles: []string{"cloud"},
		},
		Actor: &ocsfv1.Actor{
			User: &ocsfv1.User{
				Uid:  "user:admin@example.com",
				Name: "admin",
			},
		},
		Message: "Policy cluster-readonly-v1 created",
	}
}

// TestMergedProtoRoundTrip proves wire round-trip on the AuditEvent bindings.
func TestMergedProtoRoundTrip(t *testing.T) {
	t.Run("ApiActivity", func(t *testing.T) {
		roundtrip(t, buildMergedApiActivityAllowed(), &ocsfv1.AuditEvent{})
	})
	t.Run("EntityManagement", func(t *testing.T) {
		roundtrip(t, buildMergedEntityManagement(), &ocsfv1.AuditEvent{})
	})
}

// TestMergedJSONEquivalence proves the central single-topic property: the
// merged AuditEvent exports OCSF JSON deeply equal to the per-class message
// carrying the same event. (Byte order differs — field numbers differ between
// the two layouts and the exporter emits in tag order — but the parsed
// objects are identical, and OCSF JSON is the interchange format.)
func TestMergedJSONEquivalence(t *testing.T) {
	cases := []struct {
		name     string
		merged   proto.Message
		perClass proto.Message
	}{
		{"ApiActivity", buildMergedApiActivityAllowed(), buildApiActivityAllowed()},
		{"EntityManagement", buildMergedEntityManagement(), buildEntityManagement()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mergedJSON, err := exporter.ToOCSFJSON(tc.merged)
			require.NoError(t, err)
			perClassJSON, err := exporter.ToOCSFJSON(tc.perClass)
			require.NoError(t, err)

			var mergedMap, perClassMap map[string]any
			require.NoError(t, json.Unmarshal(mergedJSON, &mergedMap))
			require.NoError(t, json.Unmarshal(perClassJSON, &perClassMap))
			require.Equal(t, perClassMap, mergedMap,
				"merged AuditEvent must export the same OCSF JSON as the per-class message")
		})
	}
}

// TestMergedJSONRoundTrip proves both JSON round-trip directions on the
// merged message, including decoding per-class-produced OCSF JSON into an
// AuditEvent (the ingestion path for a single topic).
func TestMergedJSONRoundTrip(t *testing.T) {
	events := []proto.Message{
		buildMergedApiActivityAllowed(),
		buildMergedEntityManagement(),
	}

	for _, evt := range events {
		b, err := exporter.ToOCSFJSON(evt)
		require.NoError(t, err)

		var decoded ocsfv1.AuditEvent
		require.NoError(t, exporter.FromOCSFJSON(b, &decoded))
		require.True(t, proto.Equal(evt, &decoded), "proto → JSON → proto must be lossless")

		b2, err := exporter.ToOCSFJSON(&decoded)
		require.NoError(t, err)
		require.Equal(t, string(b), string(b2), "JSON → proto → JSON must be byte-identical")
	}

	// Cross-layout ingestion: JSON produced from the per-class message decodes
	// into AuditEvent and re-exports to the same object.
	perClassJSON, err := exporter.ToOCSFJSON(buildApiActivityAllowed())
	require.NoError(t, err)

	var merged ocsfv1.AuditEvent
	require.NoError(t, exporter.FromOCSFJSON(perClassJSON, &merged))
	require.True(t, proto.Equal(buildMergedApiActivityAllowed(), &merged),
		"per-class OCSF JSON must decode into the equivalent AuditEvent")
}

// TestMergedProtovalidate proves the class-aware CEL rules close the loop:
// protovalidate accepts well-formed events of both classes and rejects
// class-inconsistent ones.
func TestMergedProtovalidate(t *testing.T) {
	validator, err := protovalidate.New()
	require.NoError(t, err)

	t.Run("ValidApiActivity", func(t *testing.T) {
		require.NoError(t, validator.Validate(buildMergedApiActivityAllowed()))
	})

	t.Run("ValidEntityManagement", func(t *testing.T) {
		require.NoError(t, validator.Validate(buildMergedEntityManagement()))
	})

	t.Run("TypeUIDMismatch", func(t *testing.T) {
		evt := buildMergedApiActivityAllowed()
		evt.TypeUid = ocsfv1.AuditEvent_TYPE_UID_API_ACTIVITY_CREATE // activity_id still Read
		err := validator.Validate(evt)
		require.ErrorContains(t, err, "type_uid")
	})

	t.Run("ForeignClassField", func(t *testing.T) {
		evt := buildMergedApiActivityAllowed()
		evt.Entity = buildMergedEntityManagement().Entity // entity_management-owned field on 6003
		err := validator.Validate(evt)
		require.ErrorContains(t, err, "entity")
	})

	t.Run("MissingClassRequiredField", func(t *testing.T) {
		evt := buildMergedApiActivityAllowed()
		evt.Actor = nil // required for api_activity (6003), not blanket-required
		err := validator.Validate(evt)
		require.ErrorContains(t, err, "actor")
	})

	t.Run("MissingBlanketRequiredField", func(t *testing.T) {
		evt := buildMergedApiActivityAllowed()
		evt.Metadata = nil // required in every class → field-level required
		err := validator.Validate(evt)
		require.ErrorContains(t, err, "metadata")
	})
}

// TestMergedServerConformance validates the merged events' OCSF JSON against
// the official OCSF validate endpoint, same contract as TestServerConformance.
func TestMergedServerConformance(t *testing.T) {
	const version = "1.8.0"

	cases := []struct {
		name    string
		builder func() proto.Message
	}{
		{"AuditEvent/ApiActivity", func() proto.Message { return buildMergedApiActivityAllowed() }},
		{"AuditEvent/EntityManagement", func() proto.Message { return buildMergedEntityManagement() }},
	}

	networkUnavailable := false

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if networkUnavailable {
				t.Skip("schema.ocsf.io is unreachable; skipping remaining conformance cases")
				return
			}

			eventJSON, err := exporter.ToOCSFJSON(tc.builder())
			require.NoError(t, err, "ToOCSFJSON must succeed")

			errCount, errs, err := validateAgainstOCSFServer(t, version, eventJSON)
			if err != nil {
				if isConnectivityError(err) {
					networkUnavailable = true
					t.Skipf("OCSF server unreachable (connectivity): %v", err)
					return
				}
				t.Fatalf("OCSF server request failed (non-connectivity): %v", err)
			}

			if errCount > 0 {
				for _, e := range errs {
					t.Errorf("  attribute_path=%q  error=%q  message=%q", e.AttributePath, e.Error, e.Message)
				}
				t.Fatalf("OCSF server validation: %d error(s) for %s", errCount, tc.name)
			}

			t.Log("OCSF server validation: CLEAN (error_count=0)")
		})
	}
}
