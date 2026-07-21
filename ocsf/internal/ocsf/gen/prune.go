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
)

// maxIcebergIdentLen is PostgreSQL's identifier limit (NAMEDATALEN-1).
// Oxla's Iceberg leg names nested type aliases after the full dotted field
// path from the root message and rejects any alias longer than this, e.g.:
//
//	type alias 'actor.process.file.data_classification.discovery_details.occurrence_details'
//	exceeds PostgreSQL name limit (75 > 63)
const maxIcebergIdentLen = 63

// PruneRule identifies which --iceberg-compat rule dropped a field.
type PruneRule string

const (
	// PruneRuleValueToString (R1) demotes every field that would map to
	// google.protobuf.Value (OCSF json_t and the generic "object" bag) to a
	// proto3 string carrying the value's JSON serialization, instead of
	// dropping the field. Oxla cannot load protobuf well-known types; CREATE
	// TABLE on a schema referencing Value fails with:
	//
	//	Failed to build proto file descriptor: google/protobuf/struct.proto:
	//	Import "google/protobuf/struct.proto" has not been loaded.;
	//	ocsf.v1.AuditEvent.unmapped: ".google.protobuf.Value" is not defined.
	//
	// string is exactly what Redpanda's own proto-to-Iceberg translator already
	// materializes a Value field as (a JSON-text column), so both the Kafka and
	// Iceberg legs agree, and the field survives (queryable as text) rather
	// than being lost — which matters for non-Iceberg consumers of the topic.
	// This is an interim measure: once Oxla loads the well-known descriptors,
	// the demotion can be dropped and Value emitted structurally again.
	PruneRuleValueToString PruneRule = "R1-value-to-string"

	// PruneRuleRecursion (R2) demotes back-edge fields to a string holding the
	// recursive subtree's JSON, so the message graph is acyclic (a string is a
	// leaf, not an edge). Redpanda's proto-to-Iceberg translator rejects
	// recursive proto types (iceberg::conversion_exception "recursive type
	// detected") and sends every record to the DLQ table; the demotion keeps
	// the data as text instead of dropping the field.
	PruneRuleRecursion PruneRule = "R2-recursion-to-string"

	// PruneRulePathLength (R3) demotes message-typed fields (edges) to a string
	// holding the subtree's JSON when the dotted field path would exceed
	// maxIcebergIdentLen, because Oxla names nested type aliases after the
	// dotted field path and enforces PostgreSQL's 63-char identifier limit on
	// REFRESH. R3 NEVER touches scalar fields, and demotes the DEEPEST edge on
	// an overflowing path whose own path still fits: message types are shared,
	// so a deep never-populated embedding must not cost a shared type its
	// fields at shallow, load-bearing embeddings (e.g. actor.user.email_addr
	// survives even though User is also embedded under
	// actor.process...service_dll_file.accessor, which becomes a JSON string).
	PruneRulePathLength PruneRule = "R3-path-to-string"

	// PruneRuleEmptyMessage (R4) drops message-typed fields whose target type
	// has no surviving fields. With R1/R2/R3 all demoting to string (never
	// dropping), no message loses its fields, so this rule is a defensive
	// no-op for the OCSF schema; it still fires if a source object is empty.
	PruneRuleEmptyMessage PruneRule = "R4-empty-message"
)

// PrunedField records one field removed by PruneForIceberg.
type PrunedField struct {
	// Message is the emitted proto message name that owned the field
	// (PascalCase class or object name). Fields pruned from a selected class
	// are also absent from the merged event message, which is built from the
	// pruned class attribute sets.
	Message string
	// Field is the proto field name (= OCSF attribute name).
	Field string
	// Rule is the prune rule that removed the field.
	Rule PruneRule
}

