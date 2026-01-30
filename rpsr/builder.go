// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package rpsr

import (
	"errors"
	"fmt"
	"strings"
)

// The code for the builder is largely borrowed from
// github.com/twmb/franz-go/pkg/kadm/acl.go, BSD 3-clause licensed

// ACLBuilder helps construct sets of ACL rules for Redpanda's Schema Registry.
type ACLBuilder struct {
	allowPrincipal    []string
	denyPrincipal     []string
	anyAllowPrincipal bool
	anyDenyPrincipal  bool

	allowHost    []string
	denyHost     []string
	anyAllowHost bool
	anyDenyHost  bool

	allRegistry bool
	subjects    []string
	anyResource bool

	operations []Operation
	pattern    PatternType
}

// NewACLBuilder initializes a new ACLBuilder.
func NewACLBuilder() *ACLBuilder {
	return new(ACLBuilder)
}

// PrefixUserExcept prefixes all unqualified principals with "User:", unless they
// start with any of the specified exception prefixes (e.g., "RedpandaRole:").
func (b *ACLBuilder) PrefixUserExcept(except ...string) {
	replace := func(principal string) string {
		if !strings.HasPrefix(principal, "User:") {
			for _, e := range except {
				if strings.HasPrefix(principal, e) {
					return principal
				}
			}
			return "User:" + principal
		}
		return principal
	}
	for i, p := range b.allowPrincipal {
		b.allowPrincipal[i] = replace(p)
	}
	for i, p := range b.denyPrincipal {
		b.denyPrincipal[i] = replace(p)
	}
}

// AllowPrincipals sets principals to be granted permissions.
func (b *ACLBuilder) AllowPrincipals(principals ...string) *ACLBuilder {
	b.allowPrincipal = principals
	return b
}

// AllowPrincipalsOrAny sets allowed principals or defaults to "*" if empty.
func (b *ACLBuilder) AllowPrincipalsOrAny(principals ...string) *ACLBuilder {
	b.allowPrincipal = principals
	if len(principals) == 0 {
		b.anyAllowPrincipal = true
	}
	return b
}

// DenyPrincipals sets principals to be explicitly denied.
func (b *ACLBuilder) DenyPrincipals(principals ...string) *ACLBuilder {
	b.denyPrincipal = principals
	return b
}

// DenyPrincipalsOrAny sets denied principals or defaults to ANY principals if
// empty. Useful when filtering ACLs.
func (b *ACLBuilder) DenyPrincipalsOrAny(principals ...string) *ACLBuilder {
	b.denyPrincipal = principals
	if len(principals) == 0 {
		b.anyDenyPrincipal = true
	}
	return b
}

// AllowHosts sets hosts for which allow rules apply.
func (b *ACLBuilder) AllowHosts(hosts ...string) *ACLBuilder {
	b.allowHost = hosts
	return b
}

// AllowHostsOrAll sets allowed hosts or defaults to "*" if empty.
func (b *ACLBuilder) AllowHostsOrAll(hosts ...string) *ACLBuilder {
	b.allowHost = hosts
	if len(hosts) == 0 {
		b.allowHost = []string{"*"}
	}
	return b
}

// AllowHostsOrAny sets allowed hosts or defaults to ANY host if empty. Useful
// when filtering ACLSs.
func (b *ACLBuilder) AllowHostsOrAny(hosts ...string) *ACLBuilder {
	b.allowHost = hosts
	if len(hosts) == 0 {
		b.anyAllowHost = true
	}
	return b
}

// DenyHosts sets hosts for which deny rules apply.
func (b *ACLBuilder) DenyHosts(hosts ...string) *ACLBuilder {
	b.denyHost = hosts
	return b
}

// DenyHostsOrAny sets denied hosts or defaults to ANY host if empty. Useful
// when filtering ACLs.
func (b *ACLBuilder) DenyHostsOrAny(hosts ...string) *ACLBuilder {
	b.denyHost = hosts
	if len(hosts) == 0 {
		b.anyDenyHost = true
	}
	return b
}

