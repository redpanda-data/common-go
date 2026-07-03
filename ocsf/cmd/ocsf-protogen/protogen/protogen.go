// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package protogen contains the testable logic for the ocsf-protogen CLI.
// main.go stays thin; everything that can be unit-tested lives here.
package protogen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/gen"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/schema"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/tagmap"
)

// Config holds the parsed CLI parameters.
type Config struct {
	SchemaPath string
	Classes    []string
	Version    string
	// OutDir is the module root directory. Generated files are written under it
	// at their module-relative GeneratedFile.Path (e.g. <OutDir>/ocsf/v1/...).
	OutDir     string
	TagmapPath string
	Check      bool
	// SRSchemaOutDir, when non-empty, is the directory into which the
	// self-contained Schema-Registry schema files (<class>.sr.proto) are written
	// after the main Emit. It is unrelated to OutDir: SR files are flat, not
	// module-relative. Empty disables SR schema emission entirely.
	SRSchemaOutDir string
	// MergedMessage, when non-empty, is the proto message name (e.g.
	// "AuditEvent") of the single flat merged event message emitted IN ADDITION
	// to the per-class files: the union of all class attributes in one message,
	// for a single-topic audit log. The merged file lands next to the per-class
	// files (ocsf/v<N>/<snake_case_name>.proto) and shares the same tagmap.
	// When SRSchemaOutDir is also set, a merged <snake_case_name>.sr.proto is
	// written there too. Empty disables merged emission.
	MergedMessage string
	// MergedSRSubject, when non-empty, annotates the merged message with the
	// (redpanda.api.common.v1.schema_registry) option carrying this subject,
	// so protoc-gen-go-sr-normalize generates the SR schema and subject
	// constants from the emitted proto. Requires MergedMessage.
	MergedSRSubject string
}

// Generate loads the schema, emits the proto, writes --out and --tagmap.
// Returns the stubbed object names (may be nil) and any error.
func Generate(cfg Config) (stubbed []string, err error) {
	s, err := schema.LoadFile(cfg.SchemaPath)
	if err != nil {
		return nil, fmt.Errorf("load schema: %w", err)
	}

	if cfg.Version != s.Version {
		return nil, fmt.Errorf("--version %q does not match schema version %q", cfg.Version, s.Version)
	}

	tm, err := tagmap.Load(cfg.TagmapPath)
	if err != nil {
		return nil, fmt.Errorf("load tagmap: %w", err)
	}

	files, stubbed, err := emitAll(s, cfg, tm)
	if err != nil {
		return nil, err
	}

	if err := writeFiles(cfg.OutDir, files); err != nil {
		return nil, err
	}

	// Emit self-contained SR schemas using the SAME tagmap so field numbers match
	// the main output. Do this before tm.Save so the (idempotent) Assign calls are
	// persisted too.
	if strings.TrimSpace(cfg.SRSchemaOutDir) != "" {
		srFiles, err := emitAllSRSchemas(s, cfg, tm)
		if err != nil {
			return nil, err
		}
		if err := writeFiles(cfg.SRSchemaOutDir, srFiles); err != nil {
			return nil, err
		}
	}

	if err := tm.Save(cfg.TagmapPath); err != nil {
		return nil, fmt.Errorf("save tagmap to %q: %w", cfg.TagmapPath, err)
	}

	return stubbed, nil
}