// PruneForIceberg mutates s in place so that everything subsequently emitted
// from it (per-class protos, the merged message, the SR schemas, and the
// protovalidate rules) is representable by Redpanda's proto-to-Iceberg
// translator and queryable from Oxla (Redpanda SQL). Three rules are applied
// in order:
//
//	R1: demote every field that maps to google.protobuf.Value to a string.
//	R2: demote back-edge fields to string so the message graph is acyclic.
//	R3: demote message-typed edges (never scalars) to string so that every
//	    surviving dotted leaf path from every root fits in 63 chars.
//	R4: drop message-typed fields whose target has no surviving fields
//	    (defensive; with R1-R3 demoting rather than dropping, nothing empties).
//
// R1-R3 never drop a field: an un-representable field becomes a proto3 string
// carrying the value's JSON, so every byte survives and only queryable
// structure is lost on the pathological fields.
//
// The pruned model is then verified: every surviving dotted leaf path from
// every root is at most 63 chars, no reachable message is empty, and R3/R4
// only ever removed message-typed fields. A violation fails generation.
//
// All pruned fields are ones the downstream emitters never populate; pruning
// the model uniformly (rather than only the SR schema) keeps the Go bindings,
// the wire schema, and the analytics schema identical, so nothing can be
// populated that the Iceberg/Oxla side cannot represent.
//
// Determinism: classes are processed in sorted class-name order and fields in
// sorted attribute-name order everywhere, so a given schema and class
// selection always prunes the same fields.
//
// Roots are the selected classes. The merged event message needs no separate
// root: its attribute set is the union of the selected classes' (pruned)
// attribute sets, so its field paths are exactly the union of the class
// roots' field paths.
//
// Pruned fields keep their entries in the tagmap (the generator simply never
// Assigns them), preserving the append-only wire-stability contract: a tagmap
// produced without pruning stays compat-check-clean against one produced with
// it.
//
// Constraints (at_least_one / just_one) referencing a pruned field are
// scrubbed so the generated CEL never mentions a field that no longer exists.
//
// The returned list is sorted by (Message, Field). An error is returned when
// a class is unknown or when the acyclicity post-condition fails after R2.
func PruneForIceberg(s *schema.Schema, classNames []string) ([]PrunedField, error) {
	classes := make([]*schema.Class, 0, len(classNames))
	for _, name := range classNames {
		cls, ok := s.Classes[name]
		if !ok {
			return nil, fmt.Errorf("ocsf prune: class %q not found in schema", name)
		}
		classes = append(classes, cls)
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i].Name < classes[j].Name })

	var prunes []PrunedField
	prunes = append(prunes, demoteWellKnownValues(s, classes)...)
	prunes = append(prunes, pruneRecursion(s, classes)...)

	// Post-condition: R2 must have left the reachable object graph acyclic.
	// A single DFS pass removes every back edge of its forest, and a digraph
	// without back edges is acyclic; this check turns a violation of that
	// invariant into a generation failure instead of a DLQ'd topic.
	if _, err := topoOrder(s, classes); err != nil {
		return nil, err
	}

	p3, err := prunePathLengths(s, classes)
	if err != nil {
		return nil, err
	}
	prunes = append(prunes, p3...)
	prunes = append(prunes, pruneEmptyMessages(s, classes)...)

	// Post-condition: the surviving model must satisfy the Iceberg/Oxla
	// invariants R3 and R4 are meant to establish. A violation here is a
	// pruner bug and must fail generation, not surface as a broken topic.
	if err := verifyIcebergInvariants(s, classes); err != nil {
		return nil, err
	}

	sort.Slice(prunes, func(i, j int) bool {
		if prunes[i].Message != prunes[j].Message {
			return prunes[i].Message < prunes[j].Message
		}
		return prunes[i].Field < prunes[j].Field
	})
	return prunes, nil
}

// pruneSidecarName is the base name of the sidecar file listing pruned fields.
const pruneSidecarName = "iceberg-compat-prunes.txt"

