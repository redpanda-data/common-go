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

// mergedClasses is the class selection used across the merged-event tests.
var mergedClasses = []string{"api_activity", "entity_management"}

// TestMergeClasses_CleanUnion merges api_activity + entity_management (which
// have no semantic conflicts in OCSF 1.8.0) and verifies the union shape.
func TestMergeClasses_CleanUnion(t *testing.T) {
	s := loadFixture(t)

	m, err := gen.MergeClasses(s, mergedClasses, "AuditEvent")
	require.NoError(t, err)

	require.Equal(t, "AuditEvent", m.Name)
	require.Len(t, m.Classes, 2)
	require.Equal(t, "api_activity", m.Classes[0].Name)
	require.Equal(t, "entity_management", m.Classes[1].Name)

	// Union: every attribute of both classes is present exactly once.
	for _, cls := range m.Classes {
		for name := range cls.Attributes {
			require.Contains(t, m.Attributes, name, "attribute %q from class %q missing from merge", name, cls.Name)
		}
	}

	// Owners: base_event attributes are owned by both classes; class-specific
	// ones by exactly one. Note api is declared by BOTH classes (required in
	// api_activity, optional in entity_management).
	require.Equal(t, []string{"api_activity", "entity_management"}, m.Owners["metadata"])
	require.Equal(t, []string{"api_activity", "entity_management"}, m.Owners["api"])
	require.Equal(t, []string{"api_activity"}, m.Owners["trace"])
	require.Equal(t, []string{"entity_management"}, m.Owners["entity"])
}

// TestMergeClasses_ActivityIDAlwaysDemoted verifies the forced demotion:
// activity_id is a plain scalar even when the selected classes happen to merge
// cleanly (entity_management's enum is a strict superset of api_activity's).
func TestMergeClasses_ActivityIDAlwaysDemoted(t *testing.T) {
	s := loadFixture(t)

	m, err := gen.MergeClasses(s, mergedClasses, "AuditEvent")
	require.NoError(t, err)

	require.Contains(t, m.Demoted, "activity_id")
	require.Empty(t, m.Attributes["activity_id"].Enum, "demoted attribute must have no enum members")
}

// TestMergeClasses_ConflictingEnumDemotes verifies that a class-scoped enum
// whose values collide across classes is demoted rather than merged or failed.
// api_activity says activity_id 1 = Create; authentication says 1 = Logon.
// Beyond the forced activity_id case, this exercises the conflict-detection
// path with a synthetic attribute.
func TestMergeClasses_ConflictingEnumDemotes(t *testing.T) {
	s := syntheticSchema(
		t,
		map[string]*schema.Attribute{
			"verdict_id": {Name: "verdict_id", Type: "integer_t", Enum: []schema.EnumMember{
				{Key: 0, IntKey: true, Caption: "Unknown"},
				{Key: 1, IntKey: true, Caption: "Pass"},
			}},
		},
		map[string]*schema.Attribute{
			"verdict_id": {Name: "verdict_id", Type: "integer_t", Enum: []schema.EnumMember{
				{Key: 0, IntKey: true, Caption: "Unknown"},
				{Key: 1, IntKey: true, Caption: "Fail"}, // same key, different meaning
			}},
		},
	)

	m, err := gen.MergeClasses(s, []string{"alpha", "beta"}, "AuditEvent")
	require.NoError(t, err)
	require.Contains(t, m.Demoted, "verdict_id")
	require.Empty(t, m.Attributes["verdict_id"].Enum)
}