// emitAll runs the per-class Emit plus, when cfg.MergedMessage is set, the
// merged single-event EmitMerged, and combines the file lists.
//
// Both paths emit an identical objects.proto (same object closure, same tags
// from the shared tagmap); the duplicate is dropped after an equality check so
// a divergence surfaces as an error instead of a silent overwrite.
func emitAll(s *schema.Schema, cfg Config, tm *tagmap.TagMap) (files []gen.GeneratedFile, stubbed []string, err error) {
	files, stubbed, err = gen.Emit(s, cfg.Classes, tm, cfg.Version)
	if err != nil {
		return nil, nil, fmt.Errorf("emit proto: %w", err)
	}

	if strings.TrimSpace(cfg.MergedMessage) == "" {
		if strings.TrimSpace(cfg.MergedSRSubject) != "" {
			return nil, nil, errors.New("--merged-sr-subject requires --merged-message")
		}
		return files, stubbed, nil
	}

	if err := checkMergedNameCollision(cfg.MergedMessage, cfg.Classes); err != nil {
		return nil, nil, err
	}

	mergedFiles, mergedStubbed, err := gen.EmitMerged(s, cfg.Classes, tm, cfg.Version, cfg.MergedMessage, cfg.MergedSRSubject)
	if err != nil {
		return nil, nil, fmt.Errorf("emit merged proto: %w", err)
	}
	// Stub sets are identical by construction (same classes, same closure), so
	// the per-class stub list already covers the merged run.
	_ = mergedStubbed

	byPath := make(map[string]string, len(files))
	for _, f := range files {
		byPath[f.Path] = f.Content
	}
	for _, f := range mergedFiles {
		existing, ok := byPath[f.Path]
		if !ok {
			files = append(files, f)
			continue
		}
		if existing != f.Content {
			return nil, nil, fmt.Errorf("merged emission produced %q with different content than per-class emission", f.Path)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, stubbed, nil
}

// checkMergedNameCollision rejects a merged message name that collides with a
// selected class's PascalCase message name: the two distinct messages would
// silently share one (name, attribute) tag lineage in the tagmap.
func checkMergedNameCollision(mergedMessage string, classes []string) error {
	for _, class := range classes {
		if gen.ClassMessageName(class) == mergedMessage {
			return fmt.Errorf(
				"--merged-message %q collides with the message name of class %q; choose a distinct name",
				mergedMessage, class,
			)
		}
	}
	return nil
}

// emitAllSRSchemas runs the per-class EmitSRSchemas plus, when
// cfg.MergedMessage is set, the merged EmitMergedSRSchema.
func emitAllSRSchemas(s *schema.Schema, cfg Config, tm *tagmap.TagMap) ([]gen.GeneratedFile, error) {
	srFiles, err := gen.EmitSRSchemas(s, cfg.Classes, tm, cfg.Version)
	if err != nil {
		return nil, fmt.Errorf("emit sr schemas: %w", err)
	}
	if strings.TrimSpace(cfg.MergedMessage) != "" {
		mergedSR, err := gen.EmitMergedSRSchema(s, cfg.Classes, tm, cfg.Version, cfg.MergedMessage)
		if err != nil {
			return nil, fmt.Errorf("emit merged sr schema: %w", err)
		}
		srFiles = append(srFiles, mergedSR)
		sort.Slice(srFiles, func(i, j int) bool { return srFiles[i].Path < srFiles[j].Path })
	}
	return srFiles, nil
}

// writeFiles writes each generated file to <outDir>/<file.Path>, creating parent
// directories as needed. Paths are slash-separated and converted to the host
// separator.
func writeFiles(outDir string, files []gen.GeneratedFile) error {
	for _, f := range files {
		dst := filepath.Join(outDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return fmt.Errorf("create dir for %q: %w", dst, err)
		}
		if err := os.WriteFile(dst, []byte(f.Content), 0o600); err != nil {
			return fmt.Errorf("write proto to %q: %w", dst, err)
		}
	}
	return nil
}

// Check regenerates the proto tree in memory and detects drift vs. the committed
// baseline tree rooted at OutDir (and the committed --tagmap). It returns a
// non-nil error with a descriptive message when:
//   - the tagmap is incompatible (tag number changed, dropped without reserve, etc.)
//   - the generated file set differs from the committed tree (a file was added,
//     removed, or its content changed)
//   - a new stubbed object appears (indicates schema regression)
func Check(cfg Config) error {
	s, err := schema.LoadFile(cfg.SchemaPath)
	if err != nil {
		return fmt.Errorf("load schema: %w", err)
	}

	if cfg.Version != s.Version {
		return fmt.Errorf("--version %q does not match schema version %q", cfg.Version, s.Version)
	}

	// Load the committed tagmap as "old".
	oldTM, err := tagmap.Load(cfg.TagmapPath)
	if err != nil {
		return fmt.Errorf("load committed tagmap: %w", err)
	}

	// Create a fresh copy to emit into ("new").
	newTM, err := tagmap.Load(cfg.TagmapPath)
	if err != nil {
		return fmt.Errorf("copy tagmap for check: %w", err)
	}

	files, stubbed, err := emitAll(s, cfg, newTM)
	if err != nil {
		return err
	}

	// Read the committed tree so we can compare stub lists and full content.
	committed, err := readCommittedTree(cfg.OutDir, files)
	if err != nil {
		return err
	}

	// Concatenate committed content to detect NEW stubs (absent from baseline).
	var committedAll strings.Builder
	for _, c := range committed {
		committedAll.WriteString(c)
	}
	for _, stubName := range stubbed {
		stubDecl := "message " + stubName + " {}"
		if !strings.Contains(committedAll.String(), stubDecl) {
			return fmt.Errorf("new stub message %q appeared in generated proto but is absent from committed baseline — "+
				"schema regression or missing object in schema snapshot", stubName)
		}
	}

	// Tag-map compatibility.
	if err := tagmap.CheckCompat(oldTM, newTM); err != nil {
		return fmt.Errorf("tagmap incompatibility detected (regenerate and commit field-numbers.json):\n%w", err)
	}

	// File-set drift: any file added, removed, or changed.
	if err := diffTree(cfg.OutDir, files, committed); err != nil {
		return err
	}

	// SR schema drift (only when the SR output dir is configured). SR files are
	// written flat under SRSchemaOutDir, so we compare each generated file against
	// its committed counterpart there.
	if strings.TrimSpace(cfg.SRSchemaOutDir) != "" {
		srFiles, err := emitAllSRSchemas(s, cfg, newTM)
		if err != nil {
			return err
		}
		if err := diffSRSchemas(cfg.SRSchemaOutDir, srFiles); err != nil {
			return err
		}
	}

	return nil
}

// diffSRSchemas compares freshly generated SR schema files against their
// committed counterparts under srOutDir and returns a descriptive error on any
// missing, changed, or stray file. Stray detection mirrors diffTree: a
// committed *.sr.proto the generator no longer produces (e.g. after a class is
// dropped from --classes) must fail the check rather than rot silently.
func diffSRSchemas(srOutDir string, files []gen.GeneratedFile) error {
	want := make(map[string]struct{}, len(files))
	for _, f := range files {
		want[f.Path] = struct{}{}
		p := filepath.Join(srOutDir, filepath.FromSlash(f.Path))
		b, err := os.ReadFile(filepath.Clean(p))
		switch {
		case os.IsNotExist(err):
			return fmt.Errorf(
				"generated SR schema %q is missing from %q "+
					"(run ocsf-protogen without --check to regenerate, then commit the diff)",
				f.Path, srOutDir,
			)
		case err != nil:
			return fmt.Errorf("read committed SR schema %q: %w", p, err)
		}
		if string(b) != f.Content {
			return fmt.Errorf(
				"committed SR schema %q differs from freshly generated output "+
					"(run ocsf-protogen without --check to regenerate, then commit the diff)",
				f.Path,
			)
		}
	}

	entries, err := os.ReadDir(srOutDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read committed SR schema dir %q: %w", srOutDir, err)
	}
	var stray []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sr.proto") {
			continue
		}
		if _, ok := want[e.Name()]; !ok {
			stray = append(stray, e.Name())
		}
	}
	if len(stray) > 0 {
		sort.Strings(stray)
		return fmt.Errorf(
			"SR schema dir %q contains files not produced by the generator: %s "+
				"(run ocsf-protogen without --check to regenerate, then commit the diff)",
			srOutDir, strings.Join(stray, ", "),
		)
	}
	return nil
}