// DenyHostsOrAll sets denied hosts or defaults to "*" host if empty.
func (b *ACLBuilder) DenyHostsOrAll(hosts ...string) *ACLBuilder {
	b.denyHost = hosts
	if len(hosts) == 0 {
		b.denyHost = []string{"*"}
	}
	return b
}

// Registry indicates that ACLs should be created for the schema registry.
func (b *ACLBuilder) Registry() *ACLBuilder {
	b.allRegistry = true
	return b
}

// MaybeRegistry indicates that ACLs should be created for the registry itself.
// Registry ACLs do not include a resource name.
func (b *ACLBuilder) MaybeRegistry(r bool) *ACLBuilder {
	b.allRegistry = r
	return b
}

// Subjects sets the list of schema subjects to apply ACLs to.
func (b *ACLBuilder) Subjects(subjects ...string) *ACLBuilder {
	b.subjects = subjects
	return b
}

// Operations sets the operations the ACLs should allow or deny.
func (b *ACLBuilder) Operations(operations ...Operation) *ACLBuilder {
	b.operations = operations
	return b
}

// OperationsOrAny sets the operations or defaults to ANY if none are specified.
func (b *ACLBuilder) OperationsOrAny(operations ...Operation) *ACLBuilder {
	b.operations = operations
	if len(operations) == 0 {
		b.operations = []Operation{OperationAny}
	}
	return b
}

// Pattern sets the pattern type (LITERAL or PREFIX) for matching subject names.
func (b *ACLBuilder) Pattern(pattern PatternType) *ACLBuilder {
	b.pattern = pattern
	return b
}

// PatternOrAny sets the pattern type or defaults to ANY if not specified.
func (b *ACLBuilder) PatternOrAny(pattern PatternType) *ACLBuilder {
	b.pattern = pattern
	if pattern == "" {
		b.pattern = PatternTypeAny
	}
	return b
}

// AnyResources opts for selecting any resources, used for filtering ACLs.
func (b *ACLBuilder) AnyResources() *ACLBuilder {
	b.anyResource = true
	return b
}

// HasHosts checks if the builder has any hosts set for allow or deny rules.
func (b *ACLBuilder) HasHosts() bool {
	return len(b.allowHost) > 0 || len(b.denyHost) > 0
}

// HasPrincipals checks if the builder has any principals set for allow or
// deny rules.
func (b *ACLBuilder) HasPrincipals() bool {
	return len(b.allowPrincipal) > 0 || len(b.denyPrincipal) > 0
}

// HasAllowedPrincipals checks if the builder has any allowed principals set.
func (b *ACLBuilder) HasAllowedPrincipals() bool {
	return len(b.allowPrincipal) > 0
}

// HasDeniedPrincipals checks if the builder has any denied principals set.
func (b *ACLBuilder) HasDeniedPrincipals() bool {
	return len(b.denyPrincipal) > 0
}

// HasResources checks if the builder has any subjects or is configured to
// create ACLs for the whole schema registry.
func (b *ACLBuilder) HasResources() bool {
	return len(b.subjects) > 0 || b.allRegistry
}

// copy creates a shallow copy of the ACLBuilder.
func (b *ACLBuilder) copy() *ACLBuilder {
	c := *b
	return &c
}

