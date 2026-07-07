// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package gen_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/gen"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/schema"
)

const fixtureFile = "../schema/testdata/ocsf-1.8.0.json"

func loadFixture(t *testing.T) *schema.Schema {
	t.Helper()
	f, err := os.Open(fixtureFile)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	s, err := schema.Load(f)
	require.NoError(t, err)
	return s
}

// deref dereferences a *schema.Attribute map lookup, failing the test if absent.
func deref(t *testing.T, m map[string]*schema.Attribute, key string) schema.Attribute {
	t.Helper()
	a, ok := m[key]
	require.True(t, ok, "attribute %q must exist", key)
	return *a
}

// syn builds a minimal synthetic Attribute for type-mapping tests.
// Used when a specific primitive isn't present on the classes under test.
func syn(typeName string, objectType string, isArray bool) schema.Attribute {
	return schema.Attribute{
		Type:       typeName,
		ObjectType: objectType,
		IsArray:    isArray,
	}
}

// TestMapType is a table-driven test for MapType.
//
// Most cases are grounded in real OCSF 1.8.0 fixture attributes so that the
// mapping is verified against actual schema data. Synthetic attributes are
// used only where a specific primitive type does not appear directly on
// api_activity, entity_management, network_endpoint, or location — the four
// classes/objects inspected here — and where the attribute path is documented.
func TestMapType(t *testing.T) {
	s := loadFixture(t)

	apiActivity := s.Classes["api_activity"].Attributes
	networkEndpoint := s.Objects["network_endpoint"].Attributes
	location := s.Objects["location"].Attributes
	enrichment := s.Objects["enrichment"].Attributes

	tests := []struct {
		name    string
		attr    schema.Attribute
		want    gen.ProtoType
		wantErr bool
	}{
		// ----------------------------------------------------------------
		// timestamp_t -> int64  (base chain: timestamp_t -> long_t)
		// Fixture: api_activity.time
		// ----------------------------------------------------------------
		{
			name: "timestamp_t -> int64",
			attr: deref(t, apiActivity, "time"),
			want: gen.ProtoType{Scalar: "int64"},
		},

		// ----------------------------------------------------------------
		// string_t -> string  (base type, no chain)
		// Fixture: api_activity.action
		// ----------------------------------------------------------------
		{
			name: "string_t -> string",
			attr: deref(t, apiActivity, "action"),
			want: gen.ProtoType{Scalar: "string"},
		},

		// ----------------------------------------------------------------
		// integer_t -> int32  (base type)
		// Fixture: api_activity.action_id
		// ----------------------------------------------------------------
		{
			name: "integer_t -> int32",
			attr: deref(t, apiActivity, "action_id"),
			want: gen.ProtoType{Scalar: "int32"},
		},

		// ----------------------------------------------------------------
		// long_t -> int64  (base type)
		// Fixture: api_activity.type_uid
		// ----------------------------------------------------------------
		{
			name: "long_t -> int64",
			attr: deref(t, apiActivity, "type_uid"),
			want: gen.ProtoType{Scalar: "int64"},
		},

		// ----------------------------------------------------------------
		// boolean_t -> bool  (base type)
		// Fixture: api_activity.is_alert
		// ----------------------------------------------------------------
		{
			name: "boolean_t -> bool",
			attr: deref(t, apiActivity, "is_alert"),
			want: gen.ProtoType{Scalar: "bool"},
		},

		// ----------------------------------------------------------------
		// datetime_t -> string  (base chain: datetime_t -> string_t)
		// Fixture: api_activity.end_time_dt
		// ----------------------------------------------------------------
		{
			name: "datetime_t -> string",
			attr: deref(t, apiActivity, "end_time_dt"),
			want: gen.ProtoType{Scalar: "string"},
		},

		// ----------------------------------------------------------------
		// object_t / object_type=actor -> Message "actor"
		// Fixture: api_activity.actor
		// ----------------------------------------------------------------
		{
			name: "object_t/actor -> Message",
			attr: deref(t, apiActivity, "actor"),
			want: gen.ProtoType{Message: "actor"},
		},

		// ----------------------------------------------------------------
		// object_t is_array=true -> Repeated Message
		// Fixture: api_activity.authorizations
		// ----------------------------------------------------------------
		{
			name: "object_t is_array -> Repeated Message",
			attr: deref(t, apiActivity, "authorizations"),
			want: gen.ProtoType{Message: "authorization", Repeated: true},
		},

		// ----------------------------------------------------------------
		// object_t is_array=true (another array case)
		// Fixture: api_activity.observables
		// ----------------------------------------------------------------
		{
			name: "observables is_array -> Repeated Message",
			attr: deref(t, apiActivity, "observables"),
			want: gen.ProtoType{Message: "observable", Repeated: true},
		},

		// ----------------------------------------------------------------
		// unmapped -> object_type="object" (the generic OCSF Object).
		// The generic "object" type represents an unordered bag of arbitrary
		// attributes — equivalent to a free-form JSON object.  We map it to
		// google.protobuf.Value so generated protos can hold arbitrary payloads.
		// Fixture: api_activity.unmapped
		// ----------------------------------------------------------------
		{
			name: "unmapped generic object -> WellKnown Value",
			attr: deref(t, apiActivity, "unmapped"),
			want: gen.ProtoType{WellKnown: "google.protobuf.Value"},
		},

		// ----------------------------------------------------------------
		// ip_t -> string  (base chain: ip_t -> string_t)
		// Fixture: network_endpoint.ip
		// ----------------------------------------------------------------
		{
			name: "ip_t -> string",
			attr: deref(t, networkEndpoint, "ip"),
			want: gen.ProtoType{Scalar: "string"},
		},

		// ----------------------------------------------------------------
		// mac_t -> string  (base chain: mac_t -> string_t)
		// Fixture: network_endpoint.mac
		// ----------------------------------------------------------------
		{
			name: "mac_t -> string",
			attr: deref(t, networkEndpoint, "mac"),
			want: gen.ProtoType{Scalar: "string"},
		},

		// ----------------------------------------------------------------
		// port_t -> int32  (base chain: port_t -> integer_t)
		// Fixture: network_endpoint.port
		// ----------------------------------------------------------------
		{
			name: "port_t -> int32",
			attr: deref(t, networkEndpoint, "port"),
			want: gen.ProtoType{Scalar: "int32"},
		},

		// ----------------------------------------------------------------
		// ip_t is_array=true -> Repeated string
		// Fixture: network_endpoint.intermediate_ips
		// ----------------------------------------------------------------
		{
			name: "ip_t is_array -> Repeated string",
			attr: deref(t, networkEndpoint, "intermediate_ips"),
			want: gen.ProtoType{Scalar: "string", Repeated: true},
		},

		// ----------------------------------------------------------------
		// hostname_t -> string  (base chain: hostname_t -> string_t)
		// Fixture: network_endpoint.hostname
		// ----------------------------------------------------------------
		{
			name: "hostname_t -> string",
			attr: deref(t, networkEndpoint, "hostname"),
			want: gen.ProtoType{Scalar: "string"},
		},

		// ----------------------------------------------------------------
		// float_t -> double  (base type)
		// Fixture: location.lat
		// ----------------------------------------------------------------
		{
			name: "float_t -> double",
			attr: deref(t, location, "lat"),
			want: gen.ProtoType{Scalar: "double"},
		},

		// ----------------------------------------------------------------
		// float_t is_array=true -> Repeated double
		// Fixture: location.coordinates
		// ----------------------------------------------------------------
		{
			name: "float_t is_array -> Repeated double",
			attr: deref(t, location, "coordinates"),
			want: gen.ProtoType{Scalar: "double", Repeated: true},
		},

		// ----------------------------------------------------------------
		// json_t -> google.protobuf.Value  (base type, no chain)
		// Fixture: enrichment.data
		// ----------------------------------------------------------------
		{
			name: "json_t -> WellKnown Value",
			attr: deref(t, enrichment, "data"),
			want: gen.ProtoType{WellKnown: "google.protobuf.Value"},
		},

		// ----------------------------------------------------------------
		// uuid_t -> string  (base chain: uuid_t -> string_t)
		// Synthetic: uuid_t does not appear on the classes above but is a
		// common derived string type that must follow the chain rule.
		// ----------------------------------------------------------------
		{
			name: "uuid_t -> string (synthetic)",
			attr: syn("uuid_t", "", false),
			want: gen.ProtoType{Scalar: "string"},
		},

		// ----------------------------------------------------------------
		// Error case: unknown type
		// ----------------------------------------------------------------
		{
			name:    "unknown type -> error",
			attr:    syn("totally_unknown_t", "", false),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := gen.MapType(s, tc.attr)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