// readCommittedTree loads the committed content of every path produced by Emit
// plus every committed .proto file under the versioned directories, keyed by
// module-relative slash path. A generated path that is missing on disk is
// recorded as absent (empty string, not present in the map) so diffTree can
// report it as added.
func readCommittedTree(outDir string, files []gen.GeneratedFile) (map[string]string, error) {
	committed := make(map[string]string)

	// Read each generated file's committed counterpart (if present).
	for _, f := range files {
		p := filepath.Join(outDir, filepath.FromSlash(f.Path))
		b, err := os.ReadFile(filepath.Clean(p))
		switch {
		case err == nil:
			committed[f.Path] = string(b)
		case os.IsNotExist(err):
			// leave absent
		default:
			return nil, fmt.Errorf("read committed proto %q: %w", p, err)
		}
	}

	// Also enumerate committed .proto files under each versioned dir the
	// generator writes into, so we catch files that should have been removed.
	dirs := make(map[string]struct{})
	for _, f := range files {
		dirs[filepath.Dir(f.Path)] = struct{}{}
	}
	for d := range dirs {
		root := filepath.Join(outDir, filepath.FromSlash(d))
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read committed dir %q: %w", root, err)
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".proto" {
				continue
			}
			rel := d + "/" + e.Name()
			if _, ok := committed[rel]; ok {
				continue
			}
			b, err := os.ReadFile(filepath.Clean(filepath.Join(root, e.Name())))
			if err != nil {
				return nil, fmt.Errorf("read committed proto %q: %w", rel, err)
			}
			committed[rel] = string(b)
		}
	}

	return committed, nil
}

