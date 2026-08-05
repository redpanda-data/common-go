// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package protogen_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/redpanda-data/common-go/ocsf/cmd/ocsf-protogen/protogen"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/tagmap"
)

// schemaFixture returns the absolute path to the committed OCSF schema fixture.
func schemaFixture() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	// thisFile: .../ocsf/cmd/ocsf-protogen/protogen/protogen_test.go
	// schema:   .../ocsf/internal/ocsf/schema/testdata/ocsf-1.8.0.json
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "internal", "ocsf", "schema", "testdata")
	return filepath.Join(root, "ocsf-1.8.0.json")
}

// committedBaseline returns the absolute path to the committed baseline module
// root directory and its field-numbers.json. The proto tree lives under
// <outDir>/ocsf/v1/*.proto.
func committedBaseline() (outDir, tagmapPath string) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	// testdata is at .../ocsf/cmd/ocsf-protogen/testdata/
	td := filepath.Join(filepath.Dir(thisFile), "..", "testdata")
	return td, filepath.Join(td, "field-numbers.json")
}

// ─── ParseClasses ─────────────────────────────────────────────────────────────

func TestParseClasses_Valid(t *testing.T) {
	got, err := protogen.ParseClasses("api_activity,entity_management")
	require.NoError(t, err)
	require.Equal(t, []string{"api_activity", "entity_management"}, got)
}

func TestParseClasses_Whitespace(t *testing.T) {
	got, err := protogen.ParseClasses("  api_activity , entity_management  ")
	require.NoError(t, err)
	require.Equal(t, []string{"api_activity", "entity_management"}, got)
}

func TestParseClasses_Single(t *testing.T) {
	got, err := protogen.ParseClasses("api_activity")
	require.NoError(t, err)
	require.Equal(t, []string{"api_activity"}, got)
}

func TestParseClasses_Empty(t *testing.T) {
	_, err := protogen.ParseClasses("")
	require.Error(t, err)
}

func TestParseClasses_OnlyCommas(t *testing.T) {
	_, err := protogen.ParseClasses(",,,")
	require.Error(t, err)
}

// ─── Generate → Check (round-trip) ───────────────────────────────────────────

