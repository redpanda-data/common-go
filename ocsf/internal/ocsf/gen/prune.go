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
	// PruneRuleWellKnown (R1) drops every field that would map to
	// google.protobuf.Value (OCSF json_t and the generic "object" bag).
	// Oxla cannot load protobuf well-known types; CREATE TABLE fails with:
	//
	//	Failed to build proto file descriptor: google/protobuf/struct.proto:
	//	Import "google/protobuf/struct.proto" has not been loaded.;
	//	ocsf.v1.AuditEvent.unmapped: ".google.protobuf.Value" is not defined.
	PruneRuleWellKnown PruneRule = "R1-well-known-type"

	// PruneRuleRecursion (R2) drops back-edge fields so the message graph is
	// acyclic. Redpanda's proto-to-Iceberg translator rejects recursive proto
	// types (iceberg::conversion_exception "recursive type detected") and
	// sends every record to the DLQ table.
	PruneRuleRecursion PruneRule = "R2-recursion"

	// PruneRulePathLength (R3) drops message-typed fields (edges) whose
	// subtree cannot fit within maxIcebergIdentLen dotted-path characters at
	// the field's worst-case embedding depth, because Oxla names nested type
	// aliases after the dotted field path and enforces PostgreSQL's 63-char
	// identifier limit on REFRESH. R3 NEVER drops scalar fields: message
	// types are shared, and a deep never-populated embedding must not cost a
	// shared type its fields at shallow, load-bearing embeddings (e.g.
	// actor.user.email_addr must survive even though User is also embedded
	// under actor.process...service_dll_file.accessor). The cut therefore
	// lands on the deep embedding edge itself.
	PruneRulePathLength PruneRule = "R3-path-length"

	// PruneRuleEmptyMessage (R4) drops message-typed fields whose target
	// type has no surviving fields (everything inside was pruned by R1-R3,
	// recursively). An empty nested message carries no information and its
	// Iceberg translation is at best useless; cutting the edge also removes
	// the empty type from the emit closure. Applied to a fixpoint: a parent
	// emptied by this rule loses its own referencing edges too.
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
//	R1: drop every field that maps to google.protobuf.Value.
//	R2: drop back-edge fields so the reachable message graph is acyclic.
//	R3: drop message-typed fields (never scalars) so that every surviving
//	    dotted leaf path from every root fits in 63 chars.
//	R4: drop message-typed fields whose target has no surviving fields.
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
	prunes = append(prunes, pruneWellKnownValues(s, classes)...)
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
	sb.WriteString("# Fields pruned from the emitted protos: <Message>.<field> <rule>\n")
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