// diffTree compares the generated file set against the committed tree and
// returns a descriptive error on any add/remove/change.
func diffTree(outDir string, files []gen.GeneratedFile, committed map[string]string) error {
	want := make(map[string]string, len(files))
	for _, f := range files {
		want[f.Path] = f.Content
	}

	// Missing or changed generated files.
	for _, f := range files {
		got, ok := committed[f.Path]
		if !ok {
			return fmt.Errorf(
				"generated file %q is missing from committed tree rooted at %q "+
					"(run ocsf-protogen without --check to regenerate, then commit the diff)",
				f.Path, outDir,
			)
		}
		if got != f.Content {
			return fmt.Errorf(
				"committed proto %q differs from freshly generated output "+
					"(run ocsf-protogen without --check to regenerate, then commit the diff)",
				f.Path,
			)
		}
	}

	// Stray committed files not produced by the generator.
	stray := make([]string, 0)
	for path := range committed {
		if _, ok := want[path]; !ok {
			stray = append(stray, path)
		}
	}
	if len(stray) > 0 {
		sort.Strings(stray)
		return fmt.Errorf(
			"committed tree rooted at %q contains files not produced by the generator: %s "+
				"(run ocsf-protogen without --check to regenerate, then commit the diff)",
			outDir, strings.Join(stray, ", "),
		)
	}

	return nil
}

// CompatCheck loads two tagmap JSON files and runs CheckCompat(old, new).
//
// If oldPath does not exist (bootstrap: the baseline didn't exist on the base
// branch yet), CompatCheck returns nil and prints a diagnostic to stderr so CI
// remains green on the first PR that introduces the tagmap.
//
// Returns a non-nil error with a descriptive message if the new tagmap breaks
// wire stability (tag changed, tag dropped without reserve, reserved tag reused).
func CompatCheck(oldPath, newPath string) error {
	oldTM, err := tagmap.Load(oldPath)
	if err != nil {
		// tagmap.Load returns a new empty map for a missing file (ErrNotExist).
		// Any other error (permissions, malformed JSON) is a hard failure.
		return fmt.Errorf("load old tagmap %q: %w", oldPath, err)
	}

	// Distinguish "file did not exist" (empty map from Load) from a real load.
	// We need to check via os.Stat so we can print the bootstrap message.
	if _, statErr := os.Stat(oldPath); os.IsNotExist(statErr) {
		fmt.Fprintf(os.Stderr, "no prior tagmap at %q; skipping compat check (bootstrap)\n", oldPath)
		return nil
	}

	newTM, err := tagmap.Load(newPath)
	if err != nil {
		return fmt.Errorf("load new tagmap %q: %w", newPath, err)
	}

	if err := tagmap.CheckCompat(oldTM, newTM); err != nil {
		return fmt.Errorf("tagmap compat check failed (field numbers changed between base and PR):\n%w", err)
	}
	return nil
}

// ParseClasses splits a comma-separated class list and trims whitespace.
// Returns an error if the result is empty.
func ParseClasses(s string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(s, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("--classes must be a non-empty comma-separated list")
	}
	return out, nil
}
