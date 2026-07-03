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
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/gen"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/tagmap"
)

// mergedGoldenDir is the module root of the merged-layout golden tree:
// ocsf/v1/audit_event.proto + ocsf/v1/objects.proto under it, and the SR
// schema under mergedSRGoldenDir.
const (
	mergedGoldenDir   = "testdata/golden-merged"
	mergedSRGoldenDir = "testdata/golden-merged/sr"
	mergedMsgName     = "AuditEvent"
	mergedSRSubject   = "redpanda.ocsf.audit-events-value"
)

// emitMergedJoined runs EmitMerged and concatenates the generated files in
// path-sorted order for substring assertions.
func emitMergedJoined(t *testing.T, classNames []string) string {
	t.Helper()
	s := loadFixture(t)
	tm := tagmap.New()
	files, _, err := gen.EmitMerged(s, classNames, tm, "1.8.0", mergedMsgName, mergedSRSubject)
	require.NoError(t, err)
	var sb strings.Builder
	for _, f := range files {
		sb.WriteString(f.Content)
	}
	return sb.String()
}

// TestEmitMerged_Golden emits the merged layout for api_activity +
// entity_management and compares it (and the merged SR schema) against the
// committed goldens. Run with -update to regenerate.
func TestEmitMerged_Golden(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	files, stubbed, err := gen.EmitMerged(s, mergedClasses, tm, "1.8.0", mergedMsgName, mergedSRSubject)
	require.NoError(t, err)
	require.Empty(t, stubbed, "full 1.8.0 snapshot must not produce stubs")
	require.Len(t, files, 2)

	srFile, err := gen.EmitMergedSRSchema(s, mergedClasses, tm, "1.8.0", mergedMsgName)
	require.NoError(t, err)

	if *update {
		for _, f := range files {
			dst := filepath.Join(mergedGoldenDir, filepath.FromSlash(f.Path))
			require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
			require.NoError(t, os.WriteFile(dst, []byte(f.Content), 0o644))
		}
		srDst := filepath.Join(mergedSRGoldenDir, filepath.FromSlash(srFile.Path))
		require.NoError(t, os.MkdirAll(filepath.Dir(srDst), 0o755))
		require.NoError(t, os.WriteFile(srDst, []byte(srFile.Content), 0o644))
		t.Logf("merged golden files updated under %s", mergedGoldenDir)
		return
	}

	for _, f := range files {
		golden := filepath.Join(mergedGoldenDir, filepath.FromSlash(f.Path))
		want, readErr := os.ReadFile(golden)
		require.NoError(t, readErr, "merged golden %s missing; re-run with -update", f.Path)
		require.Equal(t, string(want), f.Content,
			"merged proto %s does not match golden; re-run with -update if intentional", f.Path)
	}

	srGolden := filepath.Join(mergedSRGoldenDir, filepath.FromSlash(srFile.Path))
	want, err := os.ReadFile(srGolden)
	require.NoError(t, err, "merged SR golden missing; re-run with -update")
	require.Equal(t, string(want), srFile.Content,
		"merged SR schema does not match golden; re-run with -update if intentional")
}

// TestEmitMerged_Paths verifies the file layout: audit_event.proto +
// objects.proto under the versioned directory.
func TestEmitMerged_Paths(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	files, _, err := gen.EmitMerged(s, mergedClasses, tm, "1.8.0", mergedMsgName, mergedSRSubject)
	require.NoError(t, err)

	paths := []string{files[0].Path, files[1].Path}
	require.ElementsMatch(t, []string{"ocsf/v1/audit_event.proto", "ocsf/v1/objects.proto"}, paths)
}

// TestEmitMerged_SingleMessage verifies exactly one top-level message in the
// merged class file, holding the union of attributes with one tag each.
func TestEmitMerged_SingleMessage(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	files, _, err := gen.EmitMerged(s, mergedClasses, tm, "1.8.0", mergedMsgName, mergedSRSubject)
	require.NoError(t, err)

	var classFile string
	for _, f := range files {
		if strings.HasSuffix(f.Path, "audit_event.proto") {
			classFile = f.Content
		}
	}
	require.NotEmpty(t, classFile)

	require.Equal(t, 1, strings.Count(classFile, "\nmessage ")+boolToInt(strings.HasPrefix(classFile, "message ")),
		"merged class file must contain exactly one message")
	require.Contains(t, classFile, "message AuditEvent {")

	// Union: fields from both classes appear in the ONE message.
	require.Contains(t, classFile, " api = ", "api_activity-owned field must be present")
	require.Contains(t, classFile, " entity = ", "entity_management-owned field must be present")
	require.Contains(t, classFile, " metadata = ", "base_event field must be present")
}

// TestEmitMerged_ActivityIDDemoted verifies activity_id is a plain int32 with
// the demotion comment, and no ActivityId enum exists.
func TestEmitMerged_ActivityIDDemoted(t *testing.T) {
	content := emitMergedJoined(t, mergedClasses)

	require.Contains(t, content, "// Class-scoped enum: value semantics depend on class_uid; see TypeUid.\n  int32 activity_id = ")
	require.NotContains(t, content, "enum ActivityId", "merged message must not declare a merged ActivityId enum")
}

