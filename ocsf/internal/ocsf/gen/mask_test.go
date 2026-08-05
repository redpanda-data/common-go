// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package gen_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/gen"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/schema"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/tagmap"
)

// maskYAML renders a mask file body from the given paths. Paths are quoted: a
// bare "*" is a YAML alias, so an unquoted wildcard-only entry would fail to
// parse as YAML before the mask validator ever saw it.
func maskYAML(paths ...string) []byte {
	var sb strings.Builder
	sb.WriteString("version: 1\npaths:\n")
	for _, p := range paths {
		sb.WriteString("  - \"" + p + "\"\n")
	}
	return []byte(sb.String())
}

// mustMask parses a mask from paths, failing the test on error.
func mustMask(t *testing.T, paths ...string) *gen.Mask {
	t.Helper()
	m, err := gen.ParseMask(maskYAML(paths...))
	require.NoError(t, err)
	return m
}

// maskSchema builds a schema with the given classes and objects. Unlike
// pruneSchema it takes the whole class map, so multi-class masking (which is
// what the merged event message exercises) can be tested.
func maskSchema(classes map[string]*schema.Class, objects map[string]*schema.Object) *schema.Schema {
	return &schema.Schema{
		Version:              "1.8.0",
		Classes:              classes,
		Objects:              objects,
		Types:                map[string]*schema.TypeDef{"string_t": {}, "integer_t": {}},
		DictionaryAttributes: map[string]*schema.DictAttr{},
	}
}

// keptNames flattens a result's keep set into "Message.field" strings.
func keptNames(res *gen.MaskResult) []string {
	out := make([]string, 0, len(res.Kept))
	for _, k := range res.Kept {
		name := k.Message + "." + k.Field
		if k.Subtree {
			name += " [subtree]"
		}
		out = append(out, name)
	}
	return out
}

// ---------------------------------------------------------------------------
// ParseMask
// ---------------------------------------------------------------------------

// TestParseMask_SortsAndDedupes verifies the parsed mask is normalised, so two
// mask files listing the same paths in a different order generate identically.
func TestParseMask_SortsAndDedupes(t *testing.T) {
	m, err := gen.ParseMask(maskYAML("time", "actor.user.name", "time"))
	require.NoError(t, err)
	require.Equal(t, []string{"actor.user.name", "time"}, m.Paths)
}

func TestParseMask_RejectsUnsupportedVersion(t *testing.T) {
	_, err := gen.ParseMask([]byte("version: 2\npaths:\n  - time\n"))
	require.ErrorContains(t, err, "unsupported version 2")
}

func TestParseMask_RejectsEmptyFile(t *testing.T) {
	_, err := gen.ParseMask(nil)
	require.ErrorContains(t, err, "file is empty")
}

func TestParseMask_RejectsEmptyPaths(t *testing.T) {
	_, err := gen.ParseMask([]byte("version: 1\npaths: []\n"))
	require.ErrorContains(t, err, "non-empty list")
}

// TestParseMask_RejectsUnknownKey verifies a typo in the mask file fails
// generation instead of being ignored — an ignored key would silently widen or
// narrow the published contract.
func TestParseMask_RejectsUnknownKey(t *testing.T) {
	_, err := gen.ParseMask([]byte("version: 1\npath:\n  - time\n"))
	require.ErrorContains(t, err, "field path not found")
}

func TestParseMask_RejectsWildcardMidPath(t *testing.T) {
	_, err := gen.ParseMask(maskYAML("actor.*.name"))
	require.ErrorContains(t, err, `only use "*" as its final segment`)
}

func TestParseMask_RejectsBareWildcard(t *testing.T) {
	_, err := gen.ParseMask(maskYAML("*"))
	require.ErrorContains(t, err, "must name a class attribute")
}

func TestParseMask_RejectsEmptySegment(t *testing.T) {
	_, err := gen.ParseMask(maskYAML("actor..name"))
	require.ErrorContains(t, err, "empty segment")
}

