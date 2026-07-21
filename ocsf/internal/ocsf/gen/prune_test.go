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

// pruneSchema builds a minimal one-class schema for pruner unit tests. The
// class is named "root" and carries the given attributes; objects is the
// object map referenced by object_t attributes.
func pruneSchema(attrs map[string]*schema.Attribute, objects map[string]*schema.Object) *schema.Schema {
	return &schema.Schema{
		Version: "1.8.0",
		Classes: map[string]*schema.Class{
			"root": {Name: "root", UID: 1001, CategoryUID: 1, Attributes: attrs},
		},
		Objects: objects,
		Types: map[string]*schema.TypeDef{
			"string_t":  {},
			"integer_t": {},
		},
		DictionaryAttributes: map[string]*schema.DictAttr{},
	}
}

// strAttr returns a plain string attribute.
func strAttr(name string) *schema.Attribute {
	return &schema.Attribute{Name: name, Type: "string_t"}
}

// objAttr returns an object_t attribute referencing the named object.
func objAttr(name, objectType string) *schema.Attribute {
	return &schema.Attribute{Name: name, Type: "object_t", ObjectType: objectType}
}

// jsonAttr returns a json_t attribute (maps to google.protobuf.Value).
func jsonAttr(name string) *schema.Attribute {
	return &schema.Attribute{Name: name, Type: "json_t"}
}

// TestPruneForIceberg_WellKnownValue verifies R1: attributes mapping to
// google.protobuf.Value (json_t, and object_t to the generic "object" bag)
// are dropped from classes and reachable objects, and the emitted proto no
// longer references struct.proto.
func TestPruneForIceberg_WellKnownValue(t *testing.T) {
	s := pruneSchema(
		map[string]*schema.Attribute{
			"name":     strAttr("name"),
			"unmapped": objAttr("unmapped", "object"), // generic bag → Value
			"payload":  jsonAttr("payload"),           // json_t → Value
			"thing":    objAttr("thing", "thing"),
		},
		map[string]*schema.Object{
			"thing": {Name: "thing", Attributes: map[string]*schema.Attribute{
				"uid":  strAttr("uid"),
				"data": jsonAttr("data"),
			}},
		},
	)

	prunes, err := gen.PruneForIceberg(s, []string{"root"})
	require.NoError(t, err)
	require.Equal(t, []gen.PrunedField{
		{Message: "Root", Field: "payload", Rule: gen.PruneRuleWellKnown},
		{Message: "Root", Field: "unmapped", Rule: gen.PruneRuleWellKnown},
		{Message: "Thing", Field: "data", Rule: gen.PruneRuleWellKnown},
	}, prunes)

	require.NotContains(t, s.Classes["root"].Attributes, "payload")
	require.NotContains(t, s.Classes["root"].Attributes, "unmapped")
	require.NotContains(t, s.Objects["thing"].Attributes, "data")

	out, _, err := emitJoined(t, s, []string{"root"}, tagmap.New(), "1.8.0")
	require.NoError(t, err)
	require.NotContains(t, out, "google/protobuf/struct.proto")
	require.NotContains(t, out, "google.protobuf.Value")
	require.Contains(t, out, "string uid")
}

// TestPruneForIceberg_CycleLenTwo verifies R2 on a cycle of length two:
// alpha → beta → alpha. The DFS visits alpha first (sorted attribute order at
// the root), so the beta→alpha edge is the back edge that gets cut.
func TestPruneForIceberg_CycleLenTwo(t *testing.T) {
	s := pruneSchema(
		map[string]*schema.Attribute{
			"a": objAttr("a", "alpha"),
		},
		map[string]*schema.Object{
			"alpha": {Name: "alpha", Attributes: map[string]*schema.Attribute{
				"to_beta": objAttr("to_beta", "beta"),
				"tag":     strAttr("tag"),
			}},
			"beta": {Name: "beta", Attributes: map[string]*schema.Attribute{
				"to_alpha": objAttr("to_alpha", "alpha"),
				"tag":      strAttr("tag"),
			}},
		},
	)

	prunes, err := gen.PruneForIceberg(s, []string{"root"})
	require.NoError(t, err)
	require.Equal(t, []gen.PrunedField{
		{Message: "Beta", Field: "to_alpha", Rule: gen.PruneRuleRecursion},
	}, prunes)

	// The forward edge survives; only the back edge is gone.
	require.Contains(t, s.Objects["alpha"].Attributes, "to_beta")
	require.NotContains(t, s.Objects["beta"].Attributes, "to_alpha")
}

