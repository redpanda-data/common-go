// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package schema provides Go types and a loader for the compiled OCSF
// (Open Cybersecurity Schema Framework) schema export.
//
// The loader supports the v2 export format returned by:
//
//	GET https://schema.ocsf.io/1.8.0/export/v2/schema
//
// In the v2 export, profile attributes are pre-merged into class and object
// attribute maps, extends chains are flattened, and enum values are inlined.
// The top-level shape differs from the legacy v1 export:
//   - types live under dictionary.types.attributes (not top-level "types")
//   - dictionary attributes live under dictionary.attributes (not top-level "dictionary_attributes")
//   - base_event is a member of the classes map (not a separate top-level key)
//   - attribute "profile" (singular *string) became "profiles" ([]string)
//
// This package normalises all of the above so the exported Go types (Schema,
// Class, Object, Attribute, etc.) remain stable regardless of which v2 export
// is loaded.
package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// Schema is the top-level compiled OCSF schema export.
type Schema struct {
	// Version is the OCSF schema version, e.g. "1.8.0".
	Version string

	// Classes is the map of event-class name to Class definition.
	// Each class corresponds to one row in the OCSF class taxonomy
	// (e.g. "api_activity", "entity_management").
	Classes map[string]*Class

	// Objects is the map of reusable complex-type name to Object definition.
	Objects map[string]*Object

	// Types is the map of scalar primitive-type name (e.g. "string_t",
	// "integer_t") to its descriptor.
	Types map[string]*TypeDef

	// DictionaryAttributes is the global attribute dictionary.  It maps
	// attribute name to its dictionary-level definition (type, caption,
	// description).  In the compiled export this lives under
	// "dictionary_attributes".
	DictionaryAttributes map[string]*DictAttr

	// BaseEvent holds the compiled base-event pseudo-class that carries the
	// attributes common to all event classes.
	BaseEvent *BaseEvent
}

// Class represents a compiled OCSF event class.
//
// The UID field equals what OCSF documentation calls "class_uid":
// it is CategoryUID*1000 + the within-category ordinal, giving values like
// 6003 (API Activity) or 3004 (Entity Management).
type Class struct {
	// Name is the machine-readable snake_case class name.
	Name string

	// Caption is the human-readable class name.
	Caption string

	// Description is the human-readable class description.
	Description string

	// UID is the fully-qualified numeric class identifier
	// (equivalent to "class_uid" in OCSF docs).  It equals
	// CategoryUID*1000 + within-category ordinal.
	UID int

	// CategoryUID is the numeric category this class belongs to.
	CategoryUID int

	// Category is the snake_case category name (e.g. "application", "iam").
	Category string

	// CategoryName is the human-readable category name.
	CategoryName string

	// Extends is the name of the parent class this class extends, if any.
	Extends string

	// Profiles lists the OCSF profile names merged into this class in the
	// compiled export.
	Profiles []string

	// Attributes is the fully-resolved attribute map for this class.
	// Profile attributes are already merged in.
	Attributes map[string]*Attribute

	// Constraints holds optional validation rules; nil when none are defined.
	Constraints *Constraints
}

// Constraints captures the optional validation rules on a class or object.
//
// Only the constraint types present in the compiled OCSF 1.x export are
// represented here.  Both fields are nil (not empty slice) when absent.
type Constraints struct {
	// AtLeastOne lists attribute names where at least one must be present
	// in a valid instance.
	AtLeastOne []string

	// JustOne lists attribute names where exactly one must be present.
	JustOne []string
}

// Object represents a reusable complex-type (object) in OCSF.
type Object struct {
	// Name is the machine-readable object name.
	Name string

	// Caption is the human-readable object name.
	Caption string

	// Description is the human-readable object description.
	Description string

	// Extends is the name of the parent object this object extends, if any.
	Extends string

	// Observable is the optional observable type ID for this object.
	Observable int

	// Attributes is the resolved attribute map for this object.
	Attributes map[string]*Attribute

	// Constraints holds optional validation rules; nil when none are defined.
	Constraints *Constraints
}

// classConstraints converts a raw constraints pointer to the public type.
// Returns nil when raw is nil or both slices are empty (keeps Constraints
// absent rather than present-but-empty, for forward-compatibility).
func classConstraints(raw *rawConstraints) *Constraints {
	if raw == nil {
		return nil
	}
	if len(raw.AtLeastOne) == 0 && len(raw.JustOne) == 0 {
		return nil
	}
	return &Constraints{
		AtLeastOne: raw.AtLeastOne,
		JustOne:    raw.JustOne,
	}
}