// TestParseMask_RejectsSubsumedPath verifies a path already covered by a "*"
// subtree is an error: it cannot affect the output, and leaving it in would make
// the widening report claim a column was asked for by two different entries.
func TestParseMask_RejectsSubsumedPath(t *testing.T) {
	_, err := gen.ParseMask(maskYAML("actor.*", "actor.user.name"))
	require.ErrorContains(t, err, `is already covered by "actor.*"`)
}

// ---------------------------------------------------------------------------
// MaskFields
// ---------------------------------------------------------------------------

// TestMaskFields_KeepsOnlyNamedPaths is the core case: named attributes survive
// at every level, everything else goes, and the intermediate edge is retained
// automatically (the mask is closed upward — callers list leaves, not prefixes).
func TestMaskFields_KeepsOnlyNamedPaths(t *testing.T) {
	s := maskSchema(
		map[string]*schema.Class{
			"root": {Name: "root", UID: 1001, Attributes: map[string]*schema.Attribute{
				"time":  strAttr("time"),
				"noise": strAttr("noise"),
				"thing": objAttr("thing", "thing"),
			}},
		},
		map[string]*schema.Object{
			"thing": {Name: "thing", Attributes: map[string]*schema.Attribute{
				"uid":  strAttr("uid"),
				"junk": strAttr("junk"),
			}},
		},
	)

	res, err := gen.MaskFields(s, []string{"root"}, mustMask(t, "time", "thing.uid"))
	require.NoError(t, err)

	require.Equal(t, []string{"time", "thing"}, sortedAttrNames(s.Classes["root"].Attributes, "time", "thing"))
	require.NotContains(t, s.Classes["root"].Attributes, "noise")
	require.Contains(t, s.Objects["thing"].Attributes, "uid")
	require.NotContains(t, s.Objects["thing"].Attributes, "junk")

	require.Equal(t, []string{"Root.thing", "Root.time", "Thing.uid"}, keptNames(res))
	require.Empty(t, res.Widened)
	require.Equal(t, 2, res.Stats.LeafPathsAfter) // time, thing.uid
	require.Equal(t, 4, res.Stats.LeafPathsBefore)
}

// TestMaskFields_SubtreeWildcard verifies "*" keeps a message-typed field's whole
// target type, including types reachable only through it.
func TestMaskFields_SubtreeWildcard(t *testing.T) {
	s := maskSchema(
		map[string]*schema.Class{
			"root": {Name: "root", UID: 1001, Attributes: map[string]*schema.Attribute{
				"thing": objAttr("thing", "thing"),
				"noise": strAttr("noise"),
			}},
		},
		map[string]*schema.Object{
			"thing": {Name: "thing", Attributes: map[string]*schema.Attribute{
				"uid":   strAttr("uid"),
				"junk":  strAttr("junk"),
				"inner": objAttr("inner", "inner"),
			}},
			"inner": {Name: "inner", Attributes: map[string]*schema.Attribute{
				"deep": strAttr("deep"),
			}},
		},
	)

	res, err := gen.MaskFields(s, []string{"root"}, mustMask(t, "thing.*"))
	require.NoError(t, err)

	require.Len(t, s.Objects["thing"].Attributes, 3, "subtree keeps every field")
	require.Contains(t, s.Objects["inner"].Attributes, "deep", "subtree is transitive")
	require.NotContains(t, s.Classes["root"].Attributes, "noise")

	require.Equal(t, []string{
		"Inner.deep", "Root.thing [subtree]", "Thing.inner", "Thing.junk", "Thing.uid",
	}, keptNames(res))
	require.Empty(t, res.Widened, `"*" asks for everything beneath it`)
}

