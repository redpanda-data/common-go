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
	"strings"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/schema"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/tagmap"
)

// ReserveTags assigns a field number to every attribute of every message the
// emitters can produce — the selected classes, their full object closure, and
// the merged message's attribute union — and must run BEFORE anything narrows
// the model (MaskFields, PruneForIceberg).
//
// It makes field numbers a function of the OCSF schema and the class selection
// alone, never of what a mask or prune chose to emit.
//
// # Why this is needed
//
// tagmap.Assign hands out the lowest free number per message, so the numbers
// depend on WHICH attributes it is asked about. Without this pass, bootstrapping
// a tagmap while a mask is active walks only the retained attributes and
// compacts them from 1: on the OCSF 1.8.0 audit-log mask every one of the 20
// surviving AuditEvent fields moves (actor 7 -> 3, time 61 -> 19, type_uid
// 66 -> 20). Nothing detects that, and every previously written record decodes
// to an empty event.
//
// Reserving from the unmasked model instead means a from-scratch tagmap is
// identical whether or not a mask is in play, so deleting the tagmap file is
// recoverable rather than silently destructive. It also makes the
// wire-stability contract structural rather than incidental: an excluded
// attribute keeps its recorded number, so adding it back to the mask later
// restores the number it always had.
//
// This does NOT emit anything. The tagmap is a build-time ledger of "what
// number would this attribute get"; the mask alone decides which attributes
// reach the generated protos.
//
// NOTE: the tagmap file remains the source of truth ACROSS OCSF versions —
// reserving is deterministic only for a fixed schema. A new OCSF release that
// inserts an alphabetically-early attribute shifts every later number, which is
// exactly what the committed tagmap prevents. This pass removes the mask's
// influence on numbering; it is not a substitute for committing the tagmap.
//
// Ordering matches emitMessage exactly (attributes sorted by name, per
// message), so the numbers are the same ones the emitters would assign. Order
// ACROSS messages is irrelevant: Assign allocates per message independently.
func ReserveTags(s *schema.Schema, classNames []string, mergedMessage string, tm *tagmap.TagMap) error {
	classes, objects, err := SelectClosure(s, classNames)
	if err != nil {
		return err
	}

	reserve := func(msgName string, attrs map[string]*schema.Attribute) error {
		for _, attrName := range sortedKeys(attrs) {
			if _, err := tm.Assign(msgName, attrName); err != nil {
				return fmt.Errorf("ocsf reserve: %s.%s: %w", msgName, attrName, err)
			}
		}
		return nil
	}

	for i := range classes {
		if err := reserve(ClassMessageName(classes[i].Name), classes[i].Attributes); err != nil {
			return err
		}
	}
	for i := range objects {
		if err := reserve(toPascalCase(objects[i].Name), objects[i].Attributes); err != nil {
			return err
		}
	}

	if strings.TrimSpace(mergedMessage) == "" {
		return nil
	}

	// The merged message owns its own tag lineage, keyed by mergedMessage. Merge
	// the UNMASKED classes so its numbering is likewise mask-independent. An
	// error here means an unmasked run would fail too, so it is propagated
	// rather than skipped.
	merged, err := MergeClasses(s, classNames, mergedMessage)
	if err != nil {
		return fmt.Errorf("ocsf reserve: merge classes for %q: %w", mergedMessage, err)
	}
	return reserve(merged.Name, merged.Attributes)
}