// Attribute is a field definition within a Class or Object.
type Attribute struct {
	// Name is the machine-readable attribute name (same as the map key).
	Name string

	// Caption is the human-readable attribute label.
	Caption string

	// Description is the human-readable description.
	Description string

	// Type is the OCSF primitive type name (e.g. "string_t", "integer_t",
	// "object_t").
	Type string

	// TypeName is the human-readable type label (e.g. "String", "Integer").
	TypeName string

	// IsArray indicates the attribute holds a list of values.
	IsArray bool

	// Requirement is one of "required", "recommended", or "optional".
	Requirement string

	// Group is the display-group hint (e.g. "primary", "classification").
	Group string

	// Profile is the profile name this attribute was merged from, or empty
	// for attributes defined directly on the class/object.
	Profile string

	// ObjectType is the name of the OCSF Object this attribute references
	// when Type == "object_t".  It is the key to look up in Schema.Objects.
	ObjectType string

	// ObjectName is the human-readable name of the referenced Object.
	ObjectName string

	// Sibling is the name of the companion string attribute for enum
	// attributes (the _id → _name relationship).
	Sibling string

	// Enum holds the ordered enum members when the attribute has enumerated
	// values.  Members are sorted by ascending integer key.
	Enum []EnumMember

	// Observable is the optional observable type ID for this attribute.
	Observable int
}

// EnumMember is a single entry in an OCSF enum.
//
// Most OCSF enums use integer keys (e.g. severity_id, activity_id).  A small
// number use string keys (e.g. TLP values in osint.tlp, CVSS depth).
// When IntKey is true the Key field holds the integer value; otherwise
// StrKey holds the raw string value.
type EnumMember struct {
	// Key is the integer enum value.  Valid only when IntKey is true.
	Key int

	// StrKey is the raw string enum key.  Valid only when IntKey is false.
	StrKey string

	// IntKey reports whether this member has an integer key.
	IntKey bool

	// Caption is the short human-readable label (e.g. "Unknown", "Allow").
	Caption string

	// Description is the longer explanation for this enum value.
	Description string
}

// TypeDef describes a scalar primitive type in the OCSF type system.
type TypeDef struct {
	// Caption is the human-readable type name.
	Caption string

	// Description is the human-readable description.
	Description string

	// Type is the base primitive type this type derives from, e.g. "string_t"
	// for ip_t, "long_t" for timestamp_t.  Empty for base types (string_t,
	// integer_t, long_t, float_t, boolean_t, json_t) that have no parent.
	Type string
}

// DictAttr is a global-dictionary attribute descriptor.
type DictAttr struct {
	// Type is the primitive type name.
	Type string

	// TypeName is the human-readable type label.
	TypeName string

	// Caption is the human-readable attribute label.
	Caption string

	// Description is the description from the global dictionary.
	Description string
}

// BaseEvent is the compiled OCSF base-event pseudo-class.
type BaseEvent struct {
	// Name is "base_event".
	Name string

	// Caption is the human-readable label.
	Caption string

	// Description is the human-readable description.
	Description string

	// Attributes are the base-event attributes common to all event classes.
	Attributes map[string]*Attribute
}

// ---------------------------------------------------------------------------
// JSON wire types (unexported) — v2 export format
// ---------------------------------------------------------------------------

// rawSchema models the v2 export top-level object:
//
//	{
//	  "version": "1.8.0",
//	  "compile_version": 1,
//	  "classes": { ... },
//	  "objects": { ... },
//	  "dictionary": { "attributes": {...}, "types": { "attributes": {...} } },
//	  "profiles": { ... },
//	  "categories": { ... },
//	  "extensions": { ... }
//	}
//
// base_event is a member of classes with uid=0, extracted by convertSchema.
type rawSchema struct {
	Version    string               `json:"version"`
	Classes    map[string]rawClass  `json:"classes"`
	Objects    map[string]rawObject `json:"objects"`
	Dictionary rawDictionary        `json:"dictionary"`
}

// rawDictionary holds the "dictionary" block from the v2 export.
type rawDictionary struct {
	Attributes map[string]rawDictAttr `json:"attributes"`
	Types      rawDictTypes           `json:"types"`
}

// rawDictTypes holds the "dictionary.types" block.  The actual type
// definitions are nested one level deeper under "attributes".
type rawDictTypes struct {
	Attributes map[string]rawTypeDef `json:"attributes"`
}

type rawConstraints struct {
	AtLeastOne []string `json:"at_least_one"`
	JustOne    []string `json:"just_one"`
}

type rawClass struct {
	Name         string                  `json:"name"`
	Caption      string                  `json:"caption"`
	Description  string                  `json:"description"`
	UID          int                     `json:"uid"`
	CategoryUID  int                     `json:"category_uid"`
	Category     string                  `json:"category"`
	CategoryName string                  `json:"category_name"`
	Extends      string                  `json:"extends"`
	Profiles     []string                `json:"profiles"`
	Attributes   map[string]rawAttribute `json:"attributes"`
	Constraints  *rawConstraints         `json:"constraints"`
}

