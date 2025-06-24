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
				OperationsOrAll().
				PatternOrLiteral(""),
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
				OperationsOrAll().
				PatternOrLiteral(""),
			expectError: true,
		},
		{
			name: "missing allow principals",
			// builder using all defaults.
			builder: NewACLBuilder().
				AllowHostsOrAny().
				OperationsOrAll().
				PatternOrLiteral(""),
			expectError: true,
		},
		{
			name: "missing allow Hosts",
			// builder using all defaults.
			builder: NewACLBuilder().
				AllowPrincipals("User:foo").
				OperationsOrAll().
				PatternOrLiteral(""),
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
			err := tt.builder.Validate()
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestACLBuilder_Build(t *testing.T) {
	tests := []struct {
		name    string
		builder func() *ACLBuilder
		exp     []ACL
	}{
		{
			name: "Valid builder with all fields - defaults",
			builder: func() *ACLBuilder {
				b := NewACLBuilder().
					AllowPrincipals("*").
					DenyPrincipalsOrAny().
					SubjectsOrAny().
					Registry().
					AllowHostsOrAny().
					DenyHostsOrAny().
					OperationsOrAll().
					PatternOrLiteral("")
				b.PrefixUserExcept("RedpandaRole:admin")
				return b
			},
			exp: []ACL{
				{Principal: "User:*", Host: "*", Operation: OperationAll, PatternType: PatternTypeLiteral, Permission: PermissionAllow, Resource: "*", ResourceType: ResourceTypeSubject},
				{Principal: "User:*", Host: "*", Operation: OperationAll, PatternType: PatternTypeLiteral, Permission: PermissionDeny, Resource: "*", ResourceType: ResourceTypeSubject},
				{Principal: "User:*", Host: "*", Operation: OperationAll, PatternType: "", Permission: PermissionAllow, Resource: "", ResourceType: ResourceTypeRegistry},
				{Principal: "User:*", Host: "*", Operation: OperationAll, PatternType: "", Permission: PermissionDeny, Resource: "", ResourceType: ResourceTypeRegistry},
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
				{Principal: "User:admin", Host: "host-1", Operation: OperationRead, Permission: PermissionAllow, Resource: "", ResourceType: ResourceTypeRegistry},
				{Principal: "User:admin", Host: "host-1", Operation: OperationWrite, Permission: PermissionAllow, Resource: "", ResourceType: ResourceTypeRegistry},
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
			require.ElementsMatch(t, tt.exp, b.Build())
		})
	}
}
