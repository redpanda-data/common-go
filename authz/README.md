# Redpanda Authorization

## Purpose

This Go module provides a common abstraction for **Role-Based Access Control (RBAC)** in Redpanda Cloud. It implements a hierarchical authorization system that checks if principals (users/services) have specific permissions on resources.

## Key Features

1. **Hierarchical Resource Model**: Resources are organized in a hierarchical path structure following [AIP-122](https://aip.dev/122) standards:
   ```
   organizations/acme/resourcegroups/foo/dataplanes/bar/mcpservers/myagenttools
   ```

2. **Permission Inheritance**: Permissions granted at a parent resource level automatically apply to all child resources. For example, permissions on a dataplane apply to all MCP servers within it.

3. **Role-Based Access**:
   - **Roles** define collections of permissions
   - **RoleBindings** associate principals with roles at specific resource scopes
   - **Policies** combine roles and bindings

4. **Pre-Computed Authorization**: The `NewResourcePolicy` function pre-computes all authorization checks for specified permissions, making runtime authorization extremely fast.

## Main Components

- `ResourcePolicy` - Pre-compiled policy scoped to a resource with specified permissions
- `Authorizer` interface with `Check()` method - Fast authorization checks
- `NewResourcePolicy()` - Compiles a policy for a resource with required permissions
- Data structures for resources, roles, bindings, and policies (model.go)

## Usage Example

```go
// Define your policy with roles and bindings
policy := authz.Policy{
    Roles: []authz.Role{
        {
            ID:          "admin",
            Permissions: []authz.PermissionName{"read", "write", "delete"},
        },
    },
    Bindings: []authz.RoleBinding{
        {
            RoleID:    "admin",
            Principal: "user:alice",
            Scope:     "organizations/acme/dataplanes/bar",
        },
    },
}

// Create a resource policy for your resource with the permissions you need
resource := authz.ResourceName("organizations/acme/dataplanes/bar/mcpservers/qux")
resourcePolicy, err := authz.NewResourcePolicy(
    policy,
    resource,
    []authz.PermissionName{"read", "write"}, // Specify all permissions you'll check
)
if err != nil {
    return err
}

// Get authorizers for specific permissions
readAuthorizer := resourcePolicy.Authorizer("read")
writeAuthorizer := resourcePolicy.Authorizer("write")

// Check if principals have permission
if readAuthorizer.Check("user:alice") {
    // Alice has read permission
}

// For child resources or API servers with dynamic resources that are managed, use SubResourceAuthorizer
childAuthorizer := resourcePolicy.SubResourceAuthorizer("tools", "mytool", "read")
if childAuthorizer.Check("user:alice") {
    // Alice has read permission on the child resource (inherited)
}
```
