// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package tagmap_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/tagmap"
)

// TestRoundTrip verifies that an existing attribute keeps its tag across a
// Save → Load cycle and that the loaded map equals the original.
func TestRoundTrip(t *testing.T) {
	tm := tagmap.New()

	tag, err := tm.Assign("ApiActivity", "time")
	require.NoError(t, err)
	require.Equal(t, int32(1), tag)

	tag2, err := tm.Assign("ApiActivity", "class_uid")
	require.NoError(t, err)
	require.Equal(t, int32(2), tag2)

	dir := t.TempDir()
	path := filepath.Join(dir, "tags.json")

	require.NoError(t, tm.Save(path))

	loaded, err := tagmap.Load(path)
	require.NoError(t, err)

	// Existing attr keeps its tag.
	gotTime, ok := loaded.Tag("ApiActivity", "time")
	require.True(t, ok)
	require.Equal(t, int32(1), gotTime)

	gotUID, ok := loaded.Tag("ApiActivity", "class_uid")
	require.True(t, ok)
	require.Equal(t, int32(2), gotUID)

	// Assign on loaded map returns the same tag, not a new one.
	reassigned, err := loaded.Assign("ApiActivity", "time")
	require.NoError(t, err)
	require.Equal(t, int32(1), reassigned)
}

// TestLoadMissing verifies that Load of a missing file returns an empty map,
// not an error (bootstrap behaviour for first run).
func TestLoadMissing(t *testing.T) {
	dir := t.TempDir()
	tm, err := tagmap.Load(filepath.Join(dir, "nonexistent.json"))
	require.NoError(t, err)
	require.NotNil(t, tm)

	// Empty map — Tag returns not-found.
	_, ok := tm.Tag("ApiActivity", "time")
	require.False(t, ok)
}

// TestAssignLowestFreeTag verifies that Assign returns the lowest free tag,
// skipping already-assigned tags and reserved tags.
func TestAssignLowestFreeTag(t *testing.T) {
	tm := tagmap.New()

	// First assignment on an empty message gets tag 1.
	tag, err := tm.Assign("Msg", "a")
	require.NoError(t, err)
	require.Equal(t, int32(1), tag)

	// Second gets tag 2.
	tag, err = tm.Assign("Msg", "b")
	require.NoError(t, err)
	require.Equal(t, int32(2), tag)

	// Retire "a" (tag 1). Its tag moves to reserved.
	require.NoError(t, tm.Retire("Msg", "a"))

	// Next new attr gets the lowest free, skipping reserved 1.
	tag, err = tm.Assign("Msg", "c")
	require.NoError(t, err)
	require.Equal(t, int32(3), tag, "must skip reserved tag 1")
}

// TestAssignSkipsReservedRange verifies that Assign skips the proto reserved
// range 19000–19999 (inclusive) and resumes at 20000.
//
// We pre-seed a map via Save→Load with tags 1..18999 already assigned, then
// ask for one more. This avoids 18999 individual Assign calls and keeps the
// test fast.
func TestAssignSkipsReservedRange(t *testing.T) {
	// Build the seed JSON by hand: fields 1..18999.
	// We marshal directly to avoid the slow iterative Assign path.
	fields := make(map[string]int32, 18999)
	for i := 1; i <= 18999; i++ {
		fields["field_"+strconv.Itoa(i)] = int32(i)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "big.json")

	type msgJSON struct {
		Fields   map[string]int32 `json:"fields"`
		Reserved []int32          `json:"reserved"`
	}
	seed := map[string]*msgJSON{
		"BigMsg": {Fields: fields, Reserved: nil},
	}

	enc, err := json.MarshalIndent(seed, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, enc, 0o600))

	tm, err := tagmap.Load(path)
	require.NoError(t, err)

	// Verify a known tag loaded correctly.
	lastTag, ok := tm.Tag("BigMsg", "field_18999")
	require.True(t, ok)
	require.Equal(t, int32(18999), lastTag)

	// The next assignment must skip 19000–19999 and land on 20000.
	tag, err := tm.Assign("BigMsg", "field_after_range")
	require.NoError(t, err)
	require.Equal(t, int32(20000), tag, "must skip the proto reserved range 19000–19999")
}

// TestRetireMovesToReserved verifies that Retire removes the attribute from
// the field map, adds its tag to reserved, and that a subsequent Assign of a
// new attribute does not reuse the retired tag.
func TestRetireMovesToReserved(t *testing.T) {
	tm := tagmap.New()

	_, err := tm.Assign("Evt", "x")
	require.NoError(t, err) // tag 1
	_, err = tm.Assign("Evt", "y")
	require.NoError(t, err) // tag 2
	_, err = tm.Assign("Evt", "z")
	require.NoError(t, err) // tag 3

	// Retire y (tag 2).
	require.NoError(t, tm.Retire("Evt", "y"))

	// y is gone from Tag lookups.
	_, ok := tm.Tag("Evt", "y")
	require.False(t, ok)

	// A new attr gets tag 4, not 2 (reserved).
	tag, err := tm.Assign("Evt", "w")
	require.NoError(t, err)
	require.Equal(t, int32(4), tag)
}

