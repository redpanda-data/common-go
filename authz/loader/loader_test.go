// Copyright 2025 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package loader

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

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
  - role_id: admin
    principal: user:alice@acme.com
    scope: organization/acme
  - role_id: viewer
    principal: user:bob@acme.com
    scope: organization/acme/dataplane/prod
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
  - role_id: admin
    principal: user:alice@acme.com
    scope: organization/acme
  - role_id: viewer
    principal: user:bob@acme.com
    scope: organization/acme/dataplane/prod
  - role_id: viewer
    principal: user:joe@acme.com
    scope: organization/acme
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
			RoleID:    "admin",
			Principal: "user:alice@acme.com",
			Scope:     "organization/acme",
		},
		{
			RoleID:    "viewer",
			Principal: "user:bob@acme.com",
			Scope:     "organization/acme/dataplane/prod",
		},
	},
}

var expectedPolicyTwo *authz.Policy = &authz.Policy{
	Roles: expectedPolicyOne.Roles,
	Bindings: append(expectedPolicyOne.Bindings, authz.RoleBinding{
		RoleID:    "viewer",
		Principal: "user:joe@acme.com",
		Scope:     "organization/acme",
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
	if reflect.DeepEqual(actual, expectedPolicyOne) {
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
	if reflect.DeepEqual(policy, expectedPolicyOne) {
		t.Errorf("expected %v, want %v", policy, expectedPolicyOne)
	}
	tmpPath := filepath.Join(dir, "rbac_policy.tmp.yaml")
	if err := os.WriteFile(tmpPath, []byte(policyTwo), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	// HACK: There is no way to ensure the goroutine that watches the files has kicked off yet,
	// because it's happening in the background, so just add a sleep here to ensure we don't miss
	// anything.
	time.Sleep(5 * time.Millisecond)
	if err := os.Rename(tmpPath, path); err != nil {
		t.Fatalf("failed to rename file: %v", err)
	}
	select {
	case policy = <-policies:
	case <-t.Context().Done():
		t.Error("test timed out")
	}
	if reflect.DeepEqual(policy, expectedPolicyTwo) {
		t.Errorf("expected %v, want %v", policy, expectedPolicyTwo)
	}
}
