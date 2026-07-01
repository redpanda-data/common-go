// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package conformance is an end-to-end acceptance test that verifies:
//
//  1. The generated Go bindings in genpb/ compile and can be instantiated
//     (consumability of the generated proto).
//
//  2. Populated events round-trip through proto binary encoding
//     (proto.Marshal → proto.Unmarshal → proto.Equal).
//
//  3. Exported OCSF JSON satisfies structural invariants derived from the
//     OCSF 1.8.0 schema (required fields present, correct JSON types).
//
//  4. External server validation against the official OCSF validate endpoint
//     at schema.ocsf.io. This step is skipped only when the endpoint is
//     unreachable (no network). A 200 response with errors is a test failure.
package conformance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	ocsfv1 "github.com/redpanda-data/common-go/ocsf/internal/ocsf/conformance/genpb"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/exporter"
)

// ────────────────────────────────────────────────────────────────────────────
// Event builders
// ────────────────────────────────────────────────────────────────────────────

// buildApiActivityAllowed builds an ApiActivity (class 6003) representing an
// authorisation decision that was allowed.  It populates all required fields
// plus several recommended ones to produce a realistic event.
//
// The event declares metadata.profiles = ["cloud", "security_control"] because:
//   - cloud belongs to the "cloud" profile
//   - disposition_id and authorizations belong to the "security_control" profile
//
// Without declaring these profiles the OCSF validate endpoint returns
// attribute_unknown errors for all three fields.
func buildApiActivityAllowed() *ocsfv1.ApiActivity {
	return &ocsfv1.ApiActivity{
		// ── Required class-level scalars ──────────────────────────────────
		ClassUid:    ocsfv1.ApiActivity_CLASS_UID_API_ACTIVITY, // 6003
		CategoryUid: ocsfv1.ApiActivity_CATEGORY_UID_APPLICATION_ACTIVITY,
		TypeUid:     ocsfv1.ApiActivity_TYPE_UID_API_ACTIVITY_READ,
		ActivityId:  ocsfv1.ApiActivity_ACTIVITY_ID_READ,
		SeverityId:  ocsfv1.ApiActivity_SEVERITY_ID_INFORMATIONAL,
		Time:        1751313600000, // 2025-06-30T12:00:00.000Z in epoch-ms

		// ── Required: actor (at_least_one: user) ─────────────────────────
		Actor: &ocsfv1.Actor{
			User: &ocsfv1.User{
				Uid:  "user:alice@example.com",
				Name: "alice",
			},
		},

		// ── Required: api ────────────────────────────────────────────────
		Api: &ocsfv1.Api{
			Operation: "DescribeTopics",
			Service: &ocsfv1.Service{
				Name: "redpanda-cloud",
			},
		},

		// ── Required: cloud (from "cloud" profile) ───────────────────────
		Cloud: &ocsfv1.Cloud{
			Provider: "AWS",
			Region:   "us-east-1",
		},

		// ── Required: metadata ───────────────────────────────────────────
		// Profiles must be declared for profile-gated attributes to be
		// accepted by the OCSF validate endpoint.
		Metadata: &ocsfv1.Metadata{
			Version: "1.8.0",
			Product: &ocsfv1.Product{
				Name:       "Redpanda Cloud",
				VendorName: "Redpanda Data",
			},
			Profiles: []string{"cloud", "security_control"},
		},

		// ── Required: osint (empty list satisfies the repeated-required field) ──
		// OCSF proto marks osint as required, but the schema allows an empty list.
		// We pass a nil slice here; the exporter omits empty repeated fields.
		Osint: nil,

		// ── Required: src_endpoint (at_least_one satisfied by ip) ────────
		SrcEndpoint: &ocsfv1.NetworkEndpoint{
			Ip: "10.0.1.42",
		},

		// ── Authorization verdict fields (from "security_control" profile) ──
		DispositionId: ocsfv1.ApiActivity_DISPOSITION_ID_ALLOWED, // 1 = Allowed
		StatusId:      ocsfv1.ApiActivity_STATUS_ID_SUCCESS,

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

// buildApiActivityUnauthorized builds an ApiActivity for an authorization
// denial (disposition_id = 26 UNAUTHORIZED, status_id = FAILURE).
func buildApiActivityUnauthorized() *ocsfv1.ApiActivity {
	evt := buildApiActivityAllowed()
	evt.DispositionId = ocsfv1.ApiActivity_DISPOSITION_ID_UNAUTHORIZED // 26
	evt.StatusId = ocsfv1.ApiActivity_STATUS_ID_FAILURE
	evt.Message = "DescribeTopics denied for alice: missing CreateTopics role"
	evt.Authorizations = []*ocsfv1.Authorization{
		{
			Decision: "Deny",
			Policy: &ocsfv1.Policy{
				Uid:  "policy:cluster-readonly-v1",
				Name: "ClusterReadOnly",
			},
		},
	}
	return evt
}

// buildEntityManagement builds an EntityManagement (class 3004) event
// representing the creation of a new IAM policy.
//
// The event declares metadata.profiles = ["cloud"] because the cloud attribute
// belongs to the "cloud" profile and is rejected as attribute_unknown without it.
func buildEntityManagement() *ocsfv1.EntityManagement {
	return &ocsfv1.EntityManagement{
		// ── Required class-level scalars ──────────────────────────────────
		ClassUid:    ocsfv1.EntityManagement_CLASS_UID_ENTITY_MANAGEMENT, // 3004
		CategoryUid: ocsfv1.EntityManagement_CATEGORY_UID_IDENTITY_ACCESS_MANAGEMENT,
		TypeUid:     ocsfv1.EntityManagement_TYPE_UID_ENTITY_MANAGEMENT_CREATE,
		ActivityId:  ocsfv1.EntityManagement_ACTIVITY_ID_CREATE,
		SeverityId:  ocsfv1.EntityManagement_SEVERITY_ID_INFORMATIONAL,
		Time:        1751313600000,

		// ── Required: entity (at_least_one: policy) ──────────────────────
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

		// ── cloud (from "cloud" profile) ─────────────────────────────────
		Cloud: &ocsfv1.Cloud{
			Provider: "AWS",
			Region:   "us-east-1",
		},

		// ── Required: metadata ───────────────────────────────────────────
		// Declare "cloud" profile so the cloud attribute is accepted by the
		// OCSF validate endpoint.
		Metadata: &ocsfv1.Metadata{
			Version: "1.8.0",
			Product: &ocsfv1.Product{
				Name:       "Redpanda Cloud",
				VendorName: "Redpanda Data",
			},
			Profiles: []string{"cloud"},
		},

		// ── Required: osint (repeated; nil means empty — omitted in JSON) ─
		Osint: nil,

		// ── Actor (optional for entity_management but realistic) ─────────
		Actor: &ocsfv1.Actor{
			User: &ocsfv1.User{
				Uid:  "user:admin@example.com",
				Name: "admin",
			},
		},

		Message: "Policy cluster-readonly-v1 created",
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Round-trip test
// ────────────────────────────────────────────────────────────────────────────

// TestProtoRoundTrip marshals and unmarshals each event and asserts
// proto.Equal holds.  This proves wire round-trip correctness on the generated
// types.
func TestProtoRoundTrip(t *testing.T) {
	t.Run("ApiActivity/Allowed", func(t *testing.T) {
		original := buildApiActivityAllowed()
		roundtrip(t, original, &ocsfv1.ApiActivity{})
	})
	t.Run("ApiActivity/Unauthorized", func(t *testing.T) {
		original := buildApiActivityUnauthorized()
		roundtrip(t, original, &ocsfv1.ApiActivity{})
	})
	t.Run("EntityManagement/Create", func(t *testing.T) {
		original := buildEntityManagement()
		roundtrip(t, original, &ocsfv1.EntityManagement{})
	})
}

func roundtrip(t *testing.T, original, decoded proto.Message) {
	t.Helper()

	wire, err := proto.Marshal(original)
	require.NoError(t, err, "proto.Marshal must succeed")
	require.NotEmpty(t, wire, "wire encoding must not be empty")

	err = proto.Unmarshal(wire, decoded)
	require.NoError(t, err, "proto.Unmarshal must succeed")

	require.True(t, proto.Equal(original, decoded),
		"proto.Equal(original, decoded) must hold after round-trip")
}

// ────────────────────────────────────────────────────────────────────────────
// OCSF JSON export + structural invariants
// ────────────────────────────────────────────────────────────────────────────

// TestOCSFJSONExport verifies that ToOCSFJSON produces valid JSON and that
// the key OCSF structural invariants hold on the output.
func TestOCSFJSONExport(t *testing.T) {
	t.Run("ApiActivity/Allowed", func(t *testing.T) {
		evt := buildApiActivityAllowed()
		b, err := exporter.ToOCSFJSON(evt)
		require.NoError(t, err)

		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))

		assertApiActivityInvariants(t, m, 1 /* ALLOWED */, 1 /* SUCCESS */)
	})

	t.Run("ApiActivity/Unauthorized", func(t *testing.T) {
		evt := buildApiActivityUnauthorized()
		b, err := exporter.ToOCSFJSON(evt)
		require.NoError(t, err)

		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))

		assertApiActivityInvariants(t, m, 26 /* UNAUTHORIZED */, 2 /* FAILURE */)
	})

	t.Run("EntityManagement/Create", func(t *testing.T) {
		evt := buildEntityManagement()
		b, err := exporter.ToOCSFJSON(evt)
		require.NoError(t, err)

		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))

		assertEntityManagementInvariants(t, m)
	})
}