// TestGenerateThenCheck generates into a temp dir, then runs Check against the
// same temp dir and asserts it passes (exit 0 equivalent).
func TestGenerateThenCheck(t *testing.T) {
	dir := t.TempDir()
	tagmapPath := filepath.Join(dir, "field-numbers.json")

	cfg := protogen.Config{
		SchemaPath: schemaFixture(),
		Classes:    []string{"api_activity", "entity_management"},
		Version:    "1.8.0",
		OutDir:     dir,
		TagmapPath: tagmapPath,
		Check:      false,
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	// The multi-file tree and the tagmap should have been created.
	require.FileExists(t, filepath.Join(dir, "ocsf", "v1", "api_activity.proto"))
	require.FileExists(t, filepath.Join(dir, "ocsf", "v1", "entity_management.proto"))
	require.FileExists(t, filepath.Join(dir, "ocsf", "v1", "objects.proto"))
	require.FileExists(t, tagmapPath)

	// --check against the just-generated baseline must pass.
	checkCfg := cfg
	checkCfg.Check = true
	err = protogen.Check(checkCfg)
	require.NoError(t, err, "--check must pass immediately after Generate on the same baseline")
}

// ─── SR schema output ─────────────────────────────────────────────────────────

// TestGenerateWritesSRSchemas verifies that when SRSchemaOutDir is set, Generate
// writes one flat <class>.sr.proto per class and that --check passes against the
// just-generated baseline.
func TestGenerateWritesSRSchemas(t *testing.T) {
	dir := t.TempDir()
	srDir := t.TempDir()
	tagmapPath := filepath.Join(dir, "field-numbers.json")

	cfg := protogen.Config{
		SchemaPath:     schemaFixture(),
		Classes:        []string{"api_activity", "entity_management"},
		Version:        "1.8.0",
		OutDir:         dir,
		TagmapPath:     tagmapPath,
		SRSchemaOutDir: srDir,
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	require.FileExists(t, filepath.Join(srDir, "api_activity.sr.proto"))
	require.FileExists(t, filepath.Join(srDir, "entity_management.sr.proto"))

	// --check against the just-generated SR baseline must pass.
	checkCfg := cfg
	checkCfg.Check = true
	require.NoError(t, protogen.Check(checkCfg),
		"--check must pass immediately after Generate on the same SR baseline")
}

// TestCheckFailsOnSRSchemaDrift verifies that --check detects drift in a
// committed SR schema file.
func TestCheckFailsOnSRSchemaDrift(t *testing.T) {
	dir := t.TempDir()
	srDir := t.TempDir()
	tagmapPath := filepath.Join(dir, "field-numbers.json")

	cfg := protogen.Config{
		SchemaPath:     schemaFixture(),
		Classes:        []string{"api_activity"},
		Version:        "1.8.0",
		OutDir:         dir,
		TagmapPath:     tagmapPath,
		SRSchemaOutDir: srDir,
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	// Corrupt the committed SR schema file.
	srPath := filepath.Join(srDir, "api_activity.sr.proto")
	content, err := os.ReadFile(srPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(srPath, append(content, []byte("\n// CORRUPTED\n")...), 0o644))

	checkCfg := cfg
	checkCfg.Check = true
	err = protogen.Check(checkCfg)
	require.Error(t, err, "--check must fail when a committed SR schema drifts")
	require.Contains(t, err.Error(), "SR schema")
	require.Contains(t, err.Error(), "differs")
}

// TestCheckFailsOnStraySRSchema verifies --check detects a committed
// *.sr.proto the generator no longer produces (e.g. after a class is dropped
// from --classes), mirroring the stray-file detection of the main tree.
func TestCheckFailsOnStraySRSchema(t *testing.T) {
	dir := t.TempDir()
	srDir := t.TempDir()
	tagmapPath := filepath.Join(dir, "field-numbers.json")

	cfg := protogen.Config{
		SchemaPath:     schemaFixture(),
		Classes:        []string{"api_activity"},
		Version:        "1.8.0",
		OutDir:         dir,
		TagmapPath:     tagmapPath,
		SRSchemaOutDir: srDir,
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	// A leftover SR schema from a since-dropped class.
	require.NoError(t, os.WriteFile(filepath.Join(srDir, "dropped_class.sr.proto"),
		[]byte("syntax = \"proto3\";\n"), 0o644))

	checkCfg := cfg
	checkCfg.Check = true
	err = protogen.Check(checkCfg)
	require.Error(t, err, "--check must fail on a stray committed SR schema")
	require.Contains(t, err.Error(), "dropped_class.sr.proto")
}

// TestGenerateMergedNameCollision verifies a merged message name that matches
// a selected class's PascalCase message name is rejected: the two messages
// would silently share one tag lineage.
func TestGenerateMergedNameCollision(t *testing.T) {
	dir := t.TempDir()
	cfg := protogen.Config{
		SchemaPath:    schemaFixture(),
		Classes:       []string{"api_activity", "entity_management"},
		Version:       "1.8.0",
		OutDir:        dir,
		TagmapPath:    filepath.Join(dir, "field-numbers.json"),
		MergedMessage: "ApiActivity", // collides with class api_activity
	}
	_, err := protogen.Generate(cfg)
	require.ErrorContains(t, err, "collides")
	require.ErrorContains(t, err, "api_activity")
}

// TestCheckFailsOnMissingSRSchema verifies that --check reports a missing SR file.
func TestCheckFailsOnMissingSRSchema(t *testing.T) {
	dir := t.TempDir()
	srDir := t.TempDir()
	tagmapPath := filepath.Join(dir, "field-numbers.json")

	cfg := protogen.Config{
		SchemaPath:     schemaFixture(),
		Classes:        []string{"api_activity"},
		Version:        "1.8.0",
		OutDir:         dir,
		TagmapPath:     tagmapPath,
		SRSchemaOutDir: srDir,
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	require.NoError(t, os.Remove(filepath.Join(srDir, "api_activity.sr.proto")))

	checkCfg := cfg
	checkCfg.Check = true
	err = protogen.Check(checkCfg)
	require.Error(t, err, "--check must fail when a committed SR schema is missing")
	require.Contains(t, err.Error(), "missing")
}

// ─── Check: tagmap incompatibility ────────────────────────────────────────────

// TestCheckFailsOnProtoDriftFromEditedTagmap verifies that protogen.Check
// detects proto drift when the on-disk tagmap has been manually edited so that
// a field's assigned tag differs from what Emit would use, causing the freshly
// generated proto to diverge from the committed baseline.
func TestCheckFailsOnProtoDriftFromEditedTagmap(t *testing.T) {
	dir := t.TempDir()
	tagmapPath := filepath.Join(dir, "field-numbers.json")

	cfg := protogen.Config{
		SchemaPath: schemaFixture(),
		Classes:    []string{"api_activity", "entity_management"},
		Version:    "1.8.0",
		OutDir:     dir,
		TagmapPath: tagmapPath,
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	// Modify a field tag in the tagmap so that Emit re-uses the stored (now
	// different) tag when Check runs.  The proto content will then differ from
	// the committed baseline (which was generated with the original tags).
	raw, err := os.ReadFile(tagmapPath)
	require.NoError(t, err)

	var wireTop map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &wireTop))

	type msgEntry struct {
		Fields   map[string]int32 `json:"fields"`
		Reserved []int32          `json:"reserved"`
	}

	// Change one field in one message to tag 9998 (unlikely to collide).
	changed := false
	for msgName, msgRaw := range wireTop {
		var entry msgEntry
		if err := json.Unmarshal(msgRaw, &entry); err != nil || len(entry.Fields) == 0 {
			continue
		}
		for attrName := range entry.Fields {
			entry.Fields[attrName] = 9998
			b, err := json.Marshal(entry)
			require.NoError(t, err)
			wireTop[msgName] = b
			changed = true
			break
		}
		if changed {
			break
		}
	}
	require.True(t, changed, "test setup: must have found at least one field to change")

	out, err := json.MarshalIndent(wireTop, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tagmapPath, out, 0o644))

	// outPath still contains the proto from the original generate (original tags).
	// The corrupted tagmap causes Emit to assign tag 9998 to that field.
	// Proto diff fires because the committed proto uses the original tag.
	checkCfg := cfg
	checkCfg.Check = true
	err = protogen.Check(checkCfg)
	require.Error(t, err, "--check must fail when tagmap causes different proto output")
	require.Contains(t, err.Error(), "differs", "error must report proto drift")
}

// ─── Check: proto drift ────────────────────────────────────────────────────────

// TestCheckFailsOnProtoDrift generates a baseline, edits the committed proto
// file, and asserts Check detects the drift.
func TestCheckFailsOnProtoDrift(t *testing.T) {
	dir := t.TempDir()
	tagmapPath := filepath.Join(dir, "field-numbers.json")

	cfg := protogen.Config{
		SchemaPath: schemaFixture(),
		Classes:    []string{"api_activity", "entity_management"},
		Version:    "1.8.0",
		OutDir:     dir,
		TagmapPath: tagmapPath,
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	// Corrupt one generated proto file in the tree.
	objPath := filepath.Join(dir, "ocsf", "v1", "objects.proto")
	content, err := os.ReadFile(objPath)
	require.NoError(t, err)
	corrupted := string(content) + "\n// CORRUPTED BY TEST\n"
	require.NoError(t, os.WriteFile(objPath, []byte(corrupted), 0o644))

	// Check must fail with a drift error.
	checkCfg := cfg
	checkCfg.Check = true
	err = protogen.Check(checkCfg)
	require.Error(t, err, "--check must fail when committed proto differs from fresh output")
	require.Contains(t, err.Error(), "differs", "error must describe the drift")
}

// ─── Check: committed baseline ───────────────────────────────────────────────

// TestCheckAgainstCommittedBaseline runs --check against the tree committed to
// the repository (testdata/ocsf/v1/*.proto and testdata/field-numbers.json).
// This test fails if the committed baseline drifts from what the generator
// would produce today.
func TestCheckAgainstCommittedBaseline(t *testing.T) {
	outDir, tagmapPath := committedBaseline()

	// Skip gracefully if the committed baseline does not exist yet
	// (before the first `go run ./cmd/ocsf-protogen --out ... --tagmap ...` is run).
	if _, err := os.Stat(filepath.Join(outDir, "ocsf", "v1", "objects.proto")); os.IsNotExist(err) {
		t.Skip("committed baseline not yet generated; run: go run ./cmd/ocsf-protogen ...")
	}
	if _, err := os.Stat(tagmapPath); os.IsNotExist(err) {
		t.Skip("committed baseline not yet generated; run: go run ./cmd/ocsf-protogen ...")
	}

	cfg := protogen.Config{
		SchemaPath: schemaFixture(),
		Classes:    []string{"api_activity", "entity_management"},
		Version:    "1.8.0",
		OutDir:     outDir,
		TagmapPath: tagmapPath,
		Check:      true,
		// The committed baseline includes the merged single-event layout
		// (audit_event.proto) with the Schema-Registry annotation; --check must
		// be configured identically to the generation run (see the ocsf-checks
		// CI job).
		MergedMessage:   "AuditEvent",
		MergedSRSubject: "redpanda.ocsf.audit-events-value",
	}

	err := protogen.Check(cfg)
	require.NoError(t, err, "--check must pass against the committed baseline")
}

// ─── Merged single-event layout ───────────────────────────────────────────────

// TestGenerateMergedThenCheck verifies MergedMessage emission: the merged
// audit_event.proto lands next to the per-class files, the merged SR schema is
// written, and --check with the same config passes.
func TestGenerateMergedThenCheck(t *testing.T) {
	dir := t.TempDir()
	srDir := t.TempDir()
	tagmapPath := filepath.Join(dir, "field-numbers.json")

	cfg := protogen.Config{
		SchemaPath:      schemaFixture(),
		Classes:         []string{"api_activity", "entity_management"},
		Version:         "1.8.0",
		OutDir:          dir,
		TagmapPath:      tagmapPath,
		SRSchemaOutDir:  srDir,
		MergedMessage:   "AuditEvent",
		MergedSRSubject: "redpanda.ocsf.audit-events-value",
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	// Per-class files AND the merged file coexist in one tree.
	require.FileExists(t, filepath.Join(dir, "ocsf", "v1", "api_activity.proto"))
	require.FileExists(t, filepath.Join(dir, "ocsf", "v1", "entity_management.proto"))
	require.FileExists(t, filepath.Join(dir, "ocsf", "v1", "audit_event.proto"))
	require.FileExists(t, filepath.Join(dir, "ocsf", "v1", "objects.proto"))

	// SR schemas: per-class plus the merged one.
	require.FileExists(t, filepath.Join(srDir, "api_activity.sr.proto"))
	require.FileExists(t, filepath.Join(srDir, "entity_management.sr.proto"))
	require.FileExists(t, filepath.Join(srDir, "audit_event.sr.proto"))

	checkCfg := cfg
	checkCfg.Check = true
	require.NoError(t, protogen.Check(checkCfg),
		"--check must pass immediately after Generate on the same merged baseline")
}

// TestGenerateMergedOnlyThenCheck verifies MergedOnly suppresses every
// per-class proto and SR artifact while retaining the merged message, its
// shared objects dependency, and native --check support.
func TestGenerateMergedOnlyThenCheck(t *testing.T) {
	dir := t.TempDir()
	srDir := t.TempDir()
	tagmapPath := filepath.Join(dir, "field-numbers.json")

	cfg := protogen.Config{
		SchemaPath:      schemaFixture(),
		Classes:         []string{"api_activity", "entity_management"},
		Version:         "1.8.0",
		OutDir:          dir,
		TagmapPath:      tagmapPath,
		SRSchemaOutDir:  srDir,
		MergedMessage:   "AuditEvent",
		MergedOnly:      true,
		MergedSRSubject: "redpanda.ocsf.audit-events-value",
		SRGoOnly:        true,
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	require.FileExists(t, filepath.Join(dir, "ocsf", "v1", "audit_event.proto"))
	require.FileExists(t, filepath.Join(dir, "ocsf", "v1", "objects.proto"))
	require.NoFileExists(t, filepath.Join(dir, "ocsf", "v1", "api_activity.proto"))
	require.NoFileExists(t, filepath.Join(dir, "ocsf", "v1", "entity_management.proto"))

	require.FileExists(t, filepath.Join(srDir, "audit_event.sr.go"))
	require.NoFileExists(t, filepath.Join(srDir, "audit_event.sr.proto"))
	require.NoFileExists(t, filepath.Join(srDir, "api_activity.sr.proto"))
	require.NoFileExists(t, filepath.Join(srDir, "api_activity.sr.go"))
	require.NoFileExists(t, filepath.Join(srDir, "entity_management.sr.proto"))
	require.NoFileExists(t, filepath.Join(srDir, "entity_management.sr.go"))

	checkCfg := cfg
	checkCfg.Check = true
	require.NoError(t, protogen.Check(checkCfg),
		"--check must pass immediately after Generate on the same merged-only baseline")
}

// TestGenerateMergedOnlyMatchesMergedOutput verifies selecting only the merged
// layout changes the file set, not the merged wire schema.
func TestGenerateMergedOnlyMatchesMergedOutput(t *testing.T) {
	full := protogen.Config{
		SchemaPath:      schemaFixture(),
		Classes:         []string{"api_activity", "entity_management"},
		Version:         "1.8.0",
		OutDir:          t.TempDir(),
		SRSchemaOutDir:  t.TempDir(),
		MergedMessage:   "AuditEvent",
		MergedSRSubject: "redpanda.ocsf.audit-events-value",
	}
	full.TagmapPath = filepath.Join(full.OutDir, "field-numbers.json")

	mergedOnly := full
	mergedOnly.OutDir = t.TempDir()
	mergedOnly.TagmapPath = filepath.Join(mergedOnly.OutDir, "field-numbers.json")
	mergedOnly.SRSchemaOutDir = t.TempDir()
	mergedOnly.MergedOnly = true

	_, err := protogen.Generate(full)
	require.NoError(t, err)
	_, err = protogen.Generate(mergedOnly)
	require.NoError(t, err)

	for _, path := range []string{
		"ocsf/v1/audit_event.proto",
		"ocsf/v1/objects.proto",
	} {
		fullContent, err := os.ReadFile(filepath.Join(full.OutDir, filepath.FromSlash(path)))
		require.NoError(t, err)
		mergedOnlyContent, err := os.ReadFile(filepath.Join(mergedOnly.OutDir, filepath.FromSlash(path)))
		require.NoError(t, err)
		require.Equal(t, fullContent, mergedOnlyContent, "merged output differs for %s", path)
	}
	for _, name := range []string{"audit_event.sr.proto", "audit_event.sr.go"} {
		fullContent, err := os.ReadFile(filepath.Join(full.SRSchemaOutDir, name))
		require.NoError(t, err)
		mergedOnlyContent, err := os.ReadFile(filepath.Join(mergedOnly.SRSchemaOutDir, name))
		require.NoError(t, err)
		require.Equal(t, fullContent, mergedOnlyContent, "merged SR output differs for %s", name)
	}
}

// TestGenerateMergedOnlyRequiresMergedMessage verifies the selection cannot be
// enabled without naming the merged message to emit.
func TestGenerateMergedOnlyRequiresMergedMessage(t *testing.T) {
	dir := t.TempDir()
	cfg := protogen.Config{
		SchemaPath: schemaFixture(),
		Classes:    []string{"api_activity"},
		Version:    "1.8.0",
		OutDir:     dir,
		TagmapPath: filepath.Join(dir, "field-numbers.json"),
		MergedOnly: true,
	}

	_, err := protogen.Generate(cfg)
	require.ErrorContains(t, err, "--merged-only requires --merged-message")
}

// TestGenerateSRSubjectRequiresMergedMessage verifies the flag dependency:
// --merged-sr-subject without --merged-message is a configuration error.
func TestGenerateSRSubjectRequiresMergedMessage(t *testing.T) {
	dir := t.TempDir()
	cfg := protogen.Config{
		SchemaPath:      schemaFixture(),
		Classes:         []string{"api_activity"},
		Version:         "1.8.0",
		OutDir:          dir,
		TagmapPath:      filepath.Join(dir, "field-numbers.json"),
		MergedSRSubject: "redpanda.ocsf.audit-events-value",
	}
	_, err := protogen.Generate(cfg)
	require.ErrorContains(t, err, "--merged-sr-subject requires --merged-message")
}

// TestCheckFailsWithoutMergedFlagOnMergedBaseline verifies that a baseline
// generated WITH MergedMessage fails --check when the flag is omitted (the
// merged file is then a stray), so CI cannot silently drop the merged layout.
func TestCheckFailsWithoutMergedFlagOnMergedBaseline(t *testing.T) {
	dir := t.TempDir()
	tagmapPath := filepath.Join(dir, "field-numbers.json")

	cfg := protogen.Config{
		SchemaPath:    schemaFixture(),
		Classes:       []string{"api_activity", "entity_management"},
		Version:       "1.8.0",
		OutDir:        dir,
		TagmapPath:    tagmapPath,
		MergedMessage: "AuditEvent",
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	checkCfg := cfg
	checkCfg.Check = true
	checkCfg.MergedMessage = ""
	err = protogen.Check(checkCfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "audit_event.proto")
}

// ─── Version cross-check ──────────────────────────────────────────────────────

// TestGenerateVersionMismatch verifies that Generate returns an error when
// --version does not match the schema's own version field (Fix 2).
func TestGenerateVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	cfg := protogen.Config{
		SchemaPath: schemaFixture(),
		Classes:    []string{"api_activity"},
		Version:    "9.9.9", // wrong: schema is 1.8.0
		OutDir:     dir,
		TagmapPath: filepath.Join(dir, "field-numbers.json"),
	}
	_, err := protogen.Generate(cfg)
	require.Error(t, err, "Generate must fail when --version mismatches schema version")
	require.Contains(t, err.Error(), "9.9.9")
	require.Contains(t, err.Error(), "1.8.0")
}

// TestCheckVersionMismatch verifies that Check returns an error when
// --version does not match the schema's own version field (Fix 2).
func TestCheckVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	// First generate a valid baseline with the correct version.
	cfg := protogen.Config{
		SchemaPath: schemaFixture(),
		Classes:    []string{"api_activity"},
		Version:    "1.8.0",
		OutDir:     dir,
		TagmapPath: filepath.Join(dir, "field-numbers.json"),
	}
	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	// Now check with the wrong version.
	cfg.Version = "9.9.9"
	cfg.Check = true
	err = protogen.Check(cfg)
	require.Error(t, err, "Check must fail when --version mismatches schema version")
	require.Contains(t, err.Error(), "9.9.9")
	require.Contains(t, err.Error(), "1.8.0")
}

// ─── CompatCheck ─────────────────────────────────────────────────────────────

// writeTagmap writes a minimal tagmap JSON with a single message "Msg" and the
// given field→tag assignments to a temp file and returns its path.
func writeTagmap(t *testing.T, fields map[string]int32) string {
	t.Helper()
	type msgEntry struct {
		Fields   map[string]int32 `json:"fields"`
		Reserved []int32          `json:"reserved"`
	}
	wire := map[string]msgEntry{
		"Msg": {Fields: fields, Reserved: nil},
	}
	b, err := json.MarshalIndent(wire, "", "  ")
	require.NoError(t, err)
	f, err := os.CreateTemp(t.TempDir(), "tagmap-*.json")
	require.NoError(t, err)
	_, err = f.Write(b)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

// TestCompatCheck_IncompatibleTagChange verifies that CompatCheck returns an
// error naming the field whose tag changed between old and new.
func TestCompatCheck_IncompatibleTagChange(t *testing.T) {
	oldPath := writeTagmap(t, map[string]int32{"field_x": 5})
	newPath := writeTagmap(t, map[string]int32{"field_x": 7})

	err := protogen.CompatCheck(oldPath, newPath)
	require.Error(t, err, "CompatCheck must fail when a tag changes")
	require.Contains(t, err.Error(), "field_x", "error must name the offending field")
}

// TestCompatCheck_CompatibleAdditive verifies that adding a new field to new
// without touching existing tags is not an error.
func TestCompatCheck_CompatibleAdditive(t *testing.T) {
	oldPath := writeTagmap(t, map[string]int32{"field_x": 5})
	newPath := writeTagmap(t, map[string]int32{"field_x": 5, "field_y": 6})

	err := protogen.CompatCheck(oldPath, newPath)
	require.NoError(t, err, "CompatCheck must pass for a purely additive change")
}

// TestCompatCheck_MissingOldFile verifies that a missing old tagmap (bootstrap:
// the base branch never had one) is treated as compatible and returns nil.
func TestCompatCheck_MissingOldFile(t *testing.T) {
	oldPath := filepath.Join(t.TempDir(), "does-not-exist.json")
	newPath := writeTagmap(t, map[string]int32{"field_x": 5})

	err := protogen.CompatCheck(oldPath, newPath)
	require.NoError(t, err, "CompatCheck must pass when old tagmap does not exist (bootstrap)")
}

// TestCompatCheck_SameFile verifies that comparing a tagmap against itself is
// always compatible.
func TestCompatCheck_SameFile(t *testing.T) {
	_, tagmapPath := committedBaseline()
	if _, err := os.Stat(tagmapPath); os.IsNotExist(err) {
		t.Skip("committed baseline not yet generated")
	}

	err := protogen.CompatCheck(tagmapPath, tagmapPath)
	require.NoError(t, err, "CompatCheck must pass when old and new are the same file")
}

// TestCompatCheck_UsesTagmapCheckCompat verifies the integration with
// tagmap.CheckCompat by constructing tagmaps directly and cross-checking the
// error text produced by CompatCheck against the underlying package.
func TestCompatCheck_UsesTagmapCheckCompat(t *testing.T) {
	oldPath := writeTagmap(t, map[string]int32{"alpha": 1, "beta": 2})
	// beta's tag changed from 2 → 3; alpha is fine.
	newPath := writeTagmap(t, map[string]int32{"alpha": 1, "beta": 3})

	err := protogen.CompatCheck(oldPath, newPath)
	require.Error(t, err)

	// Cross-check: the same inputs via tagmap.CheckCompat directly must also err.
	oldTM, loadErr := tagmap.Load(oldPath)
	require.NoError(t, loadErr)
	newTM, loadErr := tagmap.Load(newPath)
	require.NoError(t, loadErr)
	require.Error(t, tagmap.CheckCompat(oldTM, newTM))
}

// ─── Iceberg compatibility mode ───────────────────────────────────────────────

// icebergCfg returns the standard iceberg-compat config used by the tests
// below: full class selection, merged message, SR schemas, fresh temp dirs.
func icebergCfg(t *testing.T) protogen.Config {
	t.Helper()
	dir := t.TempDir()
	return protogen.Config{
		SchemaPath:      schemaFixture(),
		Classes:         []string{"api_activity", "entity_management"},
		Version:         "1.8.0",
		OutDir:          dir,
		TagmapPath:      filepath.Join(dir, "field-numbers.json"),
		SRSchemaOutDir:  t.TempDir(),
		MergedMessage:   "AuditEvent",
		MergedSRSubject: "redpanda.ocsf.audit-events-value",
		IcebergCompat:   true,
	}
}

// readTree reads every regular file under root into a map keyed by
// slash-separated relative path.
func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	require.NoError(t, err)
	return out
}

// TestGenerateIcebergCompat runs the full iceberg-compat pipeline against the
// OCSF 1.8.0 fixture: the sidecar records the known recursion back-edges, no
// emitted file (proto, SR schema, or SR Go embed) references struct.proto,
// and --check with the same config passes against the fresh baseline.
func TestGenerateIcebergCompat(t *testing.T) {
	cfg := icebergCfg(t)

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	sidecar, err := os.ReadFile(filepath.Join(cfg.OutDir, "ocsf", "v1", "iceberg-compat-prunes.txt"))
	require.NoError(t, err)
	for _, line := range []string{
		"Analytic.related_analytics R2-recursion-to-string",
		"LdapPerson.manager R2-recursion-to-string",
		"NetworkProxy.proxy_endpoint R2-recursion-to-string",
		"Process.parent_process R2-recursion-to-string",
		"ApiActivity.unmapped R1-value-to-string",
		"EntityManagement.unmapped R1-value-to-string",
		"File.accessor R3-path-to-string",
		"User.ldap_person R3-path-to-string",
	} {
		require.Contains(t, string(sidecar), line+"\n")
	}

	for path, content := range readTree(t, cfg.OutDir) {
		if strings.HasSuffix(path, ".json") {
			continue
		}
		require.NotContains(t, content, "google/protobuf/struct.proto", "file %s", path)
	}
	for path, content := range readTree(t, cfg.SRSchemaOutDir) {
		require.NotContains(t, content, "google/protobuf/struct.proto", "file %s", path)
		require.NotContains(t, content, "google.protobuf.Value", "file %s", path)
	}

	checkCfg := cfg
	checkCfg.Check = true
	require.NoError(t, protogen.Check(checkCfg),
		"--check must pass immediately after Generate with --iceberg-compat")
}

// TestGenerateIcebergCompat_Deterministic verifies two independent runs
// produce byte-identical trees (proto tree, tagmap, sidecar, SR schemas, and
// SR Go embeds).
func TestGenerateIcebergCompat_Deterministic(t *testing.T) {
	cfgA := icebergCfg(t)
	cfgB := icebergCfg(t)

	_, err := protogen.Generate(cfgA)
	require.NoError(t, err)
	_, err = protogen.Generate(cfgB)
	require.NoError(t, err)

	require.Equal(t, readTree(t, cfgA.OutDir), readTree(t, cfgB.OutDir),
		"two runs must produce byte-identical output trees")
	require.Equal(t, readTree(t, cfgA.SRSchemaOutDir), readTree(t, cfgB.SRSchemaOutDir),
		"two runs must produce byte-identical SR trees")
}

// TestIcebergCompat_TagmapCompatWithoutFlag verifies the append-only tagmap
// contract across modes: a tagmap produced WITHOUT --iceberg-compat stays
// compat-check-clean after a run WITH the flag reuses it (pruned fields keep
// their entries; nothing is renumbered or removed).
func TestIcebergCompat_TagmapCompatWithoutFlag(t *testing.T) {
	base := protogen.Config{
		SchemaPath:      schemaFixture(),
		Classes:         []string{"api_activity", "entity_management"},
		Version:         "1.8.0",
		OutDir:          t.TempDir(),
		TagmapPath:      filepath.Join(t.TempDir(), "field-numbers.json"),
		MergedMessage:   "AuditEvent",
		MergedSRSubject: "redpanda.ocsf.audit-events-value",
	}
	_, err := protogen.Generate(base)
	require.NoError(t, err)

	// Snapshot the non-compat tagmap as the "old" side.
	oldPath := filepath.Join(t.TempDir(), "old-field-numbers.json")
	raw, err := os.ReadFile(base.TagmapPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(oldPath, raw, 0o644))

	// Regenerate WITH the flag, reusing the same tagmap lineage.
	compat := base
	compat.OutDir = t.TempDir()
	compat.IcebergCompat = true
	_, err = protogen.Generate(compat)
	require.NoError(t, err)

	require.NoError(t, protogen.CompatCheck(oldPath, base.TagmapPath),
		"--compat-check must pass against a tagmap produced without --iceberg-compat")
}

// ─── SR Go embeds ─────────────────────────────────────────────────────────────

// TestGenerateWritesSRGo verifies that every .sr.proto gets a .sr.go
// companion embedding it as constants: the derived default package name, the
// per-class schema constants, and the merged subject constant.
func TestGenerateWritesSRGo(t *testing.T) {
	dir := t.TempDir()
	srDir := t.TempDir()

	cfg := protogen.Config{
		SchemaPath:      schemaFixture(),
		Classes:         []string{"api_activity", "entity_management"},
		Version:         "1.8.0",
		OutDir:          dir,
		TagmapPath:      filepath.Join(dir, "field-numbers.json"),
		SRSchemaOutDir:  srDir,
		MergedMessage:   "AuditEvent",
		MergedSRSubject: "redpanda.ocsf.audit-events-value",
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	classGo, err := os.ReadFile(filepath.Join(srDir, "api_activity.sr.go"))
	require.NoError(t, err)
	require.Contains(t, string(classGo), "package ocsfv1\n")
	require.Contains(t, string(classGo), "const ApiActivitySRSchema = `")
	require.NotContains(t, string(classGo), "SRSubject")

	mergedGo, err := os.ReadFile(filepath.Join(srDir, "audit_event.sr.go"))
	require.NoError(t, err)
	require.Contains(t, string(mergedGo), "const AuditEventSRSchema = `")
	require.Contains(t, string(mergedGo), `const AuditEventSRSubject = "redpanda.ocsf.audit-events-value"`)

	// The embedded schema text must be the exact .sr.proto content.
	mergedProto, err := os.ReadFile(filepath.Join(srDir, "audit_event.sr.proto"))
	require.NoError(t, err)
	require.Contains(t, string(mergedGo), "`"+string(mergedProto)+"`")

	// --check must pass against the just-generated SR baseline including the
	// .sr.go companions.
	checkCfg := cfg
	checkCfg.Check = true
	require.NoError(t, protogen.Check(checkCfg))
}

// TestGenerateSRGoPackageOverride verifies --sr-go-package overrides the
// derived package name.
func TestGenerateSRGoPackageOverride(t *testing.T) {
	dir := t.TempDir()
	srDir := t.TempDir()

	cfg := protogen.Config{
		SchemaPath:     schemaFixture(),
		Classes:        []string{"api_activity"},
		Version:        "1.8.0",
		OutDir:         dir,
		TagmapPath:     filepath.Join(dir, "field-numbers.json"),
		SRSchemaOutDir: srDir,
		SRGoPackage:    "auditschema",
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	classGo, err := os.ReadFile(filepath.Join(srDir, "api_activity.sr.go"))
	require.NoError(t, err)
	require.Contains(t, string(classGo), "package auditschema\n")
}

// TestGenerateSRGoOnly verifies consumers can commit the Go schema embeds
// without also retaining their intermediate .sr.proto inputs.
func TestGenerateSRGoOnly(t *testing.T) {
	dir := t.TempDir()
	srDir := t.TempDir()

	cfg := protogen.Config{
		SchemaPath:      schemaFixture(),
		Classes:         []string{"api_activity", "entity_management"},
		Version:         "1.8.0",
		OutDir:          dir,
		TagmapPath:      filepath.Join(dir, "field-numbers.json"),
		SRSchemaOutDir:  srDir,
		MergedMessage:   "AuditEvent",
		MergedSRSubject: "redpanda.ocsf.audit-events-value",
		SRGoOnly:        true,
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	for _, name := range []string{
		"api_activity.sr.go",
		"entity_management.sr.go",
		"audit_event.sr.go",
	} {
		require.FileExists(t, filepath.Join(srDir, name))
	}
	for _, name := range []string{
		"api_activity.sr.proto",
		"entity_management.sr.proto",
		"audit_event.sr.proto",
	} {
		require.NoFileExists(t, filepath.Join(srDir, name))
	}

	checkCfg := cfg
	checkCfg.Check = true
	require.NoError(t, protogen.Check(checkCfg))
}

func TestGenerateSRGoOnlyRequiresSchemaOut(t *testing.T) {
	cfg := protogen.Config{
		SchemaPath: schemaFixture(),
		Classes:    []string{"api_activity"},
		Version:    "1.8.0",
		OutDir:     t.TempDir(),
		TagmapPath: filepath.Join(t.TempDir(), "field-numbers.json"),
		SRGoOnly:   true,
	}

	_, err := protogen.Generate(cfg)
	require.ErrorContains(t, err, "--sr-go-only requires --sr-schema-out")
}

// TestCheckFailsOnStraySRGo verifies --check detects a committed *.sr.go the
// generator no longer produces.
func TestCheckFailsOnStraySRGo(t *testing.T) {
	dir := t.TempDir()
	srDir := t.TempDir()

	cfg := protogen.Config{
		SchemaPath:     schemaFixture(),
		Classes:        []string{"api_activity"},
		Version:        "1.8.0",
		OutDir:         dir,
		TagmapPath:     filepath.Join(dir, "field-numbers.json"),
		SRSchemaOutDir: srDir,
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(srDir, "dropped_class.sr.go"),
		[]byte("package ocsfv1\n"), 0o644))

	checkCfg := cfg
	checkCfg.Check = true
	err = protogen.Check(checkCfg)
	require.Error(t, err, "--check must fail on a stray committed .sr.go")
	require.Contains(t, err.Error(), "dropped_class.sr.go")
}

// cloudMaskYAML is a read mask close to what a real single-topic audit-log
// consumer publishes: the fields it actually populates, and nothing else. It
// doubles as the fixture proving the mask scales down the real OCSF schema, not
// just a synthetic one.
const cloudMaskYAML = `version: 1
paths:
  - action_id
  - activity_id
  - actor.app_name
  - actor.app_uid
  - actor.invoked_by
  - actor.user.email_addr
  - actor.user.name
  - api.operation
  - api.request.data
  - api.request.flags
  - api.response.code
  - api.response.data
  - api.response.error
  - api.response.flags
  - api.service.name
  - authorizations.decision
  - authorizations.policy.is_applied
  - authorizations.policy.name
  - authorizations.policy.uid
  - category_uid
  - class_uid
  - disposition_id
  - entity.data
  - entity.name
  - entity.type
  - entity.uid
  - entity_result.data
  - entity_result.name
  - entity_result.type
  - entity_result.uid
  - message
  - metadata.product.feature.name
  - metadata.product.name
  - metadata.product.vendor_name
  - metadata.profiles
  - metadata.tenant_uid
  - metadata.uid
  - metadata.version
  - policy.is_applied
  - policy.name
  - policy.uid
  - resources.name
  - resources.type
  - resources.uid
  - severity_id
  - src_endpoint.ip
  - src_endpoint.svc_name
  - status_detail
  - status_id
  - time
  - type_uid
`

// writeMask writes a mask file into dir and returns its path.
func writeMask(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "read-mask.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// TestGenerateWithMaskThenCheck is the end-to-end case against the real OCSF
// schema: a mask of the fields a consumer publishes collapses the emitted model
// by nearly two orders of magnitude, the report sidecar records it, and --check
// round-trips on the masked baseline.
func TestGenerateWithMaskThenCheck(t *testing.T) {
	dir := t.TempDir()
	srDir := t.TempDir()

	cfg := protogen.Config{
		SchemaPath:      schemaFixture(),
		Classes:         []string{"api_activity", "entity_management"},
		Version:         "1.8.0",
		OutDir:          dir,
		TagmapPath:      filepath.Join(dir, "field-numbers.json"),
		SRSchemaOutDir:  srDir,
		MergedMessage:   "AuditEvent",
		MergedOnly:      true,
		MergedSRSubject: "redpanda.ocsf.audit-events-value",
		SRGoOnly:        true,
		IcebergCompat:   true,
		MaskPath:        writeMask(t, dir, cloudMaskYAML),
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	// The keep set is reported against the class messages the mask resolves
	// through; the merged AuditEvent is the union of those, so it has no keep
	// entries of its own.
	report, err := os.ReadFile(filepath.Join(dir, "ocsf", "v1", "read-mask-report.txt"))
	require.NoError(t, err)
	require.Contains(t, string(report), "leaf columns:")
	require.Contains(t, string(report), "ApiActivity.actor")
	require.Contains(t, string(report), "EntityManagement.entity_result")
	require.Contains(t, string(report), "User.email_addr")
	require.Contains(t, string(report), "# (none)", "this mask closes off every shared embedding")

	// The masked schema must be dramatically smaller than the unmasked one, and
	// must no longer carry the objects the mask closed off.
	objects, err := os.ReadFile(filepath.Join(dir, "ocsf", "v1", "objects.proto"))
	require.NoError(t, err)
	require.Contains(t, string(objects), "message User {")
	require.NotContains(t, string(objects), "message Osint ",
		"osint is the single largest subtree and no mask path names it")
	require.NotContains(t, string(objects), "message Malware ")
	require.Less(t, strings.Count(string(objects), "\nmessage "), 30,
		"the retained object closure should be a fraction of the ~100 messages OCSF reaches")

	checkCfg := cfg
	checkCfg.Check = true
	require.NoError(t, protogen.Check(checkCfg),
		"--check must pass immediately after Generate on the same masked baseline")
}

// TestMaskShrinksIcebergPrunes verifies the ordering payoff on the real schema:
// masking first removes the deep subtrees that force R3 demotions, so far fewer
// fields have to be collapsed into JSON strings to satisfy Iceberg/Oxla.
func TestMaskShrinksIcebergPrunes(t *testing.T) {
	countPrunes := func(t *testing.T, maskPath string) int {
		t.Helper()
		dir := t.TempDir()
		cfg := protogen.Config{
			SchemaPath:    schemaFixture(),
			Classes:       []string{"api_activity", "entity_management"},
			Version:       "1.8.0",
			OutDir:        dir,
			TagmapPath:    filepath.Join(dir, "field-numbers.json"),
			MergedMessage: "AuditEvent",
			IcebergCompat: true,
			MaskPath:      maskPath,
		}
		_, err := protogen.Generate(cfg)
		require.NoError(t, err)

		body, err := os.ReadFile(filepath.Join(dir, "ocsf", "v1", "iceberg-compat-prunes.txt"))
		require.NoError(t, err)
		prunes := 0
		for line := range strings.SplitSeq(string(body), "\n") {
			if line != "" && !strings.HasPrefix(line, "#") {
				prunes++
			}
		}
		return prunes
	}

	unmasked := countPrunes(t, "")
	masked := countPrunes(t, writeMask(t, t.TempDir(), cloudMaskYAML))

	require.Positive(t, unmasked, "the full schema needs demotions to fit Iceberg")
	require.Less(t, masked, unmasked/2,
		"masking first should remove most of the fidelity loss (was %d prunes, now %d)", unmasked, masked)
}

// TestGenerateWithMaskPreservesFieldNumbers verifies the wire-stability
// guarantee through the CLI layer: masking an existing baseline neither
// renumbers the surviving fields nor shrinks the committed tagmap, so
// --compat-check stays clean and the mask can be widened later for free.
func TestGenerateWithMaskPreservesFieldNumbers(t *testing.T) {
	dir := t.TempDir()
	tagmapPath := filepath.Join(dir, "field-numbers.json")

	base := protogen.Config{
		SchemaPath:    schemaFixture(),
		Classes:       []string{"api_activity", "entity_management"},
		Version:       "1.8.0",
		OutDir:        dir,
		TagmapPath:    tagmapPath,
		MergedMessage: "AuditEvent",
		IcebergCompat: true,
	}

	// Generate unmasked first, so the tagmap holds every attribute.
	_, err := protogen.Generate(base)
	require.NoError(t, err)
	unmasked, err := tagmap.Load(tagmapPath)
	require.NoError(t, err)

	// Now regenerate the same baseline with a mask.
	masked := base
	masked.OutDir = t.TempDir()
	masked.MaskPath = writeMask(t, dir, cloudMaskYAML)
	_, err = protogen.Generate(masked)
	require.NoError(t, err)

	after, err := tagmap.Load(tagmapPath)
	require.NoError(t, err)
	require.NoError(t, tagmap.CheckCompat(unmasked, after),
		"masking must not renumber or drop tags — --compat-check runs exactly this")

	for _, attr := range []string{"actor", "time", "osint", "malware"} {
		want, ok := unmasked.Tag("AuditEvent", attr)
		require.True(t, ok, "baseline assigned %q", attr)
		got, ok := after.Tag("AuditEvent", attr)
		require.True(t, ok, "masked run kept the tagmap entry for %q", attr)
		require.Equal(t, want, got, "field number for %q must not move", attr)
	}
}

// TestGenerateWithMaskRejectsDroppedDiscriminator verifies the merged emission's
// hard dependency is enforced at generation time: without class_uid the merged
// CEL would reference a field that no longer exists.
func TestGenerateWithMaskRejectsDroppedDiscriminator(t *testing.T) {
	dir := t.TempDir()

	cfg := protogen.Config{
		SchemaPath:    schemaFixture(),
		Classes:       []string{"api_activity", "entity_management"},
		Version:       "1.8.0",
		OutDir:        dir,
		TagmapPath:    filepath.Join(dir, "field-numbers.json"),
		MergedMessage: "AuditEvent",
		MaskPath:      writeMask(t, dir, "version: 1\npaths:\n  - time\n  - category_uid\n  - type_uid\n"),
	}

	_, err := protogen.Generate(cfg)
	require.ErrorContains(t, err, `lost "class_uid"`)
}

// TestGenerateWithMaskUnresolvedPath verifies an allowlist entry that no longer
// matches the schema fails generation. This is what stops an OCSF rename from
// silently dropping a column consumers query.
func TestGenerateWithMaskUnresolvedPath(t *testing.T) {
	dir := t.TempDir()

	cfg := protogen.Config{
		SchemaPath: schemaFixture(),
		Classes:    []string{"api_activity"},
		Version:    "1.8.0",
		OutDir:     dir,
		TagmapPath: filepath.Join(dir, "field-numbers.json"),
		MaskPath:   writeMask(t, dir, "version: 1\npaths:\n  - time\n  - actor.user.emial_addr\n"),
	}

	_, err := protogen.Generate(cfg)
	require.ErrorContains(t, err, `has no attribute "emial_addr"`)
}

// TestGenerateWithMissingMaskFile verifies a bad --mask-file path fails loudly
// rather than falling back to emitting the whole schema.
func TestGenerateWithMissingMaskFile(t *testing.T) {
	dir := t.TempDir()

	cfg := protogen.Config{
		SchemaPath: schemaFixture(),
		Classes:    []string{"api_activity"},
		Version:    "1.8.0",
		OutDir:     dir,
		TagmapPath: filepath.Join(dir, "field-numbers.json"),
		MaskPath:   filepath.Join(dir, "does-not-exist.yaml"),
	}

	_, err := protogen.Generate(cfg)
	require.ErrorContains(t, err, "read mask file")
}

// TestMaskFromScratchTagmapMatchesUnmasked is the guarantee gen.ReserveTags
// exists to provide: field numbers are a function of the OCSF schema and the
// class selection alone, never of what the mask chose to emit. A tagmap
// bootstrapped with a mask active must therefore be identical to one
// bootstrapped without it.
//
// Before ReserveTags this was false and silently so: every one of the 20
// surviving AuditEvent fields moved (actor 7 -> 3, time 61 -> 19), which would
// decode every previously written record to an empty event.
//
// It also pins the coupling ReserveTags introduces — it must walk the same
// messages and the same within-message order as the emitters, so this test fails
// if the two ever drift apart.
func TestMaskFromScratchTagmapMatchesUnmasked(t *testing.T) {
	base := func(dir, maskPath string) protogen.Config {
		return protogen.Config{
			SchemaPath:    schemaFixture(),
			Classes:       []string{"api_activity", "entity_management"},
			Version:       "1.8.0",
			OutDir:        dir,
			TagmapPath:    filepath.Join(dir, "field-numbers.json"),
			MergedMessage: "AuditEvent",
			IcebergCompat: true,
			MaskPath:      maskPath,
		}
	}

	fullDir := t.TempDir()
	_, err := protogen.Generate(base(fullDir, ""))
	require.NoError(t, err)
	full, err := tagmap.Load(filepath.Join(fullDir, "field-numbers.json"))
	require.NoError(t, err)

	scratchDir := t.TempDir()
	_, err = protogen.Generate(base(scratchDir, writeMask(t, scratchDir, cloudMaskYAML)))
	require.NoError(t, err)
	scratch, err := tagmap.Load(filepath.Join(scratchDir, "field-numbers.json"))
	require.NoError(t, err)

	// The strongest form of the guarantee: the two bootstrapped tagmaps are the
	// same file. Not just the surviving fields — the excluded ones too, since
	// those are what let the mask be widened later without renumbering.
	fullJSON, err := os.ReadFile(filepath.Join(fullDir, "field-numbers.json"))
	require.NoError(t, err)
	scratchJSON, err := os.ReadFile(filepath.Join(scratchDir, "field-numbers.json"))
	require.NoError(t, err)
	require.Equal(t, string(fullJSON), string(scratchJSON),
		"a tagmap bootstrapped with a mask must be identical to one bootstrapped without it")

	// Spot-check the numbers that used to move, so a failure names them.
	for _, attr := range []string{"actor", "api", "metadata", "time", "type_uid"} {
		want, ok := full.Tag("AuditEvent", attr)
		require.True(t, ok)
		got, ok := scratch.Tag("AuditEvent", attr)
		require.True(t, ok)
		require.Equalf(t, want, got, "AuditEvent.%s renumbered under the mask", attr)
	}

	// Interchangeable in both directions — a from-scratch masked bootstrap is no
	// longer destructive.
	require.NoError(t, tagmap.CheckCompat(full, scratch))
	require.NoError(t, tagmap.CheckCompat(scratch, full))
}

// TestGenerateBroadToNarrowRemovesStrays is the adoption path this reconciliation
// exists for: an existing full baseline switched to the narrower layout. Before
// syncFiles the per-class artifacts stayed on disk and --check then rejected them
// as strays, with its own advice ("regenerate without --check") unable to clear
// them.
func TestGenerateBroadToNarrowRemovesStrays(t *testing.T) {
	dir := t.TempDir()
	srDir := t.TempDir()

	broad := protogen.Config{
		SchemaPath:     schemaFixture(),
		Classes:        []string{"api_activity", "entity_management"},
		Version:        "1.8.0",
		OutDir:         dir,
		TagmapPath:     filepath.Join(dir, "field-numbers.json"),
		SRSchemaOutDir: srDir,
		MergedMessage:  "AuditEvent",
	}
	_, err := protogen.Generate(broad)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(dir, "ocsf", "v1", "api_activity.proto"))
	require.FileExists(t, filepath.Join(srDir, "api_activity.sr.proto"))

	narrow := broad
	narrow.MergedOnly = true
	narrow.SRGoOnly = true
	_, err = protogen.Generate(narrow)
	require.NoError(t, err)

	// The previous layout's artifacts are gone...
	for _, p := range []string{"api_activity.proto", "entity_management.proto"} {
		require.NoFileExists(t, filepath.Join(dir, "ocsf", "v1", p))
	}
	for _, p := range []string{
		"api_activity.sr.proto", "api_activity.sr.go",
		"entity_management.sr.proto", "entity_management.sr.go",
		"audit_event.sr.proto",
	} {
		require.NoFileExists(t, filepath.Join(srDir, p))
	}
	// ...and the narrow layout's are present.
	require.FileExists(t, filepath.Join(dir, "ocsf", "v1", "audit_event.proto"))
	require.FileExists(t, filepath.Join(dir, "ocsf", "v1", "objects.proto"))
	require.FileExists(t, filepath.Join(srDir, "audit_event.sr.go"))

	checkCfg := narrow
	checkCfg.Check = true
	require.NoError(t, protogen.Check(checkCfg),
		"--check must pass right after the broad -> narrow regeneration")
}

// TestGenerateDroppingMaskClearsReport verifies the sidecars reconcile too:
// removing --mask-file must not leave a stale read-mask-report.txt describing a
// mask that is no longer applied.
func TestGenerateDroppingMaskClearsReport(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "ocsf", "v1", "read-mask-report.txt")

	masked := protogen.Config{
		SchemaPath:    schemaFixture(),
		Classes:       []string{"api_activity", "entity_management"},
		Version:       "1.8.0",
		OutDir:        dir,
		TagmapPath:    filepath.Join(dir, "field-numbers.json"),
		MergedMessage: "AuditEvent",
		MaskPath:      writeMask(t, dir, cloudMaskYAML),
	}
	_, err := protogen.Generate(masked)
	require.NoError(t, err)
	require.FileExists(t, report)

	unmasked := masked
	unmasked.MaskPath = ""
	_, err = protogen.Generate(unmasked)
	require.NoError(t, err)
	require.NoFileExists(t, report, "the stale mask report must be removed")

	checkCfg := unmasked
	checkCfg.Check = true
	require.NoError(t, protogen.Check(checkCfg))
}

// TestCheckDetectsStaleSidecar verifies check mode enumerates managed sidecars,
// not just .proto files — otherwise a stale report committed by hand (or left by
// an older generator) passes silently.
func TestCheckDetectsStaleSidecar(t *testing.T) {
	dir := t.TempDir()
	cfg := protogen.Config{
		SchemaPath:    schemaFixture(),
		Classes:       []string{"api_activity"},
		Version:       "1.8.0",
		OutDir:        dir,
		TagmapPath:    filepath.Join(dir, "field-numbers.json"),
		MergedMessage: "AuditEvent",
	}
	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "ocsf", "v1", "read-mask-report.txt"), []byte("stale\n"), 0o600))

	cfg.Check = true
	err = protogen.Check(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read-mask-report.txt")
}

// TestSyncFilesLeavesUnmanagedFilesAlone is the safety net on a destructive
// operation: reconciliation may only delete files whose names the generator
// claims, in the directories it writes to. Committed module files and anything
// hand-added must survive.
func TestSyncFilesLeavesUnmanagedFilesAlone(t *testing.T) {
	dir := t.TempDir()
	srDir := t.TempDir()
	tagmapPath := filepath.Join(dir, "field-numbers.json")

	cfg := protogen.Config{
		SchemaPath:     schemaFixture(),
		Classes:        []string{"api_activity", "entity_management"},
		Version:        "1.8.0",
		OutDir:         dir,
		TagmapPath:     tagmapPath,
		SRSchemaOutDir: srDir,
		MergedMessage:  "AuditEvent",
	}
	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	// Files the generator must never touch: module config at the out root, a
	// hand-added note inside the versioned dir, and a non-generated Go file in the
	// flat SR dir.
	bystanders := map[string]string{
		filepath.Join(dir, "buf.yaml"):               "version: v2\n",
		filepath.Join(dir, "buf.lock"):               "# lock\n",
		filepath.Join(dir, "ocsf", "v1", "NOTES.md"): "hand written\n",
		filepath.Join(srDir, "doc.go"):               "package schema\n",
		filepath.Join(srDir, "helper_extra.go.txt"):  "not generated\n",
	}
	for p, content := range bystanders {
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}

	// Regenerate narrower, which triggers reconciliation in both directories.
	narrow := cfg
	narrow.MergedOnly = true
	narrow.SRGoOnly = true
	_, err = protogen.Generate(narrow)
	require.NoError(t, err)

	for p, content := range bystanders {
		got, err := os.ReadFile(p)
		require.NoErrorf(t, err, "reconciliation deleted %q", p)
		require.Equal(t, content, string(got), "reconciliation modified %q", p)
	}
	require.FileExists(t, tagmapPath, "the tagmap must never be reconciled away")
}
