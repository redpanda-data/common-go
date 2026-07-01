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

// toPascalCase converts an OCSF snake_case name to PascalCase.
// "api_activity" → "ApiActivity", "user" → "User".
//
// Namespaced names (e.g. "win/win_service") have the namespace prefix stripped
// before conversion: "win/win_service" → "win_service" → "WinService".
// Slashes are not valid in proto message or enum names.
func toPascalCase(s string) string {
	// Strip namespace prefix (everything up to and including the last '/').
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	parts := strings.Split(s, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}

// SelectClosure returns the named classes (sorted by name) plus every object
// transitively referenced through attributes whose MapType yields a Message.
//
// Deterministic: classes and objects are each sorted by name.
// Returns an error if any named class does not exist in the schema.
func SelectClosure(s *schema.Schema, classNames []string) ([]schema.Class, []schema.Object, error) {
	// Validate and collect requested classes.
	classes := make([]schema.Class, 0, len(classNames))
	for _, name := range classNames {
		cls, ok := s.Classes[name]
		if !ok {
			return nil, nil, fmt.Errorf("ocsf emit: class %q not found in schema", name)
		}
		classes = append(classes, *cls)
	}
	sort.Slice(classes, func(i, j int) bool {
		return classes[i].Name < classes[j].Name
	})

	// Collect the transitive object closure using BFS.
	visited := make(map[string]bool)
	queue := []string{}

	// Seed with objects directly referenced by the selected classes.
	for _, cls := range classes {
		visitObjectAttrs(cls.Attributes, visited, &queue)
	}

	// BFS over the object graph.
	for len(queue) > 0 {
		objName := queue[0]
		queue = queue[1:]

		obj, ok := s.Objects[objName]
		if !ok {
			// Object referenced but not found; skip silently (schema integrity
			// is the loader's responsibility).
			continue
		}
		visitObjectAttrs(obj.Attributes, visited, &queue)
	}

	// Sort object names for determinism.
	objNames := make([]string, 0, len(visited))
	for name := range visited {
		objNames = append(objNames, name)
	}
	sort.Strings(objNames)

	objects := make([]schema.Object, 0, len(objNames))
	for _, name := range objNames {
		obj, ok := s.Objects[name]
		if !ok {
			continue
		}
		objects = append(objects, *obj)
	}

	return classes, objects, nil
}

// versionToPackage converts a semver string to a proto package suffix using
// only the major version component.  "1.8.0" → "v1", "2.0.0" → "v2".
//
// Pre-release and build metadata are stripped before parsing (everything from
// the first '-' or '+').  This matches protobuf's versioning convention: the
// package is stable across minor/patch bumps, which only add fields.
func versionToPackage(version string) (string, error) {
	// Strip pre-release and build metadata suffixes.
	clean := version
	if i := strings.IndexAny(clean, "-+"); i >= 0 {
		clean = clean[:i]
	}

	// Extract the major component (substring before the first '.').
	major := clean
	if i := strings.Index(clean, "."); i >= 0 {
		major = clean[:i]
	}

	if major == "" {
		return "", fmt.Errorf("ocsf emit: cannot parse major version from %q", version)
	}
	// Validate that major is numeric.
	for _, c := range major {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("ocsf emit: major version component %q is not numeric (version %q)", major, version)
		}
	}

	return "v" + major, nil
}

// visitObjectAttrs adds unreachable object names from attrs to queue.
// Used by SelectClosure's BFS to collect the transitive object closure.
func visitObjectAttrs(attrs map[string]*schema.Attribute, visited map[string]bool, queue *[]string) {
	for _, attr := range attrs {
		if attr.Type == objectTypeName && attr.ObjectType != "" && attr.ObjectType != genericObject {
			if !visited[attr.ObjectType] {
				visited[attr.ObjectType] = true
				*queue = append(*queue, attr.ObjectType)
			}
		}
	}
}

