// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package tagmap maintains the stable (message, attribute) → proto field-number
// map that the OCSF-to-proto generator uses when emitting .proto files.
//
// # Why this matters
//
// Proto field numbers must never change once a schema is deployed: a renumbering
// silently corrupts every stored or in-flight record.  OCSF carries no field
// numbers, so we maintain our own append-only map.  The generator calls Assign
// for every field it emits; CI calls CheckCompat to reject any incompatible
// change before it merges.
//
// # On-disk format
//
// The map is a JSON file with deterministic (sorted) keys:
//
//	{
//	  "ApiActivity": {
//	    "fields":   {"action": 3, "class_uid": 2, "time": 1},
//	    "reserved": [7, 9]
//	  },
//	  "User": {
//	    "fields":   {"name": 1},
//	    "reserved": []
//	  }
//	}
//
// Message names are sorted lexicographically; within each message, attribute
// names in "fields" are sorted and "reserved" tags are sorted numerically.
// This guarantees that two Save calls on the same in-memory state produce
// byte-identical files.
//
// # Pre-seeding
//
// Hot base-event attributes can be pre-assigned to low tags (1–15) simply by
// loading a hand-crafted seed file before the generator runs.  Assign returns
// the existing tag when the attr is already present, so the seed values are
// preserved.
//
// # Free-tag allocation
//
// Assign picks the lowest positive integer that is:
//   - not already assigned to another attr in the same message,
//   - not in the message's reserved list, and
//   - not in the proto-reserved range [19000, 19999] inclusive.
//
// Tags start at 1.
package tagmap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// protoReservedLo and protoReservedHi are the inclusive bounds of the range
// that proto3 reserves for its own extensions.  Tags in this range must never
// be assigned.
const (
	protoReservedLo = int32(19000)
	protoReservedHi = int32(19999)
)

// MessageTags holds the tag assignments and retired-tag list for one proto message.
type MessageTags struct {
	// Fields maps attribute name to its assigned proto field number.
	Fields map[string]int32 `json:"fields"`
	// Reserved holds retired tags that must never be re-assigned.
	Reserved []int32 `json:"reserved"`
}

// TagMap is the top-level map from proto message name to its tag assignments.
// It is the in-memory representation of the on-disk JSON file.
type TagMap struct {
	messages map[string]*MessageTags
}

// New returns an empty TagMap ready for use.
func New() *TagMap {
	return &TagMap{messages: make(map[string]*MessageTags)}
}

// ---------------------------------------------------------------------------
// Persistence
// ---------------------------------------------------------------------------

// jsonTagMap is the wire type used for JSON serialisation.
type jsonTagMap map[string]*jsonMessageTags

type jsonMessageTags struct {
	Fields   map[string]int32 `json:"fields"`
	Reserved []int32          `json:"reserved"`
}

// Load parses the tag-map JSON from path.
//
// A missing or empty file is not an error: it returns a new empty TagMap so
// that the first generator run bootstraps the map from scratch.
func Load(path string) (*TagMap, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return New(), nil
		}
		return nil, fmt.Errorf("tagmap: read %q: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return New(), nil
	}

	var wire jsonTagMap
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("tagmap: decode %q: %w", path, err)
	}

	tm := New()
	for msg, wm := range wire {
		mt := &MessageTags{
			Fields:   make(map[string]int32, len(wm.Fields)),
			Reserved: make([]int32, len(wm.Reserved)),
		}
		for attr, tag := range wm.Fields {
			mt.Fields[attr] = tag
		}
		copy(mt.Reserved, wm.Reserved)
		tm.messages[msg] = mt
	}

	if err := tm.validate(); err != nil {
		return nil, err
	}
	return tm, nil
}