// TestPruneForIceberg_SelfEdge verifies R2 cuts self-references.
func TestPruneForIceberg_SelfEdge(t *testing.T) {
	s := pruneSchema(
		map[string]*schema.Attribute{
			"p": objAttr("p", "proc"),
		},
		map[string]*schema.Object{
			"proc": {Name: "proc", Attributes: map[string]*schema.Attribute{
				"parent_proc": objAttr("parent_proc", "proc"),
				"pid":         strAttr("pid"),
			}},
		},
	)

	prunes, err := gen.PruneForIceberg(s, []string{"root"})
	require.NoError(t, err)
	require.Equal(t, []gen.PrunedField{
		{Message: "Proc", Field: "parent_proc", Rule: gen.PruneRuleRecursion},
	}, prunes)
	require.Contains(t, s.Objects["proc"].Attributes, "pid")
}

// TestPruneForIceberg_PathLength verifies R3 boundary behavior on edges:
// an edge whose deepest forced leaf path is exactly 63 chars is KEPT; one
// char more and the EDGE is cut (never the scalar inside the target).
func TestPruneForIceberg_PathLength(t *testing.T) {
	// Leaf path root.seed.<58 chars> = 4+1+58 = exactly 63: kept.
	// Leaf path root.abcd.<59 chars> = 4+1+59 = 64: the edge "abcd" is cut,
	// because the 59-char scalar can never be pruned.
	keep := strings.Repeat("k", 58)
	drop := strings.Repeat("d", 59)

	s := pruneSchema(
		map[string]*schema.Attribute{
			"seed": objAttr("seed", "mid"),
			"abcd": objAttr("abcd", "mid2"),
		},
		map[string]*schema.Object{
			"mid": {Name: "mid", Attributes: map[string]*schema.Attribute{
				keep: strAttr(keep),
			}},
			"mid2": {Name: "mid2", Attributes: map[string]*schema.Attribute{
				drop: strAttr(drop),
			}},
		},
	)

	prunes, err := gen.PruneForIceberg(s, []string{"root"})
	require.NoError(t, err)
	require.Equal(t, []gen.PrunedField{
		{Message: "Root", Field: "abcd", Rule: gen.PruneRulePathLength},
	}, prunes)
	require.Contains(t, s.Objects["mid"].Attributes, keep, "a leaf path of exactly 63 chars must be kept")
	require.Contains(t, s.Objects["mid2"].Attributes, drop, "scalars must never be pruned; the edge takes the cut")

	out, _, err := emitJoined(t, s, []string{"root"}, tagmap.New(), "1.8.0")
	require.NoError(t, err)
	require.NotContains(t, out, "message Mid2", "object without surviving embeddings must not be emitted")
}