// detectImports determines which proto imports are needed by scanning all
// attributes across classes and objects.
//
// Attributes that resolveProtoType cannot resolve are skipped for import
// detection: an unresolvable type simply does not force any import, and the
// error will be surfaced when emitMessage processes the same attribute.
func detectImports(s *schema.Schema, classes []schema.Class, objects []schema.Object) (needStructProto, needValidateProto bool) {
	checkAttrs := func(attrs map[string]*schema.Attribute, constraints *schema.Constraints) {
		for _, attr := range attrs {
			pt, err := resolveProtoType(s, *attr)
			if err != nil {
				// Skip: unresolvable attrs don't drive imports.
				continue
			}
			if pt.WellKnown == wellKnownValueType {
				needStructProto = true
			}
			if attr.Requirement == "required" {
				needValidateProto = true
			}
		}
		if constraints != nil && (len(constraints.AtLeastOne) > 0 || len(constraints.JustOne) > 0) {
			needValidateProto = true
		}
	}
	for i := range classes {
		checkAttrs(classes[i].Attributes, classes[i].Constraints)
	}
	for i := range objects {
		checkAttrs(objects[i].Attributes, objects[i].Constraints)
	}
	return needStructProto, needValidateProto
}

// Emit produces a deterministic proto3 document from the named classes and
// their transitive object closure.
//
// version is embedded in the proto package name using only its major component:
// "1.8.0" → package ocsf.v1, "2.0.0" → package ocsf.v2.
//
// The caller owns tm and is responsible for calling tm.Save after Emit returns.
// Emit mutates tm by calling Assign for every field it emits.
//
// stubbed is the sorted, de-duplicated list of object names that were emitted
// as empty stub messages because they were referenced by attributes but absent
// from the schema snapshot.  It is nil when no stubs were needed.  Callers can
// use this to distinguish an expected partial-fixture gap from a loader bug.
func Emit(s *schema.Schema, classNames []string, tm *tagmap.TagMap, version string) (proto string, stubbed []string, err error) {
	classes, objects, err := SelectClosure(s, classNames)
	if err != nil {
		return "", nil, err
	}

	// First pass: figure out which imports we need.
	needStructProto, needValidateProto := detectImports(s, classes, objects)

	var sb strings.Builder

	// License / header comment.
	sb.WriteString(`// Code generated by ocsf-protogen. DO NOT EDIT.
// Source: OCSF schema ` + version + `

`)

	pkgSuffix, err := versionToPackage(version)
	if err != nil {
		return "", nil, err
	}

	sb.WriteString(`syntax = "proto3";` + "\n\n")
	sb.WriteString(`package ocsf.` + pkgSuffix + ";\n\n")

	// Imports (sorted).
	var imports []string
	if needStructProto {
		imports = append(imports, `import "google/protobuf/struct.proto";`)
	}
	if needValidateProto {
		imports = append(imports, `import "buf/validate/validate.proto";`)
	}
	sort.Strings(imports)
	for _, imp := range imports {
		sb.WriteString(imp + "\n")
	}
	if len(imports) > 0 {
		sb.WriteString("\n")
	}

	// Emit classes then objects, each sorted by name.
	for i := range classes {
		msg, emitErr := emitMessage(s, tm, toPascalCase(classes[i].Name), classes[i].Attributes, classes[i].Constraints)
		if emitErr != nil {
			return "", nil, fmt.Errorf("ocsf emit: class %q: %w", classes[i].Name, emitErr)
		}
		sb.WriteString(msg)
		sb.WriteString("\n")
	}
	for i := range objects {
		msg, emitErr := emitMessage(s, tm, toPascalCase(objects[i].Name), objects[i].Attributes, objects[i].Constraints)
		if emitErr != nil {
			return "", nil, fmt.Errorf("ocsf emit: object %q: %w", objects[i].Name, emitErr)
		}
		sb.WriteString(msg)
		sb.WriteString("\n")
	}

	// Emit stub messages for object types that are referenced but absent from
	// the schema snapshot (e.g. profile-injected objects from a partial export).
	stubs := collectStubs(s, classes, objects)
	for _, stubName := range stubs {
		sb.WriteString("// Stub: referenced object not present in this schema snapshot.\n")
		sb.WriteString("message " + stubName + " {}\n\n")
	}

	var stubbedNames []string
	if len(stubs) > 0 {
		stubbedNames = stubs
	}
	return sb.String(), stubbedNames, nil
}

// resolveProtoType maps an attribute to its ProtoType, returning an error for
// unresolvable types.
//
// When MapType fails because an object is referenced but absent from the schema
// (profile-injected attributes in a partial snapshot), the attribute is treated
// as a forward-reference Message using the ObjectType name (nil error). This
// allows Emit to proceed and emit a syntactically valid proto file; stub
// messages are emitted for any such forward reference.
//
// For any other MapType failure (unknown scalar, type cycle), the error is
// returned so the caller can surface it rather than silently emitting an
// incorrect type.
func resolveProtoType(s *schema.Schema, attr schema.Attribute) (ProtoType, error) {
	pt, err := MapType(s, attr)
	if err != nil {
		// object_t with an absent object: emit a stub Message reference.
		if attr.Type == objectTypeName && attr.ObjectType != "" && attr.ObjectType != genericObject {
			return ProtoType{
				Message:  attr.ObjectType,
				Repeated: attr.IsArray,
			}, nil
		}
		return ProtoType{}, err
	}
	return pt, nil
}

