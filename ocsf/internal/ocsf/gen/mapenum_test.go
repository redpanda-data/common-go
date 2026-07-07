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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/gen"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/schema"
)

// synEnum builds a synthetic schema.Attribute with an inline enum for tests
// that do not need a real fixture attribute.
func synEnum(members []schema.EnumMember) []schema.EnumMember {
	return members
}

// TestMapEnum_SeverityID verifies the severity_id enum from the base event
// fixture.  Expected values:
//
//	0  Unknown
//	1  Informational
//	2  Low
//	3  Medium
//	4  High
//	5  Critical
//	6  Fatal
//	99 Other
func TestMapEnum_SeverityID(t *testing.T) {
	s := loadFixture(t)

	attr, ok := s.BaseEvent.Attributes["severity_id"]
	require.True(t, ok, "severity_id must exist on base_event")
	require.NotEmpty(t, attr.Enum, "severity_id must have enum members")

	pe, isProto, err := gen.MapEnum("SeverityId", attr.Enum)
	require.NoError(t, err)
	require.True(t, isProto, "severity_id is integer-keyed so must be a proto enum")
	require.False(t, pe.SynthesizedZero, "severity_id has a 0 member so no synthesized zero")
	require.Equal(t, "SeverityId", pe.Name)

	// Zero value must be first.
	require.Equal(t, int32(0), pe.Values[0].Number)
	require.Equal(t, "SEVERITY_ID_UNKNOWN", pe.Values[0].Ident)

	// Other must be present at 99.
	last := pe.Values[len(pe.Values)-1]
	require.Equal(t, int32(99), last.Number)
	require.Equal(t, "SEVERITY_ID_OTHER", last.Ident)

	// Strictly ascending order.
	for i := 1; i < len(pe.Values); i++ {
		require.Greater(t, pe.Values[i].Number, pe.Values[i-1].Number,
			"values must be in ascending order")
	}

	// isProtoEnum=true assertion already done above.

	// Check a mid-range value.
	found := false
	for _, v := range pe.Values {
		if v.Number == 4 {
			require.Equal(t, "SEVERITY_ID_HIGH", v.Ident)
			found = true
		}
	}
	require.True(t, found, "SEVERITY_ID_HIGH (4) must be present")
}

// TestMapEnum_ActivityID verifies the activity_id enum from api_activity.
// Expected values: 0 Unknown, 1 Create, 2 Read, 3 Update, 4 Delete, 99 Other.
func TestMapEnum_ActivityID(t *testing.T) {
	s := loadFixture(t)

	cls, ok := s.Classes["api_activity"]
	require.True(t, ok, "api_activity must exist")

	attr, ok := cls.Attributes["activity_id"]
	require.True(t, ok, "activity_id must exist on api_activity")
	require.NotEmpty(t, attr.Enum, "activity_id must have enum members")

	pe, isProto, err := gen.MapEnum("ActivityId", attr.Enum)
	require.NoError(t, err)
	require.True(t, isProto)
	require.False(t, pe.SynthesizedZero)
	require.Equal(t, "ActivityId", pe.Name)

	// Verify all expected idents are present with correct numbers.
	want := map[int32]string{
		0:  "ACTIVITY_ID_UNKNOWN",
		1:  "ACTIVITY_ID_CREATE",
		2:  "ACTIVITY_ID_READ",
		3:  "ACTIVITY_ID_UPDATE",
		4:  "ACTIVITY_ID_DELETE",
		99: "ACTIVITY_ID_OTHER",
	}
	require.Len(t, pe.Values, len(want))

	got := make(map[int32]string, len(pe.Values))
	for _, v := range pe.Values {
		got[v.Number] = v.Ident
	}
	require.Equal(t, want, got)

	// Ascending order.
	for i := 1; i < len(pe.Values); i++ {
		require.Greater(t, pe.Values[i].Number, pe.Values[i-1].Number)
	}
}