// TestPruneForIceberg_PathLengthDeepChain verifies R3 on a chain of objects
// whose accumulated dotted path exceeds the limit only at depth: the deepest
// edge is cut, everything above survives intact.
func TestPruneForIceberg_PathLengthDeepChain(t *testing.T) {
	// Chain prefixes: level_one_container = 19, +1+19 = 39 (two),
	// +1+21 = 61 (three). three's longest scalar is "xy" (2), so the edge
	// into three forces a 61+1+2 = 64-char leaf path and is cut. A sibling
	// chain ending in a single 1-char scalar lands at exactly 63 and stays.
	s := pruneSchema(
		map[string]*schema.Attribute{
			"level_one_container": objAttr("level_one_container", "one"), // 19
		},
		map[string]*schema.Object{
			"one": {Name: "one", Attributes: map[string]*schema.Attribute{
				"level_two_container": objAttr("level_two_container", "two"), // 39
			}},
			"two": {Name: "two", Attributes: map[string]*schema.Attribute{
				"ok":                    strAttr("ok"),                             // leaf 42: kept
				"level_three_container": objAttr("level_three_container", "three"), // 61
				"level_three_containe":  objAttr("level_three_containe", "slim"),   // 60
			}},
			"three": {Name: "three", Attributes: map[string]*schema.Attribute{
				"x":  strAttr("x"),
				"xy": strAttr("xy"), // forces 61+1+2 = 64: edge cut
			}},
			"slim": {Name: "slim", Attributes: map[string]*schema.Attribute{
				"x": strAttr("x"), // leaf 60+1+1 = 62: kept
			}},
		},
	)

	prunes, err := gen.PruneForIceberg(s, []string{"root"})
	require.NoError(t, err)
	require.Equal(t, []gen.PrunedField{
		{Message: "Two", Field: "level_three_container", Rule: gen.PruneRulePathLength},
	}, prunes)
	require.Contains(t, s.Objects["two"].Attributes, "ok", "scalars above the cut survive")
	require.Contains(t, s.Objects["two"].Attributes, "level_three_containe", "sibling chain within budget survives")
	require.Contains(t, s.Objects["slim"].Attributes, "x")
}

// TestPruneForIceberg_SubtreeDrop verifies that a message-typed field dropped
// by R3 drops its whole subtree: the target object is not evaluated field by
// field and, having no surviving embedding, is not emitted at all.
func TestPruneForIceberg_SubtreeDrop(t *testing.T) {
	longEdge := strings.Repeat("e", 64) // 64 > 63 even at the root

	s := pruneSchema(
		map[string]*schema.Attribute{
			longEdge: objAttr(longEdge, "orphan"),
			"kept":   strAttr("kept"),
		},
		map[string]*schema.Object{
			"orphan": {Name: "orphan", Attributes: map[string]*schema.Attribute{
				"also_way_too_long_to_survive_but_never_even_evaluated_by_rule_three": strAttr("x"),
			}},
		},
	)

	prunes, err := gen.PruneForIceberg(s, []string{"root"})
	require.NoError(t, err)
	// Only the edge is recorded; the orphan's own fields are not.
	require.Equal(t, []gen.PrunedField{
		{Message: "Root", Field: longEdge, Rule: gen.PruneRulePathLength},
	}, prunes)

	out, _, err := emitJoined(t, s, []string{"root"}, tagmap.New(), "1.8.0")
	require.NoError(t, err)
	require.NotContains(t, out, "message Orphan", "unreachable object must not be emitted")
}

// TestPruneForIceberg_ConstraintScrub verifies that at_least_one/just_one
// constraint lists no longer reference pruned fields, and that a fully
// scrubbed constraint set becomes nil.
func TestPruneForIceberg_ConstraintScrub(t *testing.T) {
	s := pruneSchema(
		map[string]*schema.Attribute{
			"name":     strAttr("name"),
			"unmapped": objAttr("unmapped", "object"),
			"payload":  jsonAttr("payload"),
		},
		map[string]*schema.Object{},
	)
	s.Classes["root"].Constraints = &schema.Constraints{
		AtLeastOne: []string{"unmapped", "name"},
		JustOne:    []string{"payload"},
	}

	_, err := gen.PruneForIceberg(s, []string{"root"})
	require.NoError(t, err)
	require.Equal(t, &schema.Constraints{AtLeastOne: []string{"name"}}, s.Classes["root"].Constraints)

	// Fully scrubbed constraints must become nil, not present-but-empty.
	s2 := pruneSchema(
		map[string]*schema.Attribute{
			"name":    strAttr("name"),
			"payload": jsonAttr("payload"),
		},
		map[string]*schema.Object{},
	)
	s2.Classes["root"].Constraints = &schema.Constraints{JustOne: []string{"payload"}}
	_, err = gen.PruneForIceberg(s2, []string{"root"})
	require.NoError(t, err)
	require.Nil(t, s2.Classes["root"].Constraints)
}