// ValidateCreate ensures the builder is properly configured before building ACLs.
func (b *ACLBuilder) ValidateCreate() error {
	if len(b.operations) == 0 {
		return errors.New("invalid empty operations: at least one operation is required to create an ACL")
	}
	if b.pattern == "" {
		return errors.New("invalid empty pattern type")
	}
	if len(b.allowHost) != 0 && len(b.allowPrincipal) == 0 {
		return errors.New("invalid allow hosts with no allowed principals")
	}
	if len(b.denyHost) != 0 && len(b.denyPrincipal) == 0 {
		return errors.New("invalid deny hosts with no denied principals")
	}
	if len(b.allowPrincipal) != 0 && len(b.allowHost) == 0 {
		return errors.New("invalid allow principal with no allowed hosts")
	}
	if len(b.denyPrincipal) != 0 && len(b.denyHost) == 0 {
		return errors.New("invalid deny principals with no denied hosts")
	}
	for _, p := range append(b.allowPrincipal, b.denyPrincipal...) {
		if p != "*" && (!strings.HasPrefix(p, "User:") && !strings.HasPrefix(p, "RedpandaRole:") && !strings.HasPrefix(p, "Group:")) {
			return fmt.Errorf("invalid principal %v: must start with 'User:', 'RedpandaRole:' or 'Group:'", p)
		}
	}
	return nil
}

// ValidateAndBuildCreate validates the builder's configuration and constructs ACLs.
func (b *ACLBuilder) ValidateAndBuildCreate() ([]ACL, error) {
	err := b.ValidateCreate()
	if err != nil {
		return nil, err
	}
	return b.BuildCreate(), nil
}

// BuildCreate constructs a unique list of ACL entries based on the builder's
// configuration. This builds ACLs on a more strict manner, suited for
// creating ACLs. For filter-based ACLs, use BuildFilter() instead.
//
// This method assumes the configuration has already been validated using
// Validate() to prevent empty ACLs from being built. It performs a
// combinatorial expansion of principals, hosts, operations, and resource types
// into individual ACL entries, eliminating duplicates.
func (b *ACLBuilder) BuildCreate() []ACL {
	var acls []ACL
	seen := make(map[string]struct{})
	add := func(principal string, host string, permission Permission, resourceType ResourceType, resource string) {
		for _, op := range b.operations {
			pattern := b.pattern
			// PatternTypeAny is not currently accepted, but is the equivalent
			// of an empty pattern in the context of SR ACLs.
			if b.pattern == PatternTypeAny {
				pattern = ""
			}
			acl := ACL{
				Principal:    principal,
				Resource:     resource,
				ResourceType: resourceType,
				Host:         host,
				Operation:    op,
				Permission:   permission,
				PatternType:  pattern,
			}
			key := aclKey(acl)
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				acls = append(acls, acl)
			}
		}
	}

	if b.allRegistry {
		for _, p := range b.allowPrincipal {
			for _, h := range b.allowHost {
				add(p, h, PermissionAllow, ResourceTypeRegistry, "") // Registry ACLs do not have a resource name.
			}
		}
		for _, p := range b.denyPrincipal {
			for _, h := range b.denyHost {
				add(p, h, PermissionDeny, ResourceTypeRegistry, "")
			}
		}
	}

	for _, s := range b.subjects {
		for _, p := range b.allowPrincipal {
			for _, h := range b.allowHost {
				add(p, h, PermissionAllow, ResourceTypeSubject, s)
			}
		}
		for _, p := range b.denyPrincipal {
			for _, h := range b.denyHost {
				add(p, h, PermissionDeny, ResourceTypeSubject, s)
			}
		}
	}

	return acls
}

