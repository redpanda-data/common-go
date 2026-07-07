// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package schema_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/schema"
)

const fixtureFile = "testdata/ocsf-1.8.0.json"

func loadFixture(t *testing.T) *schema.Schema {
	t.Helper()
	f, err := os.Open(fixtureFile)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	s, err := schema.Load(f)
	require.NoError(t, err)
	return s
}

// TestLoad verifies that the compiled OCSF 1.8.0 fixture can be parsed without error.
func TestLoad(t *testing.T) {
	s := loadFixture(t)
	require.NotNil(t, s)
	require.Equal(t, "1.8.0", s.Version)
}

// TestAPIActivityClass verifies class_uid and category_uid for api_activity.
func TestAPIActivityClass(t *testing.T) {
	s := loadFixture(t)

	cls, ok := s.Classes["api_activity"]
	require.True(t, ok, "api_activity class must be present")

	// uid in the export equals class_uid (category_uid*1000 + within-category-uid).
	require.Equal(t, 6003, cls.UID, "api_activity UID must be 6003")
	require.Equal(t, 6, cls.CategoryUID, "api_activity CategoryUID must be 6")
}

// TestAPIActivitySecurityControlProfileAttrs verifies that the compiled export has
// merged the security_control profile attributes into api_activity.
func TestAPIActivitySecurityControlProfileAttrs(t *testing.T) {
	s := loadFixture(t)

	cls, ok := s.Classes["api_activity"]
	require.True(t, ok)

	_, hasDispositionID := cls.Attributes["disposition_id"]
	require.True(t, hasDispositionID, "api_activity must have disposition_id from security_control profile")

	_, hasAuthorizations := cls.Attributes["authorizations"]
	require.True(t, hasAuthorizations, "api_activity must have authorizations from security_control profile")
}

// TestEntityManagementClass verifies class_uid and category_uid for entity_management.
func TestEntityManagementClass(t *testing.T) {
	s := loadFixture(t)

	cls, ok := s.Classes["entity_management"]
	require.True(t, ok, "entity_management class must be present")

	require.Equal(t, 3004, cls.UID, "entity_management UID must be 3004")
	require.Equal(t, 3, cls.CategoryUID, "entity_management CategoryUID must be 3")
}

// TestUserObjectHasNestedType verifies that the user object has at least one attribute
// whose type references another object (i.e., is itself a complex/object type).
func TestUserObjectHasNestedType(t *testing.T) {
	s := loadFixture(t)

	user, ok := s.Objects["user"]
	require.True(t, ok, "user object must be present")

	foundObjectRef := false
	for _, attr := range user.Attributes {
		if attr.ObjectType != "" {
			foundObjectRef = true
			// Verify the referenced object actually exists in the schema.
			_, exists := s.Objects[attr.ObjectType]
			require.True(t, exists, "user attribute %q references object %q which must exist in schema", attr.Name, attr.ObjectType)
			break
		}
	}
	require.True(t, foundObjectRef, "user object must have at least one attribute that references another object")
}

// TestTypeDefBaseLink verifies that derived types expose their base type
// and that primitive base types have an empty base.
func TestTypeDefBaseLink(t *testing.T) {
	s := loadFixture(t)

	// ip_t is a derived string type: its base must be "string_t".
	ipT, ok := s.Types["ip_t"]
	require.True(t, ok, "ip_t must be present in schema types")
	require.Equal(t, "string_t", ipT.Type, "ip_t must have base type string_t")

	// timestamp_t is a derived long type: its base must be "long_t".
	tsT, ok := s.Types["timestamp_t"]
	require.True(t, ok, "timestamp_t must be present in schema types")
	require.Equal(t, "long_t", tsT.Type, "timestamp_t must have base type long_t")

	// string_t is a base type: no base.
	strT, ok := s.Types["string_t"]
	require.True(t, ok, "string_t must be present in schema types")
	require.Empty(t, strT.Type, "string_t must have no base type")

	// long_t is a base type: no base.
	longT, ok := s.Types["long_t"]
	require.True(t, ok, "long_t must be present in schema types")
	require.Empty(t, longT.Type, "long_t must have no base type")
}

// TestSchema_ConstraintsLoaded verifies that the schema loader exposes
// constraints on objects (at_least_one and just_one).
func TestSchema_ConstraintsLoaded(t *testing.T) {
	s := loadFixture(t)

	user, ok := s.Objects["user"]
	require.True(t, ok)
	require.NotNil(t, user.Constraints, "user object must have constraints")
	require.Contains(t, user.Constraints.AtLeastOne, "name")
	require.Contains(t, user.Constraints.AtLeastOne, "uid")

	vuln, ok := s.Objects["vulnerability"]
	require.True(t, ok)
	require.NotNil(t, vuln.Constraints)
	require.Contains(t, vuln.Constraints.JustOne, "cve")
}

// TestAiModelAndMessageContextAreRealObjects asserts that ai_model and
// message_context load as real objects with attributes, not empty stubs.
// Both were absent from the hand-assembled fixture (ocsf-1.8.0.json ≤ v1)
// and must be present with attributes in the v2 export.
func TestAiModelAndMessageContextAreRealObjects(t *testing.T) {
	s := loadFixture(t)

	aiModel, ok := s.Objects["ai_model"]
	require.True(t, ok, "ai_model must be present in schema objects")
	require.NotEmpty(t, aiModel.Attributes, "ai_model must have at least one attribute (not a stub)")

	msgCtx, ok := s.Objects["message_context"]
	require.True(t, ok, "message_context must be present in schema objects")
	require.NotEmpty(t, msgCtx.Attributes, "message_context must have at least one attribute (not a stub)")
}

// TestLoadRejectsMissingVersion verifies that Load returns a descriptive error
// when the schema JSON has an empty (or absent) version field.
func TestLoadRejectsMissingVersion(t *testing.T) {
	raw := `{"version":"","classes":{},"objects":{},"dictionary":{"attributes":{},"types":{"attributes":{}}}}`
	_, err := schema.Load(strings.NewReader(raw))
	require.Error(t, err)
	require.Contains(t, err.Error(), "version")
}

// TestEnumOrderedAscending verifies that integer-keyed enum members are
// returned in ascending key order regardless of JSON key ordering, and that
// the first member has key 0 (Unknown).
func TestEnumOrderedAscending(t *testing.T) {
	s := loadFixture(t)

	cls, ok := s.Classes["api_activity"]
	require.True(t, ok)

	activityID, ok := cls.Attributes["activity_id"]
	require.True(t, ok, "api_activity must have activity_id attribute")
	require.NotEmpty(t, activityID.Enum, "activity_id must have enum members")

	// activity_id uses integer keys.
	require.True(t, activityID.Enum[0].IntKey, "activity_id enum must use integer keys")

	// Key 0 (Unknown) must be first.
	require.Equal(t, 0, activityID.Enum[0].Key, "first enum member must have key 0 (Unknown)")

	// Verify strictly ascending order.
	for i := 1; i < len(activityID.Enum); i++ {
		require.Greater(
			t,
			activityID.Enum[i].Key,
			activityID.Enum[i-1].Key,
			"enum members must be in ascending key order",
		)
	}
}
