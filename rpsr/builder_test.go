package rpsr

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestACLBuilder_Validate(t *testing.T) {
	tests := []struct {
		name        string
		builder     *ACLBuilder
		expectError bool
	}{
		{
			name: "valid builder with all fields - defaults",
			// builder using all defaults.
			builder: NewACLBuilder().
				AllowPrincipalsOrAny().
				DenyPrincipalsOrAny().
				AllowHostsOrAny().
				DenyHostsOrAny().
				OperationsOrAny().
				PatternOrAny(""),
			expectError: false,
		},
		{
			name: "valid builder with all fields",
			builder: NewACLBuilder().
				AllowPrincipals("User:foo", "RedpandaRole:admin").
				AllowHosts("host-1").
				Operations(OperationRead, OperationWrite).
				Pattern(PatternTypePrefix),
			expectError: false,
		},
		{
			name: "missing allow principals",
			// builder using all defaults.
			builder: NewACLBuilder().
				AllowHostsOrAny().
				Operations().
				Pattern(""),
			expectError: true,
		},
		{
			name: "missing allow principals",
			// builder using all defaults.
			builder: NewACLBuilder().
				AllowHostsOrAny().
				OperationsOrAny().
				Pattern(""),
			expectError: true,
		},
		{
			name: "missing allow Hosts",
			// builder using all defaults.
			builder: NewACLBuilder().
				AllowPrincipals("User:foo").
				Operations().
				Pattern(""),
			expectError: true,
		},
		{
			name: "missing operations",
			builder: NewACLBuilder().
				AllowPrincipals("User:alice").
				AllowHosts("*").
				Pattern(PatternTypeLiteral),
			expectError: true,
		},
		{
			name: "missing pattern",
			builder: NewACLBuilder().
				AllowPrincipals("User:alice").
				AllowHosts("*").
				Operations(OperationRead),
			expectError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.builder.ValidateCreate()
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestACLBuilder_BuildCreate(t *testing.T) {
	tests := []struct {
		name    string
		builder func() *ACLBuilder
		exp     []ACL
	}{
		{
			name: "Valid builder with all fields - wildcards",
			builder: func() *ACLBuilder {
				b := NewACLBuilder().
					AllowPrincipals("*").
					DenyPrincipals("*").
					Subjects("*").
					Registry().
					AllowHostsOrAll().
					DenyHostsOrAll().
					Operations(OperationAll).
					Pattern(PatternTypeLiteral)
				b.PrefixUserExcept("RedpandaRole:admin")
				return b
			},
			exp: []ACL{
				{Principal: "User:*", Host: "*", Operation: OperationAll, PatternType: PatternTypeLiteral, Permission: PermissionAllow, Resource: "*", ResourceType: ResourceTypeSubject},
				{Principal: "User:*", Host: "*", Operation: OperationAll, PatternType: PatternTypeLiteral, Permission: PermissionDeny, Resource: "*", ResourceType: ResourceTypeSubject},
				{Principal: "User:*", Host: "*", Operation: OperationAll, PatternType: PatternTypeLiteral, Permission: PermissionAllow, Resource: "", ResourceType: ResourceTypeRegistry},
				{Principal: "User:*", Host: "*", Operation: OperationAll, PatternType: PatternTypeLiteral, Permission: PermissionDeny, Resource: "", ResourceType: ResourceTypeRegistry},
			},
		},
		{
			name: "Valid builder",
			builder: func() *ACLBuilder {
				b := NewACLBuilder().
					AllowPrincipals("User:alice", "RedpandaRole:admin").
					DenyPrincipals("RedpandaRole:readonly").
					AllowHosts("host-1", "host-2").
					DenyHosts("host-3").
					Subjects("foo-value", "bar-value").
					Operations(OperationRead, OperationDescribe).
					Pattern(PatternTypePrefix)

				b.PrefixUserExcept("RedpandaRole:")
				return b
			},
			exp: []ACL{
				// ALLOW Operations are defined with only the allowed hosts.
				{Principal: "User:alice", Host: "host-1", Operation: OperationRead, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "foo-value", ResourceType: ResourceTypeSubject},
				{Principal: "User:alice", Host: "host-2", Operation: OperationRead, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "foo-value", ResourceType: ResourceTypeSubject},
				{Principal: "User:alice", Host: "host-1", Operation: OperationRead, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "bar-value", ResourceType: ResourceTypeSubject},
				{Principal: "User:alice", Host: "host-2", Operation: OperationRead, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "bar-value", ResourceType: ResourceTypeSubject},
				{Principal: "User:alice", Host: "host-1", Operation: OperationDescribe, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "foo-value", ResourceType: ResourceTypeSubject},
				{Principal: "User:alice", Host: "host-2", Operation: OperationDescribe, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "foo-value", ResourceType: ResourceTypeSubject},
				{Principal: "User:alice", Host: "host-1", Operation: OperationDescribe, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "bar-value", ResourceType: ResourceTypeSubject},
				{Principal: "User:alice", Host: "host-2", Operation: OperationDescribe, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "bar-value", ResourceType: ResourceTypeSubject},

				{Principal: "RedpandaRole:admin", Host: "host-1", Operation: OperationRead, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "foo-value", ResourceType: ResourceTypeSubject},
				{Principal: "RedpandaRole:admin", Host: "host-2", Operation: OperationRead, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "foo-value", ResourceType: ResourceTypeSubject},
				{Principal: "RedpandaRole:admin", Host: "host-1", Operation: OperationRead, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "bar-value", ResourceType: ResourceTypeSubject},
				{Principal: "RedpandaRole:admin", Host: "host-2", Operation: OperationRead, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "bar-value", ResourceType: ResourceTypeSubject},
				{Principal: "RedpandaRole:admin", Host: "host-1", Operation: OperationDescribe, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "foo-value", ResourceType: ResourceTypeSubject},
				{Principal: "RedpandaRole:admin", Host: "host-2", Operation: OperationDescribe, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "foo-value", ResourceType: ResourceTypeSubject},
				{Principal: "RedpandaRole:admin", Host: "host-1", Operation: OperationDescribe, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "bar-value", ResourceType: ResourceTypeSubject},
				{Principal: "RedpandaRole:admin", Host: "host-2", Operation: OperationDescribe, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "bar-value", ResourceType: ResourceTypeSubject},

				// DENY Operations are defined with only the denied hosts.
				{Principal: "RedpandaRole:readonly", Host: "host-3", Operation: OperationRead, PatternType: PatternTypePrefix, Permission: PermissionDeny, Resource: "foo-value", ResourceType: ResourceTypeSubject},
				{Principal: "RedpandaRole:readonly", Host: "host-3", Operation: OperationRead, PatternType: PatternTypePrefix, Permission: PermissionDeny, Resource: "bar-value", ResourceType: ResourceTypeSubject},
				{Principal: "RedpandaRole:readonly", Host: "host-3", Operation: OperationDescribe, PatternType: PatternTypePrefix, Permission: PermissionDeny, Resource: "foo-value", ResourceType: ResourceTypeSubject},
				{Principal: "RedpandaRole:readonly", Host: "host-3", Operation: OperationDescribe, PatternType: PatternTypePrefix, Permission: PermissionDeny, Resource: "bar-value", ResourceType: ResourceTypeSubject},
			},
		},
		{
			name: "deduplicated ACLs for same principal and host",
			builder: func() *ACLBuilder {
				return NewACLBuilder().
					AllowPrincipals("User:alice").
					AllowHosts("host1").
					Subjects("topic1", "topic1").
					Operations(OperationRead, OperationRead).
					Pattern(PatternTypeLiteral)
			},
			exp: []ACL{
				{Principal: "User:alice", Host: "host1", Operation: OperationRead, PatternType: PatternTypeLiteral, Permission: PermissionAllow, Resource: "topic1", ResourceType: ResourceTypeSubject},
			},
		},
		{
			name: "registry only with multiple ops",
			builder: func() *ACLBuilder {
				return NewACLBuilder().
					AllowPrincipals("User:admin").
					AllowHosts("host-1").
					Registry().
					Operations(OperationRead, OperationWrite).
					Pattern(PatternTypeLiteral)
			},
			exp: []ACL{
				{Principal: "User:admin", Host: "host-1", Operation: OperationRead, Permission: PermissionAllow, Resource: "", ResourceType: ResourceTypeRegistry, PatternType: PatternTypeLiteral},
				{Principal: "User:admin", Host: "host-1", Operation: OperationWrite, Permission: PermissionAllow, Resource: "", ResourceType: ResourceTypeRegistry, PatternType: PatternTypeLiteral},
			},
		},
		{
			name: "RedpandaRole subject ACLs",
			builder: func() *ACLBuilder {
				return NewACLBuilder().
					AllowPrincipals("RedpandaRole:admin").
					AllowHosts("host-2").
					Subjects("audit").
					Operations(OperationDescribe).
					Pattern(PatternTypePrefix)
			},
			exp: []ACL{
				{Principal: "RedpandaRole:admin", Host: "host-2", Operation: OperationDescribe, PatternType: PatternTypePrefix, Permission: PermissionAllow, Resource: "audit", ResourceType: ResourceTypeSubject},
			},
		},
		{
			name: "wildcard host and principal",
			builder: func() *ACLBuilder {
				return NewACLBuilder().
					AllowPrincipals("User:*").
					AllowHosts("*").
					Subjects("logs").
					Operations(OperationRead).
					Pattern(PatternTypeLiteral)
			},
			exp: []ACL{
				{Principal: "User:*", Host: "*", Operation: OperationRead, PatternType: PatternTypeLiteral, Permission: PermissionAllow, Resource: "logs", ResourceType: ResourceTypeSubject},
			},
		},
		{
			name: "deny only subject ACL",
			builder: func() *ACLBuilder {
				return NewACLBuilder().
					DenyPrincipals("User:foo").
					DenyHosts("10.0.0.1").
					Subjects("audit").
					Operations(OperationDescribeConfig).
					Pattern(PatternTypeLiteral)
			},
			exp: []ACL{
				{Principal: "User:foo", Host: "10.0.0.1", Operation: OperationDescribeConfig, PatternType: PatternTypeLiteral, Permission: PermissionDeny, Resource: "audit", ResourceType: ResourceTypeSubject},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := tt.builder()
			require.ElementsMatch(t, tt.exp, b.BuildCreate())
		})
	}
}

func TestACLBuilder_BuildFilter(t *testing.T) {
	tests := []struct {
		name    string
		builder func() *ACLBuilder
		exp     []ACL
	}{
		{
			name: "any allow and deny principals and hosts",
			builder: func() *ACLBuilder {
				return NewACLBuilder().
					AllowPrincipalsOrAny().
					DenyPrincipalsOrAny().
					AllowHostsOrAny().
					DenyHostsOrAny().
					OperationsOrAny().
					PatternOrAny("").
					AnyResources()
			},
			exp: []ACL{
				{Principal: "", Host: "", Operation: "", Permission: "", Resource: "", ResourceType: "", PatternType: ""},
			},
		},
		{
			name: "filter for specific principal, any host and any operation",
			builder: func() *ACLBuilder {
				return NewACLBuilder().
					AllowPrincipals("User:alice").
					AllowHostsOrAny().
					OperationsOrAny().
					PatternOrAny(PatternTypePrefix).
					Subjects("topic-A")
			},
			exp: []ACL{
				{Principal: "User:alice", Host: "", Operation: "", Permission: PermissionAllow, Resource: "topic-A", ResourceType: ResourceTypeSubject, PatternType: PatternTypePrefix},
			},
		},
		{
			name: "filter with deny principal and host for specific resource",
			builder: func() *ACLBuilder {
				return NewACLBuilder().
					DenyPrincipals("User:bob").
					DenyHosts("10.0.0.1").
					Operations(OperationWrite).
					Pattern(PatternTypeLiteral).
					Subjects("audit")
			},
			exp: []ACL{
				{Principal: "User:bob", Host: "10.0.0.1", Operation: OperationWrite, Permission: PermissionDeny, Resource: "audit", ResourceType: ResourceTypeSubject, PatternType: PatternTypeLiteral},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := tt.builder()
			require.ElementsMatch(t, tt.exp, b.BuildFilter())
		})
	}
}
