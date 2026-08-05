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
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/schema"
)

// maskFormatVersion is the mask-file `version` this generator understands.
// It gates the file format, not the OCSF schema version.
const maskFormatVersion = 1

// maskWildcard is the trailing path segment that keeps a message-typed field's
// entire subtree rather than one scalar leaf.
const maskWildcard = "*"

// Mask is a parsed read mask: the set of root-relative dotted attribute paths a
// consumer publishes. Everything not named (directly or under a "*" subtree) is
// dropped from the emitted schema.
//
// A mask is an ALLOWLIST, deliberately: OCSF grows, and an allowlist keeps the
// published contract the same size across schema bumps, where a denylist
// silently regrows. The cost is that a renamed OCSF attribute stops matching,
// which MaskFields reports as an error rather than silently narrowing the
// output.
type Mask struct {
	// Paths are the root-relative dotted attribute paths to keep, sorted and
	// deduplicated, as written (a subtree path keeps its trailing ".*").
	//
	// Paths are relative to a class root, e.g. "actor.user.email_addr". A path
	// ending in ".*" keeps the named message-typed field and everything beneath
	// it; any other path must end on a scalar.
	Paths []string
}

// ParseMask decodes a read-mask YAML document:
//
//	version: 1
//	paths:
//	  - time
//	  - actor.user.email_addr
//	  - metadata.*
//
// Unknown keys are rejected so a typo fails generation instead of silently
// widening the mask. The returned Mask has sorted, deduplicated paths.
func ParseMask(data []byte) (*Mask, error) {
	var raw struct {
		Version int      `yaml:"version"`
		Paths   []string `yaml:"paths"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("ocsf mask: file is empty")
		}
		return nil, fmt.Errorf("ocsf mask: parse: %w", err)
	}

	// Decode consumes ONE document. Without this, a second "---" document is
	// silently ignored, which would let a typo'd key (or a whole second policy)
	// slip past KnownFields and the path validation below — the opposite of the
	// fail-closed behaviour a mask is supposed to have.
	var trailing yaml.Node
	switch err := dec.Decode(&trailing); {
	case errors.Is(err, io.EOF):
		// Exactly one document, as required.
	case err == nil:
		return nil, errors.New("ocsf mask: file has more than one YAML document; " +
			"the mask must be a single document (no \"---\" separators)")
	default:
		return nil, fmt.Errorf("ocsf mask: parse trailing document: %w", err)
	}

	if raw.Version != maskFormatVersion {
		return nil, fmt.Errorf("ocsf mask: unsupported version %d (want %d)", raw.Version, maskFormatVersion)
	}
	if len(raw.Paths) == 0 {
		return nil, errors.New("ocsf mask: paths must be a non-empty list")
	}

	seen := make(map[string]bool, len(raw.Paths))
	paths := make([]string, 0, len(raw.Paths))
	for _, p := range raw.Paths {
		p = strings.TrimSpace(p)
		if err := validateMaskPath(p); err != nil {
			return nil, err
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	sort.Strings(paths)

	// A path already covered by a "*" subtree can never affect the output;
	// keeping it would also make the widening report misleading (the path would
	// look "asked for" via two different entries). Flag it as an authoring bug.
	for _, p := range paths {
		if err := checkNotSubsumed(p, paths); err != nil {
			return nil, err
		}
	}

	return &Mask{Paths: paths}, nil
}

// KeptField is one field the mask retains, in the same (Message, Field) shape
// PrunedField uses so the two sidecars read alike.
type KeptField struct {
	// Message is the emitted proto message name that owns the field
	// (PascalCase class or object name).
	Message string
	// Field is the proto field name (= OCSF attribute name).
	Field string
	// Subtree is true when the field was kept by a "*" path, meaning its whole
	// target type is retained unmasked.
	Subtree bool
}

// MaskStats records the size of the model either side of the mask. The leaf-path
// counts are what an Iceberg/Oxla reader sees as columns, so they are the
// numbers worth watching across OCSF bumps.
type MaskStats struct {
	LeafPathsBefore, LeafPathsAfter int
	MessagesBefore, MessagesAfter   int
}

// MaskResult is what MaskFields retained, plus what the type-scoped projection
// kept beyond the paths that were asked for.
type MaskResult struct {
	// Kept is the resolved type-scoped keep set, sorted by (Message, Field).
	Kept []KeptField
	// Widened lists the dotted leaf paths present in the masked output that no
	// mask path asked for, sorted. See MaskFields for why these arise.
	Widened []string
	// Stats records model size before and after masking.
	Stats MaskStats
}

// MaskFields mutates s in place so only the attributes named by the mask
// survive, and returns what was kept.
//
// It must run BEFORE PruneForIceberg. The prune rules react to the shape of the
// model — R3 demotes edges because a dotted path overflows 63 chars, R4 drops
// edges to emptied types — so masking first means the deep subtrees that force
// those demotions are simply gone, and the surviving fields keep their typed
// structure instead of collapsing to JSON strings.
//
// # Type-scoped semantics
//
// Mask paths are written root-relative ("actor.user.email_addr") because that is
// how consumers think about the event, but they are APPLIED type-scoped: the
// path above keeps User.email_addr on the User message wherever User is
// embedded. The schema model is a graph of shared object types, so a per-path
// mask would require specialising a type per embedding path — a message
// explosion that would also break the tagmap's (message, attr) identity.
//
// The gap between the two is reported: every leaf path in the masked output that
// no mask path asked for lands in MaskResult.Widened. In practice the gap is
// small, because sharing falls away as paths are closed — a type embedded at
// fifteen paths in the full schema is usually reachable by one after masking.
//
// # Wire stability
//
// Masking never renumbers. Excluded attributes are simply never Assign()ed, so
// they keep their tagmap entries, the tagmap file does not shrink, --compat-check
// stays clean, and un-masking a field later restores its original field number.
//
// Constraints (at_least_one / just_one) naming a dropped attribute are scrubbed,
// so the emitted CEL never references a field that no longer exists.
//
// An error is returned when a class is unknown, when a mask path does not
// resolve against the schema, or when masking would leave a reachable message
// with no fields.
func MaskFields(s *schema.Schema, classNames []string, m *Mask) (*MaskResult, error) {
	classes, err := maskClasses(s, classNames)
	if err != nil {
		return nil, err
	}

	stats := MaskStats{
		LeafPathsBefore: len(leafPaths(s, classes)),
		MessagesBefore:  len(reachableObjectNames(s, classes)) + len(classes),
	}

	keep, err := resolveMask(s, classes, m)
	if err != nil {
		return nil, err
	}

	// Snapshot reachability BEFORE mutating: an object that becomes unreachable
	// is left untouched (SelectClosure simply stops emitting it), but it must
	// still be visited here so a retained type reached only through it is masked
	// consistently.
	reachable := reachableObjectNames(s, classes)

	for _, cls := range classes {
		kept := keep.attrs[msgKey{isClass: true, name: cls.Name}]
		for _, attrName := range sortedKeys(cls.Attributes) {
			if !kept[attrName] {
				dropClassField(cls, attrName)
			}
		}
	}
	for _, objName := range reachable {
		if keep.subtree[objName] {
			continue // kept whole by a "*" path
		}
		kept, masked := keep.attrs[msgKey{name: objName}]
		if !masked {
			continue // unreachable after masking; nothing to trim
		}
		obj := s.Objects[objName]
		for _, attrName := range sortedKeys(obj.Attributes) {
			if !kept[attrName] {
				dropObjectField(obj, attrName)
			}
		}
	}

	if err := verifyMaskInvariants(s, classes); err != nil {
		return nil, err
	}

	after := leafPaths(s, classes)
	stats.LeafPathsAfter = len(after)
	stats.MessagesAfter = len(reachableObjectNames(s, classes)) + len(classes)

	return &MaskResult{
		Kept:    keep.keptFields(s),
		Widened: widenedPaths(after, m),
		Stats:   stats,
	}, nil
}

// mergedDiscriminators are the attributes a merged single-topic event message
// cannot lose. The merged emitter gates per-class ownership, conditional
// requiredness and per-class constraints on `this.class_uid` unconditionally, so
// masking class_uid away emits CEL referencing a missing field; category_uid and
// type_uid are how a reader of the single topic demuxes the class.
var mergedDiscriminators = []string{"category_uid", "class_uid", "type_uid"}

// VerifyMergedDiscriminators checks that every selected class still carries the
// attributes a merged event message needs after masking. Callers apply it only
// when emitting a merged message.
func VerifyMergedDiscriminators(s *schema.Schema, classNames []string) error {
	classes, err := maskClasses(s, classNames)
	if err != nil {
		return err
	}
	for _, cls := range classes {
		for _, attrName := range mergedDiscriminators {
			if _, ok := cls.Attributes[attrName]; !ok {
				return fmt.Errorf(
					"ocsf mask: class %q lost %q, which a merged event message requires "+
						"(the merged emitter gates its CEL on class_uid, and readers demux the "+
						"single topic on category_uid/class_uid/type_uid) — add %q to the mask",
					cls.Name, attrName, attrName)
			}
		}
	}
	return nil
}

// MaskReportName is the base name of the sidecar recording the resolved mask.
const MaskReportName = "read-mask-report.txt"

// MaskReportFile renders the deterministic sidecar describing what the mask
// kept. It lands next to the emitted protos (ocsf/v<N>/read-mask-report.txt).
//
// It lists the KEPT set rather than the dropped one: the kept set is the
// published contract (tens of lines), where the dropped set is the whole rest of
// OCSF (thousands). The widening section is the part worth reviewing — it names
// every column the type-scoped projection produced that no mask path asked for.
func MaskReportFile(version string, res *MaskResult) (GeneratedFile, error) {
	pkgSuffix, err := versionToPackage(version)
	if err != nil {
		return GeneratedFile{}, err
	}

	var sb strings.Builder
	sb.WriteString("# Code generated by ocsf-protogen --mask-file. DO NOT EDIT.\n")
	sb.WriteString("# Source: OCSF schema " + version + "\n")
	sb.WriteString("#\n")
	sb.WriteString("# Effect of the mask alone, measured on the model as loaded (before any\n")
	sb.WriteString("# --iceberg-compat pruning, which narrows it further):\n")
	fmt.Fprintf(&sb, "#   leaf columns:  %d -> %d\n", res.Stats.LeafPathsBefore, res.Stats.LeafPathsAfter)
	fmt.Fprintf(&sb, "#   message types: %d -> %d\n", res.Stats.MessagesBefore, res.Stats.MessagesAfter)
	sb.WriteString("#\n")
	sb.WriteString("# Fields kept by the mask: <Message>.<field>, with [subtree] where a \"*\"\n")
	sb.WriteString("# path kept the field's whole target type. Everything else was dropped.\n")
	for _, k := range res.Kept {
		sb.WriteString(k.Message + "." + k.Field)
		if k.Subtree {
			sb.WriteString(" [subtree]")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n# Widening: leaf columns no mask path asked for, kept because the mask is\n")
	sb.WriteString("# applied per message type and these types are embedded at more than one\n")
	sb.WriteString("# path. Review these — they are part of the published contract.\n")
	if len(res.Widened) == 0 {
		sb.WriteString("# (none)\n")
	}
	for _, p := range res.Widened {
		sb.WriteString(p + "\n")
	}

	return GeneratedFile{Path: "ocsf/" + pkgSuffix + "/" + MaskReportName, Content: sb.String()}, nil
}

// ---------------------------------------------------------------------------
// Mask path validation
// ---------------------------------------------------------------------------

// validateMaskPath rejects paths that cannot be resolved structurally, before
// any schema lookup: empty paths or segments, and "*" anywhere but last.
func validateMaskPath(path string) error {
	if path == "" {
		return errors.New("ocsf mask: empty path")
	}
	segs := strings.Split(path, ".")
	for i, seg := range segs {
		if seg == "" {
			return fmt.Errorf("ocsf mask: path %q has an empty segment", path)
		}
		if seg == maskWildcard && i != len(segs)-1 {
			return fmt.Errorf("ocsf mask: path %q may only use %q as its final segment", path, maskWildcard)
		}
	}
	if segs[0] == maskWildcard {
		return fmt.Errorf("ocsf mask: path %q must name a class attribute before %q", path, maskWildcard)
	}
	return nil
}

// checkNotSubsumed returns an error when path is already covered by a different
// "*" subtree path in paths.
func checkNotSubsumed(path string, paths []string) error {
	for _, other := range paths {
		if other == path || !strings.HasSuffix(other, "."+maskWildcard) {
			continue
		}
		prefix := strings.TrimSuffix(other, "."+maskWildcard)
		if path == prefix || strings.HasPrefix(path, prefix+".") {
			return fmt.Errorf("ocsf mask: path %q is already covered by %q; remove one", path, other)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Mask resolution
// ---------------------------------------------------------------------------

// msgKey identifies a message in the schema model. A class and an object may
// share a name, so the kind is part of the identity.
type msgKey struct {
	isClass bool
	name    string
}

// keepSet is a resolved mask: per-message attribute allowlists, plus the objects
// a "*" path retains whole.
type keepSet struct {
	attrs   map[msgKey]map[string]bool
	subtree map[string]bool
	// subtreeEdges records the (owner, attr) edges a "*" path terminated on, so
	// the report can mark them.
	subtreeEdges map[msgKey]map[string]bool
}

// resolveMask walks every mask path through the schema, recording the attribute
// each step keeps. A path that does not resolve is an error: an allowlist that
// silently stops matching would quietly shrink the published contract on an OCSF
// bump, which is exactly the failure a mask is supposed to make loud.
func resolveMask(s *schema.Schema, classes []*schema.Class, m *Mask) (*keepSet, error) {
	keep := &keepSet{
		attrs:        make(map[msgKey]map[string]bool),
		subtree:      make(map[string]bool),
		subtreeEdges: make(map[msgKey]map[string]bool),
	}
	for _, path := range m.Paths {
		if err := keep.addPath(s, classes, path); err != nil {
			return nil, err
		}
	}
	// A "*" subtree keeps everything below it, so any type reachable from it is
	// retained whole too. Done after all paths resolve so the marking sees the
	// full set of subtree roots.
	for _, root := range sortedBoolKeys(keep.subtree) {
		keep.markSubtree(s, root)
	}
	return keep, nil
}

// addPath resolves one mask path and records every attribute it keeps.
func (k *keepSet) addPath(s *schema.Schema, classes []*schema.Class, path string) error {
	segs := strings.Split(path, ".")
	wholeSubtree := segs[len(segs)-1] == maskWildcard
	if wholeSubtree {
		segs = segs[:len(segs)-1]
	}

	// The first segment addresses a class attribute. It applies to every
	// selected class that has it — the merged message is the union of the
	// classes, so a path naming a base_event attribute must reach all of them.
	matched := false
	for _, cls := range classes {
		attr, ok := cls.Attributes[segs[0]]
		if !ok {
			continue
		}
		matched = true
		owner := msgKey{isClass: true, name: cls.Name}
		k.keep(owner, segs[0])
		if err := k.descend(s, owner, attr, path, segs[0], segs[1:], wholeSubtree); err != nil {
			return err
		}
	}
	if !matched {
		return fmt.Errorf(
			"ocsf mask: path %q does not resolve: no selected class has attribute %q",
			path, segs[0])
	}
	return nil
}

// descend walks the remaining segments of a mask path from attr, recording the
// attribute kept at each step. owner/consumed describe where the walk currently
// is, for error messages.
func (k *keepSet) descend(
	s *schema.Schema, owner msgKey, attr *schema.Attribute,
	path, consumed string, rest []string, wholeSubtree bool,
) error {
	// messageEdge deliberately treats a reference to an object the snapshot does
	// not define as a non-edge, because it emits as an empty stub message. That
	// would make a mask path ending there look like a scalar terminal and let the
	// mask produce `message Phantom {}` — so reject it explicitly instead, naming
	// the missing type.
	if missing, ok := absentObjectTarget(s, attr); ok {
		return fmt.Errorf(
			"ocsf mask: path %q reaches %q, whose object type %s is absent from the schema "+
				"snapshot and would emit as an empty message; re-run with a full schema export "+
				"or drop the path",
			path, consumed, toPascalCase(missing))
	}

	target, isEdge := messageEdge(s, attr)

	if len(rest) == 0 {
		switch {
		case wholeSubtree && !isEdge:
			return fmt.Errorf(
				"ocsf mask: path %q uses %q but %q is not a message-typed field; drop the %q",
				path, maskWildcard, consumed, maskWildcard)
		case wholeSubtree:
			k.subtree[target] = true
			k.markSubtreeEdge(owner, attr.Name)
		case isEdge:
			return fmt.Errorf(
				"ocsf mask: path %q ends on message-typed field %q (type %s); "+
					"extend the path to a scalar or write %q to keep the whole subtree",
				path, consumed, toPascalCase(target), consumed+"."+maskWildcard)
		default:
			// Ends on a scalar, which addPath/descend already recorded.
		}
		return nil
	}

	if !isEdge {
		return fmt.Errorf(
			"ocsf mask: path %q cannot descend past %q: it is not a message-typed field",
			path, consumed)
	}
	next := rest[0]
	nextAttr, ok := s.Objects[target].Attributes[next]
	if !ok {
		return fmt.Errorf(
			"ocsf mask: path %q does not resolve: message %s (at %q) has no attribute %q",
			path, toPascalCase(target), consumed, next)
	}
	nextOwner := msgKey{name: target}
	k.keep(nextOwner, next)
	return k.descend(s, nextOwner, nextAttr, path, consumed+"."+next, rest[1:], wholeSubtree)
}

// absentObjectTarget returns the referenced object name when attr is an object_t
// reference to a named, non-generic object that the schema snapshot does not
// define. Those emit as empty stub messages, so messageEdge classifies them as
// non-edges; mask resolution has to tell them apart from real scalars.
//
// The generic "object" bag is excluded: it legitimately maps to a scalar (R1
// demotes it to a JSON string), so a path may end on it.
func absentObjectTarget(s *schema.Schema, attr *schema.Attribute) (string, bool) {
	if attr.Type != objectTypeName || attr.ObjectType == "" || attr.ObjectType == genericObject {
		return "", false
	}
	if _, defined := s.Objects[attr.ObjectType]; defined {
		return "", false
	}
	return attr.ObjectType, true
}

// keep records that owner retains attr.
func (k *keepSet) keep(owner msgKey, attr string) {
	if k.attrs[owner] == nil {
		k.attrs[owner] = make(map[string]bool)
	}
	k.attrs[owner][attr] = true
}

// markSubtreeEdge records that (owner, attr) is where a "*" path terminated.
func (k *keepSet) markSubtreeEdge(owner msgKey, attr string) {
	if k.subtreeEdges[owner] == nil {
		k.subtreeEdges[owner] = make(map[string]bool)
	}
	k.subtreeEdges[owner][attr] = true
}

// markSubtree marks root and every object reachable from it as retained whole.
func (k *keepSet) markSubtree(s *schema.Schema, root string) {
	queue := []string{root}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		obj, ok := s.Objects[name]
		if !ok {
			continue
		}
		for _, attrName := range sortedKeys(obj.Attributes) {
			target, isEdge := messageEdge(s, obj.Attributes[attrName])
			if !isEdge || k.subtree[target] {
				continue
			}
			k.subtree[target] = true
			queue = append(queue, target)
		}
	}
}

// keptFields flattens the keep set into the sorted report shape. Objects
// retained whole by a "*" path contribute all their surviving attributes.
func (k *keepSet) keptFields(s *schema.Schema) []KeptField {
	var kept []KeptField
	add := func(msgName string, attrs []string, subtreeEdges map[string]bool) {
		for _, attrName := range attrs {
			kept = append(kept, KeptField{
				Message: msgName,
				Field:   attrName,
				Subtree: subtreeEdges[attrName],
			})
		}
	}
	for owner, attrs := range k.attrs {
		if !owner.isClass && k.subtree[owner.name] {
			continue // covered by the subtree pass below
		}
		add(toPascalCase(owner.name), sortedBoolKeys(attrs), k.subtreeEdges[owner])
	}
	for _, objName := range sortedBoolKeys(k.subtree) {
		obj, ok := s.Objects[objName]
		if !ok {
			continue
		}
		add(toPascalCase(objName), sortedKeys(obj.Attributes), nil)
	}

	sort.Slice(kept, func(i, j int) bool {
		if kept[i].Message != kept[j].Message {
			return kept[i].Message < kept[j].Message
		}
		return kept[i].Field < kept[j].Field
	})
	return kept
}

// ---------------------------------------------------------------------------
// Post-conditions, stats and widening
// ---------------------------------------------------------------------------

// verifyMaskInvariants checks the masked model is emittable: no class and no
// reachable object may be left without fields. By construction a type is only
// reachable after masking because a path kept an attribute on it, so a violation
// is a resolver bug and must fail generation rather than emit an empty message.
func verifyMaskInvariants(s *schema.Schema, classes []*schema.Class) error {
	for _, cls := range classes {
		if len(cls.Attributes) == 0 {
			return fmt.Errorf("ocsf mask: class %q has no fields left; widen the mask", cls.Name)
		}
	}
	for _, objName := range reachableObjectNames(s, classes) {
		if len(s.Objects[objName].Attributes) == 0 {
			return fmt.Errorf(
				"ocsf mask: message %q is still reachable but has no fields left; widen the mask",
				toPascalCase(objName))
		}
	}
	return nil
}

// leafPaths returns every distinct dotted scalar-leaf path reachable from the
// selected classes, sorted — the columns an Iceberg/Oxla reader sees.
//
// The pre-mask model may still be cyclic (masking runs before R2), so an edge
// back to a type already on the current path is counted as a leaf at that edge,
// which is what R2's demotion to a JSON string will make it.
func leafPaths(s *schema.Schema, classes []*schema.Class) []string {
	seen := make(map[string]bool)

	var walk func(attrs map[string]*schema.Attribute, prefix string, stack map[string]bool)
	walk = func(attrs map[string]*schema.Attribute, prefix string, stack map[string]bool) {
		for _, attrName := range sortedKeys(attrs) {
			path := attrName
			if prefix != "" {
				path = prefix + "." + attrName
			}
			target, isEdge := messageEdge(s, attrs[attrName])
			if !isEdge || stack[target] {
				seen[path] = true
				continue
			}
			stack[target] = true
			walk(s.Objects[target].Attributes, path, stack)
			delete(stack, target)
		}
	}
	for _, cls := range classes {
		walk(cls.Attributes, "", map[string]bool{})
	}

	return sortedBoolKeys(seen)
}

// widenedPaths returns the leaf paths that no mask path asked for. These arise
// where a type kept for one embedding is also reachable from another: the mask
// is applied per type, so the fields come along at every surviving embedding.
func widenedPaths(leaves []string, m *Mask) []string {
	var widened []string
	for _, leaf := range leaves {
		if !maskAsksFor(leaf, m) {
			widened = append(widened, leaf)
		}
	}
	return widened
}

// maskAsksFor reports whether some mask path names leaf explicitly or covers it
// with a "*" subtree.
func maskAsksFor(leaf string, m *Mask) bool {
	for _, p := range m.Paths {
		if p == leaf {
			return true
		}
		if !strings.HasSuffix(p, "."+maskWildcard) {
			continue
		}
		prefix := strings.TrimSuffix(p, "."+maskWildcard)
		if leaf == prefix || strings.HasPrefix(leaf, prefix+".") {
			return true
		}
	}
	return false
}

// maskClasses resolves and sorts the selected classes, mirroring
// PruneForIceberg's contract so both stages see the same roots in the same
// order.
func maskClasses(s *schema.Schema, classNames []string) ([]*schema.Class, error) {
	classes := make([]*schema.Class, 0, len(classNames))
	for _, name := range classNames {
		cls, ok := s.Classes[name]
		if !ok {
			return nil, fmt.Errorf("ocsf mask: class %q not found in schema", name)
		}
		classes = append(classes, cls)
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i].Name < classes[j].Name })
	return classes, nil
}

// sortedBoolKeys returns the true-valued keys of m, sorted.
func sortedBoolKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}
