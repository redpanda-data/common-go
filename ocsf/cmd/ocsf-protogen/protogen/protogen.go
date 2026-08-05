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
	"path"
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
	// MergedOnly, when true, suppresses per-class proto and Schema Registry
	// files. The merged message, its shared objects.proto dependency, and any
	// configured prune sidecar are still emitted. Requires MergedMessage.
	MergedOnly bool
	// MergedSRSubject, when non-empty, annotates the merged message with the
	// (redpanda.api.common.v1.schema_registry) option carrying this subject,
	// so protoc-gen-go-sr-normalize generates the SR schema and subject
	// constants from the emitted proto. Requires MergedMessage.
	MergedSRSubject string
	// IcebergCompat, when true, prunes the loaded schema model BEFORE any
	// emission so per-class protos, the merged message, the SR schemas, and
	// validation all agree on one shape that Redpanda's proto-to-Iceberg
	// translator can represent and Oxla (Redpanda SQL) can read: fields
	// mapping to google.protobuf.Value, recursion back-edges, and fields
	// whose dotted path from a root exceeds 63 chars are dropped (see
	// gen.PruneForIceberg). A sidecar file
	// (ocsf/v<N>/iceberg-compat-prunes.txt) records every pruned field.
	IcebergCompat bool
	// SRGoPackage is the Go package name used for the generated <name>.sr.go
	// files that embed each <name>.sr.proto as Go constants next to it under
	// SRSchemaOutDir. Empty derives the name from the schema version's major
	// component ("1.8.0" → "ocsfv1"). Only used when SRSchemaOutDir is set.
	SRGoPackage string
	// MaskPath, when non-empty, is the path to a read-mask YAML file listing
	// the root-relative attribute paths to keep. Everything else is dropped
	// from the model BEFORE any emission, so the per-class protos, the merged
	// message, the SR schemas and the validation rules all describe the same
	// narrowed contract. A sidecar (ocsf/v<N>/read-mask-report.txt) records the
	// resolved keep set. Empty emits the full schema (today's behaviour).
	//
	// The mask is generator policy, not generator knowledge: which fields a
	// consumer publishes lives in the consumer's repo next to its committed
	// output, the same way --classes and --merged-message do.
	MaskPath string
}

// Generate loads the schema, emits the proto, writes --out and --tagmap.
// Returns the stubbed object names (may be nil) and any error.
func Generate(cfg Config) (stubbed []string, err error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

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

	// syncFiles, not writeFiles: a run that produces fewer artifacts than the last
	// one must clear the difference, or switching to a narrower layout leaves
	// strays that --check then rejects with no way to regenerate them away.
	if err := syncFiles(cfg.OutDir, files, gen.IsManagedOutputArtifact); err != nil {
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
		if err := syncFiles(cfg.SRSchemaOutDir, srFiles, gen.IsManagedSRArtifact); err != nil {
			return nil, err
		}
	}

	if err := tm.Save(cfg.TagmapPath); err != nil {
		return nil, fmt.Errorf("save tagmap to %q: %w", cfg.TagmapPath, err)
	}

	return stubbed, nil
}

