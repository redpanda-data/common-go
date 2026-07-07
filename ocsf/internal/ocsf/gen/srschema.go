// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package gen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/schema"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/tagmap"
)

// EmitSRSchemas produces one self-contained Schema-Registry (SR) schema file per
// named class.
//
// The generic ocsf.v<N> protos emitted by Emit carry no SR annotation, so there
// is no schema text a consumer can register directly. EmitSRSchemas fills that
// gap: for each class it inlines the class message plus its full transitive
// object closure into a single proto3 file that a consumer can hand to a Schema
// Registry as-is.
//
// For each class it emits one GeneratedFile:
//   - Path: "<class_name>.sr.proto" (flat, no directory prefix; the caller
//     decides where to write it).
//   - Content: a single self-contained proto3 file whose messages appear in this
//     order:
//     1. the CLASS message (making it Confluent message-index 0, which decoders
//     rely on),
//     2. every object in the class's transitive closure, sorted by PascalCase
//     message name,
//     3. stub messages for objects referenced but absent from the schema
//     snapshot.
//
// The file NEVER imports buf/validate/validate.proto and NEVER imports the
// shared objects file: objects are inlined here. google/protobuf/struct.proto is
// imported only when some emitted attribute resolves to google.protobuf.Value.
//
// No buf.validate output is produced: neither the per-field
// (buf.validate.field).required annotation nor the message-level CEL constraint
// blocks. Everything else (field names, tag numbers, repeated, nested enums,
// type names) is IDENTICAL to Emit's output.
//
// Field numbers reuse the wire tagmap: pass the SAME tm used for Emit. Assign is
// idempotent, so an already-assigned (message, attribute) returns its existing
// tag without mutation, and the SR schema's tags match the main output exactly.
func EmitSRSchemas(s *schema.Schema, classNames []string, tm *tagmap.TagMap, version string) ([]GeneratedFile, error) {
	pkgSuffix, err := versionToPackage(version)
	if err != nil {
		return nil, err
	}

	files := make([]GeneratedFile, 0, len(classNames))
	for _, className := range classNames {
		f, emitErr := emitSRSchemaFile(s, tm, version, pkgSuffix, className)
		if emitErr != nil {
			return nil, emitErr
		}
		files = append(files, f)
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// emitSRSchemaFile builds the self-contained SR schema file for a single class.
func emitSRSchemaFile(s *schema.Schema, tm *tagmap.TagMap, version, pkgSuffix, className string) (GeneratedFile, error) {
	classes, objects, err := SelectClosure(s, []string{className})
	if err != nil {
		return GeneratedFile{}, err
	}
	// SelectClosure validates existence, so exactly one class is returned.
	cls := classes[0]

	opts := emitOptions{omitValidate: true}

	var body strings.Builder

	// (1) Class message FIRST → Confluent message-index 0.
	classMsg, err := emitMessage(s, tm, toPascalCase(cls.Name), cls.Attributes, cls.Constraints, opts)
	if err != nil {
		return GeneratedFile{}, fmt.Errorf("ocsf sr emit: class %q: %w", cls.Name, err)
	}
	body.WriteString(classMsg)
	body.WriteString("\n")

	// (2) Objects in the transitive closure, sorted by PascalCase message name.
	sorted := make([]schema.Object, len(objects))
	copy(sorted, objects)
	sort.Slice(sorted, func(i, j int) bool {
		return toPascalCase(sorted[i].Name) < toPascalCase(sorted[j].Name)
	})
	for i := range sorted {
		msg, emitErr := emitMessage(s, tm, toPascalCase(sorted[i].Name), sorted[i].Attributes, sorted[i].Constraints, opts)
		if emitErr != nil {
			return GeneratedFile{}, fmt.Errorf("ocsf sr emit: object %q: %w", sorted[i].Name, emitErr)
		}
		body.WriteString(msg)
		body.WriteString("\n")
	}

	// (3) Stub messages for referenced-but-absent objects.
	stubs := collectStubs(s, classes, objects)
	for _, stubName := range stubs {
		body.WriteString("// Stub: referenced object not present in this schema snapshot.\n")
		body.WriteString("message " + stubName + " {}\n\n")
	}

	// struct.proto is the only import the SR file may need: never buf/validate,
	// never the objects file. Detect Value usage across the class + closure.
	needStruct, _ := detectImports(s, classes, objects)

	var imports []string
	if needStruct {
		imports = append(imports, `import "google/protobuf/struct.proto";`)
	}

	content := fileHeader(version, pkgSuffix) + importBlock(imports) + body.String()
	return GeneratedFile{Path: cls.Name + ".sr.proto", Content: content}, nil
}
