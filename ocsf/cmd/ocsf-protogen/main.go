// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Command ocsf-protogen generates proto3 definitions from a compiled OCSF
// schema JSON export: per-class messages, an optional merged single-event
// message (--merged-message), and self-contained Schema-Registry schemas
// (--sr-schema-out). The emitted protos carry buf.validate annotations; Go
// bindings are generated from them with buf + protoc-gen-go (see
// internal/ocsf/conformance/genpb/buf.gen*.yaml for the reference recipe).
//
// Normal mode (no --check):
//
//	ocsf-protogen \
//	  --schema  ocsf/internal/ocsf/schema/testdata/ocsf-1.8.0.json \
//	  --classes api_activity,entity_management \
//	  --version 1.8.0 \
//	  --out     ocsf/cmd/ocsf-protogen/testdata \
//	  --tagmap  ocsf/cmd/ocsf-protogen/testdata/field-numbers.json \
//	  --merged-message AuditEvent \
//	  --merged-only \
//	  --merged-sr-subject redpanda.ocsf.audit-events-value
//
// --out is a MODULE ROOT DIRECTORY. Files are written under it at their
// module-relative paths, e.g. <out>/ocsf/v1/api_activity.proto,
// <out>/ocsf/v1/entity_management.proto, <out>/ocsf/v1/audit_event.proto,
// <out>/ocsf/v1/objects.proto. --merged-only suppresses the per-class files.
//
// --iceberg-compat prunes the schema model before emission so the generated
// protos can be translated to Iceberg by Redpanda and read by Oxla: fields
// mapping to google.protobuf.Value are dropped, recursion back-edges are cut,
// and fields whose dotted path from a root exceeds 63 chars are removed. The
// pruned fields are recorded in <out>/ocsf/v<N>/iceberg-compat-prunes.txt.
//
// When --sr-schema-out is set, each emitted <name>.sr.proto gets a companion
// <name>.sr.go embedding the schema text (and, for the merged message, the
// subject) as Go constants; --sr-go-only suppresses the intermediate
// .sr.proto files, and --sr-go-package overrides the Go package name.
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
	mergedMessageFlag := fs.String("merged-message", "", "optional message name (e.g. AuditEvent) for a single flat message unioning all selected classes, emitted alongside the per-class files; empty disables merged emission")
	mergedOnlyFlag := fs.Bool("merged-only", false, "emit only the merged message and shared objects, suppressing per-class proto and Schema Registry files (requires --merged-message)")
	mergedSRSubjectFlag := fs.String("merged-sr-subject", "", "optional Schema Registry subject; annotates the merged message with (redpanda.api.common.v1.schema_registry) for protoc-gen-go-sr-normalize (requires --merged-message)")
	icebergCompatFlag := fs.Bool("iceberg-compat", false, "prune fields Redpanda Iceberg/Oxla cannot represent (google.protobuf.Value fields, recursion back-edges, dotted field paths over 63 chars) before emission; writes ocsf/v<N>/iceberg-compat-prunes.txt")
	srGoPackageFlag := fs.String("sr-go-package", "", "Go package name for the generated <name>.sr.go companions under --sr-schema-out (default: derived from the OCSF major version, e.g. ocsfv1)")
	srGoOnlyFlag := fs.Bool("sr-go-only", false, "emit only the .sr.go schema embeds under --sr-schema-out, suppressing the intermediate .sr.proto files")

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
		SchemaPath:      *schemaFlag,
		Classes:         classes,
		Version:         *versionFlag,
		OutDir:          *outFlag,
		TagmapPath:      *tagmapFlag,
		Check:           *checkFlag,
		SRSchemaOutDir:  *srSchemaOutFlag,
		MergedMessage:   *mergedMessageFlag,
		MergedOnly:      *mergedOnlyFlag,
		MergedSRSubject: *mergedSRSubjectFlag,
		IcebergCompat:   *icebergCompatFlag,
		SRGoPackage:     *srGoPackageFlag,
		SRGoOnly:        *srGoOnlyFlag,
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
