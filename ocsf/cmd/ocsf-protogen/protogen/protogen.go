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
	OutPath    string
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

	protoOut, stubbed, err := gen.Emit(s, cfg.Classes, tm, cfg.Version)
	if err != nil {
		return nil, fmt.Errorf("emit proto: %w", err)
	}

	if err := os.WriteFile(cfg.OutPath, []byte(protoOut), 0o600); err != nil {
		return nil, fmt.Errorf("write proto to %q: %w", cfg.OutPath, err)
	}

	if err := tm.Save(cfg.TagmapPath); err != nil {
		return nil, fmt.Errorf("save tagmap to %q: %w", cfg.TagmapPath, err)
	}

	return stubbed, nil
}

// Check regenerates the proto in memory and detects drift vs. the committed
// baseline files (--out and --tagmap). It returns a non-nil error with a
// descriptive message when:
//   - the tagmap is incompatible (tag number changed, dropped without reserve, etc.)
//   - the emitted proto text differs from the committed --out file
//   - stubbed objects appear in the output (indicates schema regression)
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

	protoOut, stubbed, err := gen.Emit(s, cfg.Classes, newTM, cfg.Version)
	if err != nil {
		return fmt.Errorf("emit proto: %w", err)
	}

	// Read the committed proto before the diff so we can compare stub lists.
	committedBytes, err := os.ReadFile(cfg.OutPath)
	if err != nil {
		return fmt.Errorf("read committed proto %q: %w", cfg.OutPath, err)
	}

	// Stubs in --check are only a hard failure when they are NEW (i.e. absent
	// from the committed baseline).  An existing partial schema fixture may
	// legitimately produce stubs, and the proto-drift check below already
	// catches any regression.  We detect "new" stubs by checking whether the
	// fresh proto introduces stub declarations not present in the baseline.
	for _, stubName := range stubbed {
		stubDecl := "message " + stubName + " {}"
		if !strings.Contains(string(committedBytes), stubDecl) {
			return fmt.Errorf("new stub message %q appeared in generated proto but is absent from committed baseline — "+
				"schema regression or missing object in schema snapshot", stubName)
		}
	}

	// Tag-map compatibility.
	if err := tagmap.CheckCompat(oldTM, newTM); err != nil {
		return fmt.Errorf("tagmap incompatibility detected (regenerate and commit field-numbers.json):\n%w", err)
	}

	// Proto content drift.
	if string(committedBytes) != protoOut {
		return fmt.Errorf(
			"committed proto %q differs from freshly generated output "+
				"(run ocsf-protogen without --check to regenerate, then commit the diff)",
			cfg.OutPath,
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