// TestEmitMerged_UnionEnums verifies ClassUid/CategoryUid/TypeUid union the
// values of every selected class.
func TestEmitMerged_UnionEnums(t *testing.T) {
	content := emitMergedJoined(t, mergedClasses)

	require.Contains(t, content, "CLASS_UID_API_ACTIVITY = 6003")
	require.Contains(t, content, "CLASS_UID_ENTITY_MANAGEMENT = 3004")
	require.Contains(t, content, "CATEGORY_UID_APPLICATION_ACTIVITY = 6")
	require.Contains(t, content, "CATEGORY_UID_IDENTITY_ACCESS_MANAGEMENT = 3")
	require.Contains(t, content, "TYPE_UID_API_ACTIVITY_CREATE = 600301")
	require.Contains(t, content, "TYPE_UID_ENTITY_MANAGEMENT_CREATE = 300401")
}

// TestEmitMerged_TypeUIDConsistencyCEL verifies the OCSF type_uid invariant is
// enforced via message-level CEL.
func TestEmitMerged_TypeUIDConsistencyCEL(t *testing.T) {
	content := emitMergedJoined(t, mergedClasses)

	require.Contains(t, content, `id: "AuditEvent.type_uid"`)
	require.Contains(t, content, `expression: "this.type_uid == this.class_uid * 100 + this.activity_id"`)
}

// TestEmitMerged_OwnershipCEL verifies class-owned fields are gated on
// class_uid.
func TestEmitMerged_OwnershipCEL(t *testing.T) {
	content := emitMergedJoined(t, mergedClasses)

	// trace is api_activity-only (6003).
	require.Contains(t, content, `id: "AuditEvent.own.trace"`)
	require.Contains(t, content, `expression: "!has(this.trace) || this.class_uid == 6003"`)
	// entity is entity_management-only (3004).
	require.Contains(t, content, `id: "AuditEvent.own.entity"`)
	require.Contains(t, content, `expression: "!has(this.entity) || this.class_uid == 3004"`)
	// metadata and api are owned by every class: no ownership gate.
	require.NotContains(t, content, `id: "AuditEvent.own.metadata"`)
	require.NotContains(t, content, `id: "AuditEvent.own.api"`)
}

// TestEmitMerged_ConditionalRequiredCEL verifies per-class requiredness is
// enforced with class_uid-gated CEL, not blanket field annotations.
func TestEmitMerged_ConditionalRequiredCEL(t *testing.T) {
	content := emitMergedJoined(t, mergedClasses)

	// actor is required by api_activity (6003) but not entity_management.
	require.Contains(t, content, `id: "AuditEvent.req.api_activity.actor"`)
	require.Contains(t, content, `expression: "this.class_uid != 6003 || has(this.actor)"`)
	// entity is required by entity_management (3004).
	require.Contains(t, content, `id: "AuditEvent.req.entity_management.entity"`)
	require.Contains(t, content, `expression: "this.class_uid != 3004 || has(this.entity)"`)

	// actor must NOT carry the blanket field-level required annotation.
	require.NotRegexp(t, `actor = \d+ \[\(buf\.validate\.field\)\.required = true\]`, content)
	// metadata (required everywhere) must keep the blanket annotation.
	require.Regexp(t, `metadata = \d+ \[\(buf\.validate\.field\)\.required = true\]`, content)
}

// TestEmitMerged_TagStability verifies that adding a class to the selection
// never changes an already-assigned tag: the wire-stability guarantee for the
// merged message.
func TestEmitMerged_TagStability(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	_, _, err := gen.EmitMerged(s, mergedClasses, tm, "1.8.0", mergedMsgName, mergedSRSubject)
	require.NoError(t, err)

	// Record every AuditEvent tag after the first run. Assign is idempotent,
	// so re-asking returns the stored tag without mutation.
	m, err := gen.MergeClasses(s, mergedClasses, mergedMsgName)
	require.NoError(t, err)
	before := make(map[string]int32, len(m.Attributes))
	for name := range m.Attributes {
		tag, assignErr := tm.Assign(mergedMsgName, name)
		require.NoError(t, assignErr)
		before[name] = tag
	}

	// Second run with an additional class (authentication conflicts with
	// api_activity on activity_id, exercising demotion too).
	wider := append([]string{"authentication"}, mergedClasses...)
	_, _, err = gen.EmitMerged(s, wider, tm, "1.8.0", mergedMsgName, mergedSRSubject)
	require.NoError(t, err)

	for name, want := range before {
		got, assignErr := tm.Assign(mergedMsgName, name)
		require.NoError(t, assignErr)
		require.Equal(t, want, got, "tag for %q changed after adding a class", name)
	}
}

// TestEmitMerged_ConflictingClassDemotes verifies EmitMerged succeeds when a
// genuinely conflicting class (authentication) is selected, with activity_id
// demoted rather than erroring.
func TestEmitMerged_ConflictingClassDemotes(t *testing.T) {
	content := emitMergedJoined(t, []string{"api_activity", "entity_management", "authentication"})

	require.Contains(t, content, "int32 activity_id = ")
	require.NotContains(t, content, "enum ActivityId")
	require.Contains(t, content, "CLASS_UID_AUTHENTICATION = 3002")
}

