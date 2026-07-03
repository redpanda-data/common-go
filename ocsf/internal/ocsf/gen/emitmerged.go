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
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/tagmap"
)

// demotedComment is emitted above every field demoted from a nested enum to a
// plain scalar in the merged message.
const demotedComment = "Class-scoped enum: value semantics depend on class_uid; see TypeUid."

// EmitMerged produces the single-event proto3 layout: one flat message (named
// msgName, e.g. "AuditEvent") holding the union of all attributes across the
// named classes, plus the shared objects.proto with the transitive object
// closure. This is the layout for a single audit-log topic carrying events of
// every selected class.
//
// Unlike Emit (one message per class), the merged message:
//
//   - Unions attributes across classes per MergeClasses. Shared base_event
//     attributes appear once with one tag; class-owned attributes are plain
//     optional fields.
//   - Demotes class-scoped enums (activity_id, and any enum whose values
//     conflict across classes) to plain scalars, with an explanatory comment.
//   - Marks a field (buf.validate.field).required only when it is required in
//     EVERY selected class; per-class requiredness and class-field ownership
//     are enforced with class_uid-gated message-level CEL instead, so a
//     protovalidate pass on the merged message validates exactly the class the
//     event claims to be.
//   - Adds the OCSF type_uid consistency rule
//     (type_uid == class_uid * 100 + activity_id) as message-level CEL.
//
// Field numbers are keyed by (msgName, attribute) in tm: a fresh lineage,
// independent of the per-class messages, append-only across class additions.
// Object messages reuse their existing (object, attribute) keys, so object
// tags are identical between Emit and EmitMerged output.
//
// stubbed follows the same contract as Emit.
func EmitMerged(s *schema.Schema, classNames []string, tm *tagmap.TagMap, version, msgName string) (files []GeneratedFile, stubbed []string, err error) {
	merged, err := MergeClasses(s, classNames, msgName)
	if err != nil {
		return nil, nil, err
	}

	// Object closure is the union across all selected classes — identical to
	// what the per-class layout needs, so SelectClosure is reused as-is.
	classes, objects, err := SelectClosure(s, classNames)
	if err != nil {
		return nil, nil, err
	}

	pkgSuffix, err := versionToPackage(version)
	if err != nil {
		return nil, nil, err
	}
	dir := "ocsf/" + pkgSuffix

	stubs := collectStubs(s, classes, objects)
	hasObjects := len(objects) > 0 || len(stubs) > 0

	classFile, err := emitMergedClassFile(s, tm, merged, version, pkgSuffix, dir, hasObjects)
	if err != nil {
		return nil, nil, err
	}

	objFile, err := emitObjectsFile(s, tm, version, pkgSuffix, dir, objects, stubs)
	if err != nil {
		return nil, nil, err
	}

	files = []GeneratedFile{classFile, objFile}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	var stubbedNames []string
	if len(stubs) > 0 {
		stubbedNames = stubs
	}
	return files, stubbedNames, nil
}

// EmitMergedSRSchema produces the single self-contained Schema-Registry schema
// for the merged event message: the merged message first (Confluent
// message-index 0), then every object in the union closure inlined, then
// stubs. Same contract as EmitSRSchemas (no buf/validate, no cross-file
// imports, tags from the shared tagmap), but one file for the whole topic
// instead of one per class.
//
// The returned file's Path is "<snake(msgName)>.sr.proto" (e.g.
// "audit_event.sr.proto").
func EmitMergedSRSchema(s *schema.Schema, classNames []string, tm *tagmap.TagMap, version, msgName string) (GeneratedFile, error) {
	merged, err := MergeClasses(s, classNames, msgName)
	if err != nil {
		return GeneratedFile{}, err
	}

	classes, objects, err := SelectClosure(s, classNames)
	if err != nil {
		return GeneratedFile{}, err
	}

	pkgSuffix, err := versionToPackage(version)
	if err != nil {
		return GeneratedFile{}, err
	}

	opts := emitOptions{
		omitValidate:  true,
		fieldComments: demotedFieldComments(merged),
	}

	var body strings.Builder

	// (1) Merged event message FIRST → Confluent message-index 0.
	classMsg, err := emitMessage(s, tm, merged.Name, merged.Attributes, nil, opts)
	if err != nil {
		return GeneratedFile{}, fmt.Errorf("ocsf sr emit: merged %q: %w", merged.Name, err)
	}
	body.WriteString(classMsg)
	body.WriteString("\n")

	// (2) Objects in the union closure, sorted by PascalCase message name.
	sorted := make([]schema.Object, len(objects))
	copy(sorted, objects)
	sort.Slice(sorted, func(i, j int) bool {
		return toPascalCase(sorted[i].Name) < toPascalCase(sorted[j].Name)
	})
	for i := range sorted {
		msg, emitErr := emitMessage(s, tm, toPascalCase(sorted[i].Name), sorted[i].Attributes, sorted[i].Constraints, emitOptions{omitValidate: true})
		if emitErr != nil {
			return GeneratedFile{}, fmt.Errorf("ocsf sr emit: object %q: %w", sorted[i].Name, emitErr)
		}
		body.WriteString(msg)
		body.WriteString("\n")
	}

	// (3) Stub messages for referenced-but-absent objects.
	stubs := collectStubs(s, classes, objects)
	for _, stubName := range stubs {
		body.WriteString("// Stub: referenced object not present in this schema snapshot.\n")
		body.WriteString("message " + stubName + " {}\n\n")
	}

	needStruct := mergedNeedsStruct(s, merged)
	if !needStruct {
		needStruct, _ = detectImports(s, nil, objects)
	}

	var imports []string
	if needStruct {
		imports = append(imports, `import "google/protobuf/struct.proto";`)
	}

	content := fileHeader(version, pkgSuffix) + importBlock(imports) + body.String()
	return GeneratedFile{Path: msgFileBase(msgName) + ".sr.proto", Content: content}, nil
}

