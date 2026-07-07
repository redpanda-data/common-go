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
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/gen"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/tagmap"
)

// srGoldenDir is where the committed SR schema goldens live. Files land directly
// under it as <class_name>.sr.proto (flat, no version directory).
const srGoldenDir = "testdata/golden/sr"

// srClasses is the class selection used by the SR golden test.
var srClasses = []string{"api_activity", "entity_management"}

// TestEmitSRSchemas_Golden emits the SR schema files for api_activity +
// entity_management and compares each against the committed golden under
// testdata/golden/sr/. Run with -update to regenerate.
func TestEmitSRSchemas_Golden(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	files, err := gen.EmitSRSchemas(s, srClasses, tm, "1.8.0")
	require.NoError(t, err)
	require.Len(t, files, len(srClasses))

	if *update {
		require.NoError(t, os.MkdirAll(srGoldenDir, 0o755))
		for _, f := range files {
			dst := filepath.Join(srGoldenDir, filepath.FromSlash(f.Path))
			require.NoError(t, os.WriteFile(dst, []byte(f.Content), 0o644))
		}
		t.Logf("SR golden files updated under %s (%d files)", srGoldenDir, len(files))
		return
	}

	for _, f := range files {
		golden := filepath.Join(srGoldenDir, filepath.FromSlash(f.Path))
		want, readErr := os.ReadFile(golden)
		require.NoError(t, readErr, "SR golden %s missing; re-run with -update", f.Path)
		require.Equal(t, string(want), f.Content,
			"SR schema %s does not match golden; re-run with -update if intentional", f.Path)
	}
}

// TestEmitSRSchemas_Paths verifies each file is named <class_name>.sr.proto with
// no directory prefix.
func TestEmitSRSchemas_Paths(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	files, err := gen.EmitSRSchemas(s, srClasses, tm, "1.8.0")
	require.NoError(t, err)

	got := make([]string, 0, len(files))
	for _, f := range files {
		got = append(got, f.Path)
		require.NotContains(t, f.Path, "/", "SR schema path must be flat (no directory prefix)")
	}
	require.ElementsMatch(t, []string{"api_activity.sr.proto", "entity_management.sr.proto"}, got)
}

// TestEmitSRSchemas_NoBufValidate asserts that NO buf.validate substring and NO
// buf/validate import appears anywhere in any SR schema output.
func TestEmitSRSchemas_NoBufValidate(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	files, err := gen.EmitSRSchemas(s, srClasses, tm, "1.8.0")
	require.NoError(t, err)

	for _, f := range files {
		require.NotContains(t, f.Content, "buf.validate",
			"SR schema %s must not contain any buf.validate output", f.Path)
		require.NotContains(t, f.Content, `import "buf/validate`,
			"SR schema %s must not import buf/validate", f.Path)
		require.NotContains(t, f.Content, `import "ocsf/`,
			"SR schema %s must not import the objects file (objects are inlined)", f.Path)
	}
}

// TestEmitSRSchemas_ClassMessageFirst asserts the class message is the FIRST
// `message ` declaration in each file (Confluent message-index 0).
func TestEmitSRSchemas_ClassMessageFirst(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	files, err := gen.EmitSRSchemas(s, srClasses, tm, "1.8.0")
	require.NoError(t, err)

	// class_name -> expected leading message name.
	wantFirst := map[string]string{
		"api_activity.sr.proto":      "ApiActivity",
		"entity_management.sr.proto": "EntityManagement",
	}

	msgRe := regexp.MustCompile(`(?m)^message (\w+) \{`)
	for _, f := range files {
		m := msgRe.FindStringSubmatch(f.Content)
		require.NotNil(t, m, "SR schema %s must contain a top-level message", f.Path)
		require.Equal(t, wantFirst[f.Path], m[1],
			"first message in %s must be the class message (index 0)", f.Path)

		// Package sanity: SR schema must use the same package as the main output.
		require.Contains(t, f.Content, "package ocsf.v1;")
	}
}

