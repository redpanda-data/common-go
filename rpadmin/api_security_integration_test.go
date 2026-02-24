//go:build integration

package rpadmin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"
)

func TestSecurityService(t *testing.T) {
	ctx := t.Context()
	redpandaContainer, err := redpanda.Run(ctx, "redpandadata/redpanda-nightly:v0.0.0-20260224gitf6ddce2")

	require.NoError(t, err)
	t.Cleanup(func() {
		redpandaContainer.Terminate(ctx)
	})

	addr, err := redpandaContainer.AdminAPIAddress(ctx)
	require.NoError(t, err)

	client, err := NewAdminAPI([]string{addr}, &NopAuth{}, nil)
	require.NoError(t, err)

	t.Run("list empty", func(t *testing.T) {
		resp, err := client.Roles(ctx, "", "", "")
		require.NoError(t, err)
		assert.Equal(t, 0, len(resp.Roles))
	})

	t.Run("create", func(t *testing.T) {
		resp, err := client.CreateRole(ctx, "testrole")
		require.NoError(t, err)
		assert.Equal(t, "testrole", resp.RoleName)
	})

	t.Run("get", func(t *testing.T) {
		resp, err := client.Role(ctx, "testrole")
		require.NoError(t, err)
		assert.Equal(t, "testrole", resp.RoleName)
		assert.Equal(t, 0, len(resp.Members))
	})

	t.Run("assign members", func(t *testing.T) {
		require.NoError(t, client.CreateUser(ctx, "testuser", "testpass", ScramSha256))
		resp, err := client.AssignRole(ctx, "testrole", []RoleMember{
			{Name: "testuser", PrincipalType: "User"},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, len(resp.Removed))
		assert.Equal(t, 1, len(resp.Added))
	})

	t.Run("list members", func(t *testing.T) {
		resp, err := client.RoleMembers(ctx, "testrole")
		require.NoError(t, err)
		assert.Equal(t, 1, len(resp.Members))
	})

	t.Run("list filtered", func(t *testing.T) {
		cases := []struct {
			name          string
			prefix        string
			principalType string
			principal     string
			count         int
		}{
			{"unfiltered", "", "", "", 1},
			{"known prefix", "test", "", "", 1},
			{"unknown prefix", "user", "", "", 0},
			{"user principal", "", "User", "testuser", 1},
			{"unknown user principal", "", "User", "testuse", 0},
			{"group principal", "", "Group", "testuser", 0},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				resp, err := client.Roles(ctx, tc.prefix, tc.principal, tc.principalType)
				require.NoError(t, err)
				assert.Equal(t, tc.count, len(resp.Roles))
			})
		}
	})

	t.Run("unassign members", func(t *testing.T) {
		resp, err := client.UnassignRole(ctx, "testrole", []RoleMember{
			{Name: "testuser", PrincipalType: "User"},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, len(resp.Removed))
		assert.Equal(t, 0, len(resp.Added))
	})

	t.Run("update membership", func(t *testing.T) {
		require.NoError(t, client.CreateUser(ctx, "testuser2", "testpass2", ScramSha256))
		// Add both users back in one call
		resp, err := client.UpdateRoleMembership(ctx, "testrole", []RoleMember{
			{Name: "testuser", PrincipalType: "User"},
			{Name: "testuser2", PrincipalType: "User"},
		}, nil, false)
		require.NoError(t, err)
		assert.Equal(t, 2, len(resp.Added))
		assert.Equal(t, 0, len(resp.Removed))
	})

	t.Run("delete", func(t *testing.T) {
		require.NoError(t, client.DeleteRole(ctx, "testrole", false))

		resp, err := client.Roles(ctx, "", "", "")
		require.NoError(t, err)
		assert.Equal(t, 0, len(resp.Roles))
	})
}