// TestMergeClasses_EnumSupersetUnions verifies that non-conflicting enums
// union: shared values agree, extra values are kept.
func TestMergeClasses_EnumSupersetUnions(t *testing.T) {
	s := syntheticSchema(
		t,
		map[string]*schema.Attribute{
			"state_id": {Name: "state_id", Type: "integer_t", Enum: []schema.EnumMember{
				{Key: 0, IntKey: true, Caption: "Unknown"},
				{Key: 1, IntKey: true, Caption: "Active"},
			}},
		},
		map[string]*schema.Attribute{
			"state_id": {Name: "state_id", Type: "integer_t", Enum: []schema.EnumMember{
				{Key: 0, IntKey: true, Caption: "Unknown"},
				{Key: 1, IntKey: true, Caption: "Active"},
				{Key: 2, IntKey: true, Caption: "Suspended"},
			}},
		},
	)

	m, err := gen.MergeClasses(s, []string{"alpha", "beta"}, "AuditEvent")
	require.NoError(t, err)
	require.NotContains(t, m.Demoted, "state_id")
	require.Len(t, m.Attributes["state_id"].Enum, 3)
}

// TestMergeClasses_EnumPresenceMismatchDemotes verifies that an attribute with
// an enum in one class and none in another is demoted.
func TestMergeClasses_EnumPresenceMismatchDemotes(t *testing.T) {
	s := syntheticSchema(
		t,
		map[string]*schema.Attribute{
			"state_id": {Name: "state_id", Type: "integer_t", Enum: []schema.EnumMember{
				{Key: 0, IntKey: true, Caption: "Unknown"},
			}},
		},
		map[string]*schema.Attribute{
			"state_id": {Name: "state_id", Type: "integer_t"},
		},
	)

	m, err := gen.MergeClasses(s, []string{"alpha", "beta"}, "AuditEvent")
	require.NoError(t, err)
	require.Contains(t, m.Demoted, "state_id")
}

// TestMergeClasses_StringKeyedEnumConflictDemotes exercises the string-keyed
// (StrKey) branch of the enum union: same key, different caption demotes.
func TestMergeClasses_StringKeyedEnumConflictDemotes(t *testing.T) {
	s := syntheticSchema(
		t,
		map[string]*schema.Attribute{
			"tlp": {Name: "tlp", Type: "string_t", Enum: []schema.EnumMember{
				{StrKey: "RED", Caption: "Red"},
				{StrKey: "AMBER", Caption: "Amber"},
			}},
		},
		map[string]*schema.Attribute{
			"tlp": {Name: "tlp", Type: "string_t", Enum: []schema.EnumMember{
				{StrKey: "RED", Caption: "Restricted"}, // same key, different meaning
			}},
		},
	)

	m, err := gen.MergeClasses(s, []string{"alpha", "beta"}, "AuditEvent")
	require.NoError(t, err)
	require.Contains(t, m.Demoted, "tlp")
	require.Empty(t, m.Attributes["tlp"].Enum)
}

// TestMergeClasses_StringKeyedEnumUnions verifies non-conflicting string-keyed
// enums union by key.
func TestMergeClasses_StringKeyedEnumUnions(t *testing.T) {
	s := syntheticSchema(
		t,
		map[string]*schema.Attribute{
			"tlp": {Name: "tlp", Type: "string_t", Enum: []schema.EnumMember{
				{StrKey: "RED", Caption: "Red"},
			}},
		},
		map[string]*schema.Attribute{
			"tlp": {Name: "tlp", Type: "string_t", Enum: []schema.EnumMember{
				{StrKey: "RED", Caption: "Red"},
				{StrKey: "GREEN", Caption: "Green"},
			}},
		},
	)

	m, err := gen.MergeClasses(s, []string{"alpha", "beta"}, "AuditEvent")
	require.NoError(t, err)
	require.NotContains(t, m.Demoted, "tlp")
	require.Len(t, m.Attributes["tlp"].Enum, 2)
}

// TestMergeClasses_MixedEnumKindDemotes exercises the kind-mismatch branch:
// one class integer-keyed, the other string-keyed for the same attribute.
func TestMergeClasses_MixedEnumKindDemotes(t *testing.T) {
	s := syntheticSchema(
		t,
		map[string]*schema.Attribute{
			"state": {Name: "state", Type: "string_t", Enum: []schema.EnumMember{
				{Key: 1, IntKey: true, Caption: "Active"},
			}},
		},
		map[string]*schema.Attribute{
			"state": {Name: "state", Type: "string_t", Enum: []schema.EnumMember{
				{StrKey: "ACTIVE", Caption: "Active"},
			}},
		},
	)

	m, err := gen.MergeClasses(s, []string{"alpha", "beta"}, "AuditEvent")
	require.NoError(t, err)
	require.Contains(t, m.Demoted, "state")
}

