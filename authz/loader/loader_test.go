// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package loader

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/redpanda-data/common-go/authz"
)

const policyOne = `roles:
  - id: admin
    permissions:
      - read
      - write
      - delete
  - id: viewer
    permissions:
      - read
bindings:
  - role: admin
    principal: user:alice@acme.com
    scope: organizations/acme
  - role: viewer
    principal: user:bob@acme.com
    scope: organizations/acme/dataplanes/prod
`

const policyTwo = `roles:
  - id: admin
    permissions:
      - read
      - write
      - delete
  - id: viewer
    permissions:
      - read
bindings:
  - role: admin
    principal: user:alice@acme.com
    scope: organizations/acme
  - role: viewer
    principal: user:bob@acme.com
    scope: organizations/acme/dataplanes/prod
  - role: viewer
    principal: user:joe@acme.com
    scope: organizations/acme
`

var expectedPolicyOne authz.Policy = authz.Policy{
	Roles: []authz.Role{
		{
			ID: "admin",
			Permissions: []authz.PermissionName{
				"read", "write", "delete",
			},
		},
		{
			ID:          "viewer",
			Permissions: []authz.PermissionName{"read"},
		},
	},
	Bindings: []authz.RoleBinding{
		{
			Role:      "admin",
			Principal: "user:alice@acme.com",
			Scope:     "organizations/acme",
		},
		{
			Role:      "viewer",
			Principal: "user:bob@acme.com",
			Scope:     "organizations/acme/dataplanes/prod",
		},
	},
}

var expectedPolicyTwo authz.Policy = authz.Policy{
	Roles: expectedPolicyOne.Roles,
	Bindings: append(expectedPolicyOne.Bindings, authz.RoleBinding{
		Role:      "viewer",
		Principal: "user:joe@acme.com",
		Scope:     "organizations/acme",
	}),
}

func TestFileLoader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(policyOne), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	actual, err := LoadPolicyFromFile(path)
	if err != nil {
		t.Fatalf("failed to load policy: %v", err)
	}
	if !reflect.DeepEqual(actual, expectedPolicyOne) {
		t.Errorf("expected %v, want %v", actual, expectedPolicyOne)
	}
}

func TestFileWatcher(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dir")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("failed to make dir: %v", err)
	}
	path := filepath.Join(dir, "rbac_policy.yaml")
	if err := os.WriteFile(path, []byte(policyOne), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	policies := make(chan authz.Policy)
	policy, unwatch, err := WatchPolicyFile(path, func(policy authz.Policy, err error) {
		if err != nil {
			t.Errorf("error while watching file: %v", err)
		}
		policies <- policy
	})
	if err != nil {
		t.Fatalf("failed to watch file: %v", err)
	}
	defer unwatch()
	if !reflect.DeepEqual(policy, expectedPolicyOne) {
		t.Errorf("expected %v, want %v", policy, expectedPolicyOne)
	}
	tmpPath := filepath.Join(dir, "rbac_policy.tmp.yaml")
	if err := os.WriteFile(tmpPath, []byte(policyTwo), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		t.Fatalf("failed to rename file: %v", err)
	}
	select {
	case policy = <-policies:
	case <-t.Context().Done():
		t.Error("test timed out")
	}
	if !reflect.DeepEqual(policy, expectedPolicyTwo) {
		t.Errorf("expected %v, want %v", policy, expectedPolicyTwo)
	}
}