// TestMaskFields_ScrubsConstraints verifies at_least_one/just_one lose their
// references to dropped attributes, so the emitted CEL never names a field that
// no longer exists.
func TestMaskFields_ScrubsConstraints(t *testing.T) {
	s := maskSchema(
		map[string]*schema.Class{
			"root": {
				Name: "root", UID: 1001,
				Attributes: map[string]*schema.Attribute{
					"a": strAttr("a"),
					"b": strAttr("b"),
					"c": strAttr("c"),
				},
				Constraints: &schema.Constraints{AtLeastOne: []string{"a", "b"}, JustOne: []string{"b", "c"}},
			},
		},
		nil,
	)

	_, err := gen.MaskFields(s, []string{"root"}, mustMask(t, "a"))
	require.NoError(t, err)
	require.Equal(t, &schema.Constraints{AtLeastOne: []string{"a"}}, s.Classes["root"].Constraints,
		"just_one emptied entirely, so it is absent rather than present-but-empty")
}

// TestMaskFields_DropsUnreachableObjectsFromEmission verifies masking an edge
// away removes its whole target type from the emitted objects.proto — the size
// win is structural, not just fewer fields on retained messages.
func TestMaskFields_DropsUnreachableObjectsFromEmission(t *testing.T) {
	s := maskSchema(
		map[string]*schema.Class{
			"root": {Name: "root", UID: 1001, Attributes: map[string]*schema.Attribute{
				"keep": objAttr("keep", "keeper"),
				"drop": objAttr("drop", "dropper"),
			}},
		},
		map[string]*schema.Object{
			"keeper":  {Name: "keeper", Attributes: map[string]*schema.Attribute{"uid": strAttr("uid")}},
			"dropper": {Name: "dropper", Attributes: map[string]*schema.Attribute{"uid": strAttr("uid")}},
		},
	)

	res, err := gen.MaskFields(s, []string{"root"}, mustMask(t, "keep.uid"))
	require.NoError(t, err)
	require.Equal(t, 2, res.Stats.MessagesAfter, "root + keeper")
	require.Equal(t, 3, res.Stats.MessagesBefore)

	files, _, err := gen.Emit(s, []string{"root"}, tagmap.New(), "1.8.0")
	require.NoError(t, err)
	var objects string
	for _, f := range files {
		if strings.HasSuffix(f.Path, "objects.proto") {
			objects = f.Content
		}
	}
	require.Contains(t, objects, "message Keeper {")
	require.NotContains(t, objects, "message Dropper")
}

// TestMaskFields_WideningReportsSharedType is the honest-semantics case. The
// mask is applied per message TYPE, so asking for actor.user.name and
// target.user.email keeps BOTH fields on the shared User type — which means
// actor.user.email and target.user.name exist too, though neither was asked for.
// Those extra columns are part of the published contract and must be reported.
func TestMaskFields_WideningReportsSharedType(t *testing.T) {
	s := maskSchema(
		map[string]*schema.Class{
			"root": {Name: "root", UID: 1001, Attributes: map[string]*schema.Attribute{
				"actor":  objAttr("actor", "holder"),
				"target": objAttr("target", "holder"),
			}},
		},
		map[string]*schema.Object{
			"holder": {Name: "holder", Attributes: map[string]*schema.Attribute{
				"user": objAttr("user", "user"),
			}},
			"user": {Name: "user", Attributes: map[string]*schema.Attribute{
				"name":  strAttr("name"),
				"email": strAttr("email"),
				"phone": strAttr("phone"),
			}},
		},
	)

	res, err := gen.MaskFields(s, []string{"root"}, mustMask(t, "actor.user.name", "target.user.email"))
	require.NoError(t, err)
	require.Equal(t, []string{"actor.user.email", "target.user.name"}, res.Widened,
		"the shared User type carries both fields to both embeddings")
	require.NotContains(t, s.Objects["user"].Attributes, "phone",
		"the mask still narrows User — widening is not the same as giving up")
}