type rawObject struct {
	Name        string                  `json:"name"`
	Caption     string                  `json:"caption"`
	Description string                  `json:"description"`
	Extends     string                  `json:"extends"`
	Observable  int                     `json:"observable"`
	Attributes  map[string]rawAttribute `json:"attributes"`
	Constraints *rawConstraints         `json:"constraints"`
}

// rawAttribute models a single attribute entry in v2.
//
// Key changes from the legacy format:
//   - "profile" (*string) → "profiles" ([]string): the attribute now carries
//     a list of profile names it was merged from, not a single pointer.
//   - "observable" is absent from class-level attributes in v2 (still present
//     on some object-level attributes; parsed but harmless if missing).
//   - Additional v2 keys ("suppress_checks", "references", "source",
//     "extension", "extension_id", "@deprecated") are intentionally ignored.
type rawAttribute struct {
	Caption     string `json:"caption"`
	Description string `json:"description"`
	Type        string `json:"type"`
	TypeName    string `json:"type_name"`
	IsArray     bool   `json:"is_array"`
	Requirement string `json:"requirement"`
	Group       string `json:"group"`
	// Profiles lists the profile(s) this attribute was merged from (v2 format).
	// The legacy "profile" (*string) field is no longer emitted by the server.
	Profiles   []string                 `json:"profiles"`
	ObjectType string                   `json:"object_type"`
	ObjectName string                   `json:"object_name"`
	Sibling    string                   `json:"sibling"`
	Enum       map[string]rawEnumMember `json:"enum"`
	Observable int                      `json:"observable"`
}

type rawEnumMember struct {
	Caption     string `json:"caption"`
	Description string `json:"description"`
}

type rawTypeDef struct {
	Caption     string `json:"caption"`
	Description string `json:"description"`
	// Type is the base type name for derived types (e.g. "string_t" for ip_t).
	// The OCSF v2 export uses the key "type" for this link under
	// dictionary.types.attributes.
	Type string `json:"type"`
}

type rawDictAttr struct {
	Type        string `json:"type"`
	TypeName    string `json:"type_name"`
	Caption     string `json:"caption"`
	Description string `json:"description"`
}

// ---------------------------------------------------------------------------
// Loader
// ---------------------------------------------------------------------------

// Load parses a compiled OCSF schema export JSON from r.
//
// The caller is responsible for closing r.
func Load(r io.Reader) (*Schema, error) {
	var raw rawSchema
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("ocsf schema: decode JSON: %w", err)
	}
	return convertSchema(&raw)
}

// LoadFile opens path and calls Load.
func LoadFile(path string) (*Schema, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("ocsf schema: open %q: %w", path, err)
	}
	defer f.Close()
	return Load(f)
}

// ---------------------------------------------------------------------------
// Conversion helpers
// ---------------------------------------------------------------------------

func convertSchema(raw *rawSchema) (*Schema, error) {
	if raw.Version == "" {
		return nil, errors.New("ocsf schema: missing or empty \"version\" field")
	}

	// In the v2 export, base_event is a member of the classes map with uid=0.
	// Extract it before iterating over regular classes.
	rawBE, hasBaseEvent := raw.Classes["base_event"]

	classCount := len(raw.Classes)
	if hasBaseEvent {
		classCount-- // base_event is not a real class
	}

	s := &Schema{
		Version:              raw.Version,
		Classes:              make(map[string]*Class, classCount),
		Objects:              make(map[string]*Object, len(raw.Objects)),
		Types:                make(map[string]*TypeDef, len(raw.Dictionary.Types.Attributes)),
		DictionaryAttributes: make(map[string]*DictAttr, len(raw.Dictionary.Attributes)),
	}

	for name, rc := range raw.Classes {
		if name == "base_event" {
			continue
		}
		cls, err := convertClass(name, &rc)
		if err != nil {
			return nil, fmt.Errorf("ocsf schema: class %q: %w", name, err)
		}
		s.Classes[name] = cls
	}

	for name, ro := range raw.Objects {
		obj, err := convertObject(name, &ro)
		if err != nil {
			return nil, fmt.Errorf("ocsf schema: object %q: %w", name, err)
		}
		s.Objects[name] = obj
	}

	// Types live under dictionary.types.attributes in the v2 export.
	for name, rt := range raw.Dictionary.Types.Attributes {
		s.Types[name] = &TypeDef{
			Caption:     rt.Caption,
			Description: rt.Description,
			Type:        rt.Type,
		}
	}

	// Dictionary attributes live under dictionary.attributes in the v2 export.
	for name, rd := range raw.Dictionary.Attributes {
		s.DictionaryAttributes[name] = &DictAttr{
			Type:        rd.Type,
			TypeName:    rd.TypeName,
			Caption:     rd.Caption,
			Description: rd.Description,
		}
	}

	if hasBaseEvent {
		be, err := convertBaseEventFromClass(&rawBE)
		if err != nil {
			return nil, fmt.Errorf("ocsf schema: base_event: %w", err)
		}
		s.BaseEvent = be
	}

	return s, nil
}

