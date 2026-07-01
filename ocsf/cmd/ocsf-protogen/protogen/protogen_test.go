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

// committedBaseline returns the absolute path to the committed proto baseline
// and field-numbers.json that were generated from the CLI.
func committedBaseline() (protoPath, tagmapPath string) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	// testdata is at .../ocsf/cmd/ocsf-protogen/testdata/
	td := filepath.Join(filepath.Dir(thisFile), "..", "testdata")
	return filepath.Join(td, "api_entity.proto"), filepath.Join(td, "field-numbers.json")
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
	outPath := filepath.Join(dir, "out.proto")
	tagmapPath := filepath.Join(dir, "field-numbers.json")

	cfg := protogen.Config{
		SchemaPath: schemaFixture(),
		Classes:    []string{"api_activity", "entity_management"},
		Version:    "1.8.0",
		OutPath:    outPath,
		TagmapPath: tagmapPath,
		Check:      false,
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	// Both files should have been created.
	require.FileExists(t, outPath)
	require.FileExists(t, tagmapPath)

	// --check against the just-generated baseline must pass.
	checkCfg := cfg
	checkCfg.Check = true
	err = protogen.Check(checkCfg)
	require.NoError(t, err, "--check must pass immediately after Generate on the same baseline")
}

// ─── Check: tagmap incompatibility ────────────────────────────────────────────

// TestCheckFailsOnProtoDriftFromEditedTagmap verifies that protogen.Check
// detects proto drift when the on-disk tagmap has been manually edited so that
// a field's assigned tag differs from what Emit would use, causing the freshly
// generated proto to diverge from the committed baseline.
func TestCheckFailsOnProtoDriftFromEditedTagmap(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.proto")
	tagmapPath := filepath.Join(dir, "field-numbers.json")

	cfg := protogen.Config{
		SchemaPath: schemaFixture(),
		Classes:    []string{"api_activity", "entity_management"},
		Version:    "1.8.0",
		OutPath:    outPath,
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
	outPath := filepath.Join(dir, "out.proto")
	tagmapPath := filepath.Join(dir, "field-numbers.json")

	cfg := protogen.Config{
		SchemaPath: schemaFixture(),
		Classes:    []string{"api_activity", "entity_management"},
		Version:    "1.8.0",
		OutPath:    outPath,
		TagmapPath: tagmapPath,
	}

	_, err := protogen.Generate(cfg)
	require.NoError(t, err)

	// Corrupt the committed proto file.
	content, err := os.ReadFile(outPath)
	require.NoError(t, err)
	corrupted := string(content) + "\n// CORRUPTED BY TEST\n"
	require.NoError(t, os.WriteFile(outPath, []byte(corrupted), 0o644))

	// Check must fail with a drift error.
	checkCfg := cfg
	checkCfg.Check = true
	err = protogen.Check(checkCfg)
	require.Error(t, err, "--check must fail when committed proto differs from fresh output")
	require.Contains(t, err.Error(), "differs", "error must describe the drift")
}

// ─── Check: committed baseline ───────────────────────────────────────────────

// TestCheckAgainstCommittedBaseline runs --check against the files committed to
// the repository (testdata/api_entity.proto and testdata/field-numbers.json).
// This test fails if the committed baseline drifts from what the generator
// would produce today.
func TestCheckAgainstCommittedBaseline(t *testing.T) {
	protoPath, tagmapPath := committedBaseline()

	// Skip gracefully if the committed baseline does not exist yet
	// (before the first `go run ./cmd/ocsf-protogen --out ... --tagmap ...` is run).
	if _, err := os.Stat(protoPath); os.IsNotExist(err) {
		t.Skip("committed baseline not yet generated; run: go run ./cmd/ocsf-protogen ...")
	}
	if _, err := os.Stat(tagmapPath); os.IsNotExist(err) {
		t.Skip("committed baseline not yet generated; run: go run ./cmd/ocsf-protogen ...")
	}

	cfg := protogen.Config{
		SchemaPath: schemaFixture(),
		Classes:    []string{"api_activity", "entity_management"},
		Version:    "1.8.0",
		OutPath:    protoPath,
		TagmapPath: tagmapPath,
		Check:      true,
	}

	err := protogen.Check(cfg)
	require.NoError(t, err, "--check must pass against the committed baseline")
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
		OutPath:    filepath.Join(dir, "out.proto"),
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
		OutPath:    filepath.Join(dir, "out.proto"),
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