// pruneWellKnownValues applies R1: every attribute mapping to
// google.protobuf.Value is dropped from the selected classes and from every
// object reachable from them. Value attributes are never edges in the message
// graph, so reachability is unchanged by the removal.
func pruneWellKnownValues(s *schema.Schema, classes []*schema.Class) []PrunedField {
	var prunes []PrunedField
	for _, cls := range classes {
		for _, name := range sortedKeys(cls.Attributes) {
			if isValueAttr(cls.Attributes[name]) {
				dropClassField(cls, name)
				prunes = append(prunes, PrunedField{toPascalCase(cls.Name), name, PruneRuleWellKnown})
			}
		}
	}
	for _, objName := range reachableObjectNames(s, classes) {
		obj := s.Objects[objName]
		for _, name := range sortedKeys(obj.Attributes) {
			if isValueAttr(obj.Attributes[name]) {
				dropObjectField(obj, name)
				prunes = append(prunes, PrunedField{toPascalCase(obj.Name), name, PruneRuleWellKnown})
			}
		}
	}
	return prunes
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
				dropObjectField(obj, attrName)
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

// irreducibleSuffixes computes, for every object in order (a parents-first
// topological order of the post-R2 DAG), the shortest leaf suffix the object
// forces on any embedding that keeps it:
//
//   - scalar fields can never be pruned, so an object with scalars forces its
//     LONGEST scalar name (keeping an edge to it means every scalar path
//     underneath must fit);
//   - an object with only message-typed fields forces at least its cheapest
//     edge (len(name) + 1 + suffix of the target), since keeping it with zero
//     fields is pointless (R4 would cut the edge anyway);
//   - an empty object forces nothing (R4 cuts edges to it).
//
// The value is a lower bound: R3 may later cut edges, which only shrinks real
// suffixes, and verifyIcebergInvariants re-checks the final model exactly.
func irreducibleSuffixes(s *schema.Schema, order []string) map[string]int {
	irr := make(map[string]int, len(order))
	// Children before parents: reverse topological order.
	for i := len(order) - 1; i >= 0; i-- {
		name := order[i]
		obj := s.Objects[name]
		maxScalar, minEdge := -1, -1
		for _, attrName := range sortedKeys(obj.Attributes) {
			if target, ok := messageEdge(s, obj.Attributes[attrName]); ok {
				cand := len(attrName) + 1 + irr[target]
				if minEdge < 0 || cand < minEdge {
					minEdge = cand
				}
				continue
			}
			if len(attrName) > maxScalar {
				maxScalar = len(attrName)
			}
		}
		switch {
		case maxScalar >= 0:
			irr[name] = maxScalar
		case minEdge >= 0:
			irr[name] = minEdge
		default:
			irr[name] = 0
		}
	}
	return irr
}

// prunePathLengths applies R3: a message-typed field f on type T is dropped
// when its worst-case embedding cannot fit its target's irreducible suffix
// within the limit:
//
//	maxPrefix(T) + 1 + len(f.name) + 1 + irreducibleSuffix(target) > 63
//
// where maxPrefix(T) is the longest surviving dotted path instantiating T
// from any root, across all embeddings. Scalar fields are NEVER dropped: the
// fidelity loss lands on deep embedding edges (which the downstream emitters
// never populate), not on a shared type's shallow fields. A field of exactly
// 63 path chars is kept.
//
// The graph is acyclic after R2, so one top-down sweep in topological order
// is exact: parents are fully decided before a type's own prefix is read, a
// cut edge contributes no prefix to its target, and an object left with no
// surviving embedding is skipped entirely and falls out of the emit closure.
func prunePathLengths(s *schema.Schema, classes []*schema.Class) ([]PrunedField, error) {
	order, err := topoOrder(s, classes)
	if err != nil {
		return nil, err
	}
	irr := irreducibleSuffixes(s, order)

	var prunes []PrunedField

	// maxPrefix[obj] is the length of the longest surviving dotted path that
	// instantiates obj; present[obj] means obj has at least one surviving
	// embedding.
	maxPrefix := make(map[string]int)
	present := make(map[string]bool)
	relax := func(target string, length int) {
		if !present[target] || length > maxPrefix[target] {
			maxPrefix[target] = length
		}
		present[target] = true
	}

	// Root class attributes: the dotted path is the attribute name itself.
	for _, cls := range classes {
		for _, attrName := range sortedKeys(cls.Attributes) {
			target, ok := messageEdge(s, cls.Attributes[attrName])
			if !ok {
				continue // scalars are never pruned by R3
			}
			if len(attrName)+1+irr[target] > maxIcebergIdentLen {
				dropClassField(cls, attrName)
				prunes = append(prunes, PrunedField{toPascalCase(cls.Name), attrName, PruneRulePathLength})
				continue
			}
			relax(target, len(attrName))
		}
	}

	for _, objName := range order {
		if !present[objName] {
			// Every embedding of this object was dropped: the whole subtree
			// is gone and the object leaves the emit closure.
			continue
		}
		obj := s.Objects[objName]
		prefix := maxPrefix[objName]
		for _, attrName := range sortedKeys(obj.Attributes) {
			target, ok := messageEdge(s, obj.Attributes[attrName])
			if !ok {
				continue // scalars are never pruned by R3
			}
			pathLen := prefix + 1 + len(attrName)
			if pathLen+1+irr[target] > maxIcebergIdentLen {
				dropObjectField(obj, attrName)
				prunes = append(prunes, PrunedField{toPascalCase(obj.Name), attrName, PruneRulePathLength})
				continue
			}
			relax(target, pathLen)
		}
	}
	return prunes, nil
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