// collectStubs returns the set of object names that are referenced (as Message
// types) through attributes but are absent from the schema objects map.  These
// need stub message declarations so the emitted proto compiles.
func collectStubs(s *schema.Schema, classes []schema.Class, objects []schema.Object) []string {
	// Build the set of object names that will be emitted as messages.
	emitted := make(map[string]bool, len(objects))
	for _, o := range objects {
		emitted[toPascalCase(o.Name)] = true
	}

	stubs := make(map[string]bool)
	check := func(attrs map[string]*schema.Attribute) {
		for _, attr := range attrs {
			if attr.Type == objectTypeName && attr.ObjectType != "" && attr.ObjectType != genericObject {
				if _, ok := s.Objects[attr.ObjectType]; !ok {
					msgName := toPascalCase(attr.ObjectType)
					if !emitted[msgName] {
						stubs[msgName] = true
					}
				}
			}
		}
	}
	for i := range classes {
		check(classes[i].Attributes)
	}
	for i := range objects {
		check(objects[i].Attributes)
	}

	result := make([]string, 0, len(stubs))
	for name := range stubs {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// fieldSpec holds the resolved proto field information for a single attribute.
type fieldSpec struct {
	tag int32
	// typeName is the exact string emitted in the .proto file: a proto3 scalar
	// keyword ("string", "int64", etc.), a PascalCase message name, or a
	// fully-qualified well-known type name ("google.protobuf.Value").
	typeName string
	name     string // proto field name (= OCSF attr name verbatim)
	repeated bool
	required bool
	// enumName is non-empty when this field has a nested enum; it holds the
	// PascalCase enum name (e.g. "ActivityId").
	enumName string
}

// resolveFieldSpec builds a fieldSpec (and optional nested enum) for one attribute.
func resolveFieldSpec(s *schema.Schema, tm *tagmap.TagMap, msgName, attrName string, attr *schema.Attribute) (fieldSpec, *ProtoEnum, error) {
	pt, err := resolveProtoType(s, *attr)
	if err != nil {
		return fieldSpec{}, nil, fmt.Errorf("attribute %q: %w", attrName, err)
	}

	tag, err := tm.Assign(msgName, attrName)
	if err != nil {
		return fieldSpec{}, nil, fmt.Errorf("attribute %q: assign tag: %w", attrName, err)
	}

	fs := fieldSpec{
		tag:      tag,
		name:     attrName,
		repeated: pt.Repeated,
		required: attr.Requirement == "required",
	}

	switch {
	case pt.WellKnown != "":
		fs.typeName = pt.WellKnown
	case pt.Message != "":
		fs.typeName = toPascalCase(pt.Message)
	default:
		// Scalar — check if this attribute has an int-keyed enum.
		if len(attr.Enum) > 0 {
			enumName := toPascalCase(attrName)
			pe, isProtoEnum, enumErr := MapEnum(enumName, attr.Enum)
			if enumErr != nil {
				return fieldSpec{}, nil, fmt.Errorf("attribute %q: map enum: %w", attrName, enumErr)
			}
			if isProtoEnum {
				fs.typeName = enumName
				fs.enumName = enumName
				return fs, &pe, nil
			}
			// String-keyed enum: emit as string.
			fs.typeName = "string"
		} else {
			fs.typeName = pt.Scalar
		}
	}

	return fs, nil, nil
}

// emitMessage generates one proto message block for the given message name,
// attribute map, and optional constraints.
func emitMessage(s *schema.Schema, tm *tagmap.TagMap, msgName string, attrs map[string]*schema.Attribute, constraints *schema.Constraints) (string, error) {
	// Sort attribute names for determinism during tag assignment.
	attrNames := make([]string, 0, len(attrs))
	for name := range attrs {
		attrNames = append(attrNames, name)
	}
	sort.Strings(attrNames)

	// Resolve each attribute and assign tags.
	fields := make([]fieldSpec, 0, len(attrNames))
	// Collect nested enums keyed by enum name (in case duplicates arise).
	nestedEnums := make(map[string]ProtoEnum)

	for _, attrName := range attrNames {
		fs, pe, err := resolveFieldSpec(s, tm, msgName, attrName, attrs[attrName])
		if err != nil {
			return "", err
		}
		if pe != nil {
			nestedEnums[fs.enumName] = *pe
		}
		fields = append(fields, fs)
	}

	// Sort nested enum names for deterministic output.
	enumNames := make([]string, 0, len(nestedEnums))
	for name := range nestedEnums {
		enumNames = append(enumNames, name)
	}
	sort.Strings(enumNames)

	// Sort fields by ascending tag for output.
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].tag < fields[j].tag
	})

	var sb strings.Builder
	sb.WriteString("message " + msgName + " {\n")

	// Emit constraint CEL options (before nested enums and fields).
	if constraints != nil {
		if len(constraints.AtLeastOne) > 0 {
			cel := atLeastOneCEL(msgName, constraints.AtLeastOne)
			sb.WriteString(cel)
		}
		if len(constraints.JustOne) > 0 {
			cel := justOneCEL(msgName, constraints.JustOne)
			sb.WriteString(cel)
		}
	}

	// Emit nested enums.
	for _, enumName := range enumNames {
		pe := nestedEnums[enumName]
		sb.WriteString(emitEnum(pe))
	}

	// Emit fields.
	for _, fs := range fields {
		line := "  "
		if fs.repeated {
			line += "repeated "
		}
		line += fs.typeName + " " + fs.name + " = " + fmt.Sprintf("%d", fs.tag)
		// protovalidate interprets `required` on a repeated field as "non-empty
		// (len >= 1)", but OCSF "required" on an array means only "key present".
		// Fields like osint can be legitimately empty, so we skip the annotation
		// for repeated fields to avoid wrongly rejecting valid events.
		if fs.required && !fs.repeated {
			line += ` [(buf.validate.field).required = true]`
		}
		line += ";\n"
		sb.WriteString(line)
	}

	sb.WriteString("}\n")
	return sb.String(), nil
}