// PruneSidecarFile renders the deterministic sidecar recording what
// --iceberg-compat pruned: one "<Message>.<field> <rule>" line per pruned
// field, sorted. It lands next to the emitted protos
// (ocsf/v<N>/iceberg-compat-prunes.txt) so reviewers can diff fidelity loss
// across OCSF versions.
func PruneSidecarFile(version string, prunes []PrunedField) (GeneratedFile, error) {
	pkgSuffix, err := versionToPackage(version)
	if err != nil {
		return GeneratedFile{}, err
	}

	lines := make([]string, 0, len(prunes))
	for _, p := range prunes {
		lines = append(lines, p.Message+"."+p.Field+" "+string(p.Rule))
	}
	sort.Strings(lines)

	var sb strings.Builder
	sb.WriteString("# Code generated by ocsf-protogen --iceberg-compat. DO NOT EDIT.\n")
	sb.WriteString("# Source: OCSF schema " + version + "\n")
	sb.WriteString("# Fields transformed for the emitted protos: <Message>.<field> <rule>\n")
	sb.WriteString("# R1/R2/R3 demote the field to a string holding its JSON (data kept,\n")
	sb.WriteString("# structure not queryable); R4 drops it. See prune.go for the rules.\n")
	for _, line := range lines {
		sb.WriteString(line + "\n")
	}
	return GeneratedFile{Path: "ocsf/" + pkgSuffix + "/" + pruneSidecarName, Content: sb.String()}, nil
}

// isValueAttr reports whether attr maps to google.protobuf.Value (R1).
func isValueAttr(attr *schema.Attribute) bool {
	return attr.Type == wellKnownType ||
		(attr.Type == objectTypeName && attr.ObjectType == genericObject)
}

// messageEdge returns the referenced object name when attr is an edge in the
// message graph: an object_t reference to a non-generic object that exists in
// the schema. References to absent objects become empty stub messages, which
// have no fields and therefore no outgoing edges.
func messageEdge(s *schema.Schema, attr *schema.Attribute) (target string, ok bool) {
	if attr.Type != objectTypeName || attr.ObjectType == "" || attr.ObjectType == genericObject {
		return "", false
	}
	if _, exists := s.Objects[attr.ObjectType]; !exists {
		return "", false
	}
	return attr.ObjectType, true
}

// scrubConstraints removes name from both constraint lists, returning nil when
// nothing remains (Constraints stay absent rather than present-but-empty).
func scrubConstraints(c *schema.Constraints, name string) *schema.Constraints {
	if c == nil {
		return nil
	}
	c.AtLeastOne = removeString(c.AtLeastOne, name)
	c.JustOne = removeString(c.JustOne, name)
	if len(c.AtLeastOne) == 0 && len(c.JustOne) == 0 {
		return nil
	}
	return c
}