// TestMaskFields_NoWideningWhenSharingCollapses verifies the reassuring half of
// the type-scoped trade-off: closing one embedding removes the sharing, so the
// same field set reports no widening at all.
func TestMaskFields_NoWideningWhenSharingCollapses(t *testing.T) {
	s := maskSchema(
		map[string]*schema.Class{
			"root": {Name: "root", UID: 1001, Attributes: map[string]*schema.Attribute{
				"actor":  objAttr("actor", "holder"),
				"target": objAttr("target", "holder"),
			}},
		},
		map[string]*schema.Object{
			"holder": {Name: "holder", Attributes: map[string]*schema.Attribute{
				"user": objAttr("user", "user"),
			}},
			"user": {Name: "user", Attributes: map[string]*schema.Attribute{
				"name":  strAttr("name"),
				"email": strAttr("email"),
			}},
		},
	)

	res, err := gen.MaskFields(s, []string{"root"}, mustMask(t, "actor.user.name"))
	require.NoError(t, err)
	require.Empty(t, res.Widened, "target was dropped, so User is reachable by one path only")
	require.NotContains(t, s.Classes["root"].Attributes, "target")
}

// TestMaskFields_PreservesTagNumbers is the wire-stability guarantee: masking
// does not renumber surviving fields, and the excluded attributes keep their
// tagmap entries, so --compat-check stays clean and un-masking a field later
// restores its original field number.
func TestMaskFields_PreservesTagNumbers(t *testing.T) {
	newSchema := func() *schema.Schema {
		return maskSchema(
			map[string]*schema.Class{
				"root": {Name: "root", UID: 1001, Attributes: map[string]*schema.Attribute{
					"aaa": strAttr("aaa"),
					"bbb": strAttr("bbb"),
					"ccc": strAttr("ccc"),
				}},
			},
			nil,
		)
	}

	// Baseline: emit the full schema so every attribute gets a tag.
	full := tagmap.New()
	_, _, err := gen.Emit(newSchema(), []string{"root"}, full, "1.8.0")
	require.NoError(t, err)
	bbbTag, ok := full.Tag("Root", "bbb")
	require.True(t, ok)

	// Now mask "bbb" away and emit through the SAME tagmap.
	masked := newSchema()
	_, err = gen.MaskFields(masked, []string{"root"}, mustMask(t, "aaa", "ccc"))
	require.NoError(t, err)
	_, _, err = gen.Emit(masked, []string{"root"}, full, "1.8.0")
	require.NoError(t, err)

	for _, attr := range []string{"aaa", "ccc"} {
		before, _ := full.Tag("Root", attr)
		require.NotZero(t, before, "surviving field %q keeps a tag", attr)
	}
	stillThere, ok := full.Tag("Root", "bbb")
	require.True(t, ok, "a masked-away attribute keeps its tagmap entry")
	require.Equal(t, bbbTag, stillThere, "and keeps its original number, so un-masking is free")
	require.NoError(t, tagmap.CheckCompat(full, full))
}

// TestMaskFields_RunsBeforePruneKeepsStructure verifies the ordering pays off:
// the deep subtree that would force an R3 demotion is gone, so the surviving
// fields stay typed instead of collapsing to a JSON string.
func TestMaskFields_RunsBeforePruneKeepsStructure(t *testing.T) {
	// A leaf name chosen so the path fits under "shallow." (7+1+55 = 63, the
	// limit) but overflows under "deep.nested." (4+1+6+1+55 = 67). Only the deep
	// embedding is unrepresentable.
	deepName := strings.Repeat("z", 55)
	newSchema := func() *schema.Schema {
		return maskSchema(
			map[string]*schema.Class{
				"root": {Name: "root", UID: 1001, Attributes: map[string]*schema.Attribute{
					"shallow": objAttr("shallow", "shared"),
					"deep":    objAttr("deep", "wrapper"),
				}},
			},
			map[string]*schema.Object{
				"wrapper": {Name: "wrapper", Attributes: map[string]*schema.Attribute{
					"nested": objAttr("nested", "shared"),
				}},
				"shared": {Name: "shared", Attributes: map[string]*schema.Attribute{
					deepName: strAttr(deepName),
				}},
			},
		)
	}

	// Without a mask the long path forces a prune.
	unmasked, err := gen.PruneForIceberg(newSchema(), []string{"root"})
	require.NoError(t, err)
	require.NotEmpty(t, unmasked, "the deep embedding overflows the identifier limit")

	// Masking the deep branch away first leaves nothing for the pruner to do.
	s := newSchema()
	_, err = gen.MaskFields(s, []string{"root"}, mustMask(t, "shallow."+deepName))
	require.NoError(t, err)
	prunes, err := gen.PruneForIceberg(s, []string{"root"})
	require.NoError(t, err)
	require.Empty(t, prunes, "the mask removed the reason for the R3 demotion")
	require.Contains(t, s.Objects["shared"].Attributes, deepName)
	require.Equal(t, "string_t", s.Objects["shared"].Attributes[deepName].Type,
		"and the surviving field was never demoted")
}

