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
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/gen"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/schema"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/tagmap"
)

// -update regenerates golden files when passed to `go test`.
var update = flag.Bool("update", false, "regenerate golden files")

// goldenPath is the directory for golden .proto files.
const goldenDir = "testdata/golden"

// TestSelectClosure_ApiActivity verifies that SelectClosure returns the api_activity
// class plus the full transitive object closure, all sorted deterministically.
func TestSelectClosure_ApiActivity(t *testing.T) {
	s := loadFixture(t)

	classes, objects, err := gen.SelectClosure(s, []string{"api_activity"})
	require.NoError(t, err)
	require.Len(t, classes, 1)
	require.Equal(t, "api_activity", classes[0].Name)

	// There must be at least one object (actor is directly referenced).
	require.NotEmpty(t, objects)

	// actor must be in closure because api_activity.actor references it.
	actorFound := false
	for _, o := range objects {
		if o.Name == "actor" {
			actorFound = true
		}
	}
	require.True(t, actorFound, "actor object must be in transitive closure")

	// Objects must be sorted by name.
	for i := 1; i < len(objects); i++ {
		require.Less(t, objects[i-1].Name, objects[i].Name,
			"objects must be sorted by name")
	}
}

// TestSelectClosure_UnknownClass verifies that requesting a non-existent class
// returns a descriptive error.
func TestSelectClosure_UnknownClass(t *testing.T) {
	s := loadFixture(t)
	_, _, err := gen.SelectClosure(s, []string{"nonexistent_class_xyz"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonexistent_class_xyz")
}

// TestSelectClosure_Deterministic verifies that two SelectClosure calls with the
// same inputs produce identical results.
func TestSelectClosure_Deterministic(t *testing.T) {
	s := loadFixture(t)

	classes1, objects1, err := gen.SelectClosure(s, []string{"api_activity"})
	require.NoError(t, err)

	classes2, objects2, err := gen.SelectClosure(s, []string{"api_activity"})
	require.NoError(t, err)

	require.Equal(t, len(classes1), len(classes2))
	require.Equal(t, len(objects1), len(objects2))
	for i := range objects1 {
		require.Equal(t, objects1[i].Name, objects2[i].Name)
	}
}

// TestEmit_PackageAndSyntax verifies the proto3 header of the emitted file.
func TestEmit_PackageAndSyntax(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	out, _, err := gen.Emit(s, []string{"api_activity"}, tm, "1.8.0")
	require.NoError(t, err)

	require.Contains(t, out, `syntax = "proto3";`)
	require.Contains(t, out, `package ocsf.v1;`)
}

// minimalSchema returns a minimal schema suitable for package-name tests.
func minimalSchema(version string) *schema.Schema {
	return &schema.Schema{
		Version: version,
		Classes: map[string]*schema.Class{
			"stub_event": {
				Name:       "stub_event",
				Attributes: map[string]*schema.Attribute{},
			},
		},
		Objects:              map[string]*schema.Object{},
		Types:                map[string]*schema.TypeDef{},
		DictionaryAttributes: map[string]*schema.DictAttr{},
		BaseEvent:            &schema.BaseEvent{Attributes: map[string]*schema.Attribute{}},
	}
}

// TestVersionToPackage_TableDriven verifies that Emit uses the correct proto
// package suffix for a variety of semver input strings.
//
// The expected package names follow proto versioning convention: only the OCSF
// major version is encoded in the package (1.8.0 → ocsf.v1, 2.0.0 → ocsf.v2).
func TestVersionToPackage_TableDriven(t *testing.T) {
	cases := []struct {
		version string
		wantPkg string // expected "package ocsf.<suffix>;" line; empty = error expected
		wantErr bool
	}{
		{version: "1.8.0", wantPkg: "package ocsf.v1;"},
		{version: "1.9.0-dev", wantPkg: "package ocsf.v1;"},
		{version: "1.9.0-rc.2", wantPkg: "package ocsf.v1;"},
		{version: "2.0.0", wantPkg: "package ocsf.v2;"},
		{version: "1", wantPkg: "package ocsf.v1;"},
		{version: "0.42.0", wantPkg: "package ocsf.v0;"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.version, func(t *testing.T) {
			s := minimalSchema(tc.version)
			tm := tagmap.New()
			out, _, err := gen.Emit(s, []string{"stub_event"}, tm, tc.version)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Contains(t, out, tc.wantPkg,
				"emitted proto must contain %q for version %q", tc.wantPkg, tc.version)
		})
	}
}

// TestEmit_MessageApiActivity verifies the ApiActivity message is emitted for
// the api_activity class.
func TestEmit_MessageApiActivity(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	out, _, err := gen.Emit(s, []string{"api_activity"}, tm, "1.8.0")
	require.NoError(t, err)

	require.Contains(t, out, "message ApiActivity {")
}

// TestEmit_NestedEnum verifies that an int-keyed enum attribute (activity_id)
// produces a nested enum named ActivityId inside ApiActivity, with a correct
// zero value ACTIVITY_ID_UNKNOWN = 0.
func TestEmit_NestedEnum(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	out, _, err := gen.Emit(s, []string{"api_activity"}, tm, "1.8.0")
	require.NoError(t, err)

	require.Contains(t, out, "enum ActivityId {")
	require.Contains(t, out, "ACTIVITY_ID_UNKNOWN = 0;")
}

// TestEmit_ObjectTypedField verifies that an object-typed attribute (actor)
// is emitted as a message reference.
func TestEmit_ObjectTypedField(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	out, _, err := gen.Emit(s, []string{"api_activity"}, tm, "1.8.0")
	require.NoError(t, err)

	// actor field should be "Actor actor = <N>;"
	require.Contains(t, out, "Actor actor =")
}

// TestEmit_RepeatedField verifies that an is_array attribute (authorizations)
// is emitted as repeated.
func TestEmit_RepeatedField(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	out, _, err := gen.Emit(s, []string{"api_activity"}, tm, "1.8.0")
	require.NoError(t, err)

	require.Contains(t, out, "repeated Authorization authorizations =")
}

// TestEmit_WellKnownImport verifies that when a json_t / generic-object field
// is present (api_activity.unmapped), the struct.proto import is emitted.
func TestEmit_WellKnownImport(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	out, _, err := gen.Emit(s, []string{"api_activity"}, tm, "1.8.0")
	require.NoError(t, err)

	require.Contains(t, out, `import "google/protobuf/struct.proto";`)
}

// TestEmit_RequiredFieldAnnotation verifies that a required attribute emits
// the protovalidate required annotation and triggers the validate.proto import.
func TestEmit_RequiredFieldAnnotation(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	out, _, err := gen.Emit(s, []string{"api_activity"}, tm, "1.8.0")
	require.NoError(t, err)

	// api_activity.time is required
	require.Contains(t, out, "(buf.validate.field).required = true")
	require.Contains(t, out, `import "buf/validate/validate.proto";`)
}

// TestEmit_StableTags verifies that field tags are stable across two Emit calls
// using a fresh tag map (i.e., the second call reuses tags assigned by the first
// because Assign is idempotent).
func TestEmit_StableTags(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	out1, _, err := gen.Emit(s, []string{"api_activity"}, tm, "1.8.0")
	require.NoError(t, err)

	out2, _, err := gen.Emit(s, []string{"api_activity"}, tm, "1.8.0")
	require.NoError(t, err)

	require.Equal(t, out1, out2, "two Emit calls with the same tag map must produce identical output")
}

// TestEmit_Deterministic verifies that two Emit calls with equivalent fresh tag
// maps produce byte-identical output.
func TestEmit_Deterministic(t *testing.T) {
	s := loadFixture(t)

	tm1 := tagmap.New()
	out1, _, err := gen.Emit(s, []string{"api_activity"}, tm1, "1.8.0")
	require.NoError(t, err)

	tm2 := tagmap.New()
	out2, _, err := gen.Emit(s, []string{"api_activity"}, tm2, "1.8.0")
	require.NoError(t, err)

	require.Equal(t, out1, out2, "Emit with fresh tag maps must be byte-identical")
}

// TestEmit_ConstraintCEL verifies that objects with at_least_one constraints
// emit buf.validate CEL options.  The user object has
// at_least_one: [account, name, uid].
func TestEmit_ConstraintCEL(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	// api_activity pulls in the user object transitively.
	out, _, err := gen.Emit(s, []string{"api_activity"}, tm, "1.8.0")
	require.NoError(t, err)

	// The User message must contain a CEL option for at_least_one.
	require.Contains(t, out, "buf.validate.message")
	require.Contains(t, out, "User.at_least_one")
}

// TestEmit_JustOneCEL verifies that a just_one constraint emits correct CEL
// asserting exactly one field is present.  vulnerability has just_one: [advisory, cve, cwe].
func TestEmit_JustOneCEL(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	out, _, err := gen.Emit(s, []string{"api_activity"}, tm, "1.8.0")
	require.NoError(t, err)

	// vulnerability is in the transitive closure
	require.Contains(t, out, "Vulnerability.just_one")
}

// TestEmit_Golden is a golden-file test.  It emits api_activity + entity_management
// and compares against testdata/golden/api_entity.proto.
//
// Run with -update to regenerate the golden file.
func TestEmit_Golden(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	out, _, err := gen.Emit(s, []string{"api_activity", "entity_management"}, tm, "1.8.0")
	require.NoError(t, err)

	goldenFile := goldenDir + "/api_entity.proto"

	if *update {
		require.NoError(t, os.MkdirAll(goldenDir, 0o755))
		require.NoError(t, os.WriteFile(goldenFile, []byte(out), 0o644))
		t.Logf("golden file updated: %s", goldenFile)
		return
	}

	golden, err := os.ReadFile(goldenFile)
	if os.IsNotExist(err) {
		t.Fatalf("golden file %s does not exist; run with -update to generate it", goldenFile)
	}
	require.NoError(t, err)
	require.Equal(t, string(golden), out,
		"emitted proto does not match golden; re-run with -update if intentional")
}

// TestEmit_ImportOrder verifies that imports appear in sorted order.
func TestEmit_ImportOrder(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	out, _, err := gen.Emit(s, []string{"api_activity"}, tm, "1.8.0")
	require.NoError(t, err)

	bufIdx := strings.Index(out, `"buf/validate/validate.proto"`)
	googleIdx := strings.Index(out, `"google/protobuf/struct.proto"`)
	require.Greater(t, googleIdx, -1, "struct.proto import must be present")
	require.Greater(t, bufIdx, -1, "validate.proto import must be present")
	// buf/validate comes before google/protobuf lexicographically ('b' < 'g')
	require.Less(t, bufIdx, googleIdx, "imports must be sorted: buf before google")
}

// TestEmit_StubbedObjects_ReportsAbsent verifies that Emit returns the names of
// objects that were emitted as empty stubs because they were referenced in
// attributes but absent from the schema snapshot.  A synthetic schema is used so
// the test does not depend on the fixture's particular missing objects.
func TestEmit_StubbedObjects_ReportsAbsent(t *testing.T) {
	// Build a minimal schema: one class with a single attribute referencing
	// "phantom_obj", which is intentionally absent from the objects map.
	s := &schema.Schema{
		Version: "0.0.1",
		Classes: map[string]*schema.Class{
			"test_event": {
				Name: "test_event",
				Attributes: map[string]*schema.Attribute{
					"the_field": {
						Name:       "the_field",
						Type:       "object_t",
						ObjectType: "phantom_obj",
					},
				},
			},
		},
		Objects: map[string]*schema.Object{},
		Types: map[string]*schema.TypeDef{
			"string_t": {Caption: "String"},
		},
		DictionaryAttributes: map[string]*schema.DictAttr{},
		BaseEvent:            &schema.BaseEvent{Attributes: map[string]*schema.Attribute{}},
	}

	tm := tagmap.New()
	_, stubbed, err := gen.Emit(s, []string{"test_event"}, tm, "0.0.1")
	require.NoError(t, err)
	require.Equal(t, []string{"PhantomObj"}, stubbed,
		"Emit must report PascalCase name of the absent object in stubbed")
}

// TestEmit_StubbedObjects_NoneWhenFullyResolvable verifies that Emit returns
// nil stubbed when all objects transitively referenced by the selected classes
// are present in the schema snapshot.  Uses entity_management, whose full
// transitive closure is present in the 1.8.0 fixture.
func TestEmit_StubbedObjects_NoneWhenFullyResolvable(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	_, stubbed, err := gen.Emit(s, []string{"entity_management"}, tm, "1.8.0")
	require.NoError(t, err)
	require.Empty(t, stubbed,
		"Emit must return empty stubbed when all referenced objects are present in the schema")
}

// TestEmit_RequiredRepeatedFieldNoAnnotation verifies that a required REPEATED
// field does NOT get the (buf.validate.field).required annotation (Fix 1).
//
// protovalidate interprets `required` on a repeated field as "len >= 1", but
// OCSF "required" on an array means only "key present" — the array may be
// legitimately empty. api_activity.osint is repeated+required and must NOT get
// the annotation. api_activity.time is singular+required and MUST keep it.
func TestEmit_RequiredRepeatedFieldNoAnnotation(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	out, _, err := gen.Emit(s, []string{"api_activity"}, tm, "1.8.0")
	require.NoError(t, err)

	// osint is repeated+required: must NOT carry the annotation.
	require.NotContains(t, out, "repeated Osint osint = 37 [(buf.validate.field).required = true]",
		"required repeated field must not get (buf.validate.field).required annotation")
	require.Contains(t, out, "repeated Osint osint =",
		"osint field must still be emitted as repeated")

	// time is singular+required: must still carry the annotation.
	require.Contains(t, out, "(buf.validate.field).required = true",
		"singular required fields must still get the annotation")
}

// TestEmit_UnresolvableScalarErrors verifies that Emit returns an error (not a
// silent fallback to google.protobuf.Value) when an attribute has a scalar type
// that cannot be resolved through the schema's type chain.
//
// Before FIX 6, resolveProtoType fell back to WellKnown="google.protobuf.Value"
// for non-object type errors, hiding the unresolvable type silently.
func TestEmit_UnresolvableScalarErrors(t *testing.T) {
	s := &schema.Schema{
		Version: "0.0.1",
		Classes: map[string]*schema.Class{
			"bad_event": {
				Name: "bad_event",
				Attributes: map[string]*schema.Attribute{
					"mystery_field": {
						Name: "mystery_field",
						// This scalar type is not in Types map and not in baseMapping.
						Type: "totally_unknown_scalar_t",
					},
				},
			},
		},
		Objects:              map[string]*schema.Object{},
		Types:                map[string]*schema.TypeDef{},
		DictionaryAttributes: map[string]*schema.DictAttr{},
		BaseEvent:            &schema.BaseEvent{Attributes: map[string]*schema.Attribute{}},
	}

	tm := tagmap.New()
	_, _, err := gen.Emit(s, []string{"bad_event"}, tm, "0.0.1")
	require.Error(t, err, "Emit must return an error for an unresolvable scalar type")
	require.Contains(t, err.Error(), "totally_unknown_scalar_t",
		"error must name the unresolvable type")
}

// TestEmit_DeterministicWithSavedTagMap verifies that a TagMap saved after one
// Emit call and loaded back produces byte-identical output on a second Emit call.
// This strengthens the wire-stability guarantee: the on-disk format round-trips
// without changing field numbers.
func TestEmit_DeterministicWithSavedTagMap(t *testing.T) {
	s := loadFixture(t)

	// First run: emit with a fresh TagMap and save it.
	tm1 := tagmap.New()
	out1, _, err := gen.Emit(s, []string{"api_activity"}, tm1, "1.8.0")
	require.NoError(t, err)

	tagFile := filepath.Join(t.TempDir(), "tags.json")
	require.NoError(t, tm1.Save(tagFile))

	// Second run: load the saved TagMap and emit again.
	tm2, err := tagmap.Load(tagFile)
	require.NoError(t, err)

	out2, _, err := gen.Emit(s, []string{"api_activity"}, tm2, "1.8.0")
	require.NoError(t, err)

	require.Equal(t, out1, out2,
		"Emit output must be byte-identical when using a TagMap loaded from the saved file")
}