// BuildFilter constructs a list of ACL entries based on the builder's
// configuration for use in filtering existing ACLs. Unlike Build(), this
// method supports "any" wildcards for principals, hosts, operations,
// permissions, and resources.
//
// This method is tolerant of incomplete or broad configurations and performs a
// combinatorial expansion of the fields into individual ACL entries,
// including empty fields to match any value. It eliminates duplicate entries
// based on a computed key.
//
// Use BuildFilter() when querying or filtering existing ACLs, not for creating
// new ACL entries.
func (b *ACLBuilder) BuildFilter() []ACL {
	var acls []ACL
	seen := make(map[string]struct{})

	// If all allow and deny principals and hosts are set to “any”, treat them
	// as a single wildcard group by resetting fields and disabling expansion.
	var anyAnyPrincipal, anyAnyHost bool
	b, anyAnyPrincipal, anyAnyHost = normalizeBuilder(b)

	operations := b.operations
	if len(operations) == 0 {
		operations = []Operation{OperationAny}
	}

	var registry []string
	if b.allRegistry {
		registry = []string{""} // Registry ACLs don't have a resource name.
	}
	for _, resource := range []struct {
		t     ResourceType
		names []string
	}{
		{ResourceTypeRegistry, registry},
		{ResourceTypeSubject, b.subjects},
	} {
		resType := resource.t
		resNames := resource.names
		if b.anyResource {
			resType = ""
			resNames = []string{""}
		}
		for _, name := range resNames {
			for _, op := range operations {
				for _, perm := range []struct {
					principals   []string
					anyPrincipal bool
					hosts        []string
					anyHost      bool
					permType     Permission
				}{
					{
						b.allowPrincipal,
						b.anyAllowPrincipal,
						b.allowHost,
						b.anyAllowHost,
						PermissionAllow,
					},
					{
						b.denyPrincipal,
						b.anyDenyPrincipal,
						b.denyHost,
						b.anyDenyHost,
						PermissionDeny,
					},
					{
						nil,
						anyAnyPrincipal,
						nil,
						anyAnyHost,
						PermissionAny,
					},
				} {
					if perm.anyPrincipal {
						perm.principals = []string{""}
					}
					if perm.anyHost {
						perm.hosts = []string{""}
					}
					expandPrincipalsHosts(&acls, seen, perm.principals, perm.hosts, perm.permType, resType, name, op, b.pattern)
				}
			}
		}
	}
	return acls
}

// expandPrincipalsHosts expands the given principals and hosts into the passed
// ACLs slice, ensuring that each combination of ACL fields is unique.
//
// It uses the 'seen' map to track seen combinations to avoid duplicates.
func expandPrincipalsHosts(acls *[]ACL, seen map[string]struct{}, principals, hosts []string, perm Permission, resType ResourceType, name string, op Operation, pattern PatternType) {
	// Note, Redpanda currently does not support ACLs fields with ANY, instead
	// we can treat them as empty values to achieve the same effect.
	// This whole function logic is based on that assumption.

	// PatternTypeAny, OperationAny, and PermissionAny are not currently
	// accepted, but are the equivalent of an empty value in the context
	// of SR ACLs.
	if pattern == PatternTypeAny {
		pattern = ""
	}
	if op == OperationAny {
		op = ""
	}
	if perm == PermissionAny {
		perm = ""
	}
	for _, principal := range principals {
		for _, host := range hosts {
			acl := ACL{
				Principal:    principal,
				Resource:     name,
				ResourceType: resType,
				Host:         host,
				Operation:    op,
				Permission:   perm,
				PatternType:  pattern,
			}
			key := aclKey(acl)
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				*acls = append(*acls, acl)
			}
		}
	}
}

// normalizeBuilder normalizes the ACLBuilder by checking if all
// allow/deny principals and hosts are set to "any". If so, it resets the
// allow/deny fields to nil and sets the anyAllow/anyDeny flags to false.
func normalizeBuilder(b *ACLBuilder) (br *ACLBuilder, anyAnyPrincipal bool, anyAnyHost bool) {
	if b.anyAllowPrincipal && b.anyDenyPrincipal && b.anyAllowHost && b.anyDenyHost {
		anyAnyPrincipal = true
		anyAnyHost = true

		b = b.copy()
		b.allowPrincipal = nil
		b.allowHost = nil
		b.denyPrincipal = nil
		b.denyHost = nil
		b.anyAllowPrincipal = false
		b.anyAllowHost = false
		b.anyDenyPrincipal = false
		b.anyDenyHost = false
	}
	return b, anyAnyPrincipal, anyAnyHost
}