// ---------------------------------------------------------------------------
// MaskFields errors
// ---------------------------------------------------------------------------

// TestMaskFields_ErrUnresolvedPath is the guard that makes an allowlist safe
// across OCSF bumps: a renamed attribute stops matching and fails generation
// rather than silently dropping a column consumers query.
func TestMaskFields_ErrUnresolvedPath(t *testing.T) {
	s := maskSchema(
		map[string]*schema.Class{
			"root": {Name: "root", UID: 1001, Attributes: map[string]*schema.Attribute{"time": strAttr("time")}},
		},
		nil,
	)
	_, err := gen.MaskFields(s, []string{"root"}, mustMask(t, "time_of_day"))
	require.ErrorContains(t, err, `no selected class has attribute "time_of_day"`)
}

func TestMaskFields_ErrUnknownNestedAttribute(t *testing.T) {
	s := maskSchema(
		map[string]*schema.Class{
			"root": {Name: "root", UID: 1001, Attributes: map[string]*schema.Attribute{
				"thing": objAttr("thing", "thing"),
			}},
		},
		map[string]*schema.Object{
			"thing": {Name: "thing", Attributes: map[string]*schema.Attribute{"uid": strAttr("uid")}},
		},
	)
	_, err := gen.MaskFields(s, []string{"root"}, mustMask(t, "thing.nope"))
	require.ErrorContains(t, err, `message Thing (at "thing") has no attribute "nope"`)
}

// TestMaskFields_ErrEndsOnMessageField verifies a path stopping on a
// message-typed field is rejected with the fix in the message, rather than
// silently emitting an empty message.
func TestMaskFields_ErrEndsOnMessageField(t *testing.T) {
	s := maskSchema(
		map[string]*schema.Class{
			"root": {Name: "root", UID: 1001, Attributes: map[string]*schema.Attribute{
				"thing": objAttr("thing", "thing"),
			}},
		},
		map[string]*schema.Object{
			"thing": {Name: "thing", Attributes: map[string]*schema.Attribute{"uid": strAttr("uid")}},
		},
	)
	_, err := gen.MaskFields(s, []string{"root"}, mustMask(t, "thing"))
	require.ErrorContains(t, err, `write "thing.*" to keep the whole subtree`)
}

func TestMaskFields_ErrDescendPastScalar(t *testing.T) {
	s := maskSchema(
		map[string]*schema.Class{
			"root": {Name: "root", UID: 1001, Attributes: map[string]*schema.Attribute{"time": strAttr("time")}},
		},
		nil,
	)
	_, err := gen.MaskFields(s, []string{"root"}, mustMask(t, "time.nested"))
	require.ErrorContains(t, err, `cannot descend past "time"`)
}

func TestMaskFields_ErrWildcardOnScalar(t *testing.T) {
	s := maskSchema(
		map[string]*schema.Class{
			"root": {Name: "root", UID: 1001, Attributes: map[string]*schema.Attribute{"time": strAttr("time")}},
		},
		nil,
	)
	_, err := gen.MaskFields(s, []string{"root"}, mustMask(t, "time.*"))
	require.ErrorContains(t, err, `is not a message-typed field`)
}

