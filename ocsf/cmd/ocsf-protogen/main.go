// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Command ocsf-protogen generates proto3 definitions from a compiled OCSF
// schema JSON export.
//
// Normal mode (no --check):
//
//	ocsf-protogen \
//	  --schema  ocsf/internal/ocsf/schema/testdata/ocsf-1.8.0.json \
//	  --classes api_activity,entity_management \
//	  --version 1.8.0 \
//	  --out     ocsf/cmd/ocsf-protogen/testdata \
//	  --tagmap  ocsf/cmd/ocsf-protogen/testdata/field-numbers.json
//
// --out is a MODULE ROOT DIRECTORY. Files are written under it at their
// module-relative paths, e.g. <out>/ocsf/v1/api_activity.proto,
// <out>/ocsf/v1/entity_management.proto, <out>/ocsf/v1/objects.proto.
//
// Check mode (for CI — verifies committed baseline is up-to-date):
//
//	ocsf-protogen --check \
//	  --schema  ocsf/internal/ocsf/schema/testdata/ocsf-1.8.0.json \
//	  --classes api_activity,entity_management \
//	  --version 1.8.0 \
//	  --out     ocsf/cmd/ocsf-protogen/testdata \
//	  --tagmap  ocsf/cmd/ocsf-protogen/testdata/field-numbers.json
//
// Compat-check mode (for CI — verifies field numbers didn't regress vs base branch):
//
//	ocsf-protogen --compat-check \
//	  --old /tmp/old-field-numbers.json \
//	  --new ocsf/cmd/ocsf-protogen/testdata/field-numbers.json
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/redpanda-data/common-go/ocsf/cmd/ocsf-protogen/protogen"
)

// defaultSchemaPath returns the path to the committed schema fixture relative
// to this source file.  It resolves correctly whether the binary is invoked
// from any working directory via `go run`.
//
// NOTE: this path is only meaningful for `go run` and tests; compiled binaries
// embed no source path and must pass --schema explicitly.
func defaultSchemaPath() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "internal", "ocsf", "schema", "testdata", "ocsf-1.8.0.json")
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "ocsf-protogen: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("ocsf-protogen", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	schemaFlag := fs.String("schema", defaultSchemaPath(), "path to compiled OCSF schema JSON")
	classesFlag := fs.String("classes", "", "comma-separated OCSF class names (e.g. api_activity,entity_management)")
	versionFlag := fs.String("version", "1.8.0", "OCSF schema version string (e.g. 1.8.0)")
	outFlag := fs.String("out", "", "output module root directory; files are written under it at ocsf/v<N>/*.proto (required)")
	tagmapFlag := fs.String("tagmap", "", "path to field-numbers JSON (created on first run)")
	checkFlag := fs.Bool("check", false, "check committed baseline matches fresh generation (for CI)")
	compatCheckFlag := fs.Bool("compat-check", false, "check wire stability between two tagmap files (use with --old and --new)")
	oldFlag := fs.String("old", "", "path to the base-branch tagmap JSON (for --compat-check)")
	newFlag := fs.String("new", "", "path to the PR tagmap JSON (for --compat-check)")
	srSchemaOutFlag := fs.String("sr-schema-out", "", "optional directory for self-contained Schema-Registry schemas (<class>.sr.proto); empty disables SR emission")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// --compat-check is a standalone mode: compare --old and --new tagmaps.
	if *compatCheckFlag {
		if strings.TrimSpace(*oldFlag) == "" {
			return errors.New("--compat-check requires --old <path>")
		}
		if strings.TrimSpace(*newFlag) == "" {
			return errors.New("--compat-check requires --new <path>")
		}
		if err := protogen.CompatCheck(*oldFlag, *newFlag); err != nil {
			return err
		}
		fmt.Println("ok")
		return nil
	}

	// Validate required flags for generate / check modes.
	if strings.TrimSpace(*outFlag) == "" {
		return errors.New("--out is required")
	}
	if strings.TrimSpace(*tagmapFlag) == "" {
		return errors.New("--tagmap is required")
	}

	classes, err := protogen.ParseClasses(*classesFlag)
	if err != nil {
		return err
	}

	cfg := protogen.Config{
		SchemaPath:     *schemaFlag,
		Classes:        classes,
		Version:        *versionFlag,
		OutDir:         *outFlag,
		TagmapPath:     *tagmapFlag,
		Check:          *checkFlag,
		SRSchemaOutDir: *srSchemaOutFlag,
	}

	if cfg.Check {
		if err := protogen.Check(cfg); err != nil {
			return err
		}
		fmt.Println("ok")
		return nil
	}

	stubbed, err := protogen.Generate(cfg)
	if err != nil {
		return err
	}

	if len(stubbed) > 0 {
		fmt.Fprintf(os.Stderr, "WARNING: the following objects are referenced in attributes but absent "+
			"from the schema snapshot and were emitted as empty stubs:\n")
		for _, name := range stubbed {
			fmt.Fprintf(os.Stderr, "  - %s\n", name)
		}
		fmt.Fprintf(os.Stderr, "This is expected for partial schema exports. "+
			"Re-run with a full schema snapshot to resolve.\n")
	}

	fmt.Printf("wrote proto tree under %s\n", cfg.OutDir)
	fmt.Printf("saved tagmap %s\n", cfg.TagmapPath)
	return nil
}
