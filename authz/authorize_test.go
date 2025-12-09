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

package authz_test

import (
	"fmt"
	"testing"

	"github.com/redpanda-data/common-go/authz"
)

// Example demonstrates how to use the authorization module to check permissions
// for principals on hierarchical resources.
func Example() {
	// Define a policy with roles and bindings
	policy := authz.Policy{
		Roles: []authz.Role{
			{
				ID:          "mcp.admin",
				Permissions: []authz.PermissionName{"tool_invoke", "tool_list"},
			},
			{
				ID:          "mcp.user",
				Permissions: []authz.PermissionName{"tool_invoke"},
			},
		},
		Bindings: []authz.RoleBinding{
			{
				Role:      "mcp.admin",
				Principal: "user:alice",
				Scope:     "organization/acme/resourcegroup/foo/dataplane/bar",
			},
			{
				Role:      "mcp.user",
				Principal: "user:bob",
				Scope:     "organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/qux",
			},
		},
	}

	// Create a resource policy for a specific resource with the permissions we need
	resource := authz.ResourceName("organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/qux")
	resourcePolicy, err := authz.NewResourcePolicy(policy, resource, []authz.PermissionName{"tool_invoke"})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// Get an authorizer for the tool_invoke permission (O(1) lookup)
	authorizer := resourcePolicy.Authorizer("tool_invoke")

	// Check if principals have permission
	// Alice has admin role at dataplane level, which inherits to child MCP server
	fmt.Printf("Alice has tool_invoke permission: %v\n", authorizer.Check("user:alice"))

	// Bob has user role at the MCP server level
	fmt.Printf("Bob has tool_invoke permission: %v\n", authorizer.Check("user:bob"))

	// Charlie has no role binding
	fmt.Printf("Charlie has tool_invoke permission: %v\n", authorizer.Check("user:charlie"))

	// Output:
	// Alice has tool_invoke permission: true
	// Bob has tool_invoke permission: true
	// Charlie has tool_invoke permission: false
}

func TestCompileAuthorizer(t *testing.T) {
	policy := authz.Policy{
		Roles: []authz.Role{
			{
				ID:          "admin",
				Permissions: []authz.PermissionName{"read", "write", "delete"},
			},
			{
				ID:          "editor",
				Permissions: []authz.PermissionName{"read", "write"},
			},
			{
				ID:          "viewer",
				Permissions: []authz.PermissionName{"read"},
			},
		},
		Bindings: []authz.RoleBinding{
			{
				Role:      "admin",
				Principal: "user:alice",
				Scope:     "organization/acme",
			},
			{
				Role:      "editor",
				Principal: "user:bob",
				Scope:     "organization/acme/resourcegroup/foo",
			},
			{
				Role:      "viewer",
				Principal: "user:charlie",
				Scope:     "organization/acme/resourcegroup/foo/dataplane/bar",
			},
		},
	}

	tests := []struct {
		name       string
		resource   authz.ResourceName
		permission authz.PermissionName
		principal  authz.PrincipalID
		want       bool
	}{
		{
			name:       "admin at root has permission on deep resource",
			resource:   "organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/qux",
			permission: "delete",
			principal:  "user:alice",
			want:       true,
		},
		{
			name:       "editor at mid-level has permission on deep resource",
			resource:   "organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/qux",
			permission: "write",
			principal:  "user:bob",
			want:       true,
		},
		{
			name:       "viewer at leaf has permission on same resource",
			resource:   "organization/acme/resourcegroup/foo/dataplane/bar",
			permission: "read",
			principal:  "user:charlie",
			want:       true,
		},
		{
			name:       "viewer does not have write permission",
			resource:   "organization/acme/resourcegroup/foo/dataplane/bar",
			permission: "write",
			principal:  "user:charlie",
			want:       false,
		},
		{
			name:       "editor does not have delete permission",
			resource:   "organization/acme/resourcegroup/foo/dataplane/bar",
			permission: "delete",
			principal:  "user:bob",
			want:       false,
		},
		{
			name:       "unknown principal has no permission",
			resource:   "organization/acme/resourcegroup/foo/dataplane/bar",
			permission: "read",
			principal:  "user:unknown",
			want:       false,
		},
		{
			name:       "viewer permission does not propagate to parent",
			resource:   "organization/acme/resourcegroup/foo",
			permission: "read",
			principal:  "user:charlie",
			want:       false,
		},
		{
			name:       "permission check at root resource",
			resource:   "",
			permission: "read",
			principal:  "user:alice",
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resourcePolicy, err := authz.NewResourcePolicy(policy, tt.resource, []authz.PermissionName{tt.permission})
			if err != nil {
				t.Fatalf("NewResourcePolicy() error = %v", err)
			}
			authorizer := resourcePolicy.Authorizer(tt.permission)
			got := authorizer.Check(tt.principal)
			if got != tt.want {
				t.Errorf("Authorizer.Check(%v) = %v, want %v", tt.principal, got, tt.want)
			}
		})
	}
}