// emitEnum formats a proto3 nested enum declaration.
func emitEnum(pe ProtoEnum) string {
	var sb strings.Builder
	sb.WriteString("  enum " + pe.Name + " {\n")
	for _, v := range pe.Values {
		sb.WriteString("    " + v.Ident + " = " + fmt.Sprintf("%d", v.Number) + ";\n")
	}
	sb.WriteString("  }\n")
	return sb.String()
}

// atLeastOneCEL generates a buf.validate CEL option asserting at least one
// of the listed fields is set.
//
// Generated expression: has(this.a) || has(this.b) || has(this.c)
func atLeastOneCEL(msgName string, fields []string) string {
	sorted := make([]string, len(fields))
	copy(sorted, fields)
	sort.Strings(sorted)

	parts := make([]string, len(sorted))
	for i, f := range sorted {
		parts[i] = "has(this." + f + ")"
	}
	expr := strings.Join(parts, " || ")
	fieldList := "[" + strings.Join(sorted, ", ") + "]"
	return fmt.Sprintf(
		"  option (buf.validate.message).cel = {\n"+
			"    id: %q,\n"+
			"    message: %q,\n"+
			"    expression: %q\n"+
			"  };\n",
		msgName+".at_least_one",
		"at least one of "+fieldList+" must be set",
		expr,
	)
}

// justOneCEL generates a buf.validate CEL option asserting exactly one of the
// listed fields is set.
//
// Generated expression:
//
//	(has(this.a) ? 1 : 0) + (has(this.b) ? 1 : 0) + ... == 1
func justOneCEL(msgName string, fields []string) string {
	sorted := make([]string, len(fields))
	copy(sorted, fields)
	sort.Strings(sorted)

	parts := make([]string, len(sorted))
	for i, f := range sorted {
		parts[i] = "(has(this." + f + ") ? 1 : 0)"
	}
	expr := strings.Join(parts, " + ") + " == 1"
	fieldList := "[" + strings.Join(sorted, ", ") + "]"
	return fmt.Sprintf(
		"  option (buf.validate.message).cel = {\n"+
			"    id: %q,\n"+
			"    message: %q,\n"+
			"    expression: %q\n"+
			"  };\n",
		msgName+".just_one",
		"exactly one of "+fieldList+" must be set",
		expr,
	)
}
