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
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/schema"
)

// ProtoEnumValue is a single value in a generated proto3 enum declaration.
//
// Ident is the fully-qualified proto enum value identifier, prefixed with the
// enum type name in UPPER_SNAKE form to avoid C++ scope collisions between
// enums that share common value names (e.g. UNKNOWN, OTHER).
// Number is the integer assigned to the value in the .proto file.
//
// The proto emitter iterates Values to write enum body lines.
type ProtoEnumValue struct {
	Ident  string
	Number int32
}

// ProtoEnum is the proto3 representation of an OCSF integer-keyed enum.
//
// Name holds the proto enum type name as supplied by the caller (e.g.
// "SeverityId").  Values is sorted ascending by Number with the zero value
// always first.
//
// SynthesizedZero is true when the OCSF source enum had no key-0 member and
// MapEnum prepended a synthetic <PREFIX>_UNKNOWN = 0 entry.  Callers should
// surface this so reviewers know the zero value was invented rather than taken
// from the schema.
//
// OCSF enums have a sibling free-text string field (e.g. severity_id int +
// severity string).  That sibling field is retained as a plain proto string
// field alongside the enum field; it is not modelled here because it is a
// field-emission concern, not an enum-mapping concern.
type ProtoEnum struct {
	Name            string
	Values          []ProtoEnumValue
	SynthesizedZero bool
}

// MapEnum maps an OCSF enum (expressed as the ordered []schema.EnumMember
// slice from schema.Attribute.Enum) to a proto3 ProtoEnum descriptor.
//
// typeName is the proto enum type name the caller has already determined (e.g.
// "SeverityId").  MapEnum uses it as-is for ProtoEnum.Name and derives the
// value-identifier prefix from its UPPER_SNAKE form.
//
// Returns:
//   - pe, true, nil  — integer-keyed enum; pe is ready for emission.
//   - zero, false, nil — string-keyed enum; the caller should represent the
//     field as a plain proto string instead.
//   - zero, false, err — any member has a negative key (OCSF doesn't use
//     negative values; surfacing is preferable to silently emitting a 10-byte
//     varint).
//
// Zero value: if no member with Key==0 exists, a synthetic entry named
// <PREFIX>_UNKNOWN = 0 is prepended and SynthesizedZero is set.
//
// Value identifiers follow the rule: <UPPER_SNAKE(typeName)>_<sanitizedCaption>.
// Caption sanitization: uppercase, replace runs of non-alphanumeric characters
// with a single "_", trim leading/trailing "_", collapse repeated "_".
// Duplicate resulting identifiers are resolved deterministically by appending
// "_2", "_3", … to the later colliding entry (later = higher key number).
func MapEnum(typeName string, members []schema.EnumMember) (pe ProtoEnum, isProtoEnum bool, err error) {
	if len(members) == 0 {
		// Empty enum is technically valid; treat as int-keyed since there is
		// nothing to distinguish it as string-keyed.
		return ProtoEnum{Name: typeName}, true, nil
	}

	// Determine key kind from the first member (schema guarantees homogeneous keys).
	if !members[0].IntKey {
		return ProtoEnum{}, false, nil
	}

	prefix := toUpperSnake(typeName)

	// Validate keys and build values.
	values := make([]ProtoEnumValue, 0, len(members))
	for _, m := range members {
		if m.Key < 0 {
			return ProtoEnum{}, false, fmt.Errorf(
				"ocsf gen: enum %q: negative key %d is not allowed in proto3",
				typeName, m.Key,
			)
		}
		if m.Key > math.MaxInt32 {
			return ProtoEnum{}, false, fmt.Errorf(
				"ocsf gen: enum %q: key %d overflows int32",
				typeName, m.Key,
			)
		}
		ident := prefix + "_" + sanitizeCaption(m.Caption)
		values = append(values, ProtoEnumValue{
			Ident:  ident,
			Number: int32(m.Key),
		})
	}

	// Sort ascending by number (schema.sortedEnumMembers already does this, but
	// the caller might pass an unsorted slice, so be defensive).
	sort.Slice(values, func(i, j int) bool {
		return values[i].Number < values[j].Number
	})

	// Synthesize a zero value if needed.
	synthesized := false
	if len(values) == 0 || values[0].Number != 0 {
		zero := ProtoEnumValue{Ident: prefix + "_UNKNOWN", Number: 0}
		values = append([]ProtoEnumValue{zero}, values...)
		synthesized = true
	}

	// De-duplicate identical idents deterministically.  Values are already in
	// ascending key order; the first occurrence of an ident keeps its name, each
	// subsequent collision gets a numeric suffix (_2, _3, …).
	seen := make(map[string]int, len(values)) // ident -> next suffix
	for i, v := range values {
		base := v.Ident
		if _, exists := seen[base]; !exists {
			seen[base] = 2
			continue
		}
		// Collision: find a free suffixed ident.
		suffix := seen[base]
		for {
			candidate := fmt.Sprintf("%s_%d", base, suffix)
			if _, used := seen[candidate]; !used {
				values[i].Ident = candidate
				seen[base] = suffix + 1
				seen[candidate] = 2
				break
			}
			suffix++
		}
	}

	return ProtoEnum{
		Name:            typeName,
		Values:          values,
		SynthesizedZero: synthesized,
	}, true, nil
}

// toUpperSnake converts a camelCase or PascalCase identifier to UPPER_SNAKE.
// "SeverityId" -> "SEVERITY_ID", "ActivityId" -> "ACTIVITY_ID".
func toUpperSnake(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) && !unicode.IsUpper(runes[i-1]) {
			b.WriteRune('_')
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

// sanitizeCaption converts a human-readable caption to an UPPER_SNAKE
// identifier fragment suitable for use in a proto enum value name.
//
// Rules:
//  1. Uppercase all letters.
//  2. Replace any run of non-alphanumeric characters with a single "_".
//  3. Trim leading and trailing "_".
func sanitizeCaption(caption string) string {
	var b strings.Builder
	inRun := false
	for _, r := range caption {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToUpper(r))
			inRun = false
		} else if !inRun {
			b.WriteRune('_')
			inRun = true
		}
	}
	result := strings.Trim(b.String(), "_")
	return result
}