// TestEmitSRSchemas_FieldNumberParity runs Emit and EmitSRSchemas with the SAME
// fresh tagmap and asserts that representative field tags on the ApiActivity
// class message are identical between the main api_activity.proto and the
// api_activity.sr.proto.
func TestEmitSRSchemas_FieldNumberParity(t *testing.T) {
	s := loadFixture(t)
	tm := tagmap.New()

	mainFiles, _, err := gen.Emit(s, []string{"api_activity"}, tm, "1.8.0")
	require.NoError(t, err)

	srFiles, err := gen.EmitSRSchemas(s, []string{"api_activity"}, tm, "1.8.0")
	require.NoError(t, err)

	mainContent := findFile(t, filenameToContent(mainFiles), "ocsf/v1/api_activity.proto")
	srContent := findFile(t, filenameToContent(srFiles), "api_activity.sr.proto")

	// Representative fields on ApiActivity.
	for _, field := range []string{"activity_id", "actor", "time", "type_uid"} {
		mainTag := extractTag(t, mainContent, field)
		srTag := extractTag(t, srContent, field)
		require.Equal(t, mainTag, srTag,
			"field %q must have the same tag in main and SR output", field)
	}
}

// filenameToContent builds a path→content map from a slice of GeneratedFiles.
func filenameToContent(files []gen.GeneratedFile) map[string]string {
	m := make(map[string]string, len(files))
	for _, f := range files {
		m[f.Path] = f.Content
	}
	return m
}

func findFile(t *testing.T, m map[string]string, path string) string {
	t.Helper()
	c, ok := m[path]
	require.True(t, ok, "expected generated file %q", path)
	return c
}

// extractTag finds the ` <field> = <n>;` tag for a field within a message body.
// It matches the first occurrence, which for the class-first files is the class
// message's field.
func extractTag(t *testing.T, content, field string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)\b` + regexp.QuoteMeta(field) + ` = (\d+)`)
	m := re.FindStringSubmatch(content)
	require.NotNil(t, m, "field %q not found in content", field)
	return m[1]
}

// TestEmitSRSchemas_Deterministic verifies two calls with fresh equivalent
// tagmaps produce byte-identical output.
func TestEmitSRSchemas_Deterministic(t *testing.T) {
	s := loadFixture(t)

	f1, err := gen.EmitSRSchemas(s, srClasses, tagmap.New(), "1.8.0")
	require.NoError(t, err)
	f2, err := gen.EmitSRSchemas(s, srClasses, tagmap.New(), "1.8.0")
	require.NoError(t, err)

	require.Equal(t, len(f1), len(f2))
	for i := range f1 {
		require.Equal(t, f1[i].Path, f2[i].Path)
		require.Equal(t, f1[i].Content, f2[i].Content)
	}
}

// TestEmitSRSchemas_UnknownClass verifies a non-existent class errors.
func TestEmitSRSchemas_UnknownClass(t *testing.T) {
	s := loadFixture(t)
	_, err := gen.EmitSRSchemas(s, []string{"nonexistent_class_xyz"}, tagmap.New(), "1.8.0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonexistent_class_xyz")
}

// TestEmitSRSchemas_HeaderShape verifies the shared header lines.
func TestEmitSRSchemas_HeaderShape(t *testing.T) {
	s := loadFixture(t)
	files, err := gen.EmitSRSchemas(s, []string{"api_activity"}, tagmap.New(), "1.8.0")
	require.NoError(t, err)
	require.Len(t, files, 1)
	c := files[0].Content
	require.True(t, strings.HasPrefix(c, "// Code generated by ocsf-protogen. DO NOT EDIT.\n"))
	require.Contains(t, c, "// Source: OCSF schema 1.8.0")
	require.Contains(t, c, `syntax = "proto3";`)
}