// TestEmitMerged_SRSubjectAnnotation verifies the Schema-Registry message
// option and its import are emitted when a subject is provided, and absent
// when it is empty.
func TestEmitMerged_SRSubjectAnnotation(t *testing.T) {
	s := loadFixture(t)

	files, _, err := gen.EmitMerged(s, mergedClasses, tagmap.New(), "1.8.0", mergedMsgName, mergedSRSubject)
	require.NoError(t, err)
	var classFile string
	for _, f := range files {
		if strings.HasSuffix(f.Path, "audit_event.proto") {
			classFile = f.Content
		}
	}
	require.Contains(t, classFile, `import "redpanda/api/common/v1/schema_registry.proto";`)
	require.Contains(t, classFile, "option (redpanda.api.common.v1.schema_registry) = {")
	require.Contains(t, classFile, `subject: "redpanda.ocsf.audit-events-value"`)

	// Empty subject: no annotation, no import.
	bare, _, err := gen.EmitMerged(s, mergedClasses, tagmap.New(), "1.8.0", mergedMsgName, "")
	require.NoError(t, err)
	for _, f := range bare {
		require.NotContains(t, f.Content, "schema_registry")
	}
}

// TestEmitMergedSRSchema_Shape verifies the SR schema: merged message first,
// self-contained, no buf.validate anywhere, and no Schema-Registry annotation
// (the .sr.proto must not depend on redpanda/api/common options).
func TestEmitMergedSRSchema_Shape(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	f, err := gen.EmitMergedSRSchema(s, mergedClasses, tm, "1.8.0", mergedMsgName)
	require.NoError(t, err)

	require.Equal(t, "audit_event.sr.proto", f.Path)
	require.NotContains(t, f.Content, "buf.validate")
	require.NotContains(t, f.Content, `import "ocsf/`)
	require.NotContains(t, f.Content, "schema_registry")

	// Merged message must be the FIRST message (Confluent index 0).
	firstMsg := strings.Index(f.Content, "message ")
	require.GreaterOrEqual(t, firstMsg, 0)
	require.True(t, strings.HasPrefix(f.Content[firstMsg:], "message AuditEvent {"),
		"merged message must be first for Confluent message-index 0")
}

// TestEmitMergedSRSchema_FieldNumberParity verifies the SR schema carries the
// exact same tags as the main merged output when sharing a tagmap.
func TestEmitMergedSRSchema_FieldNumberParity(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	files, _, err := gen.EmitMerged(s, mergedClasses, tm, "1.8.0", mergedMsgName, mergedSRSubject)
	require.NoError(t, err)
	srFile, err := gen.EmitMergedSRSchema(s, mergedClasses, tm, "1.8.0", mergedMsgName)
	require.NoError(t, err)

	var classFile string
	for _, f := range files {
		if strings.HasSuffix(f.Path, "audit_event.proto") {
			classFile = f.Content
		}
	}

	mainMsg := extractMessage(t, classFile, "AuditEvent")
	srMsg := extractMessage(t, srFile.Content, "AuditEvent")

	mainTags := extractFieldTags(mainMsg)
	srTags := extractFieldTags(srMsg)
	require.Equal(t, mainTags, srTags, "SR schema field numbers must match the main merged output")
	require.NotEmpty(t, mainTags)
}

// TestEmitMerged_Deterministic verifies two runs produce identical bytes.
func TestEmitMerged_Deterministic(t *testing.T) {
	a := emitMergedJoined(t, mergedClasses)
	b := emitMergedJoined(t, mergedClasses)
	require.Equal(t, a, b)
}

// extractMessage returns the body of the named top-level message.
func extractMessage(t *testing.T, content, name string) string {
	t.Helper()
	start := strings.Index(content, "message "+name+" {")
	require.GreaterOrEqual(t, start, 0, "message %s not found", name)
	// Top-level messages end at the first "\n}\n" after their start.
	end := strings.Index(content[start:], "\n}\n")
	require.GreaterOrEqual(t, end, 0)
	return content[start : start+end]
}

// extractFieldTags parses "name = N" pairs from a message body.
func extractFieldTags(msg string) map[string]string {
	tags := make(map[string]string)
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, " = ") || strings.HasPrefix(line, "//") ||
			strings.HasPrefix(line, "option") || !strings.HasSuffix(line, ";") {
			continue
		}
		// Skip enum value lines (ALL_CAPS idents).
		parts := strings.SplitN(line, " = ", 2)
		fields := strings.Fields(parts[0])
		fieldName := fields[len(fields)-1]
		if strings.ToUpper(fieldName) == fieldName {
			continue
		}
		num := strings.TrimSuffix(parts[1], ";")
		if i := strings.Index(num, " ["); i >= 0 {
			num = num[:i]
		}
		tags[fieldName] = num
	}
	return tags
}

// boolToInt converts a bool to 1/0 for message-count arithmetic.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