// assertApiActivityInvariants checks the required OCSF fields and types for
// class 6003 (api_activity).
func assertApiActivityInvariants(t *testing.T, m map[string]any, wantDispositionID, wantStatusID float64) {
	t.Helper()

	// class_uid must be the integer 6003.
	requireJSONNumber(t, m, "class_uid", 6003)
	// category_uid must be 6.
	requireJSONNumber(t, m, "category_uid", 6)
	// time must be a JSON number (not a string).
	requireJSONNumberPresent(t, m, "time")
	// severity_id must be an integer.
	requireJSONNumberPresent(t, m, "severity_id")
	// type_uid must be a JSON number.
	requireJSONNumberPresent(t, m, "type_uid")
	// activity_id must be a JSON number.
	requireJSONNumberPresent(t, m, "activity_id")

	// disposition_id must match the expected value.
	requireJSONNumber(t, m, "disposition_id", wantDispositionID)
	// status_id must match.
	requireJSONNumber(t, m, "status_id", wantStatusID)

	// Keys must be snake_case.
	for _, camel := range []string{"classUid", "categoryUid", "severityId", "typeUid", "activityId", "dispositionId", "statusId"} {
		require.NotContains(t, m, camel, "camelCase key %q must not appear in OCSF JSON output", camel)
	}

	// actor must be a JSON object with a nested user.
	actorRaw, ok := m["actor"]
	require.True(t, ok, "actor must be present")
	actor, ok := actorRaw.(map[string]any)
	require.True(t, ok, "actor must be a JSON object, got %T", actorRaw)
	userRaw, ok := actor["user"]
	require.True(t, ok, "actor.user must be present")
	user, ok := userRaw.(map[string]any)
	require.True(t, ok, "actor.user must be a JSON object, got %T", userRaw)
	require.NotEmpty(t, user["uid"], "actor.user.uid must be non-empty")

	// api must be a JSON object with operation.
	apiRaw, ok := m["api"]
	require.True(t, ok, "api must be present")
	apiObj, ok := apiRaw.(map[string]any)
	require.True(t, ok, "api must be a JSON object, got %T", apiRaw)
	require.NotEmpty(t, apiObj["operation"], "api.operation must be non-empty")

	// cloud must be present with provider (from "cloud" profile).
	cloudRaw, ok := m["cloud"]
	require.True(t, ok, "cloud must be present")
	cloud, ok := cloudRaw.(map[string]any)
	require.True(t, ok, "cloud must be a JSON object, got %T", cloudRaw)
	require.NotEmpty(t, cloud["provider"], "cloud.provider must be non-empty")

	// metadata must be present with version and product.
	metaRaw, ok := m["metadata"]
	require.True(t, ok, "metadata must be present")
	meta, ok := metaRaw.(map[string]any)
	require.True(t, ok, "metadata must be a JSON object, got %T", metaRaw)
	require.NotEmpty(t, meta["version"], "metadata.version must be non-empty")
	productRaw, ok := meta["product"]
	require.True(t, ok, "metadata.product must be present")
	product, ok := productRaw.(map[string]any)
	require.True(t, ok, "metadata.product must be a JSON object, got %T", productRaw)
	require.NotEmpty(t, product["name"], "metadata.product.name must be non-empty")

	// src_endpoint must be present.
	seRaw, ok := m["src_endpoint"]
	require.True(t, ok, "src_endpoint must be present")
	se, ok := seRaw.(map[string]any)
	require.True(t, ok, "src_endpoint must be a JSON object, got %T", seRaw)
	require.NotEmpty(t, se["ip"], "src_endpoint.ip must be non-empty")

	// authorizations must be a JSON array (from "security_control" profile).
	authRaw, ok := m["authorizations"]
	require.True(t, ok, "authorizations must be present")
	authArr, ok := authRaw.([]any)
	require.True(t, ok, "authorizations must be a JSON array, got %T", authRaw)
	require.NotEmpty(t, authArr, "authorizations must be non-empty")

	// First authorization must have a nested policy.uid.
	firstAuth, ok := authArr[0].(map[string]any)
	require.True(t, ok, "authorizations[0] must be a JSON object, got %T", authArr[0])
	policyRaw, ok := firstAuth["policy"]
	require.True(t, ok, "authorizations[0].policy must be present")
	policy, ok := policyRaw.(map[string]any)
	require.True(t, ok, "authorizations[0].policy must be a JSON object, got %T", policyRaw)
	require.NotEmpty(t, policy["uid"], "authorizations[0].policy.uid must be non-empty")
}