func convertClass(name string, rc *rawClass) (*Class, error) {
	attrs, err := convertAttributes(rc.Attributes)
	if err != nil {
		return nil, err
	}
	return &Class{
		Name:         name,
		Caption:      rc.Caption,
		Description:  rc.Description,
		UID:          rc.UID,
		CategoryUID:  rc.CategoryUID,
		Category:     rc.Category,
		CategoryName: rc.CategoryName,
		Extends:      rc.Extends,
		Profiles:     rc.Profiles,
		Attributes:   attrs,
		Constraints:  classConstraints(rc.Constraints),
	}, nil
}

func convertObject(name string, ro *rawObject) (*Object, error) {
	attrs, err := convertAttributes(ro.Attributes)
	if err != nil {
		return nil, err
	}
	return &Object{
		Name:        name,
		Caption:     ro.Caption,
		Description: ro.Description,
		Extends:     ro.Extends,
		Observable:  ro.Observable,
		Attributes:  attrs,
		Constraints: classConstraints(ro.Constraints),
	}, nil
}

// convertBaseEventFromClass converts the raw class entry for "base_event"
// (which the v2 export stores inside the classes map) into a BaseEvent.
func convertBaseEventFromClass(rc *rawClass) (*BaseEvent, error) {
	attrs, err := convertAttributes(rc.Attributes)
	if err != nil {
		return nil, err
	}
	return &BaseEvent{
		Name:        rc.Name,
		Caption:     rc.Caption,
		Description: rc.Description,
		Attributes:  attrs,
	}, nil
}

func convertAttributes(raw map[string]rawAttribute) (map[string]*Attribute, error) {
	if raw == nil {
		return nil, nil
	}
	out := make(map[string]*Attribute, len(raw))
	for name, ra := range raw {
		attr, err := convertAttribute(name, &ra)
		if err != nil {
			return nil, fmt.Errorf("attribute %q: %w", name, err)
		}
		out[name] = attr
	}
	return out, nil
}

func convertAttribute(name string, ra *rawAttribute) (*Attribute, error) {
	// v2 uses "profiles" ([]string).  Collapse to a single string for
	// compatibility with the Attribute.Profile field: take the first entry
	// when present, empty string otherwise.  Attributes merged from exactly
	// one profile carry a single-element list; base attributes carry none.
	profile := ""
	if len(ra.Profiles) > 0 {
		profile = ra.Profiles[0]
	}

	members, err := sortedEnumMembers(ra.Enum)
	if err != nil {
		return nil, fmt.Errorf("enum: %w", err)
	}

	return &Attribute{
		Name:        name,
		Caption:     ra.Caption,
		Description: ra.Description,
		Type:        ra.Type,
		TypeName:    ra.TypeName,
		IsArray:     ra.IsArray,
		Requirement: ra.Requirement,
		Group:       ra.Group,
		Profile:     profile,
		ObjectType:  ra.ObjectType,
		ObjectName:  ra.ObjectName,
		Sibling:     ra.Sibling,
		Enum:        members,
		Observable:  ra.Observable,
	}, nil
}

// sortedEnumMembers converts the string-keyed enum map into a slice with
// deterministic ordering.
//
// Most OCSF enums use integer keys (e.g. severity_id).  The function probes
// the first key to decide the kind: if it parses as an integer, all keys are
// treated as integers and the slice is sorted by ascending integer value.
// Otherwise (including a mix of int-parseable and non-parseable keys) all
// entries are treated as string-keyed and sorted lexicographically.
func sortedEnumMembers(raw map[string]rawEnumMember) ([]EnumMember, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	// Probe whether keys are integers.
	allInt := true
	for keyStr := range raw {
		if _, err := strconv.Atoi(keyStr); err != nil {
			allInt = false
			break
		}
	}

	members := make([]EnumMember, 0, len(raw))
	for keyStr, rm := range raw {
		m := EnumMember{
			Caption:     rm.Caption,
			Description: rm.Description,
		}
		if allInt {
			k, err := strconv.Atoi(keyStr)
			if err != nil {
				// Should not happen after probe, but be defensive.
				return nil, fmt.Errorf("enum key %q: %w", keyStr, err)
			}
			m.Key = k
			m.IntKey = true
		} else {
			m.StrKey = keyStr
		}
		members = append(members, m)
	}

	sort.Slice(members, func(i, j int) bool {
		if members[i].IntKey {
			return members[i].Key < members[j].Key
		}
		return members[i].StrKey < members[j].StrKey
	})

	return members, nil
}