// TestMaskFields_ErrClassEmptied verifies a mask naming nothing on one of the
// selected classes fails rather than emitting a fieldless message.
func TestMaskFields_ErrClassEmptied(t *testing.T) {
	s := maskSchema(
		map[string]*schema.Class{
			"first":  {Name: "first", UID: 1001, Attributes: map[string]*schema.Attribute{"a": strAttr("a")}},
			"second": {Name: "second", UID: 1002, Attributes: map[string]*schema.Attribute{"b": strAttr("b")}},
		},
		nil,
	)
	_, err := gen.MaskFields(s, []string{"first", "second"}, mustMask(t, "a"))
	require.ErrorContains(t, err, `class "second" has no fields left`)
}

// TestMaskFields_AppliesToEverySelectedClass verifies a path naming a shared
// (base_event) attribute is kept on every selected class, which is what the
// merged event message's attribute union depends on.
func TestMaskFields_AppliesToEverySelectedClass(t *testing.T) {
	s := maskSchema(
		map[string]*schema.Class{
			"first": {Name: "first", UID: 1001, Attributes: map[string]*schema.Attribute{
				"time": strAttr("time"), "noise": strAttr("noise"),
			}},
			"second": {Name: "second", UID: 1002, Attributes: map[string]*schema.Attribute{
				"time": strAttr("time"), "other": strAttr("other"),
			}},
		},
		nil,
	)
	_, err := gen.MaskFields(s, []string{"first", "second"}, mustMask(t, "time"))
	require.NoError(t, err)
	require.Equal(t, []string{"time"}, sortedAttrNames(s.Classes["first"].Attributes, "time"))
	require.Equal(t, []string{"time"}, sortedAttrNames(s.Classes["second"].Attributes, "time"))
}

// ---------------------------------------------------------------------------
// Merged discriminators and the report
// ---------------------------------------------------------------------------

// TestVerifyMergedDiscriminators verifies the guard on the merged emission's
// hard dependency: its CEL gates on this.class_uid unconditionally, so a mask
// that drops the discriminators would emit rules referencing missing fields.
func TestVerifyMergedDiscriminators(t *testing.T) {
	withAttrs := func(names ...string) *schema.Schema {
		attrs := make(map[string]*schema.Attribute, len(names))
		for _, n := range names {
			attrs[n] = strAttr(n)
		}
		return maskSchema(
			map[string]*schema.Class{"root": {Name: "root", UID: 1001, Attributes: attrs}},
			nil,
		)
	}

	require.NoError(t, gen.VerifyMergedDiscriminators(
		withAttrs("category_uid", "class_uid", "type_uid", "time"), []string{"root"}))

	err := gen.VerifyMergedDiscriminators(withAttrs("category_uid", "type_uid"), []string{"root"})
	require.ErrorContains(t, err, `class "root" lost "class_uid"`)
	require.ErrorContains(t, err, "add \"class_uid\" to the mask")
}

// TestMaskReportFile verifies the sidecar is deterministic and carries the
// numbers worth watching across OCSF bumps.
func TestMaskReportFile(t *testing.T) {
	res := &gen.MaskResult{
		Kept: []gen.KeptField{
			{Message: "Root", Field: "thing", Subtree: true},
			{Message: "Root", Field: "time"},
		},
		Widened: []string{"target.user.name"},
		Stats:   gen.MaskStats{LeafPathsBefore: 3993, LeafPathsAfter: 51, MessagesBefore: 101, MessagesAfter: 15},
	}

	f, err := gen.MaskReportFile("1.8.0", res)
	require.NoError(t, err)
	require.Equal(t, "ocsf/v1/read-mask-report.txt", f.Path)
	require.Contains(t, f.Content, "leaf columns:  3993 -> 51")
	require.Contains(t, f.Content, "message types: 101 -> 15")
	require.Contains(t, f.Content, "Root.thing [subtree]\n")
	require.Contains(t, f.Content, "Root.time\n")
	require.Contains(t, f.Content, "target.user.name\n")

	again, err := gen.MaskReportFile("1.8.0", res)
	require.NoError(t, err)
	require.Equal(t, f.Content, again.Content)
}

