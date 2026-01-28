// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package authz

import (
	"path"
	"strings"
)

// ResourceName is a unique and hierarchical name for a resource inside the permissions model.
//
// For example, an MCP server inside the dataplane could have full name such as:
//
//	organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/myagenttools
//
// For more information see: https://aip.dev/122
//
// The default ResourceName is the root resource that exists outside the resource realm.
type ResourceName string

// Name returns the final component of the full name, which is the resource's name.
func (r ResourceName) Name() ResourceID {
	if r == "" {
		return ""
	}
	return ResourceID(path.Base(string(r)))
}

// Type returns the type of the component, which is the second to last component.
func (r ResourceName) Type() ResourceType {
	if r == "" {
		return ""
	}
	t := ResourceType(path.Base(path.Dir(string(r))))
	if t == "." {
		return ""
	}
	return t
}

// Parent returns the parent resource for this resource.
//
// For example, an MCP server inside the dataplane could have full name such as:
//
//	organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/myagenttools
//
// It's parent would be:
//
//	organization/acme/resourcegroup/foo/dataplane/bar
func (r ResourceName) Parent() ResourceName {
	if r == "" {
		return ""
	}
	p := ResourceName(path.Dir(path.Dir(string(r))))
	if p == "." {
		return ""
	}
	return p
}

// Child returns the child resource from this resource.
//
// For example, a dataplane could have full name such as:
//
//	organization/acme/resourcegroup/foo/dataplane/bar
//
// It's [ResourceName.Child] for an `mcpserver` called `myagenttools` would be:
//
//	organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/myagenttools
func (r ResourceName) Child(t ResourceType, n ResourceID) ResourceName {
	return ResourceName(path.Join(string(r), string(t), string(n)))
}

// Relative returns the relative resources from r to the parent. If the passed in resource name
// is not an ancestor then false is returned.
//
// For example, a resource r that is:
//
//	organization/acme/resourcegroup/foo/dataplane/bar
//
// And an ancestor resource of:
//
//	organization/acme
//
// Will result in a relative name of:
//
//	resourcegroup/foo/dataplane/bar
func (r ResourceName) Relative(ancestor ResourceName) (name ResourceName, isChild bool) {
	if r == ancestor { // You are an ancestor of yourself
		return "", true
	}
	if ancestor != "" {
		ancestor += "/"
	}
	after, found := strings.CutPrefix(string(r), string(ancestor))
	if !found {
		return "", false
	}
	return ResourceName(after), true
}

// String implements fmt.Stringer.
func (r ResourceName) String() string {
	return string(r)
}

// ResourceID is the unique identifier or name within the scoped resource model.
//
// For example, if the [ResourceName] is
//
//	organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/myagenttools
//
// Then the [ResourceID] is `myagenttools`
//
// For more information see: aip.dev/122
type ResourceID string

// String implements fmt.Stringer.
func (r ResourceID) String() string {
	return string(r)
}

// ResourceType is the type of the resource.
//
// For example, if the [ResourceName] is
//
//	organization/acme/resourcegroup/foo/dataplane/bar/mcpserver/myagenttools
//
// Then the [ResourceType] is `mcpserver`
//
// For more information see: aip.dev/122
type ResourceType string

// String implements fmt.Stringer.
func (r ResourceType) String() string {
	return string(r)
}

// RoleID is a unique identifier that describes the role.
type RoleID string

// PrincipalID is a unique identifier that describes a principal.
type PrincipalID string

// UserPrincipal creates a PrincipalID for a user via email
func UserPrincipal(email string) PrincipalID {
	return PrincipalID("User:" + email)
}

// GroupPrincipal creates a PrincipalID for a group
func GroupPrincipal(groupName string) PrincipalID {
	return PrincipalID("Group:" + groupName)
}

// PermissionName is the string name of the permission.
type PermissionName string

// Role a collection of permissions with a unique identifier.
type Role struct {
	ID          RoleID           `json:"id" yaml:"id" mapstructure:"id"`
	Permissions []PermissionName `json:"permissions" yaml:"permissions" mapstructure:"permissions"`
}

// RoleBinding is what associates a principal with a role in the permissions model.
type RoleBinding struct {
	Role      RoleID      `json:"role" yaml:"role" mapstructure:"role"`
	Principal PrincipalID `json:"principal" yaml:"principal" mapstructure:"principal"`
	// Scope is the level at which the role is bound, which allows the permission
	// to be granted to this resource as well as all sub-resources.
	Scope ResourceName `json:"scope" yaml:"scope" mapstructure:"scope"`
}

// Policy is a collection of roles and bindings that make up all the information
// required to enforce access and authorize actions in a component.
type Policy struct {
	Roles    []Role        `json:"roles" yaml:"roles" mapstructure:"roles"`
	Bindings []RoleBinding `json:"bindings" yaml:"bindings" mapstructure:"bindings"`
}
