// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package conformance_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	ocsfv1 "github.com/redpanda-data/common-go/ocsf/internal/ocsf/conformance/genpb/ocsf/v1"
)

// TestGenPBInSyncWithGolden guards the manual `buf generate` step that
// produces the committed genpb bindings: for each event message, every field
// (name and tag) in the compiled descriptor must match the committed golden
// .proto exactly. Without this, editing a golden and forgetting to regenerate
// genpb would leave every conformance test passing against STALE bindings.
func TestGenPBInSyncWithGolden(t *testing.T) {
	cases := []struct {
		goldenPath string
		msgName    string
		msg        proto.Message
	}{
		{"../gen/testdata/golden-merged/ocsf/v1/audit_event.proto", "AuditEvent", &ocsfv1.AuditEvent{}},
		{"../gen/testdata/golden/ocsf/v1/api_activity.proto", "ApiActivity", &ocsfv1.ApiActivity{}},
		{"../gen/testdata/golden/ocsf/v1/entity_management.proto", "EntityManagement", &ocsfv1.EntityManagement{}},
	}

	for _, tc := range cases {
		t.Run(tc.msgName, func(t *testing.T) {
			goldenTags := parseGoldenFieldTags(t, tc.goldenPath, tc.msgName)
			require.NotEmpty(t, goldenTags)

			fields := tc.msg.ProtoReflect().Descriptor().Fields()
			descTags := make(map[string]int32, fields.Len())
			for i := range fields.Len() {
				fd := fields.Get(i)
				descTags[string(fd.Name())] = int32(fd.Number())
			}

			require.Equal(t, goldenTags, descTags,
				"genpb bindings are out of sync with %s — run `buf generate` with the genpb templates and commit the diff",
				tc.goldenPath)
		})
	}
}

// TestGenPBObjectsInSyncWithGolden extends the sync guard to a load-bearing
// shared object (Metadata) from objects.proto, so a stale objects.pb.go is
// caught too.
func TestGenPBObjectsInSyncWithGolden(t *testing.T) {
	goldenTags := parseGoldenFieldTags(t, "../gen/testdata/golden/ocsf/v1/objects.proto", "Metadata")
	require.NotEmpty(t, goldenTags)

	fields := (&ocsfv1.Metadata{}).ProtoReflect().Descriptor().Fields()
	descTags := make(map[string]int32, fields.Len())
	for i := range fields.Len() {
		fd := fields.Get(i)
		descTags[string(fd.Name())] = int32(fd.Number())
	}
	require.Equal(t, goldenTags, descTags,
		"genpb objects.pb.go is out of sync with the golden objects.proto")
}

// fieldLineRE matches a proto field declaration line inside a message body:
// optional "repeated", a type name (possibly dotted), a snake_case field
// name, and the tag. Options in [...] and trailing comments are tolerated.
var fieldLineRE = regexp.MustCompile(`^\s*(?:repeated\s+)?[A-Za-z0-9_.]+\s+([a-z0-9_]+)\s*=\s*(\d+)\s*[;\[]`)

// parseGoldenFieldTags extracts field name -> tag from the named top-level
// message in a golden .proto file.
func parseGoldenFieldTags(t *testing.T, path, msgName string) map[string]int32 {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	require.NoError(t, err, "golden proto %s must be readable", path)

	content := string(raw)
	start := strings.Index(content, "message "+msgName+" {")
	require.GreaterOrEqual(t, start, 0, "message %s not found in %s", msgName, path)
	end := strings.Index(content[start:], "\n}\n")
	require.GreaterOrEqual(t, end, 0)
	body := content[start : start+end]

	tags := make(map[string]int32)
	depth := 0
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip nested enum bodies and option blocks.
		if strings.HasPrefix(trimmed, "enum ") || strings.HasPrefix(trimmed, "option ") {
			depth++
		}
		if depth > 0 {
			if strings.HasSuffix(trimmed, "}") || strings.HasSuffix(trimmed, "};") {
				depth--
			}
			continue
		}
		m := fieldLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		var tag int32
		for _, c := range m[2] {
			tag = tag*10 + c - '0'
		}
		tags[m[1]] = tag
	}
	return tags
}