// removeString returns s without any occurrence of name.
func removeString(s []string, name string) []string {
	out := s[:0]
	for _, v := range s {
		if v != name {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// dropClassField removes the named attribute from cls and scrubs constraints.
func dropClassField(cls *schema.Class, name string) {
	delete(cls.Attributes, name)
	cls.Constraints = scrubConstraints(cls.Constraints, name)
}

// dropObjectField removes the named attribute from obj and scrubs constraints.
func dropObjectField(obj *schema.Object, name string) {
	delete(obj.Attributes, name)
	obj.Constraints = scrubConstraints(obj.Constraints, name)
}

// reachableObjectNames returns (sorted) the objects transitively reachable
// from the selected classes through message edges, on the current (possibly
// already pruned) attribute maps.
func reachableObjectNames(s *schema.Schema, classes []*schema.Class) []string {
	visited := make(map[string]bool)
	var queue []string
	enqueue := func(attrs map[string]*schema.Attribute) {
		for _, name := range sortedKeys(attrs) {
			if target, ok := messageEdge(s, attrs[name]); ok && !visited[target] {
				visited[target] = true
				queue = append(queue, target)
			}
		}
	}
	for _, cls := range classes {
		enqueue(cls.Attributes)
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		enqueue(s.Objects[name].Attributes)
	}

	names := make([]string, 0, len(visited))
	for name := range visited {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// demoteWellKnownValues applies R1: every attribute mapping to
// google.protobuf.Value is retyped to a proto3 string in the selected classes
// and in every object reachable from them. Value attributes are never edges in
// the message graph, and demotion turns them into scalars (not edges), so
// reachability is unchanged. Because the retyped fields are now string leaves,
// they must run before R3 (path length), which then accounts for them.
func demoteWellKnownValues(s *schema.Schema, classes []*schema.Class) []PrunedField {
	var demotes []PrunedField
	for _, cls := range classes {
		for _, name := range sortedKeys(cls.Attributes) {
			if isValueAttr(cls.Attributes[name]) {
				demoteToString(cls.Attributes[name])
				demotes = append(demotes, PrunedField{toPascalCase(cls.Name), name, PruneRuleValueToString})
			}
		}
	}
	for _, objName := range reachableObjectNames(s, classes) {
		obj := s.Objects[objName]
		for _, name := range sortedKeys(obj.Attributes) {
			if isValueAttr(obj.Attributes[name]) {
				demoteToString(obj.Attributes[name])
				demotes = append(demotes, PrunedField{toPascalCase(obj.Name), name, PruneRuleValueToString})
			}
		}
	}
	return demotes
}

// demoteToString retypes a google.protobuf.Value attribute to the OCSF
// string base type in place, so the emitter produces a proto3 `string` (or
// `repeated string` when the attribute was an array). Constraints referencing
// the attribute are deliberately left intact — unlike a drop, the field still
// exists, so any at_least_one/just_one that names it remains satisfiable.
func demoteToString(attr *schema.Attribute) {
	attr.Type = stringBaseType
	attr.ObjectType = ""
}

// pruneRecursion applies R2 with a single depth-first walk over message-typed
// fields from each selected root class: roots in sorted class-name order,
// fields in sorted attribute-name order. A field whose target type is gray
// (on the current DFS path) is a back edge — self-references included — and
// is dropped from its owning object. Removing every back edge of one DFS
// forest leaves the graph acyclic; PruneForIceberg verifies that as a
// post-condition.
func pruneRecursion(s *schema.Schema, classes []*schema.Class) []PrunedField {
	const (
		white = iota // not yet visited
		gray         // on the current DFS path
		black        // fully explored
	)
	state := make(map[string]int)
	var prunes []PrunedField

	var visit func(objName string)
	visit = func(objName string) {
		state[objName] = gray
		obj := s.Objects[objName]
		for _, attrName := range sortedKeys(obj.Attributes) {
			target, ok := messageEdge(s, obj.Attributes[attrName])
			if !ok {
				continue
			}
			switch state[target] {
			case gray:
				// Back edge: demote it to a string holding the recursive
				// subtree's JSON. This breaks the cycle (a string is a leaf,
				// not an edge) without losing the data, so the whole graph is
				// acyclic and Redpanda's translator accepts it.
				demoteToString(obj.Attributes[attrName])
				prunes = append(prunes, PrunedField{toPascalCase(obj.Name), attrName, PruneRuleRecursion})
			case white:
				visit(target)
			}
		}
		state[objName] = black
	}

	for _, cls := range classes {
		for _, attrName := range sortedKeys(cls.Attributes) {
			// Classes are never referenced by object_t attributes, so a class
			// root can never itself be the target of a back edge.
			if target, ok := messageEdge(s, cls.Attributes[attrName]); ok && state[target] == white {
				visit(target)
			}
		}
	}
	return prunes
}

// topoOrder returns the objects reachable from the selected classes in a
// deterministic topological order (Kahn's algorithm with a sorted ready set),
// or an error naming the cycle participants when the graph is not acyclic.
func topoOrder(s *schema.Schema, classes []*schema.Class) ([]string, error) {
	names := reachableObjectNames(s, classes)
	inSet := make(map[string]bool, len(names))
	for _, n := range names {
		inSet[n] = true
	}

	indeg := make(map[string]int, len(names))
	edges := make(map[string][]string, len(names))
	for _, n := range names {
		attrs := s.Objects[n].Attributes
		for _, attrName := range sortedKeys(attrs) {
			if target, ok := messageEdge(s, attrs[attrName]); ok && inSet[target] {
				edges[n] = append(edges[n], target)
				indeg[target]++
			}
		}
	}

	var ready []string
	for _, n := range names {
		if indeg[n] == 0 {
			ready = append(ready, n)
		}
	}
	order := make([]string, 0, len(names))
	for len(ready) > 0 {
		sort.Strings(ready)
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)
		for _, target := range edges[n] {
			indeg[target]--
			if indeg[target] == 0 {
				ready = append(ready, target)
			}
		}
	}

	if len(order) != len(names) {
		var cyclic []string
		for _, n := range names {
			if indeg[n] > 0 {
				cyclic = append(cyclic, n)
			}
		}
		sort.Strings(cyclic)
		return nil, fmt.Errorf(
			"ocsf prune: recursion pruning left a cycle among objects: %s",
			strings.Join(cyclic, ", "))
	}
	return order, nil
}

// edgeRef identifies one message-typed field (an edge) by its owning message
// and attribute name. ownerClass is true when the owner is a root class rather
// than an object.
type edgeRef struct {
	ownerClass bool
	owner      string
	attr       string
}

// pathEnv is the worst-case dotted-path picture of the current (partly demoted)
// model: for every reachable object, the longest dotted path that instantiates
// it, and the edge that achieved that longest path (its parent on the worst
// path). Demoted edges are strings, not edges, so they neither propagate depth
// nor appear here.
type pathEnv struct {
	maxPrefix map[string]int
	present   map[string]bool
	parent    map[string]edgeRef
}

// worstPaths recomputes the worst-case path environment over the edges that are
// still message-typed (i.e. not yet demoted). The post-R2 graph is acyclic, so
// a single relaxation pass in topological order is exact.
func worstPaths(s *schema.Schema, classes []*schema.Class, order []string) pathEnv {
	env := pathEnv{
		maxPrefix: make(map[string]int),
		present:   make(map[string]bool),
		parent:    make(map[string]edgeRef),
	}
	relax := func(target string, length int, e edgeRef) {
		if !env.present[target] || length > env.maxPrefix[target] {
			env.maxPrefix[target] = length
			env.parent[target] = e
		}
		env.present[target] = true
	}
	for _, cls := range classes {
		for _, attrName := range sortedKeys(cls.Attributes) {
			if target, ok := messageEdge(s, cls.Attributes[attrName]); ok {
				relax(target, len(attrName), edgeRef{true, cls.Name, attrName})
			}
		}
	}
	for _, objName := range order {
		if !env.present[objName] {
			continue
		}
		prefix := env.maxPrefix[objName]
		for _, attrName := range sortedKeys(s.Objects[objName].Attributes) {
			if target, ok := messageEdge(s, s.Objects[objName].Attributes[attrName]); ok {
				relax(target, prefix+1+len(attrName), edgeRef{false, objName, attrName})
			}
		}
	}
	return env
}

// prunePathLengths applies R3: it demotes message-typed edges to JSON-strings
// (never drops, never touches scalars) until every surviving dotted leaf path
// fits maxIcebergIdentLen, because Oxla names nested type aliases after the
// dotted field path and rejects any longer than PostgreSQL's 63-char limit on
// REFRESH.
//
// Demoting an edge turns its whole subtree into one string leaf at the edge's
// own path, which both removes the deep paths underneath and keeps the data
// (as text). To preserve as much typed structure as possible, when a leaf
// overflows R3 demotes the DEEPEST edge on that leaf's worst-case path whose
// own path still fits — collapsing the smallest subtree that resolves the
// overflow. Shared types therefore keep their fields at shallow, load-bearing
// embeddings (actor.user.email_addr survives) while a too-deep embedding of the
// same type (…file.accessor) becomes a JSON string.
//
// It runs to a fixpoint: each pass recomputes worst-case paths over the
// not-yet-demoted edges (the graph only shrinks, so this terminates), finds one
// overflowing scalar leaf, and demotes an ancestor edge to fix it. A root-class
// scalar whose bare name already exceeds the limit is unfixable (scalars are
// never demoted) and returns an error.
func prunePathLengths(s *schema.Schema, classes []*schema.Class) ([]PrunedField, error) {
	var prunes []PrunedField
	for {
		order, err := topoOrder(s, classes)
		if err != nil {
			return nil, err
		}
		env := worstPaths(s, classes, order)

		// Find one overflowing scalar leaf (already-demoted edges are strings
		// at paths we guaranteed <= limit, so they never overflow). A root
		// scalar overflowing on its bare name is unfixable.
		victim, dropIt, found, err := findPathOverflow(s, classes, order, env)
		if err != nil {
			return nil, err
		}
		if !found {
			return prunes, nil
		}
		if dropIt {
			// The chosen edge's own name already exceeds the limit, so no
			// string demotion can give it a fitting alias — it is
			// unrepresentable and must be dropped. (Does not occur in real
			// OCSF, whose field names are short; defensive.)
			dropEdge(s, victim)
		} else {
			demoteEdge(s, victim)
		}
		prunes = append(prunes, prunedFromEdge(s, victim))
	}
}

// findPathOverflow scans every surviving scalar leaf for a dotted path over the
// limit and, when it finds one, returns the deepest edge on that leaf's
// worst-case path whose own demoted-leaf path still fits the limit — the edge
// to demote. It walks up parent edges from the overflowing object until it
// reaches one whose path fits (root edges always do).
func findPathOverflow(s *schema.Schema, classes []*schema.Class, order []string, env pathEnv) (victim edgeRef, dropIt, found bool, err error) {
	overflows := func(owner string, ownerPrefix int, ownerIsClass bool) (edgeRef, bool, bool, error) {
		var attrs map[string]*schema.Attribute
		if ownerIsClass {
			attrs = s.Classes[owner].Attributes
		} else {
			attrs = s.Objects[owner].Attributes
		}
		for _, attrName := range sortedKeys(attrs) {
			if _, ok := messageEdge(s, attrs[attrName]); ok {
				continue // edges are handled via their own targets
			}
			pathLen := len(attrName)
			if !ownerIsClass {
				pathLen = ownerPrefix + 1 + len(attrName)
			}
			if pathLen <= maxIcebergIdentLen {
				continue
			}
			// This scalar overflows. A root scalar overflowing on its bare
			// name is unfixable (scalars are never demoted or dropped).
			if ownerIsClass {
				return edgeRef{}, false, false, fmt.Errorf(
					"ocsf prune: class %s scalar %q has a %d-char path (limit %d) and scalars are never pruned",
					toPascalCase(owner), attrName, pathLen, maxIcebergIdentLen)
			}
			e, drop := demotableAncestor(env, owner)
			return e, drop, true, nil
		}
		return edgeRef{}, false, false, nil
	}

	for _, cls := range classes {
		if e, drop, ok, err := overflows(cls.Name, 0, true); ok || err != nil {
			return e, drop, ok, err
		}
	}
	for _, objName := range order {
		if !env.present[objName] {
			continue
		}
		if e, drop, ok, err := overflows(objName, env.maxPrefix[objName], false); ok || err != nil {
			return e, drop, ok, err
		}
	}
	return edgeRef{}, false, false, nil
}

// demotableAncestor walks up the worst-case parent chain from obj and returns
// the deepest edge on it whose own path still fits the limit — demoting that
// edge collapses obj's overflowing subtree into a string at a fitting path.
// If the walk reaches a root edge whose own name already exceeds the limit, no
// string alias can fit either, so it returns dropIt=true for that edge (it is
// unrepresentable and must be dropped). Root edges usually have short names, so
// the common outcome is a demotable edge with dropIt=false.
func demotableAncestor(env pathEnv, obj string) (e edgeRef, dropIt bool) {
	cur := obj
	for {
		edge := env.parent[cur]
		if env.maxPrefix[cur] <= maxIcebergIdentLen {
			return edge, false // demote this edge; its leaf path fits
		}
		if edge.ownerClass {
			return edge, true // root edge name itself exceeds the limit
		}
		cur = edge.owner
	}
}

func dropEdge(s *schema.Schema, e edgeRef) {
	if e.ownerClass {
		dropClassField(s.Classes[e.owner], e.attr)
		return
	}
	dropObjectField(s.Objects[e.owner], e.attr)
}

func demoteEdge(s *schema.Schema, e edgeRef) {
	if e.ownerClass {
		demoteToString(s.Classes[e.owner].Attributes[e.attr])
		return
	}
	demoteToString(s.Objects[e.owner].Attributes[e.attr])
}

func prunedFromEdge(s *schema.Schema, e edgeRef) PrunedField {
	name := e.owner
	if e.ownerClass {
		name = s.Classes[e.owner].Name
	} else {
		name = s.Objects[e.owner].Name
	}
	return PrunedField{toPascalCase(name), e.attr, PruneRulePathLength}
}

// pruneEmptyMessages applies R4 to a fixpoint: every message-typed field
// whose target (present in the schema) has no surviving fields is dropped.
// Cutting such an edge can empty its parent, so the loop repeats until
// nothing changes. The fixpoint is unique: each round cuts exactly the edges
// to currently-empty targets, independent of iteration order.
func pruneEmptyMessages(s *schema.Schema, classes []*schema.Class) []PrunedField {
	var prunes []PrunedField
	for {
		emptySet := make(map[string]bool)
		for _, name := range reachableObjectNames(s, classes) {
			if len(s.Objects[name].Attributes) == 0 {
				emptySet[name] = true
			}
		}
		if len(emptySet) == 0 {
			return prunes
		}

		changed := false
		cut := func(attrs map[string]*schema.Attribute, msgName string, drop func(string)) {
			for _, attrName := range sortedKeys(attrs) {
				if target, ok := messageEdge(s, attrs[attrName]); ok && emptySet[target] {
					drop(attrName)
					prunes = append(prunes, PrunedField{msgName, attrName, PruneRuleEmptyMessage})
					changed = true
				}
			}
		}
		for _, cls := range classes {
			cut(cls.Attributes, toPascalCase(cls.Name), func(name string) { dropClassField(cls, name) })
		}
		for _, objName := range reachableObjectNames(s, classes) {
			obj := s.Objects[objName]
			cut(obj.Attributes, toPascalCase(obj.Name), func(name string) { dropObjectField(obj, name) })
		}
		if !changed {
			return prunes
		}
	}
}

// verifyIcebergInvariants re-checks the final pruned model from scratch and
// returns an error when any invariant the prune rules must establish is
// violated:
//
//   - every surviving dotted leaf path from every root is at most 63 chars
//     (checked exactly: longest surviving prefix per type plus each scalar);
//   - no reachable message (class or object) is left without fields.
//
// R3/R4 only ever remove message-typed fields by construction, so a scalar
// that cannot fit (possible only for an over-long root-level scalar name) is
// reported as an error instead of being silently pruned.
func verifyIcebergInvariants(s *schema.Schema, classes []*schema.Class) error {
	order, err := topoOrder(s, classes)
	if err != nil {
		return err
	}

	maxPrefix := make(map[string]int)
	present := make(map[string]bool)
	relax := func(target string, length int) {
		if !present[target] || length > maxPrefix[target] {
			maxPrefix[target] = length
		}
		present[target] = true
	}

	for _, cls := range classes {
		if len(cls.Attributes) == 0 {
			return fmt.Errorf("ocsf prune: class %q has no surviving fields", cls.Name)
		}
		for _, attrName := range sortedKeys(cls.Attributes) {
			if target, ok := messageEdge(s, cls.Attributes[attrName]); ok {
				relax(target, len(attrName))
				continue
			}
			if len(attrName) > maxIcebergIdentLen {
				return fmt.Errorf(
					"ocsf prune: scalar field %s.%s has a %d-char path (limit %d) and scalars are never pruned",
					toPascalCase(cls.Name), attrName, len(attrName), maxIcebergIdentLen)
			}
		}
	}

	for _, objName := range order {
		if !present[objName] {
			continue
		}
		obj := s.Objects[objName]
		if len(obj.Attributes) == 0 {
			return fmt.Errorf("ocsf prune: reachable message %q has no surviving fields", toPascalCase(obj.Name))
		}
		prefix := maxPrefix[objName]
		for _, attrName := range sortedKeys(obj.Attributes) {
			pathLen := prefix + 1 + len(attrName)
			if target, ok := messageEdge(s, obj.Attributes[attrName]); ok {
				relax(target, pathLen)
				continue
			}
			if pathLen > maxIcebergIdentLen {
				return fmt.Errorf(
					"ocsf prune: scalar field %s.%s has a %d-char worst-case path (limit %d)",
					toPascalCase(obj.Name), attrName, pathLen, maxIcebergIdentLen)
			}
		}
	}
	return nil
}