// TestMergeClasses_TypeMismatchErrors verifies that the same attribute name
// with different OCSF types across classes is a hard error.
func TestMergeClasses_TypeMismatchErrors(t *testing.T) {
	s := syntheticSchema(
		t,
		map[string]*schema.Attribute{
			"payload": {Name: "payload", Type: "string_t"},
		},
		map[string]*schema.Attribute{
			"payload": {Name: "payload", Type: "integer_t"},
		},
	)

	_, err := gen.MergeClasses(s, []string{"alpha", "beta"}, "AuditEvent")
	require.ErrorContains(t, err, `attribute "payload"`)
	require.ErrorContains(t, err, "conflicts")
}

// TestMergeClasses_RequiredOnlyWhenRequiredEverywhere verifies the requirement
// merge rule with the real schema: metadata is required in both classes, actor
// is required only in api_activity.
func TestMergeClasses_RequiredOnlyWhenRequiredEverywhere(t *testing.T) {
	s := loadFixture(t)

	m, err := gen.MergeClasses(s, mergedClasses, "AuditEvent")
	require.NoError(t, err)

	require.Equal(t, "required", m.Attributes["metadata"].Requirement)
	require.Equal(t, "required", m.Attributes["class_uid"].Requirement)
	// actor: required in api_activity, weaker in entity_management.
	require.Equal(t, "recommended", m.Attributes["actor"].Requirement)
	// api: required in api_activity, absent from entity_management.
	require.Equal(t, "recommended", m.Attributes["api"].Requirement)
}

// TestMergeClasses_UnknownClass verifies the error for a class missing from
// the schema.
func TestMergeClasses_UnknownClass(t *testing.T) {
	s := loadFixture(t)
	_, err := gen.MergeClasses(s, []string{"api_activity", "no_such_class"}, "AuditEvent")
	require.ErrorContains(t, err, `"no_such_class"`)
}

// TestMergeClasses_ClassUIDUnion verifies the class_uid / category_uid /
// type_uid enums union disjoint values from every class.
func TestMergeClasses_ClassUIDUnion(t *testing.T) {
	s := loadFixture(t)

	m, err := gen.MergeClasses(s, mergedClasses, "AuditEvent")
	require.NoError(t, err)

	classUIDs := enumKeys(m.Attributes["class_uid"].Enum)
	require.Contains(t, classUIDs, 6003)
	require.Contains(t, classUIDs, 3004)

	catUIDs := enumKeys(m.Attributes["category_uid"].Enum)
	require.Contains(t, catUIDs, 6)
	require.Contains(t, catUIDs, 3)

	typeUIDs := enumKeys(m.Attributes["type_uid"].Enum)
	require.Contains(t, typeUIDs, 600301) // API Activity: Create
	require.Contains(t, typeUIDs, 300401) // Entity Management: Create
}

// syntheticSchema builds a minimal two-class schema for merge unit tests.
func syntheticSchema(t *testing.T, alphaAttrs, betaAttrs map[string]*schema.Attribute) *schema.Schema {
	t.Helper()
	return &schema.Schema{
		Version: "1.8.0",
		Classes: map[string]*schema.Class{
			"alpha": {Name: "alpha", UID: 1001, CategoryUID: 1, Attributes: alphaAttrs},
			"beta":  {Name: "beta", UID: 1002, CategoryUID: 1, Attributes: betaAttrs},
		},
		Objects: map[string]*schema.Object{},
		Types: map[string]*schema.TypeDef{
			"string_t":  {},
			"integer_t": {},
		},
	}
}

// enumKeys extracts the integer keys of an enum member slice.
func enumKeys(members []schema.EnumMember) []int {
	keys := make([]int, 0, len(members))
	for _, m := range members {
		keys = append(keys, m.Key)
	}
	return keys
}