func TestCompileAuthorizer_MissingRole(t *testing.T) {
	policy := authz.Policy{
		Roles: []authz.Role{
			{
				ID:          "admin",
				Permissions: []authz.PermissionName{"read"},
			},
		},
		Bindings: []authz.RoleBinding{
			{
				Role:      "nonexistent",
				Principal: "user:alice",
				Scope:     "organization/acme",
			},
		},
	}

	_, err := authz.NewResourcePolicy(policy, "organization/acme", []authz.PermissionName{"read"})
	if err == nil {
		t.Fatal("NewResourcePolicy() expected error for missing role, got nil")
	}
}

func TestCompileAuthorizer_MultipleBindings(t *testing.T) {
	policy := authz.Policy{
		Roles: []authz.Role{
			{
				ID:          "role1",
				Permissions: []authz.PermissionName{"read"},
			},
			{
				ID:          "role2",
				Permissions: []authz.PermissionName{"write"},
			},
		},
		Bindings: []authz.RoleBinding{
			{
				Role:      "role1",
				Principal: "user:alice",
				Scope:     "organization/acme",
			},
			{
				Role:      "role2",
				Principal: "user:alice",
				Scope:     "organization/acme/resourcegroup/foo",
			},
		},
	}

	resourcePolicy, err := authz.NewResourcePolicy(policy, "organization/acme/resourcegroup/foo", []authz.PermissionName{"write"})
	if err != nil {
		t.Fatalf("NewResourcePolicy() error = %v", err)
	}

	authorizer := resourcePolicy.Authorizer("write")
	if !authorizer.Check("user:alice") {
		t.Error("Expected user:alice to have write permission")
	}
}

func TestUnknownPermission(t *testing.T) {
	policy := authz.Policy{
		Roles: []authz.Role{
			{
				ID:          "admin",
				Permissions: []authz.PermissionName{"read", "write"},
			},
		},
		Bindings: []authz.RoleBinding{
			{
				Role:      "admin",
				Principal: "user:alice",
				Scope:     "organization/acme",
			},
		},
	}

	// Create policy with only "read" permission
	resourcePolicy, err := authz.NewResourcePolicy(policy, "organization/acme", []authz.PermissionName{"read"})
	if err != nil {
		t.Fatalf("NewResourcePolicy() error = %v", err)
	}

	// Request a permission that wasn't provided to NewResourcePolicy
	unknownAuthorizer := resourcePolicy.Authorizer("delete")
	if unknownAuthorizer.Check("user:alice") {
		t.Error("Expected unknown permission to deny access")
	}

	// Request a permission that exists in the role but wasn't provided to NewResourcePolicy
	writeAuthorizer := resourcePolicy.Authorizer("write")
	if writeAuthorizer.Check("user:alice") {
		t.Error("Expected non-compiled permission to deny access")
	}

	// Verify the compiled permission works
	readAuthorizer := resourcePolicy.Authorizer("read")
	if !readAuthorizer.Check("user:alice") {
		t.Error("Expected compiled permission to grant access")
	}
}

func TestSubResourceAuthorizer(t *testing.T) {
	policy := authz.Policy{
		Roles: []authz.Role{
			{
				ID:          "dataplane.admin",
				Permissions: []authz.PermissionName{"tool_invoke", "tool_list"},
			},
			{
				ID:          "mcpserver.user",
				Permissions: []authz.PermissionName{"tool_invoke"},
			},
		},
		Bindings: []authz.RoleBinding{
			{
				Role:      "dataplane.admin",
				Principal: "user:alice",
				Scope:     "organization/acme/resourcegroup/foo/dataplane/bar",
			},
			{
				Role:      "mcpserver.user",
				Principal: "user:bob",
				Scope:     "organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/qux",
			},
		},
	}

	// Create a resource policy for the dataplane with the permissions we need
	dataplaneResource := authz.ResourceName("organization/acme/resourcegroup/foo/dataplane/bar")
	resourcePolicy, err := authz.NewResourcePolicy(policy, dataplaneResource, []authz.PermissionName{"tool_invoke", "tool_list"})
	if err != nil {
		t.Fatalf("NewResourcePolicy() error = %v", err)
	}

	// Test authorizer for the dataplane itself
	dataplaneAuthorizer := resourcePolicy.Authorizer("tool_list")
	if !dataplaneAuthorizer.Check("user:alice") {
		t.Error("Expected alice to have tool_list permission on dataplane")
	}
	if dataplaneAuthorizer.Check("user:bob") {
		t.Error("Expected bob to NOT have tool_list permission on dataplane")
	}

	// Test sub-resource authorizer for an MCP server
	mcpServerAuthorizer := resourcePolicy.SubResourceAuthorizer("mcpserver", "qux", "tool_invoke")

	// Alice has admin role at dataplane level, should inherit to MCP server
	if !mcpServerAuthorizer.Check("user:alice") {
		t.Error("Expected alice to have tool_invoke permission on mcpserver (inherited)")
	}

	// Bob has user role at the specific MCP server level
	if !mcpServerAuthorizer.Check("user:bob") {
		t.Error("Expected bob to have tool_invoke permission on mcpserver")
	}

	// Charlie has no permissions
	if mcpServerAuthorizer.Check("user:charlie") {
		t.Error("Expected charlie to NOT have tool_invoke permission on mcpserver")
	}

	// Test sub-resource authorizer for a different MCP server
	otherMcpServerAuthorizer := resourcePolicy.SubResourceAuthorizer("mcpserver", "other", "tool_invoke")

	// Alice should still have access (inherited from dataplane)
	if !otherMcpServerAuthorizer.Check("user:alice") {
		t.Error("Expected alice to have tool_invoke permission on other mcpserver (inherited)")
	}

	// Bob should NOT have access (only bound to 'qux' mcpserver)
	if otherMcpServerAuthorizer.Check("user:bob") {
		t.Error("Expected bob to NOT have tool_invoke permission on other mcpserver")
	}
}