// assertEntityManagementInvariants checks the required OCSF fields for
// class 3004 (entity_management).
func assertEntityManagementInvariants(t *testing.T, m map[string]any) {
	t.Helper()

	requireJSONNumber(t, m, "class_uid", 3004)
	requireJSONNumber(t, m, "category_uid", 3)
	requireJSONNumberPresent(t, m, "time")
	requireJSONNumberPresent(t, m, "severity_id")
	requireJSONNumberPresent(t, m, "type_uid")
	requireJSONNumberPresent(t, m, "activity_id")

	// entity must be a JSON object (at_least_one: policy is satisfied by uid+name).
	entityRaw, ok := m["entity"]
	require.True(t, ok, "entity must be present")
	entity, ok := entityRaw.(map[string]any)
	require.True(t, ok, "entity must be a JSON object, got %T", entityRaw)
	require.NotEmpty(t, entity["uid"], "entity.uid must be non-empty")
	// type_id must be an integer, not a string.
	typeIDRaw, ok := entity["type_id"]
	require.True(t, ok, "entity.type_id must be present")
	_, isNum := typeIDRaw.(float64)
	require.True(t, isNum, "entity.type_id must be a JSON number (OCSF enum = integer), got %T", typeIDRaw)

	// cloud must be present with provider (from "cloud" profile).
	cloudRaw, ok := m["cloud"]
	require.True(t, ok, "cloud must be present")
	cloud, ok := cloudRaw.(map[string]any)
	require.True(t, ok, "cloud must be a JSON object")
	require.NotEmpty(t, cloud["provider"])

	// metadata with version + product.
	metaRaw, ok := m["metadata"]
	require.True(t, ok, "metadata must be present")
	meta, ok := metaRaw.(map[string]any)
	require.True(t, ok, "metadata must be a JSON object, got %T", metaRaw)
	require.NotEmpty(t, meta["version"])
}