// TestRetireIdempotent verifies that Retire is idempotent: calling it on an
// already-retired (or never-assigned) attr is a no-op, not an error.
func TestRetireIdempotent(t *testing.T) {
	tm := tagmap.New()

	_, err := tm.Assign("Msg", "a")
	require.NoError(t, err)

	require.NoError(t, tm.Retire("Msg", "a"))
	// Second Retire on same attr should not error.
	require.NoError(t, tm.Retire("Msg", "a"))
	// Retire on never-assigned attr.
	require.NoError(t, tm.Retire("Msg", "never_assigned"))
}

// TestCheckCompatTagChanged verifies that CheckCompat returns an error when a
// (message,attr) pair has a different tag in new vs old.
func TestCheckCompatTagChanged(t *testing.T) {
	old := tagmap.New()
	_, err := old.Assign("Evt", "time")
	require.NoError(t, err) // tag 1

	// Build a new map where "time" has tag 2 (simulating a renumbering bug).
	newTM := tagmap.New()
	_, err = newTM.Assign("Evt", "other") // tag 1
	require.NoError(t, err)
	_, err = newTM.Assign("Evt", "time") // tag 2 — wrong
	require.NoError(t, err)

	err = tagmap.CheckCompat(old, newTM)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Evt")
	require.Contains(t, err.Error(), "time")
}

// TestCheckCompatDroppedWithoutReserving verifies that CheckCompat returns an
// error when an old assigned tag is silently dropped (neither still assigned
// to the same attr nor reserved in new).
func TestCheckCompatDroppedWithoutReserving(t *testing.T) {
	old := tagmap.New()
	_, err := old.Assign("Evt", "action")
	require.NoError(t, err) // tag 1

	// New map omits "action" entirely and doesn't reserve tag 1.
	newTM := tagmap.New()
	_, err = newTM.Assign("Evt", "other_field")
	require.NoError(t, err) // tag 1 reused — incompatible

	err = tagmap.CheckCompat(old, newTM)
	require.Error(t, err)
	require.Contains(t, err.Error(), "action")
}

// TestCheckCompatReservedTagReused verifies that CheckCompat returns an error
// when a tag that was in old.reserved is now assigned to some attribute in new.
func TestCheckCompatReservedTagReused(t *testing.T) {
	old := tagmap.New()
	_, err := old.Assign("Evt", "gone")
	require.NoError(t, err) // tag 1
	require.NoError(t, old.Retire("Evt", "gone"))
	// Now tag 1 is in old.reserved.

	// New map re-assigns tag 1 to a different attr — incompatible.
	newTM := tagmap.New()
	_, err = newTM.Assign("Evt", "new_field")
	require.NoError(t, err) // tag 1 — reuses reserved tag

	err = tagmap.CheckCompat(old, newTM)
	require.Error(t, err)
	require.Contains(t, err.Error(), "new_field")
}

// TestCheckCompatAdditiveIsOK verifies that purely additive changes (new
// attributes, new messages, new reserved entries) are compatible.
func TestCheckCompatAdditiveIsOK(t *testing.T) {
	old := tagmap.New()
	_, err := old.Assign("Evt", "time")
	require.NoError(t, err) // tag 1
	_, err = old.Assign("Evt", "action")
	require.NoError(t, err) // tag 2

	// New map keeps existing tags and adds more.
	newTM := tagmap.New()
	_, err = newTM.Assign("Evt", "time")
	require.NoError(t, err) // tag 1 same
	_, err = newTM.Assign("Evt", "action")
	require.NoError(t, err) // tag 2 same
	_, err = newTM.Assign("Evt", "severity_id")
	require.NoError(t, err) // tag 3 — new attr

	// New message entirely.
	_, err = newTM.Assign("User", "name")
	require.NoError(t, err)

	require.NoError(t, tagmap.CheckCompat(old, newTM))
}