func setupLargeScalePolicy() authz.Policy {
	// Generate 1000 roles with 10 permissions each
	roles := make([]authz.Role, 1000)
	for i := range 1000 {
		permissions := make([]authz.PermissionName, 10)
		for j := range 10 {
			permissions[j] = authz.PermissionName(fmt.Sprintf("permission_%d_%d", i, j))
		}
		roles[i] = authz.Role{
			ID:          authz.RoleID(fmt.Sprintf("role_%d", i)),
			Permissions: permissions,
		}
	}

	// Generate 100k principals with bindings
	// Distribute them across different scopes and roles
	bindings := make([]authz.RoleBinding, 100000)
	scopes := []authz.ResourceName{
		"organization/acme",
		"organization/acme/resourcegroup/foo",
		"organization/acme/resourcegroup/foo/dataplane/bar",
		"organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/qux",
	}
	for i := range 100000 {
		bindings[i] = authz.RoleBinding{
			Role:      authz.RoleID(fmt.Sprintf("role_%d", i%1000)),
			Principal: authz.PrincipalID(fmt.Sprintf("user:%d", i)),
			Scope:     scopes[i%len(scopes)],
		}
	}

	return authz.Policy{
		Roles:    roles,
		Bindings: bindings,
	}
}

func Benchmark_1000Roles_100kPrincipals(b *testing.B) {
	policy := setupLargeScalePolicy()
	resource := authz.ResourceName("organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/qux")
	permission := authz.PermissionName("permission_42_5")
	permissions := []authz.PermissionName{permission}

	b.Run("NewResourcePolicy", func(b *testing.B) {
		for b.Loop() {
			_, err := authz.NewResourcePolicy(policy, resource, permissions)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	// Compile once for the check benchmarks
	resourcePolicy, err := authz.NewResourcePolicy(policy, resource, permissions)
	if err != nil {
		b.Fatal(err)
	}

	b.Run("Authorizer", func(b *testing.B) {
		for b.Loop() {
			_ = resourcePolicy.Authorizer(permission)
		}
	})

	authorizer := resourcePolicy.Authorizer(permission)

	b.Run("AuthorizerCheck", func(b *testing.B) {
		// Test checking various principals - some with access, some without
		testPrincipals := make([]authz.PrincipalID, 1000)
		for i := range 1000 {
			testPrincipals[i] = authz.PrincipalID(fmt.Sprintf("user:%d", i*100))
		}

		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			_ = authorizer.Check(testPrincipals[i%len(testPrincipals)])
		}
	})

	b.Run("AuthorizerCheck_WorstCase", func(b *testing.B) {
		// Worst case: check a principal that doesn't exist
		for b.Loop() {
			_ = authorizer.Check("user:nonexistent")
		}
	})

	b.Run("SubResourceAuthorizer", func(b *testing.B) {
		// Benchmark creating authorizer for a child resource
		for b.Loop() {
			_ = resourcePolicy.SubResourceAuthorizer("mcpserver", "child", permission)
		}
	})

	subAuthorizer := resourcePolicy.SubResourceAuthorizer("mcpserver", "child", permission)
	b.Run("SubResourceAuthorizerCheck", func(b *testing.B) {
		// Test checking various principals on sub-resource
		testPrincipals := make([]authz.PrincipalID, 1000)
		for i := range 1000 {
			testPrincipals[i] = authz.PrincipalID(fmt.Sprintf("user:%d", i*100))
		}

		b.ResetTimer()
		for i := 0; b.Loop(); i++ {
			_ = subAuthorizer.Check(testPrincipals[i%len(testPrincipals)])
		}
	})
}