// ────────────────────────────────────────────────────────────────────────────
// External conformance validation (OCSF server validate endpoint)
// ────────────────────────────────────────────────────────────────────────────

// validationError is a single error entry from the OCSF validate response.
type validationError struct {
	Error         string `json:"error"`
	Message       string `json:"message"`
	Attribute     string `json:"attribute"`
	AttributePath string `json:"attribute_path"`
}

// validateResponse is the response body from POST /api/v2/validate.
type validateResponse struct {
	Errors       []validationError `json:"errors"`
	Warnings     []validationError `json:"warnings"`
	ErrorCount   int               `json:"error_count"`
	WarningCount int               `json:"warning_count"`
}

// ocsfValidateClient is a reusable HTTP client for the OCSF validate endpoint.
// It does not follow redirects to a different host.
var ocsfValidateClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			return fmt.Errorf("redirect to different host %q refused", req.URL.Host)
		}
		return nil
	},
}

// validateAgainstOCSFServer POSTs eventJSON to the OCSF server validate
// endpoint for the given version and returns the parsed response.
//
// Transport-level failures (dial, timeout, DNS) return a non-nil err.
// A non-200 HTTP response also returns a non-nil err (server reachable but
// unhealthy — this is a test failure, not a skip). A 200 response that
// carries validation errors returns err == nil with errCount > 0.
func validateAgainstOCSFServer(t *testing.T, version string, eventJSON []byte) (errCount int, errs []validationError, err error) {
	t.Helper()

	url := fmt.Sprintf("https://schema.ocsf.io/%s/api/v2/validate", version)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader(eventJSON))
	if err != nil {
		return 0, nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ocsfValidateClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, nil, fmt.Errorf("OCSF validate returned HTTP %d; body: %s", resp.StatusCode, body)
	}

	var vr validateResponse
	if err := json.Unmarshal(body, &vr); err != nil {
		return 0, nil, fmt.Errorf("parsing validate response (status %d): %w\nbody: %s", resp.StatusCode, err, body)
	}

	return vr.ErrorCount, vr.Errors, nil
}