// emitMergedClassFile builds the proto file holding the merged event message.
func emitMergedClassFile(s *schema.Schema, tm *tagmap.TagMap, merged *Merged, version, pkgSuffix, dir string, hasObjects bool) (GeneratedFile, error) {
	opts := emitOptions{
		fieldComments: demotedFieldComments(merged),
		extraCEL:      mergedCEL(merged),
	}

	msg, err := emitMessage(s, tm, merged.Name, merged.Attributes, nil, opts)
	if err != nil {
		return GeneratedFile{}, fmt.Errorf("ocsf emit: merged %q: %w", merged.Name, err)
	}

	needObjects := hasObjects && attrsReferenceObject(merged.Attributes)
	needStruct := mergedNeedsStruct(s, merged)
	needValidate := len(opts.extraCEL) > 0 || anyRequired(merged.Attributes)

	var imports []string
	if needObjects {
		imports = append(imports, `import "`+dir+"/"+objectsFileName+`";`)
	}
	if needStruct {
		imports = append(imports, `import "google/protobuf/struct.proto";`)
	}
	if needValidate {
		imports = append(imports, `import "buf/validate/validate.proto";`)
	}

	content := fileHeader(version, pkgSuffix) + importBlock(imports) + msg + "\n"
	return GeneratedFile{Path: dir + "/" + msgFileBase(merged.Name) + ".proto", Content: content}, nil
}

// mergedCEL builds the message-level CEL blocks for the merged event message,
// in deterministic order:
//
//  1. type_uid consistency (OCSF invariant: type_uid = class_uid*100 + activity_id),
//  2. per-attribute class ownership (fields owned by a strict subset of the
//     selected classes may only be set when class_uid is one of the owners),
//  3. per-class conditional requiredness (attributes required by class C but
//     not by every class must be present when class_uid == C),
//  4. per-class at_least_one / just_one constraints, gated on class_uid.
func mergedCEL(merged *Merged) []string {
	var cels []string
	cels = append(cels, typeUIDConsistencyCEL(merged)...)
	cels = append(cels, ownershipCELs(merged)...)
	cels = append(cels, condRequiredCELs(merged)...)
	cels = append(cels, gatedConstraintCELs(merged)...)
	return cels
}

// typeUIDConsistencyCEL builds the type_uid = class_uid*100 + activity_id
// rule. All three are base_event attributes, so they are always present in a
// merge; guard anyway for partial fixtures.
func typeUIDConsistencyCEL(merged *Merged) []string {
	if !hasAll(merged.Attributes, "type_uid", "class_uid", "activity_id") {
		return nil
	}
	return []string{celOption(
		merged.Name+".type_uid",
		"type_uid must equal class_uid * 100 + activity_id",
		"this.type_uid == this.class_uid * 100 + this.activity_id",
	)}
}

// ownershipCELs gates attributes owned by a strict subset of the selected
// classes on class_uid. Attributes owned by every class need no gate.
func ownershipCELs(merged *Merged) []string {
	total := len(merged.Classes)
	uidByClass := make(map[string]int, total)
	for _, cls := range merged.Classes {
		uidByClass[cls.Name] = cls.UID
	}

	var cels []string
	for _, attrName := range sortedKeys(merged.Attributes) {
		classOwners := merged.Owners[attrName]
		if len(classOwners) == 0 || len(classOwners) == total {
			continue
		}
		uids := make([]string, 0, len(classOwners))
		for _, owner := range classOwners {
			uids = append(uids, fmt.Sprintf("%d", uidByClass[owner]))
		}
		var cond string
		if len(uids) == 1 {
			cond = "this.class_uid == " + uids[0]
		} else {
			cond = "this.class_uid in [" + strings.Join(uids, ", ") + "]"
		}
		cels = append(cels, celOption(
			merged.Name+".own."+attrName,
			attrName+" is only valid for class_uid "+strings.Join(uids, ", "),
			"!has(this."+attrName+") || "+cond,
		))
	}
	return cels
}