func TestMaskReportFile_NoWidening(t *testing.T) {
	f, err := gen.MaskReportFile("1.8.0", &gen.MaskResult{})
	require.NoError(t, err)
	require.Contains(t, f.Content, "# (none)")
}

// sortedAttrNames returns the wanted names that are present in attrs, in the
// order given — a readable way to assert an exact surviving attribute set.
func sortedAttrNames(attrs map[string]*schema.Attribute, want ...string) []string {
	out := make([]string, 0, len(want))
	for _, name := range want {
		if _, ok := attrs[name]; ok {
			out = append(out, name)
		}
	}
	return out
}

// TestParseMask_RejectsMultipleDocuments verifies the parser consumes the whole
// file. yaml.Decoder.Decode reads ONE document, so without an explicit EOF check
// a second "---" document is silently ignored — which would let a typo'd key slip
// past KnownFields and defeat the mask's fail-closed contract.
func TestParseMask_RejectsMultipleDocuments(t *testing.T) {
	_, err := gen.ParseMask([]byte("version: 1\npaths:\n  - time\n---\npaths_typo:\n  - nonsense\n"))
	require.ErrorContains(t, err, "more than one YAML document")
}

// TestMaskFields_ErrAbsentObjectTarget covers the partial-export case: an
// object_t attribute whose target the snapshot does not define emits as an empty
// stub message, and messageEdge classifies it as a non-edge. Mask resolution must
// not mistake that for a scalar terminal, or the mask silently produces
// `message Phantom {}`.
func TestMaskFields_ErrAbsentObjectTarget(t *testing.T) {
	newSchema := func() *schema.Schema {
		return maskSchema(
			map[string]*schema.Class{
				"root": {Name: "root", UID: 1001, Attributes: map[string]*schema.Attribute{
					"time":    strAttr("time"),
					"phantom": objAttr("phantom", "phantom"), // target absent
				}},
			},
			map[string]*schema.Object{},
		)
	}

	_, err := gen.MaskFields(newSchema(), []string{"root"}, mustMask(t, "time", "phantom"))
	require.ErrorContains(t, err, "absent from the schema")
	require.ErrorContains(t, err, "Phantom")

	// The wildcard form is equally invalid — there is no subtree to keep.
	_, err = gen.MaskFields(newSchema(), []string{"root"}, mustMask(t, "time", "phantom.*"))
	require.ErrorContains(t, err, "absent from the schema")

	// And descending through one names the missing type rather than reporting a
	// missing attribute on it.
	_, err = gen.MaskFields(newSchema(), []string{"root"}, mustMask(t, "time", "phantom.uid"))
	require.ErrorContains(t, err, "absent from the schema")
}

// TestMaskFields_GenericObjectStillTerminates guards the other side of that
// check: the generic "object" bag legitimately maps to a scalar (R1 demotes it to
// a JSON string), so a path may end on it.
func TestMaskFields_GenericObjectStillTerminates(t *testing.T) {
	s := maskSchema(
		map[string]*schema.Class{
			"root": {Name: "root", UID: 1001, Attributes: map[string]*schema.Attribute{
				"time":     strAttr("time"),
				"unmapped": objAttr("unmapped", "object"), // generic bag
			}},
		},
		map[string]*schema.Object{},
	)
	_, err := gen.MaskFields(s, []string{"root"}, mustMask(t, "time", "unmapped"))
	require.NoError(t, err)
	require.Contains(t, s.Classes["root"].Attributes, "unmapped")
}
