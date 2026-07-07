// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package gen provides code-generation helpers that consume the loaded OCSF
// schema model and produce proto3 type descriptors for use by the proto emitter.
package gen

import (
	"fmt"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/schema"
)

// ProtoType describes the proto3 type of a single generated field.
//
// Exactly one of Scalar, Message, or WellKnown is non-empty:
//
//   - Scalar holds a proto3 primitive keyword: "string", "int32", "int64",
//     "double", or "bool".
//   - Message holds the OCSF object name (a key in Schema.Objects) when the
//     attribute references a complex type.  The emitter turns this into a
//     message reference.
//   - WellKnown holds a fully-qualified well-known type name, currently only
//     "google.protobuf.Value" for json_t and the generic OCSF "object" type.
//
// Repeated mirrors Attribute.IsArray and signals that the field must be
// declared as a repeated proto3 field.
type ProtoType struct {
	Scalar    string
	Message   string
	WellKnown string
	Repeated  bool
}

// baseMapping maps OCSF base primitive type names to proto3 scalar keywords.
// Only the six true base types are listed here; derived types are resolved
// transitively via the Types chain before this table is consulted.
var baseMapping = map[string]string{
	"string_t":  "string",
	"integer_t": "int32",
	"long_t":    "int64",
	"float_t":   "double",
	"boolean_t": "bool",
}

// These sentinels drive the dispatch branches in MapType and resolveProtoType.
// Each name matches an exact OCSF schema string or the proto well-known type it maps to.
const (
	// objectTypeName is the OCSF attribute type string for complex object references.
	objectTypeName = "object_t"

	// genericObject is the OCSF object name for the unstructured bag type.
	// It has no schema-defined attributes; we map it to google.protobuf.Value.
	genericObject = "object"

	// wellKnownType is the OCSF type name for embedded JSON values.
	wellKnownType = "json_t"

	// wellKnownValueType is the fully-qualified proto well-known type used for
	// json_t and the generic "object" type.
	wellKnownValueType = "google.protobuf.Value"
)

// MapType maps an OCSF Attribute to a ProtoType by resolving the attribute's
// OCSF type against the schema's types map (for scalar/derived types) or
// objects map (for complex types).
//
// Type resolution follows this order:
//  1. If attr.Type is "object_t", attr.ObjectType names a complex object.
//     The special object name "object" (the OCSF generic bag type) maps to
//     google.protobuf.Value instead of a message reference.
//  2. If attr.Type is "json_t", the result is WellKnown google.protobuf.Value.
//  3. Otherwise attr.Type must be a key in s.Types.  The type chain is walked
//     (via TypeDef.Type) until a base type with no parent is reached, then the
//     base-mapping table is consulted.  Cycles are detected and cause an error.
//
// attr.IsArray maps directly to Repeated.
// An unresolvable type (absent from both types and objects, or a cycle in the
// type chain) returns a non-nil error.
func MapType(s *schema.Schema, attr schema.Attribute) (ProtoType, error) {
	var pt ProtoType
	pt.Repeated = attr.IsArray

	typeName := attr.Type

	// object_t — the attribute holds a complex OCSF object.
	if typeName == objectTypeName {
		objectName := attr.ObjectType
		if objectName == genericObject {
			// The generic OCSF "object" is an unstructured bag; map to Value.
			pt.WellKnown = wellKnownValueType
			return pt, nil
		}
		// Verify the object actually exists in the schema.
		if _, ok := s.Objects[objectName]; !ok {
			return ProtoType{}, fmt.Errorf("ocsf gen: attribute references unknown object %q", objectName)
		}
		pt.Message = objectName
		return pt, nil
	}

	// json_t — embedded JSON value.
	if typeName == wellKnownType {
		pt.WellKnown = wellKnownValueType
		return pt, nil
	}

	// Primitive or derived type — walk the base chain.
	scalar, err := resolveScalar(s, typeName)
	if err != nil {
		return ProtoType{}, err
	}
	pt.Scalar = scalar
	return pt, nil
}

// resolveScalar walks the TypeDef chain from typeName up to a base type and
// returns the corresponding proto3 scalar keyword.
//
// Cycles are detected by limiting traversal to len(s.Types)+1 steps.
func resolveScalar(s *schema.Schema, typeName string) (string, error) {
	// Maximum chain depth: one more than the number of registered types,
	// which is a safe bound for detecting cycles.
	const maxDepth = 64

	current := typeName
	for i := 0; i < maxDepth; i++ {
		// Check whether this is a base type.
		if scalar, ok := baseMapping[current]; ok {
			return scalar, nil
		}

		// Not a base type — look it up in the types map and follow the chain.
		td, ok := s.Types[current]
		if !ok {
			return "", fmt.Errorf("ocsf gen: unknown type %q (not in schema types map)", typeName)
		}

		if td.Type == "" {
			// No parent but also not a known base — the type exists in the
			// schema but we have no mapping for it (e.g. json_t was handled
			// above; an exotic future base type).
			return "", fmt.Errorf("ocsf gen: type %q has no base mapping and no parent type", typeName)
		}

		current = td.Type
	}

	return "", fmt.Errorf("ocsf gen: type chain from %q exceeds max depth (possible cycle)", typeName)
}