// TestPruneForIceberg_RootScalarTooLong verifies the invariant guard: a
// scalar leaf path that cannot fit has no edge to cut, and since scalars are
// never pruned, generation must fail rather than emit a broken schema.
func TestPruneForIceberg_RootScalarTooLong(t *testing.T) {
	long := strings.Repeat("s", 64)
	s := pruneSchema(
		map[string]*schema.Attribute{
			long:   strAttr(long),
			"name": strAttr("name"),
		},
		map[string]*schema.Object{},
	)
	_, err := gen.PruneForIceberg(s, []string{"root"})
	require.ErrorContains(t, err, "scalar")
	require.ErrorContains(t, err, "never pruned")
}

// TestPruneForIceberg_EmptyMessageCascade verifies R4 to a fixpoint: an
// object emptied by R1 loses its referencing edge, which empties its parent,
// which loses its own referencing edge in turn.
func TestPruneForIceberg_EmptyMessageCascade(t *testing.T) {
	s := pruneSchema(
		map[string]*schema.Attribute{
			"w":    objAttr("w", "wrap"),
			"name": strAttr("name"),
		},
		map[string]*schema.Object{
			"wrap": {Name: "wrap", Attributes: map[string]*schema.Attribute{
				"inner": objAttr("inner", "bag"),
			}},
			"bag": {Name: "bag", Attributes: map[string]*schema.Attribute{
				"data": jsonAttr("data"), // R1 empties bag
			}},
		},
	)

	prunes, err := gen.PruneForIceberg(s, []string{"root"})
	require.NoError(t, err)
	require.Equal(t, []gen.PrunedField{
		{Message: "Bag", Field: "data", Rule: gen.PruneRuleWellKnown},
		{Message: "Root", Field: "w", Rule: gen.PruneRuleEmptyMessage},
		{Message: "Wrap", Field: "inner", Rule: gen.PruneRuleEmptyMessage},
	}, prunes)

	out, _, err := emitJoined(t, s, []string{"root"}, tagmap.New(), "1.8.0")
	require.NoError(t, err)
	require.NotContains(t, out, "message Bag", "emptied object must not be emitted")
	require.NotContains(t, out, "message Wrap", "transitively emptied object must not be emitted")
}

// TestPruneForIceberg_UnknownClass verifies a descriptive error for an
// unknown class name.
func TestPruneForIceberg_UnknownClass(t *testing.T) {
	s := pruneSchema(map[string]*schema.Attribute{"name": strAttr("name")}, map[string]*schema.Object{})
	_, err := gen.PruneForIceberg(s, []string{"no_such_class"})
	require.ErrorContains(t, err, "no_such_class")
}

// TestPruneSidecarFile verifies the sidecar path, header, and sorted line
// format.
func TestPruneSidecarFile(t *testing.T) {
	f, err := gen.PruneSidecarFile("1.8.0", []gen.PrunedField{
		{Message: "Process", Field: "parent_process", Rule: gen.PruneRuleRecursion},
		{Message: "ApiActivity", Field: "unmapped", Rule: gen.PruneRuleWellKnown},
	})
	require.NoError(t, err)
	require.Equal(t, "ocsf/v1/iceberg-compat-prunes.txt", f.Path)
	require.Equal(t,
		"# Code generated by ocsf-protogen --iceberg-compat. DO NOT EDIT.\n"+
			"# Source: OCSF schema 1.8.0\n"+
			"# Fields pruned from the emitted protos: <Message>.<field> <rule>\n"+
			"ApiActivity.unmapped R1-well-known-type\n"+
			"Process.parent_process R2-recursion\n",
		f.Content)
}