// condRequiredCELs enforces per-class requiredness for attributes required by
// one class but not blanket-required (those already carry the field-level
// annotation). Repeated attributes are skipped: OCSF "required" on an array
// means only "key present", an empty list is valid, mirroring the per-class
// emitter's rule.
func condRequiredCELs(merged *Merged) []string {
	var cels []string
	for _, cls := range merged.Classes {
		reqAttrs := make([]string, 0, 8)
		for attrName, attr := range cls.Attributes {
			mergedAttr := merged.Attributes[attrName]
			if attr.Requirement == requirementRequired && mergedAttr.Requirement != requirementRequired && !attr.IsArray {
				reqAttrs = append(reqAttrs, attrName)
			}
		}
		sort.Strings(reqAttrs)
		for _, attrName := range reqAttrs {
			cels = append(cels, celOption(
				fmt.Sprintf("%s.req.%s.%s", merged.Name, cls.Name, attrName),
				fmt.Sprintf("%s is required when class_uid == %d (%s)", attrName, cls.UID, cls.Name),
				fmt.Sprintf("this.class_uid != %d || has(this.%s)", cls.UID, attrName),
			))
		}
	}
	return cels
}

// gatedConstraintCELs re-emits each class's at_least_one/just_one constraints
// gated on class_uid.
func gatedConstraintCELs(merged *Merged) []string {
	var cels []string
	for _, cls := range merged.Classes {
		if cls.Constraints == nil {
			continue
		}
		gate := fmt.Sprintf("this.class_uid != %d || ", cls.UID)
		if len(cls.Constraints.AtLeastOne) > 0 {
			expr, fieldList := atLeastOneExpr(cls.Constraints.AtLeastOne)
			cels = append(cels, celOption(
				fmt.Sprintf("%s.constraint.%s.at_least_one", merged.Name, cls.Name),
				fmt.Sprintf("at least one of %s must be set when class_uid == %d (%s)", fieldList, cls.UID, cls.Name),
				gate+"("+expr+")",
			))
		}
		if len(cls.Constraints.JustOne) > 0 {
			expr, fieldList := justOneExpr(cls.Constraints.JustOne)
			cels = append(cels, celOption(
				fmt.Sprintf("%s.constraint.%s.just_one", merged.Name, cls.Name),
				fmt.Sprintf("exactly one of %s must be set when class_uid == %d (%s)", fieldList, cls.UID, cls.Name),
				gate+"("+expr+")",
			))
		}
	}
	return cels
}

// demotedFieldComments maps each demoted attribute to its emitted comment.
func demotedFieldComments(merged *Merged) map[string]string {
	if len(merged.Demoted) == 0 {
		return nil
	}
	comments := make(map[string]string, len(merged.Demoted))
	for _, name := range merged.Demoted {
		comments[name] = demotedComment
	}
	return comments
}

// mergedNeedsStruct reports whether any merged attribute resolves to
// google.protobuf.Value.
func mergedNeedsStruct(s *schema.Schema, merged *Merged) bool {
	for _, attr := range merged.Attributes {
		pt, err := resolveProtoType(s, *attr)
		if err != nil {
			continue
		}
		if pt.WellKnown == wellKnownValueType {
			return true
		}
	}
	return false
}

// attrsReferenceObject reports whether any attribute points at a (non-generic)
// object type.
func attrsReferenceObject(attrs map[string]*schema.Attribute) bool {
	for _, attr := range attrs {
		if attr.Type == objectTypeName && attr.ObjectType != "" && attr.ObjectType != genericObject {
			return true
		}
	}
	return false
}

// anyRequired reports whether any non-repeated attribute is required.
// Repeated attributes never carry the field-level required annotation (see
// emitMessage), so they must not force the buf/validate import either.
func anyRequired(attrs map[string]*schema.Attribute) bool {
	for _, attr := range attrs {
		if attr.Requirement == requirementRequired && !attr.IsArray {
			return true
		}
	}
	return false
}

// hasAll reports whether every named attribute exists in attrs.
func hasAll(attrs map[string]*schema.Attribute, names ...string) bool {
	for _, name := range names {
		if _, ok := attrs[name]; !ok {
			return false
		}
	}
	return true
}

// msgFileBase converts a PascalCase message name to its snake_case file base:
// "AuditEvent" → "audit_event".
func msgFileBase(msgName string) string {
	return strings.ToLower(toUpperSnake(msgName))
}