// emitAll runs the per-class Emit plus, when cfg.MergedMessage is set, the
// merged single-event EmitMerged, and combines the file lists. With
// cfg.MergedOnly, only EmitMerged runs.
//
// Both paths emit an identical objects.proto (same object closure, same tags
// from the shared tagmap); the duplicate is dropped after an equality check so
// a divergence surfaces as an error instead of a silent overwrite.
//
// When cfg.MaskPath or cfg.IcebergCompat is set, narrowModel shrinks the schema
// model in place first — the caller-visible *schema.Schema mutates, so a later
// emitAllSRSchemas call on the same instance emits the same shape — and the
// corresponding sidecar files are appended to the returned file list.
//
// Tags are reserved over the UNMASKED, UNPRUNED model before that, so field
// numbers never depend on what the mask or prune chose to emit (see
// gen.ReserveTags).
func emitAll(s *schema.Schema, cfg Config, tm *tagmap.TagMap) (files []gen.GeneratedFile, stubbed []string, err error) {
	if err := gen.ReserveTags(s, cfg.Classes, cfg.MergedMessage, tm); err != nil {
		return nil, nil, err
	}

	sidecars, err := narrowModel(s, cfg)
	if err != nil {
		return nil, nil, err
	}

	if !cfg.MergedOnly {
		files, stubbed, err = gen.Emit(s, cfg.Classes, tm, cfg.Version)
		if err != nil {
			return nil, nil, fmt.Errorf("emit proto: %w", err)
		}
	}

	if strings.TrimSpace(cfg.MergedMessage) == "" {
		if strings.TrimSpace(cfg.MergedSRSubject) != "" {
			return nil, nil, errors.New("--merged-sr-subject requires --merged-message")
		}
		files, err = finishEmit(files, sidecars, cfg)
		return files, stubbed, err
	}

	if err := checkMergedNameCollision(cfg.MergedMessage, cfg.Classes); err != nil {
		return nil, nil, err
	}

	mergedFiles, mergedStubbed, err := gen.EmitMerged(s, cfg.Classes, tm, cfg.Version, cfg.MergedMessage, cfg.MergedSRSubject)
	if err != nil {
		return nil, nil, fmt.Errorf("emit merged proto: %w", err)
	}
	if cfg.MergedOnly {
		files, err = finishEmit(mergedFiles, sidecars, cfg)
		return files, mergedStubbed, err
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
	files, err = finishEmit(files, sidecars, cfg)
	return files, stubbed, err
}

// finishEmit appends the read-mask and iceberg-compat sidecars (whichever are
// present), sorts the file list, and runs the iceberg-compat post-condition.
func finishEmit(files []gen.GeneratedFile, sidecars []gen.GeneratedFile, cfg Config) ([]gen.GeneratedFile, error) {
	files = append(files, sidecars...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if cfg.IcebergCompat {
		if err := assertNoStructImport(files); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// narrowModel applies the read mask and then the iceberg-compat prune, mutating
// s in place, and returns their sidecar files in output order.
//
// The order matters: the prune rules react to the shape of the model (R3 demotes
// an edge because a dotted path overflows 63 chars, R4 drops edges to emptied
// types), so masking first means the deep subtrees that force those demotions
// are already gone and the surviving fields stay typed instead of collapsing to
// JSON strings.
func narrowModel(s *schema.Schema, cfg Config) ([]gen.GeneratedFile, error) {
	var sidecars []gen.GeneratedFile

	if strings.TrimSpace(cfg.MaskPath) != "" {
		f, err := applyMask(s, cfg)
		if err != nil {
			return nil, err
		}
		sidecars = append(sidecars, f)
	}

	if cfg.IcebergCompat {
		prunes, err := gen.PruneForIceberg(s, cfg.Classes)
		if err != nil {
			return nil, fmt.Errorf("iceberg-compat prune: %w", err)
		}
		f, err := gen.PruneSidecarFile(cfg.Version, prunes)
		if err != nil {
			return nil, fmt.Errorf("iceberg-compat sidecar: %w", err)
		}
		sidecars = append(sidecars, f)
		fmt.Fprintf(os.Stderr, "iceberg-compat: pruned %d fields (see %s)\n", len(prunes), f.Path)
	}

	return sidecars, nil
}

// applyMask loads cfg.MaskPath, narrows the schema model to the fields it names,
// and returns the report sidecar.
//
// When a merged message is being emitted the class discriminators must survive,
// or the merged emitter produces CEL referencing fields that no longer exist —
// checked here rather than left to fail at protovalidate compile time.
func applyMask(s *schema.Schema, cfg Config) (gen.GeneratedFile, error) {
	data, err := os.ReadFile(filepath.Clean(cfg.MaskPath))
	if err != nil {
		return gen.GeneratedFile{}, fmt.Errorf("read mask file %q: %w", cfg.MaskPath, err)
	}
	mask, err := gen.ParseMask(data)
	if err != nil {
		return gen.GeneratedFile{}, fmt.Errorf("mask file %q: %w", cfg.MaskPath, err)
	}

	res, err := gen.MaskFields(s, cfg.Classes, mask)
	if err != nil {
		return gen.GeneratedFile{}, fmt.Errorf("apply mask %q: %w", cfg.MaskPath, err)
	}
	if strings.TrimSpace(cfg.MergedMessage) != "" {
		if err := gen.VerifyMergedDiscriminators(s, cfg.Classes); err != nil {
			return gen.GeneratedFile{}, err
		}
	}

	f, err := gen.MaskReportFile(cfg.Version, res)
	if err != nil {
		return gen.GeneratedFile{}, fmt.Errorf("mask report: %w", err)
	}
	// One line, matching the iceberg-compat prune line: the detail lives in the
	// committed report, this is just the headline an operator wants after running
	// generate.
	fmt.Fprintf(os.Stderr,
		"read-mask: kept %d fields, %d widened; leaf columns %d -> %d, message types %d -> %d (see %s)\n",
		len(res.Kept), len(res.Widened), res.Stats.LeafPathsBefore, res.Stats.LeafPathsAfter,
		res.Stats.MessagesBefore, res.Stats.MessagesAfter, f.Path)
	return f, nil
}

// assertNoStructImport is a belt-and-braces post-condition for
// --iceberg-compat: after R1 pruning no generated file may reference
// google/protobuf/struct.proto — Oxla cannot load protobuf well-known types,
// so a surviving import means the pruner missed a google.protobuf.Value field.
func assertNoStructImport(files []gen.GeneratedFile) error {
	for _, f := range files {
		if strings.Contains(f.Content, "google/protobuf/struct.proto") {
			return fmt.Errorf("iceberg-compat: %q still references google/protobuf/struct.proto after pruning", f.Path)
		}
	}
	return nil
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
// cfg.MergedMessage is set, the merged EmitMergedSRSchema. Every emitted
// .sr.proto gets a companion .sr.go embedding it as Go constants
// (<MessageName>SRSchema, and <MessageName>SRSubject for the merged message
// when cfg.MergedSRSubject is set), so consumers no longer hand-embed the
// schema text.
func emitAllSRSchemas(s *schema.Schema, cfg Config, tm *tagmap.TagMap) ([]gen.GeneratedFile, error) {
	var srFiles []gen.GeneratedFile
	var err error
	if !cfg.MergedOnly {
		srFiles, err = gen.EmitSRSchemas(s, cfg.Classes, tm, cfg.Version)
		if err != nil {
			return nil, fmt.Errorf("emit sr schemas: %w", err)
		}
	}
	// Map each SR schema to the message name whose constants embed it.
	// Per-class files are named "<class>.sr.proto"; subjects only apply to
	// the merged message.
	type srConst struct {
		msgName string
		subject string
	}
	consts := make(map[string]srConst, len(cfg.Classes)+1)
	if !cfg.MergedOnly {
		for _, class := range cfg.Classes {
			consts[class+".sr.proto"] = srConst{msgName: gen.ClassMessageName(class)}
		}
	}
	if strings.TrimSpace(cfg.MergedMessage) != "" {
		mergedSR, err := gen.EmitMergedSRSchema(s, cfg.Classes, tm, cfg.Version, cfg.MergedMessage)
		if err != nil {
			return nil, fmt.Errorf("emit merged sr schema: %w", err)
		}
		srFiles = append(srFiles, mergedSR)
		consts[mergedSR.Path] = srConst{msgName: cfg.MergedMessage, subject: cfg.MergedSRSubject}
	}

	pkgName := strings.TrimSpace(cfg.SRGoPackage)
	if pkgName == "" {
		pkgName, err = gen.SRGoPackageForVersion(cfg.Version)
		if err != nil {
			return nil, fmt.Errorf("derive sr-go package: %w", err)
		}
	}
	goFiles := make([]gen.GeneratedFile, 0, len(srFiles))
	for _, f := range srFiles {
		c, ok := consts[f.Path]
		if !ok {
			return nil, fmt.Errorf("emit sr go: no message name known for SR schema %q", f.Path)
		}
		goFile, err := gen.EmitSRGo(pkgName, cfg.Version, c.msgName, c.subject, f)
		if err != nil {
			return nil, fmt.Errorf("emit sr go for %q: %w", f.Path, err)
		}
		goFiles = append(goFiles, goFile)
	}
	srFiles = append(srFiles, goFiles...)

	sort.Slice(srFiles, func(i, j int) bool { return srFiles[i].Path < srFiles[j].Path })
	if cfg.IcebergCompat {
		if err := assertNoStructImport(srFiles); err != nil {
			return nil, err
		}
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

// syncFiles writes files under root and then removes generator-managed artifacts
// in the same directories that this run no longer produces.
//
// Without the removal step, narrowing the output (--merged-only, --mask-file)
// leaves the previous layout's files on disk: --check reports them
// as strays, and its advice to "regenerate without --check" cannot clear them.
// Reconciling makes broad -> narrow the ordinary regenerate-and-commit flow.
//
// Deletion is confined to the directories the generator writes into AND to names
// the managed predicate claims, so unrelated committed files in the tree
// (buf.yaml, buf.lock, the tagmap) are never candidates.
func syncFiles(root string, files []gen.GeneratedFile, managed func(base string) bool) error {
	if err := writeFiles(root, files); err != nil {
		return err
	}

	want := make(map[string]struct{}, len(files))
	dirs := make(map[string]struct{})
	for _, f := range files {
		want[f.Path] = struct{}{}
		dirs[path.Dir(f.Path)] = struct{}{}
	}

	var removed []string
	for dir := range dirs {
		abs := filepath.Join(root, filepath.FromSlash(dir))
		entries, err := os.ReadDir(abs)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read output dir %q: %w", abs, err)
		}
		for _, e := range entries {
			if e.IsDir() || !managed(e.Name()) {
				continue
			}
			rel := e.Name()
			if dir != "." {
				rel = dir + "/" + e.Name()
			}
			if _, produced := want[rel]; produced {
				continue
			}
			if err := os.Remove(filepath.Join(abs, e.Name())); err != nil {
				return fmt.Errorf("remove stale generated file %q: %w", rel, err)
			}
			removed = append(removed, rel)
		}
	}

	// Report deletions in one line — they are the surprising part of a generate
	// run, so they are named rather than merely counted, but a narrowing
	// transition can drop a dozen files and should not bury the rest of the output.
	if len(removed) > 0 {
		sort.Strings(removed)
		fmt.Fprintf(os.Stderr, "removed %d stale generated file(s): %s\n",
			len(removed), strings.Join(removed, ", "))
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
	if err := validateConfig(cfg); err != nil {
		return err
	}

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

func validateConfig(cfg Config) error {
	if cfg.MergedOnly && strings.TrimSpace(cfg.MergedMessage) == "" {
		return errors.New("--merged-only requires --merged-message")
	}
	return nil
}

// diffSRSchemas compares freshly generated SR schema files against their
// committed counterparts under srOutDir and returns a descriptive error on any
// missing, changed, or stray file. Stray detection mirrors diffTree: a
// committed *.sr.proto or *.sr.go the generator no longer produces (e.g. after
// a class is dropped from --classes) must fail the check rather than rot
// silently.
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
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".sr.proto") && !strings.HasSuffix(e.Name(), ".sr.go")) {
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

	// Also enumerate every committed generator-managed artifact under each
	// versioned dir the generator writes into, so we catch files that should have
	// been removed. This covers the sidecars as well as the protos: dropping
	// --mask-file or --iceberg-compat must not leave a stale report behind.
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
			if e.IsDir() || !gen.IsManagedOutputArtifact(e.Name()) {
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