// isConnectivityError reports whether err represents a genuine connectivity
// failure: a dial/timeout error, DNS failure, or context deadline exceeded.
// It returns false for any other error (redirect refused, non-200 HTTP, etc.),
// which must be treated as a hard test failure rather than a skip.
func isConnectivityError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// url.Error wraps net.OpError for dial/lookup failures.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	return false
}

// TestServerConformance validates the exported OCSF JSON for each event
// against the official OCSF validate endpoint at schema.ocsf.io.
//
// The test is skipped only when the validate POST fails with a genuine
// connectivity error (dial/timeout/DNS) on the first event. Any other error
// (non-200 HTTP, redirect refused) is a hard test failure. A 200 response that
// carries validation errors is also a FAILURE, not a skip.
//
// Manual equivalent:
//
//	curl -s -X POST https://schema.ocsf.io/1.8.0/api/v2/validate \
//	  -H 'Content-Type: application/json' \
//	  -d @event.json | jq .
func TestServerConformance(t *testing.T) {
	const version = "1.8.0"

	cases := []struct {
		name    string
		builder func() proto.Message
	}{
		{"ApiActivity/Allowed", func() proto.Message { return buildApiActivityAllowed() }},
		{"ApiActivity/Unauthorized", func() proto.Message { return buildApiActivityUnauthorized() }},
		{"EntityManagement/Create", func() proto.Message { return buildEntityManagement() }},
	}

	networkUnavailable := false

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if networkUnavailable {
				t.Skip("schema.ocsf.io is unreachable; skipping remaining conformance cases")
				return
			}

			eventJSON, err := exporter.ToOCSFJSON(tc.builder())
			require.NoError(t, err, "ToOCSFJSON must succeed")

			t.Logf("event JSON: %s", eventJSON)

			errCount, errs, err := validateAgainstOCSFServer(t, version, eventJSON)
			if err != nil {
				if isConnectivityError(err) {
					networkUnavailable = true
					t.Skipf("OCSF server unreachable (connectivity): %v\nManual: curl -s -X POST https://schema.ocsf.io/%s/api/v2/validate -H 'Content-Type: application/json' -d @event.json", err, version)
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

// ────────────────────────────────────────────────────────────────────────────
// JSON Schema validation (Go-side raw schema check)
// ────────────────────────────────────────────────────────────────────────────

// TestOCSFSchemaRequiredFields validates the exported JSON against the
// required-field list from the OCSF 1.8.0 server validate endpoint.  This is
// an offline, hermetic check that proves the exported events are not trivially
// wrong.
//
// cloud, disposition_id, and authorizations are profile-gated attributes.
// They are valid for these classes when the event declares the corresponding
// profiles in metadata.profiles ("cloud" and "security_control").  Our events
// do declare those profiles and populate the fields.
func TestOCSFSchemaRequiredFields(t *testing.T) {
	// Required fields per class, as enforced by the OCSF 1.8.0 validate endpoint.
	apiActivityRequired := []string{
		"activity_id",
		"actor",
		"api",
		"category_uid",
		"class_uid",
		"metadata",
		// "osint" is required in the schema but our events have an empty list, which
		// the exporter omits (proto3 empty repeated = zero value = omitted).
		// The OCSF JSON Schema allows the field to be absent when the list is empty,
		// so we do not assert its presence here.
		"severity_id",
		"src_endpoint",
		"time",
		"type_uid",
	}

	entityManagementRequired := []string{
		"activity_id",
		"category_uid",
		"class_uid",
		"entity",
		"metadata",
		// "osint" — same note as above.
		"severity_id",
		"time",
		"type_uid",
	}

	t.Run("ApiActivity/Allowed", func(t *testing.T) {
		b, err := exporter.ToOCSFJSON(buildApiActivityAllowed())
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		for _, field := range apiActivityRequired {
			require.Contains(t, m, field, "required field %q must be present in api_activity JSON", field)
		}
	})

	t.Run("ApiActivity/Unauthorized", func(t *testing.T) {
		b, err := exporter.ToOCSFJSON(buildApiActivityUnauthorized())
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		for _, field := range apiActivityRequired {
			require.Contains(t, m, field, "required field %q must be present in api_activity JSON", field)
		}
	})

	t.Run("EntityManagement/Create", func(t *testing.T) {
		b, err := exporter.ToOCSFJSON(buildEntityManagement())
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		for _, field := range entityManagementRequired {
			require.Contains(t, m, field, "required field %q must be present in entity_management JSON", field)
		}
	})
}

// TestRawJSONBytes verifies key byte-level invariants: integer enums, unquoted
// int64 timestamps, and snake_case keys.  These are the three divergence
// points between ToOCSFJSON and stock protojson.
func TestRawJSONBytes(t *testing.T) {
	t.Run("ApiActivity", func(t *testing.T) {
		b, err := exporter.ToOCSFJSON(buildApiActivityAllowed())
		require.NoError(t, err)

		// class_uid must be the integer 6003, not a string or the enum name.
		require.True(t, bytes.Contains(b, []byte(`"class_uid":6003`)),
			"class_uid must be emitted as integer 6003; raw: %s", b)
		// time must be an unquoted integer.
		require.True(t, bytes.Contains(b, []byte(`"time":1751313600000`)),
			"time must be an unquoted integer; raw: %s", b)
		// No camelCase keys in the output.
		require.False(t, bytes.Contains(b, []byte(`classUid`)),
			"camelCase key classUid must not appear; raw: %s", b)
		// status_id = 1 as integer (SUCCESS).
		require.True(t, bytes.Contains(b, []byte(`"status_id":1`)),
			"status_id must be emitted as integer 1 (Success); raw: %s", b)
	})

	t.Run("ApiActivity/Unauthorized", func(t *testing.T) {
		b, err := exporter.ToOCSFJSON(buildApiActivityUnauthorized())
		require.NoError(t, err)

		// disposition_id = 26 (UNAUTHORIZED).
		require.True(t, bytes.Contains(b, []byte(`"disposition_id":26`)),
			"disposition_id must be 26 (Unauthorized); raw: %s", b)
		// status_id = 2 (FAILURE).
		require.True(t, bytes.Contains(b, []byte(`"status_id":2`)),
			"status_id must be 2 (Failure); raw: %s", b)
	})

	t.Run("EntityManagement", func(t *testing.T) {
		b, err := exporter.ToOCSFJSON(buildEntityManagement())
		require.NoError(t, err)

		require.True(t, bytes.Contains(b, []byte(`"class_uid":3004`)),
			"class_uid must be 3004; raw: %s", b)
		require.True(t, bytes.Contains(b, []byte(`"category_uid":3`)),
			"category_uid must be 3; raw: %s", b)
	})
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

// requireJSONNumber asserts that m[key] is a JSON number equal to want.
func requireJSONNumber(t *testing.T, m map[string]any, key string, want float64) {
	t.Helper()
	raw, ok := m[key]
	require.True(t, ok, "key %q must be present", key)
	f, isFloat := raw.(float64)
	require.True(t, isFloat, "key %q must be a JSON number, got %T", key, raw)
	require.Equal(t, want, f, "key %q must equal %.0f", key, want)
}

// requireJSONNumberPresent asserts that m[key] exists and is a JSON number.
func requireJSONNumberPresent(t *testing.T, m map[string]any, key string) {
	t.Helper()
	raw, ok := m[key]
	require.True(t, ok, "key %q must be present", key)
	_, isFloat := raw.(float64)
	require.True(t, isFloat, "key %q must be a JSON number, got %T", key, raw)
}