// Save writes the tag map to path with deterministic output.
func (tm *TagMap) Save(path string) error {
	// Build a sorted list of message names.
	msgNames := make([]string, 0, len(tm.messages))
	for name := range tm.messages {
		msgNames = append(msgNames, name)
	}
	sort.Strings(msgNames)

	// Use an ordered encoding: encode each message in key order by building
	// the JSON manually through encoding/json with sorted intermediaries.
	//
	// We use a two-pass approach: marshal into a plain map with sorted keys,
	// relying on encoding/json's map key sorting (Go 1.12+: json.Marshal
	// sorts map keys lexicographically), then do the same for fields.
	wire := make(jsonTagMap, len(tm.messages))
	for _, name := range msgNames {
		mt := tm.messages[name]

		// Sort reserved.
		reserved := make([]int32, len(mt.Reserved))
		copy(reserved, mt.Reserved)
		sort.Slice(reserved, func(i, j int) bool { return reserved[i] < reserved[j] })

		// Fields map — encoding/json sorts string map keys, so just copy.
		fields := make(map[string]int32, len(mt.Fields))
		for k, v := range mt.Fields {
			fields[k] = v
		}

		wire[name] = &jsonMessageTags{
			Fields:   fields,
			Reserved: reserved,
		}
	}

	// encoding/json sorts map[string]* keys, so the top-level message order
	// is already lexicographic.  Marshal with indent for human readability.
	out, err := json.MarshalIndent(wire, "", "  ")
	if err != nil {
		return fmt.Errorf("tagmap: marshal: %w", err)
	}
	out = append(out, '\n')

	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("tagmap: write %q: %w", path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Tag lookup and allocation
// ---------------------------------------------------------------------------

// Tag returns the assigned proto field number for (message, attr).
// The second return value is false if no tag has been assigned yet.
func (tm *TagMap) Tag(message, attr string) (int32, bool) {
	mt, ok := tm.messages[message]
	if !ok {
		return 0, false
	}
	tag, ok := mt.Fields[attr]
	return tag, ok
}

// Assign returns the existing tag for (message, attr) if one is already
// recorded; otherwise it allocates the lowest free tag and records it.
//
// "Free" means the tag is not currently assigned to any attr in the message,
// not in the message's reserved list, and not in the proto-reserved range
// 19000–19999 inclusive.  Tags start at 1.
func (tm *TagMap) Assign(message, attr string) (int32, error) {
	mt := tm.getOrCreate(message)

	// Return existing tag if already assigned.
	if tag, ok := mt.Fields[attr]; ok {
		return tag, nil
	}

	// Build a set of all occupied tags for this message.
	occupied := make(map[int32]bool, len(mt.Fields)+len(mt.Reserved))
	for _, t := range mt.Fields {
		occupied[t] = true
	}
	for _, t := range mt.Reserved {
		occupied[t] = true
	}

	// Find the lowest free tag starting at 1.
	var tag int32 = 1
	for {
		if tag >= protoReservedLo && tag <= protoReservedHi {
			// Jump over the entire reserved range in one step.
			tag = protoReservedHi + 1
			continue
		}
		if !occupied[tag] {
			break
		}
		tag++
	}

	mt.Fields[attr] = tag
	return tag, nil
}

// Retire removes attr's assignment from (message) and adds its former tag to
// the message's reserved list so it can never be reused.
//
// Retire is idempotent: calling it on an already-retired or never-assigned
// attr is a no-op.
func (tm *TagMap) Retire(message, attr string) error {
	mt, ok := tm.messages[message]
	if !ok {
		return nil
	}

	tag, assigned := mt.Fields[attr]
	if !assigned {
		return nil
	}

	// Remove from fields.
	delete(mt.Fields, attr)

	// Add to reserved if not already there.
	for _, r := range mt.Reserved {
		if r == tag {
			return nil
		}
	}
	mt.Reserved = append(mt.Reserved, tag)
	return nil
}

// ---------------------------------------------------------------------------
// Compatibility check
// ---------------------------------------------------------------------------

// CheckCompat reports every incompatibility between old and new TagMaps.
//
// Incompatibilities detected:
//
//  1. A (message, attr) whose tag changed between old and new.
//  2. A tag that was assigned in old but is neither assigned to the same attr
//     nor reserved in new (silently dropped without reserving).
//  3. A tag that was listed in old.reserved but is now assigned to some attr
//     in new (reserved tag reused).
//  4. A tag that was listed in old.reserved but is absent from new.reserved
//     (reservation dropped — reopens the tag for reuse against old decoders).
//
// Compatible changes (new attrs, new messages, new reserved entries) do not
// produce errors.
func CheckCompat(old, next *TagMap) error {
	var errs []string

	for msg, oldMT := range old.messages {
		newMT, msgExists := next.messages[msg]
		checkAttrTags(msg, oldMT, newMT, msgExists, &errs)
		if msgExists {
			checkReservedReuse(msg, oldMT, newMT, &errs)
			checkUnreserved(msg, oldMT, newMT, &errs)
		}
	}

	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs) // deterministic error order
	return fmt.Errorf("tagmap: compatibility check failed:\n  %s", strings.Join(errs, "\n  "))
}

// checkAttrTags checks that no tag changed and no tag was dropped without reserving.
func checkAttrTags(msg string, oldMT, newMT *MessageTags, msgExists bool, errs *[]string) {
	for attr, oldTag := range oldMT.Fields {
		if !msgExists {
			*errs = append(*errs, fmt.Sprintf(
				"message %q attr %q had tag %d in old map but message is absent in new map (tag not reserved)",
				msg, attr, oldTag))
			continue
		}

		newTag, inNew := newMT.Fields[attr]
		if inNew {
			// Attr still assigned — tag must not have changed.
			if newTag != oldTag {
				*errs = append(*errs, fmt.Sprintf(
					"message %q attr %q tag changed: old=%d new=%d",
					msg, attr, oldTag, newTag))
			}
			// No further check needed: tag is still owned by this attr.
			continue
		}

		// Attr is no longer assigned in new — the old tag must be reserved.
		if !containsTag(newMT.Reserved, oldTag) {
			*errs = append(*errs, fmt.Sprintf(
				"message %q attr %q (tag %d) was dropped from new map without being reserved",
				msg, attr, oldTag))
		}
	}
}

// checkReservedReuse checks that no reserved tag was reused for a new attr.
func checkReservedReuse(msg string, oldMT, newMT *MessageTags, errs *[]string) {
	for _, oldReserved := range oldMT.Reserved {
		for newAttr, newTag := range newMT.Fields {
			if newTag == oldReserved {
				*errs = append(*errs, fmt.Sprintf(
					"message %q attr %q (tag %d) reuses a tag from old reserved list",
					msg, newAttr, newTag))
			}
		}
	}
}

// checkUnreserved checks that every tag in old.Reserved is still reserved in
// new.  Dropping a reservation reopens the tag for allocation, which would
// silently reuse a wire number that old decoders still associate with a
// retired field.
func checkUnreserved(msg string, oldMT, newMT *MessageTags, errs *[]string) {
	for _, oldReserved := range oldMT.Reserved {
		if !containsTag(newMT.Reserved, oldReserved) {
			*errs = append(*errs, fmt.Sprintf(
				"message %q reserved tag %d was removed (must remain reserved forever)",
				msg, oldReserved))
		}
	}
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// validate checks the in-memory state for structural violations and returns a
// combined error naming every offending (message, attr/tag) pair.
//
// Rules:
//   - Every field tag must be > 0.
//   - Every reserved tag must be > 0.
//   - No two fields within the same message may share a tag.
//   - A field's tag must not also appear in that message's reserved list.
func (tm *TagMap) validate() error {
	var errs []string

	// Iterate in a deterministic order so error messages are stable.
	msgNames := make([]string, 0, len(tm.messages))
	for name := range tm.messages {
		msgNames = append(msgNames, name)
	}
	sort.Strings(msgNames)

	for _, msg := range msgNames {
		mt := tm.messages[msg]

		// Build reserved set for quick lookup and validate reserved values.
		reservedSet := make(map[int32]bool, len(mt.Reserved))
		for _, r := range mt.Reserved {
			if r <= 0 {
				errs = append(errs, fmt.Sprintf(
					"message %q has invalid reserved tag %d (must be > 0)", msg, r))
				continue
			}
			if r >= protoReservedLo && r <= protoReservedHi {
				errs = append(errs, fmt.Sprintf(
					"message %q has reserved tag %d in proto-reserved range [%d, %d]",
					msg, r, protoReservedLo, protoReservedHi))
				continue
			}
			reservedSet[r] = true
		}

		// Track seen field tags to detect duplicates.
		seenTags := make(map[int32]string, len(mt.Fields))

		// Sort attr names for deterministic error output.
		attrNames := make([]string, 0, len(mt.Fields))
		for attr := range mt.Fields {
			attrNames = append(attrNames, attr)
		}
		sort.Strings(attrNames)

		for _, attr := range attrNames {
			tag := mt.Fields[attr]

			if tag <= 0 {
				errs = append(errs, fmt.Sprintf(
					"message %q attr %q has invalid tag %d (must be > 0)", msg, attr, tag))
				continue
			}

			if tag >= protoReservedLo && tag <= protoReservedHi {
				errs = append(errs, fmt.Sprintf(
					"message %q attr %q has tag %d in proto-reserved range [%d, %d]",
					msg, attr, tag, protoReservedLo, protoReservedHi))
				continue
			}

			if prev, dup := seenTags[tag]; dup {
				errs = append(errs, fmt.Sprintf(
					"message %q attrs %q and %q share tag %d (duplicate)", msg, prev, attr, tag))
			} else {
				seenTags[tag] = attr
			}

			if reservedSet[tag] {
				errs = append(errs, fmt.Sprintf(
					"message %q attr %q tag %d also appears in reserved list", msg, attr, tag))
			}
		}
	}

	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)
	return fmt.Errorf("tagmap: invalid tag map:\n  %s", strings.Join(errs, "\n  "))
}

func (tm *TagMap) getOrCreate(message string) *MessageTags {
	mt, ok := tm.messages[message]
	if !ok {
		mt = &MessageTags{
			Fields:   make(map[string]int32),
			Reserved: nil,
		}
		tm.messages[message] = mt
	}
	return mt
}

func containsTag(reserved []int32, tag int32) bool {
	for _, r := range reserved {
		if r == tag {
			return true
		}
	}
	return false
}
