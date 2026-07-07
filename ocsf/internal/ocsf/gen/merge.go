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
	"errors"
	"fmt"
	"sort"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/schema"
)

// requirementRequired is the OCSF requirement level that maps to a
// protovalidate required annotation.
const requirementRequired = "required"

// forcedScalarAttrs lists attributes that are ALWAYS emitted as plain scalars
// in a merged message, never as a nested enum, regardless of whether the
// selected classes happen to merge cleanly today.
//
// activity_id is class-scoped by OCSF design: the same numeric value means
// different things in different classes (1 = Create in api_activity but
// Logon in authentication). Emitting a merged enum for it would be wrong the
// moment a conflicting class is selected, and switching an already-emitted
// enum field to int32 later is wire-compatible but breaks generated-code
// consumers. So it is int32 from day one; the per-class semantics live in
// the (class_uid, activity_id) pair and the TypeUid enum.
var forcedScalarAttrs = map[string]bool{
	"activity_id": true,
}

// Merged is the result of unioning the attributes of several OCSF classes
// into a single flat proto message definition.
type Merged struct {
	// Name is the proto message name for the merged event (e.g. "AuditEvent").
	Name string

	// Classes holds the selected classes, sorted by name.
	Classes []schema.Class

	// Attributes is the merged attribute map. Enum-valued attributes carry the
	// union of enum members across all owning classes; demoted attributes have
	// their Enum cleared so the emitter falls back to the plain scalar type.
	Attributes map[string]*schema.Attribute

	// Owners maps each attribute name to the sorted names of the classes that
	// declare it. Attributes owned by every selected class (the base_event
	// set) have len(Owners[a]) == len(Classes).
	Owners map[string][]string

	// Demoted lists (sorted) the attribute names that were demoted from a
	// nested enum to a plain scalar, either because they are in
	// forcedScalarAttrs or because their enum value sets conflict across the
	// selected classes.
	Demoted []string
}

// MergeClasses unions the attribute maps of the named classes into a single
// flat message definition suitable for a one-topic event stream.
//
// Merge rules per attribute name:
//
//   - Type, ObjectType, and IsArray must agree across every class that
//     declares the attribute; any mismatch is a hard error (it would change
//     the proto field type depending on class selection).
//
//   - Integer-keyed enums merge by value union. If the same numeric value
//     maps to different captions in two classes (a class-scoped enum such as
//     activity_id), the attribute is demoted to its plain scalar type instead
//     of failing: OCSF defines such semantics via the (class_uid, value)
//     pair, which the merged message preserves. If one class declares an enum
//     and another declares none for the same attribute, the attribute is
//     demoted too.
//
//   - Attributes in forcedScalarAttrs are demoted unconditionally so their
//     emitted type never changes when classes are added.
//
//   - Requirement is "required" only when EVERY selected class declares the
//     attribute as required. Anything weaker becomes "recommended": a blanket
//     proto-level required annotation would wrongly reject events of classes
//     that do not use the attribute. Per-class requiredness is enforced with
//     class_uid-gated CEL by the emitter.
//
// The returned Merged is deterministic for a given schema and class set.
func MergeClasses(s *schema.Schema, classNames []string, msgName string) (*Merged, error) {
	if msgName == "" {
		return nil, errors.New("ocsf merge: merged message name must not be empty")
	}

	classes := make([]schema.Class, 0, len(classNames))
	for _, name := range classNames {
		cls, ok := s.Classes[name]
		if !ok {
			return nil, fmt.Errorf("ocsf merge: class %q not found in schema", name)
		}
		classes = append(classes, *cls)
	}
	if len(classes) == 0 {
		return nil, errors.New("ocsf merge: at least one class is required")
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i].Name < classes[j].Name })

	merged := make(map[string]*schema.Attribute)
	owners := make(map[string][]string)
	demoted := make(map[string]bool)

	for _, cls := range classes {
		attrNames := sortedKeys(cls.Attributes)
		for _, attrName := range attrNames {
			attr := cls.Attributes[attrName]
			owners[attrName] = append(owners[attrName], cls.Name)

			existing, seen := merged[attrName]
			if !seen {
				cp := *attr
				merged[attrName] = &cp
				continue
			}

			if err := checkAttrShape(attrName, existing, attr, cls.Name); err != nil {
				return nil, err
			}

			union, conflict := unionEnumMembers(existing.Enum, attr.Enum)
			if conflict {
				demoted[attrName] = true
			}
			existing.Enum = union
		}
	}

	// Apply forced and conflict demotions: clearing Enum makes the emitter
	// fall back to the attribute's plain scalar type (int32 for integer_t).
	for name, attr := range merged {
		if forcedScalarAttrs[name] {
			demoted[name] = true
		}
		if demoted[name] {
			attr.Enum = nil
		}
	}

	// Merge requirement: required only when required in every selected class.
	for name, attr := range merged {
		attr.Requirement = mergedRequirement(name, classes)
	}

	for name := range owners {
		sort.Strings(owners[name])
	}

	demotedNames := make([]string, 0, len(demoted))
	for name := range demoted {
		demotedNames = append(demotedNames, name)
	}
	sort.Strings(demotedNames)

	return &Merged{
		Name:       msgName,
		Classes:    classes,
		Attributes: merged,
		Owners:     owners,
		Demoted:    demotedNames,
	}, nil
}