// TestCheckCompatNewReservedIsOK verifies that adding a new entry to reserved
// in new (without touching existing assignments) is a compatible change.
func TestCheckCompatNewReservedIsOK(t *testing.T) {
	old := tagmap.New()
	_, err := old.Assign("Evt", "time")
	require.NoError(t, err) // tag 1
	_, err = old.Assign("Evt", "action")
	require.NoError(t, err) // tag 2

	// new retires "action" — adds 2 to reserved, removes it from fields.
	newTM := tagmap.New()
	_, err = newTM.Assign("Evt", "time")
	require.NoError(t, err) // tag 1 same
	_, err = newTM.Assign("Evt", "action")
	require.NoError(t, err) // tag 2
	require.NoError(t, newTM.Retire("Evt", "action"))

	// "action" was in old assigned — it's now retired in new, which is
	// compatible: the tag (2) is still tracked (reserved).
	require.NoError(t, tagmap.CheckCompat(old, newTM))
}

// TestSaveDeterministic verifies that Save produces byte-identical output
// across two calls on the same map, including when reserved entries are present.
func TestSaveDeterministic(t *testing.T) {
	tm := tagmap.New()

	// Add fields in non-alphabetical order to test sorting.
	for _, attr := range []string{"zebra", "apple", "mango"} {
		_, err := tm.Assign("Fruit", attr)
		require.NoError(t, err)
	}
	for _, attr := range []string{"z_msg", "a_msg"} {
		_, err := tm.Assign("Veggie", attr)
		require.NoError(t, err)
	}

	// Retire an attr so the sorted-reserved output path is exercised.
	require.NoError(t, tm.Retire("Fruit", "zebra"))
	require.NoError(t, tm.Retire("Veggie", "z_msg"))

	dir := t.TempDir()
	path1 := filepath.Join(dir, "tags1.json")
	path2 := filepath.Join(dir, "tags2.json")

	require.NoError(t, tm.Save(path1))
	require.NoError(t, tm.Save(path2))

	b1, err := os.ReadFile(path1)
	require.NoError(t, err)
	b2, err := os.ReadFile(path2)
	require.NoError(t, err)

	require.True(t, bytes.Equal(b1, b2), "Save output must be byte-deterministic")
}

// TestMultipleMessages verifies that tag allocation is per-message: two
// different messages each start from tag 1 independently.
func TestMultipleMessages(t *testing.T) {
	tm := tagmap.New()

	tag, err := tm.Assign("Msg1", "field")
	require.NoError(t, err)
	require.Equal(t, int32(1), tag)

	tag, err = tm.Assign("Msg2", "field")
	require.NoError(t, err)
	require.Equal(t, int32(1), tag, "each message has its own tag space")
}

// TestLoadEmptyFile verifies that an existing but empty file is treated as an
// empty map, not an error.
func TestLoadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	require.NoError(t, os.WriteFile(path, []byte(""), 0o600))

	tm, err := tagmap.Load(path)
	require.NoError(t, err)
	require.NotNil(t, tm)
}

// TestLoadRejectsZeroFieldTag verifies that Load returns a descriptive error
// when a field tag is 0 (or negative), naming the offending message and attr.
func TestLoadRejectsZeroFieldTag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")

	raw := `{
  "Evt": {
    "fields":   {"action": 0, "time": 1},
    "reserved": []
  }
}`
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))

	_, err := tagmap.Load(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Evt")
	require.Contains(t, err.Error(), "action")
}

// TestLoadRejectsDuplicateTag verifies that Load returns a descriptive error
// when two fields in the same message share the same tag.
func TestLoadRejectsDuplicateTag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.json")

	// JSON doesn't normally allow duplicate keys in a single object, but we can
	// craft a collision via two different attr names pointing at the same number.
	raw := `{
  "Evt": {
    "fields":   {"action": 2, "time": 2},
    "reserved": []
  }
}`
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))

	_, err := tagmap.Load(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Evt")
	// The error must mention tag 2.
	require.Contains(t, err.Error(), "2")
}

// TestLoadRejectsFieldTagInReserved verifies that Load returns an error when a
// field's tag also appears in that message's reserved list.
func TestLoadRejectsFieldTagInReserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conflict.json")

	raw := `{
  "Evt": {
    "fields":   {"action": 3},
    "reserved": [1, 3, 5]
  }
}`
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))

	_, err := tagmap.Load(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Evt")
	require.Contains(t, err.Error(), "action")
}

// TestLoadRejectsZeroReservedTag verifies that Load returns an error when a
// reserved entry is <= 0, naming the offending message.
func TestLoadRejectsZeroReservedTag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "badreserved.json")

	raw := `{
  "Evt": {
    "fields":   {"time": 1},
    "reserved": [0]
  }
}`
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))

	_, err := tagmap.Load(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Evt")
}