// TestPruneForIceberg_Fixture runs the pruner against the full OCSF 1.8.0
// fixture with the production class selection and checks the known recursion
// back-edges are cut, the result is deterministic, and no Value mapping or
// over-long path survives in the emitted output.
func TestPruneForIceberg_Fixture(t *testing.T) {
	classes := []string{"api_activity", "entity_management"}

	s1 := loadFixture(t)
	prunes1, err := gen.PruneForIceberg(s1, classes)
	require.NoError(t, err)

	s2 := loadFixture(t)
	prunes2, err := gen.PruneForIceberg(s2, classes)
	require.NoError(t, err)
	require.Equal(t, prunes1, prunes2, "pruning must be deterministic")

	// The four back-edges that create every cycle in OCSF 1.8.0 for this
	// class selection.
	require.Subset(t, prunes1, []gen.PrunedField{
		{Message: "Analytic", Field: "related_analytics", Rule: gen.PruneRuleRecursion},
		{Message: "LdapPerson", Field: "manager", Rule: gen.PruneRuleRecursion},
		{Message: "NetworkProxy", Field: "proxy_endpoint", Rule: gen.PruneRuleRecursion},
		{Message: "Process", Field: "parent_process", Rule: gen.PruneRuleRecursion},
	})

	// R3/R4 must never touch a scalar: every pruned field that is not an R1
	// well-known-type drop must be message-typed in the original schema.
	fresh := loadFixture(t)
	byMessage := make(map[string]map[string]*schema.Attribute)
	for _, cls := range fresh.Classes {
		byMessage[gen.ClassMessageName(cls.Name)] = cls.Attributes
	}
	for _, obj := range fresh.Objects {
		byMessage[gen.ClassMessageName(obj.Name)] = obj.Attributes
	}
	for _, p := range prunes1 {
		if p.Rule == gen.PruneRuleWellKnown {
			continue
		}
		attr, ok := byMessage[p.Message][p.Field]
		require.True(t, ok, "pruned field %s.%s not found in fixture", p.Message, p.Field)
		require.Equal(t, "object_t", attr.Type,
			"%s pruned scalar %s.%s; only message-typed fields may be cut", p.Rule, p.Message, p.Field)
	}

	out, _, err := emitJoined(t, s1, classes, tagmap.New(), "1.8.0")
	require.NoError(t, err)
	require.NotContains(t, out, "google.protobuf.Value")
	require.NotContains(t, out, "google/protobuf/struct.proto")
}

// assertPathSurvives walks a dotted field path through the pruned schema,
// starting at the union of the selected classes' attributes (the merged
// message view), and fails if any segment was pruned.
func assertPathSurvives(t *testing.T, s *schema.Schema, classes []string, path string) {
	t.Helper()
	segs := strings.Split(path, ".")

	var attr *schema.Attribute
	for _, className := range classes {
		if a, ok := s.Classes[className].Attributes[segs[0]]; ok {
			attr = a
			break
		}
	}
	require.NotNilf(t, attr, "root field %q of path %q did not survive pruning", segs[0], path)

	for _, seg := range segs[1:] {
		require.Equalf(t, "object_t", attr.Type, "segment before %q in path %q is not message-typed", seg, path)
		obj, ok := s.Objects[attr.ObjectType]
		require.Truef(t, ok, "object %q in path %q missing from schema", attr.ObjectType, path)
		attr, ok = obj.Attributes[seg]
		require.Truef(t, ok, "field %q of path %q did not survive pruning", seg, path)
	}
}

// TestPruneForIceberg_MapperFieldsSurvive is the downstream regression gate:
// every field path the cloudv2 audit-event mapper (pkg/ocsfevent) populates
// must survive --iceberg-compat pruning for OCSF 1.8.0 with classes
// api_activity + entity_management. R3 landing a cut on any of these shallow,
// load-bearing paths is a bug, whatever deep embedding provoked it.
func TestPruneForIceberg_MapperFieldsSurvive(t *testing.T) {
	classes := []string{"api_activity", "entity_management"}
	s := loadFixture(t)
	_, err := gen.PruneForIceberg(s, classes)
	require.NoError(t, err)

	for _, path := range []string{
		"class_uid",
		"category_uid",
		"type_uid",
		"activity_id",
		"severity_id",
		"status_id",
		"status_detail",
		"disposition_id",
		"time",
		"actor.user.email_addr",
		"actor.user.name",
		"api.operation",
		"src_endpoint.ip",
		"src_endpoint.svc_name",
		"src_endpoint.intermediate_ips",
		"resources.type",
		"resources.uid",
		"resources.name",
		"authorizations.decision",
		"authorizations.policy.uid",
		"authorizations.policy.name",
		"authorizations.policy.is_applied",
		"entity.type",
		"entity.uid",
		"entity.name",
		"metadata.version",
		"metadata.tenant_uid",
		"metadata.product.name",
		"metadata.profiles",
	} {
		assertPathSurvives(t, s, classes, path)
	}
}
