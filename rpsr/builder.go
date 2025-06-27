package rpsr

import (
	"errors"
	"fmt"
	"strings"
)

// ACLBuilder helps construct sets of ACL rules for Redpanda's Schema Registry.
type ACLBuilder struct {
	allowPrincipal []string
	denyPrincipal  []string

	allowHost []string
	denyHost  []string

	allRegistry bool
	subjects    []string

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
		b.allowPrincipal = []string{"*"}
	}
	return b
}

// DenyPrincipals sets principals to be explicitly denied.
func (b *ACLBuilder) DenyPrincipals(principals ...string) *ACLBuilder {
	b.denyPrincipal = principals
	return b
}

// DenyPrincipalsOrAny sets denied principals or defaults to "*" if empty.
func (b *ACLBuilder) DenyPrincipalsOrAny(principals ...string) *ACLBuilder {
	b.denyPrincipal = principals
	if len(principals) == 0 {
		b.denyPrincipal = []string{"*"}
	}
	return b
}

// AllowHosts sets hosts for which allow rules apply.
func (b *ACLBuilder) AllowHosts(hosts ...string) *ACLBuilder {
	b.allowHost = hosts
	return b
}

// AllowHostsOrAny sets allowed hosts or defaults to "*" if empty.
func (b *ACLBuilder) AllowHostsOrAny(hosts ...string) *ACLBuilder {
	b.allowHost = hosts
	if len(hosts) == 0 {
		b.allowHost = []string{"*"}
	}
	return b
}

// DenyHosts sets hosts for which deny rules apply.
func (b *ACLBuilder) DenyHosts(hosts ...string) *ACLBuilder {
	b.denyHost = hosts
	return b
}

// DenyHostsOrAny sets denied hosts or defaults to "*" if empty.
func (b *ACLBuilder) DenyHostsOrAny(hosts ...string) *ACLBuilder {
	b.denyHost = hosts
	if len(hosts) == 0 {
		b.denyHost = []string{"*"}
	}
	return b
}

// Registry indicates that ACLs should be created for the registry itself.
// Registry ACLs do not include a resource name.
func (b *ACLBuilder) Registry() *ACLBuilder {
	b.allRegistry = true
	return b
}

// Subjects sets the list of schema subjects to apply ACLs to.
func (b *ACLBuilder) Subjects(subjects ...string) *ACLBuilder {
	b.subjects = subjects
	return b
}

// SubjectsOrAny sets subjects or defaults to "*" if none are provided.
func (b *ACLBuilder) SubjectsOrAny(subjects ...string) *ACLBuilder {
	b.subjects = subjects
	if len(subjects) == 0 {
		b.subjects = []string{"*"}
	}
	return b
}

// Operations sets the operations the ACLs should allow or deny.
func (b *ACLBuilder) Operations(operations ...Operation) *ACLBuilder {
	b.operations = operations
	return b
}

// OperationsOrAll sets the operations or defaults to ALL if none are specified.
func (b *ACLBuilder) OperationsOrAll(operations ...Operation) *ACLBuilder {
	b.operations = operations
	if len(operations) == 0 {
		b.operations = []Operation{OperationAll}
	}
	return b
}

// Pattern sets the pattern type (LITERAL or PREFIX) for matching subject names.
func (b *ACLBuilder) Pattern(pattern PatternType) *ACLBuilder {
	b.pattern = pattern
	return b
}

// PatternOrLiteral sets the pattern type or defaults to LITERAL if not
// specified.
func (b *ACLBuilder) PatternOrLiteral(pattern PatternType) *ACLBuilder {
	b.pattern = pattern
	if pattern == "" {
		b.pattern = PatternTypeLiteral
	}
	return b
}

// Validate ensures the builder is properly configured before building ACLs.
func (b *ACLBuilder) Validate() error {
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
		if p != "*" && !(strings.HasPrefix(p, "User:") || strings.HasPrefix(p, "RedpandaRole:")) {
			return fmt.Errorf("invalid principal %v: must start with 'User:' or 'RedpandaRole:'", p)
		}
	}
	return nil
}

// ValidateAndBuild validates the builder's configuration and constructs ACLs.
func (b *ACLBuilder) ValidateAndBuild() ([]ACL, error) {
	err := b.Validate()
	if err != nil {
		return nil, err
	}
	return b.Build(), nil
}

// Build constructs a unique list of ACL entries based on the builder's
// configuration.
//
// This method assumes the configuration has already been validated using
// Validate() to prevent empty ACLs from being built. It performs a
// combinatorial expansion of principals, hosts, operations, and resource types
// into individual ACL entries, eliminating duplicates.
func (b *ACLBuilder) Build() []ACL {
	var acls []ACL
	seen := make(map[string]struct{})

	add := func(principal string, host string, permission Permission, resourceType ResourceType, resource string) {
		for _, op := range b.operations {
			acl := ACL{
				Principal:    principal,
				Resource:     resource,
				ResourceType: resourceType,
				Host:         host,
				Operation:    op,
				Permission:   permission,
			}
			if resourceType != ResourceTypeRegistry {
				acl.PatternType = b.pattern
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