// TestCheckCompatMultipleOffenders verifies that CheckCompat collects ALL
// incompatibilities and returns them in a single error rather than stopping
// at the first one found.
func TestCheckCompatMultipleOffenders(t *testing.T) {
	old := tagmap.New()
	_, err := old.Assign("Evt", "time")
	require.NoError(t, err) // tag 1
	_, err = old.Assign("Evt", "action")
	require.NoError(t, err) // tag 2
	// Also retire tag 5 so we can test reserved-tag reuse.
	_, err = old.Assign("Evt", "gone")
	require.NoError(t, err) // tag 3
	require.NoError(t, old.Retire("Evt", "gone"))
	// Now old: fields={time:1, action:2}, reserved=[3]

	// Build a new map with two simultaneous incompatibilities:
	//   1. "time" has tag 99 instead of 1 (tag changed).
	//   2. "action" has tag 2 but a new attr "new_field" gets tag 3,
	//      reusing the reserved tag.
	// We build the new map by direct JSON seed to control exact tag values.
	var newTM *tagmap.TagMap
	raw := `{
  "Evt": {
    "fields":   {"time": 99, "action": 2, "new_field": 3},
    "reserved": []
  }
}`
	dir := t.TempDir()
	seedPath := filepath.Join(dir, "seed.json")
	require.NoError(t, os.WriteFile(seedPath, []byte(raw), 0o600))
	newTM, err = tagmap.Load(seedPath)
	require.NoError(t, err)

	err = tagmap.CheckCompat(old, newTM)
	require.Error(t, err)

	// Both offenders must appear in the single error string.
	require.Contains(t, err.Error(), "time")      // tag changed
	require.Contains(t, err.Error(), "new_field") // reused reserved tag
}

// TestLoadRejectsProtoReservedRangeTag verifies that Load returns an error when
// a field or reserved tag falls within the proto-reserved range 19000–19999.
func TestLoadRejectsProtoReservedRangeTag(t *testing.T) {
	t.Run("field tag 19000", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "proto_range_field.json")
		raw := `{"Evt":{"fields":{"bad":19000},"reserved":[]}}`
		require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))

		_, err := tagmap.Load(path)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Evt")
		require.Contains(t, err.Error(), "19000")
	})

	t.Run("reserved tag 19500", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "proto_range_reserved.json")
		raw := `{"Evt":{"fields":{},"reserved":[19500]}}`
		require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))

		_, err := tagmap.Load(path)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Evt")
		require.Contains(t, err.Error(), "19500")
	})
}

// TestCheckCompatUnreservedTagFails verifies that CheckCompat returns an error
// when a tag that was in old.Reserved is no longer reserved in new — even though
// no field was re-assigned to it yet.  Removing a reservation reopens that tag
// for allocation, which would silently reuse a tag that old decoders still
// associate with a retired field.
func TestCheckCompatUnreservedTagFails(t *testing.T) {
	// old: fields={a:1}, reserved=[2]
	old := tagmap.New()
	_, err := old.Assign("M", "a")
	require.NoError(t, err) // tag 1
	_, err = old.Assign("M", "b")
	require.NoError(t, err) // tag 2
	require.NoError(t, old.Retire("M", "b"))
	// Now old: fields={a:1}, reserved=[2]

	// new: same fields, but reserved is empty — tag 2 reservation dropped.
	newTM := tagmap.New()
	_, err = newTM.Assign("M", "a")
	require.NoError(t, err) // tag 1

	err = tagmap.CheckCompat(old, newTM)
	require.Error(t, err, "dropping a reservation must be an incompatibility")
	// Error must name the message and the tag.
	require.Contains(t, err.Error(), "M")
	require.Contains(t, err.Error(), "2")
}

// TestLoadSaveRoundTripByteIdentical verifies that Load → Save → Load → Save
// produces byte-identical output on both saves (no format drift).
func TestLoadSaveRoundTripByteIdentical(t *testing.T) {
	// Build a map with fields and retired tags.
	tm := tagmap.New()
	_, err := tm.Assign("Evt", "time")
	require.NoError(t, err) // tag 1
	_, err = tm.Assign("Evt", "action")
	require.NoError(t, err) // tag 2
	_, err = tm.Assign("Evt", "gone")
	require.NoError(t, err) // tag 3
	require.NoError(t, tm.Retire("Evt", "gone"))
	_, err = tm.Assign("User", "name")
	require.NoError(t, err)

	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.json")
	pathB := filepath.Join(dir, "b.json")

	// First save.
	require.NoError(t, tm.Save(pathA))

	// Load it back, save to B.
	loaded, err := tagmap.Load(pathA)
	require.NoError(t, err)
	require.NoError(t, loaded.Save(pathB))

	bytesA, err := os.ReadFile(pathA)
	require.NoError(t, err)
	bytesB, err := os.ReadFile(pathB)
	require.NoError(t, err)

	require.True(t, bytes.Equal(bytesA, bytesB), "Load→Save must be byte-identical to original Save")
}