// checkAttrShape verifies that two class-level definitions of the same
// attribute agree on everything that determines the proto field type.
func checkAttrShape(name string, a, b *schema.Attribute, bClass string) error {
	if a.Type != b.Type {
		return fmt.Errorf("ocsf merge: attribute %q: type %q conflicts with %q in class %q",
			name, a.Type, b.Type, bClass)
	}
	if a.ObjectType != b.ObjectType {
		return fmt.Errorf("ocsf merge: attribute %q: object type %q conflicts with %q in class %q",
			name, a.ObjectType, b.ObjectType, bClass)
	}
	if a.IsArray != b.IsArray {
		return fmt.Errorf("ocsf merge: attribute %q: is_array %v conflicts with %v in class %q",
			name, a.IsArray, b.IsArray, bClass)
	}
	return nil
}

// unionEnumMembers merges two enum member slices by key, returning the sorted
// union and whether a semantic conflict was found.
//
// A conflict is any of:
//   - the same integer key mapping to different captions in a and b,
//   - exactly one side having enum members at all (presence mismatch), or
//   - mixed key kinds (one side integer-keyed, the other string-keyed).
//
// String-keyed enums are unioned by StrKey with the same caption rule; they
// emit as plain proto strings either way, but a caption conflict still means
// the value semantics are class-scoped, so the caller records the demotion.
func unionEnumMembers(a, b []schema.EnumMember) (union []schema.EnumMember, conflict bool) {
	if len(a) == 0 && len(b) == 0 {
		return nil, false
	}
	// Presence mismatch: one class constrains values, the other doesn't.
	if (len(a) == 0) != (len(b) == 0) {
		if len(a) == 0 {
			return b, true
		}
		return a, true
	}
	// Kind mismatch.
	if a[0].IntKey != b[0].IntKey {
		return a, true
	}

	key := func(m schema.EnumMember) string {
		if m.IntKey {
			return fmt.Sprintf("i:%d", m.Key)
		}
		return "s:" + m.StrKey
	}

	byKey := make(map[string]schema.EnumMember, len(a)+len(b))
	for _, m := range a {
		byKey[key(m)] = m
	}
	for _, m := range b {
		if prev, ok := byKey[key(m)]; ok {
			if prev.Caption != m.Caption {
				conflict = true
			}
			continue
		}
		byKey[key(m)] = m
	}

	union = make([]schema.EnumMember, 0, len(byKey))
	for _, m := range byKey {
		union = append(union, m)
	}
	sort.Slice(union, func(i, j int) bool {
		if union[i].IntKey {
			return union[i].Key < union[j].Key
		}
		return union[i].StrKey < union[j].StrKey
	})
	return union, conflict
}

// mergedRequirement returns "required" when every selected class declares the
// attribute with requirement "required"; otherwise "recommended".
func mergedRequirement(attrName string, classes []schema.Class) string {
	for _, cls := range classes {
		attr, ok := cls.Attributes[attrName]
		if !ok || attr.Requirement != requirementRequired {
			return "recommended"
		}
	}
	return requirementRequired
}

// sortedKeys returns the sorted keys of an attribute map.
func sortedKeys(m map[string]*schema.Attribute) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
