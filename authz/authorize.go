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
	"fmt"
	"slices"
)

// Authorizer is the primary interface in order to check if a principal is granted
// a given permission.
type Authorizer interface {
	// Check returns true if the principal has permission for the given action.
	Check(p PrincipalID) bool
}

// ResourcePolicy is a compiled policy that is scoped to a single resource (and it's children resources).
type ResourcePolicy struct {
	resource        ResourceName
	roleByID        map[RoleID]Role
	bindingsByScope map[ResourceName][]RoleBinding
	// Pre-computed authorizers per permission for this resource
	authorizersByPerm map[PermissionName]*authorizerImpl
}

// Authorizer returns an [Authorizer] for this resource and the given permission.
// The permission must have been provided to NewResourcePolicy.
func (r *ResourcePolicy) Authorizer(perm PermissionName) Authorizer {
	auth, ok := r.authorizersByPerm[perm]
	if !ok {
		// Return an authorizer that always denies
		return &authorizerImpl{checkers: nil}
	}
	return auth
}

// SubResourceAuthorizer returns an [Authorizer] for a child resource and the given permission.
// The permission must have been provided to NewResourcePolicy.
func (r *ResourcePolicy) SubResourceAuthorizer(t ResourceType, id ResourceID, perm PermissionName) Authorizer {
	parentAuth, ok := r.authorizersByPerm[perm]
	if !ok {
		// Return an authorizer that always denies
		return &authorizerImpl{checkers: nil}
	}
	childResource := r.resource.Child(t, id)
	childChecker := r.buildChecker(childResource, perm)
	return &authorizerImpl{checkers: append(parentAuth.checkers, childChecker)}
}

func (r *ResourcePolicy) buildChecker(scope ResourceName, perm PermissionName) *resourceChecker {
	checker := &resourceChecker{principals: map[PrincipalID]struct{}{}}
	for _, binding := range r.bindingsByScope[scope] {
		role, ok := r.roleByID[binding.Role]
		if !ok {
			// Skip bindings with missing roles during runtime checks
			continue
		}
		// This is expected to be small, so iterating every time is fine.
		if slices.Index(role.Permissions, perm) != -1 {
			checker.principals[binding.Principal] = struct{}{}
		}
	}
	return checker
}

// NewResourcePolicy creates a pre-compiled resource policy for a specific resource.
//
// This function pre-computes all authorization data for the specified permissions,
// making runtime authorization checks extremely fast (O(1) map lookups with zero allocations).
//
// For example, if a resource `organizations/acme/dataplanes/bar/mcpservers/qux`
// wanted to check `tool_invoke` and `tool_list` permissions:
//
//	func RunMCPServer() error {
//	  // Pre-compile the policy with all permissions you'll need
//	  resourcePolicy, err := authz.NewResourcePolicy(
//	    MustLoadAuthzPolicy(),
//	    "organizations/acme/dataplanes/bar/mcpservers/qux",
//	    []authz.PermissionName{"tool_invoke", "tool_list"},
//	  )
//	  if err != nil {
//	  	return err
//	  }
//
//	  // Get authorizers for specific permissions (fast, zero allocations)
//	  invokeEnforcer := resourcePolicy.Authorizer("tool_invoke")
//	  listEnforcer := resourcePolicy.Authorizer("tool_list")
//
//	  server.AddToolMiddleware(func(ctx context.Context) error {
//	  	principal, err := CheckAuthentication(ctx)
//	  	if err != nil {
//	  		return fmt.Errorf("unauthorized", err)
//	  	}
//	  	if !invokeEnforcer.Check(PrincipalID(principal)) {
//	  		return errors.New("permission denied")
//	  	}
//	  	return nil
//	  })
//	  return server.Run()
//	}
//
// This function should not be called in a hot path - call it once during initialization.
// The resulting ResourcePolicy and Authorizers are very performant for runtime checks.
func NewResourcePolicy(
	p Policy,
	resource ResourceName,
	permissions []PermissionName,
) (*ResourcePolicy, error) {
	roleByID := map[RoleID]Role{}
	for _, role := range p.Roles {
		roleByID[role.ID] = role
	}

	bindingsByScope := map[ResourceName][]RoleBinding{}
	for _, binding := range p.Bindings {
		// Validate that the role exists
		if _, ok := roleByID[binding.Role]; !ok {
			return &ResourcePolicy{}, fmt.Errorf("missing role %q for binding", binding.Role)
		}
		bindings := bindingsByScope[binding.Scope]
		bindingsByScope[binding.Scope] = append(bindings, binding)
	}

	rp := &ResourcePolicy{
		resource:          resource,
		roleByID:          roleByID,
		bindingsByScope:   bindingsByScope,
		authorizersByPerm: make(map[PermissionName]*authorizerImpl, len(permissions)),
	}

	// Pre-compute authorizers for all requested permissions
	for _, perm := range permissions {
		// Walk up the resource hierarchy and accumulate all principals into a single checker
		accumulated := &resourceChecker{principals: map[PrincipalID]struct{}{}}
		current := resource
		for current != "" {
			// Add principals from this scope to the accumulated checker
			for _, binding := range bindingsByScope[current] {
				role, ok := roleByID[binding.Role]
				if !ok {
					continue
				}
				if slices.Index(role.Permissions, perm) != -1 {
					accumulated.principals[binding.Principal] = struct{}{}
				}
			}
			current = current.Parent()
		}
		rp.authorizersByPerm[perm] = &authorizerImpl{checkers: []*resourceChecker{accumulated}}
	}

	return rp, nil
}

type resourceChecker struct {
	// principals that have access to this permission at this scope
	principals map[PrincipalID]struct{}
}

type authorizerImpl struct {
	checkers []*resourceChecker
}

// Check implements Authorizer.
func (a *authorizerImpl) Check(p PrincipalID) bool {
	for _, checker := range a.checkers {
		if _, ok := checker.principals[p]; ok {
			return true
		}
	}
	return false
}
