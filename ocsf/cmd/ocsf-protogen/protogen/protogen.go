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

	files, stubbed, err := gen.Emit(s, cfg.Classes, tm, cfg.Version)
	if err != nil {
		return nil, fmt.Errorf("emit proto: %w", err)
	}

	if err := writeFiles(cfg.OutDir, files); err != nil {
		return nil, err
	}

	if err := tm.Save(cfg.TagmapPath); err != nil {
		return nil, fmt.Errorf("save tagmap to %q: %w", cfg.TagmapPath, err)
	}

	return stubbed, nil
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

	files, stubbed, err := gen.Emit(s, cfg.Classes, newTM, cfg.Version)
	if err != nil {
		return fmt.Errorf("emit proto: %w", err)
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
	return diffTree(cfg.OutDir, files, committed)
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