// TestMapEnum_StringKeyed verifies that a string-keyed enum returns
// isProtoEnum=false and no error.
func TestMapEnum_StringKeyed(t *testing.T) {
	members := synEnum([]schema.EnumMember{
		{StrKey: "GREEN", Caption: "Green", IntKey: false},
		{StrKey: "AMBER", Caption: "Amber", IntKey: false},
		{StrKey: "RED", Caption: "Red", IntKey: false},
	})

	pe, isProto, err := gen.MapEnum("Tlp", members)
	require.NoError(t, err)
	require.False(t, isProto, "string-keyed enum must not be a proto enum")
	require.Zero(t, pe, "ProtoEnum must be zero value when isProtoEnum=false")
}

// TestMapEnum_SynthesizedZero verifies that when an integer-keyed enum has no
// 0 member, a zero value named <PREFIX>_UNKNOWN is prepended and
// SynthesizedZero is set.
func TestMapEnum_SynthesizedZero(t *testing.T) {
	members := synEnum([]schema.EnumMember{
		{Key: 1, Caption: "Alpha", IntKey: true},
		{Key: 2, Caption: "Beta", IntKey: true},
		{Key: 99, Caption: "Other", IntKey: true},
	})

	pe, isProto, err := gen.MapEnum("MyEnum", members)
	require.NoError(t, err)
	require.True(t, isProto)
	require.True(t, pe.SynthesizedZero, "must synthesize a zero value when none exists")

	// Zero value must be first.
	require.Equal(t, int32(0), pe.Values[0].Number)
	require.Equal(t, "MY_ENUM_UNKNOWN", pe.Values[0].Ident)

	// Rest in ascending order following the synthesized zero.
	require.Equal(t, int32(1), pe.Values[1].Number)
	require.Equal(t, int32(2), pe.Values[2].Number)
	require.Equal(t, int32(99), pe.Values[3].Number)
}

// TestMapEnum_DuplicateIdents verifies deterministic de-duplication when two
// captions sanitize to the same identifier.  "Alpha-1" and "Alpha 1" both
// sanitize to "ALPHA_1", so the second must become "ALPHA_1_2".
func TestMapEnum_DuplicateIdents(t *testing.T) {
	members := synEnum([]schema.EnumMember{
		{Key: 0, Caption: "Unknown", IntKey: true},
		{Key: 1, Caption: "Alpha-1", IntKey: true},
		{Key: 2, Caption: "Alpha 1", IntKey: true},
	})

	pe, isProto, err := gen.MapEnum("Thing", members)
	require.NoError(t, err)
	require.True(t, isProto)
	require.False(t, pe.SynthesizedZero)

	identsByNumber := make(map[int32]string, len(pe.Values))
	for _, v := range pe.Values {
		identsByNumber[v.Number] = v.Ident
	}

	// Both must have distinct idents.
	i1 := identsByNumber[1]
	i2 := identsByNumber[2]
	require.NotEqual(t, i1, i2, "duplicate captions must produce distinct idents")

	// The earlier one (lower key, processed first in ascending order) keeps the
	// base ident; the collision gets the suffix.
	require.Equal(t, "THING_ALPHA_1", i1)
	require.Equal(t, "THING_ALPHA_1_2", i2)
}

// TestMapEnum_NegativeKey verifies that a negative integer key causes an error.
func TestMapEnum_NegativeKey(t *testing.T) {
	members := synEnum([]schema.EnumMember{
		{Key: -1, Caption: "Negative", IntKey: true},
		{Key: 0, Caption: "Unknown", IntKey: true},
	})

	_, _, err := gen.MapEnum("Bad", members)
	require.Error(t, err, "negative enum key must produce an error")
}

// TestMapEnum_Int32Overflow verifies that a key exceeding math.MaxInt32 causes
// an error instead of silently truncating (Fix 3).  OCSF key values come from
// long_t attributes which can technically exceed the int32 range.
func TestMapEnum_Int32Overflow(t *testing.T) {
	const overflowKey = int(1<<32 + 1) // 4_294_967_297 — well above MaxInt32
	members := synEnum([]schema.EnumMember{
		{Key: 0, Caption: "Unknown", IntKey: true},
		{Key: overflowKey, Caption: "TooBig", IntKey: true},
	})

	_, _, err := gen.MapEnum("Overflow", members)
	require.Error(t, err, "key > math.MaxInt32 must produce an error")
	require.Contains(t, err.Error(), "overflows int32")
}
