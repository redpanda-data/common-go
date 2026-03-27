// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package rpadmin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	adminv2connect "buf.build/gen/go/redpandadata/core/connectrpc/go/redpanda/core/admin/v2/adminv2connect"
	adminv2 "buf.build/gen/go/redpandadata/core/protocolbuffers/go/redpanda/core/admin/v2"
	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSecurityService lets individual tests override only the methods they need.
type mockSecurityService struct {
	adminv2connect.UnimplementedSecurityServiceHandler
	listRoles      func(context.Context, *connect.Request[adminv2.ListRolesRequest]) (*connect.Response[adminv2.ListRolesResponse], error)
	createRole     func(context.Context, *connect.Request[adminv2.CreateRoleRequest]) (*connect.Response[adminv2.CreateRoleResponse], error)
	addRoleMembers func(context.Context, *connect.Request[adminv2.AddRoleMembersRequest]) (*connect.Response[adminv2.AddRoleMembersResponse], error)
}

func (m *mockSecurityService) ListRoles(ctx context.Context, req *connect.Request[adminv2.ListRolesRequest]) (*connect.Response[adminv2.ListRolesResponse], error) {
	if m.listRoles != nil {
		return m.listRoles(ctx, req)
	}
	return m.UnimplementedSecurityServiceHandler.ListRoles(ctx, req)
}

func (m *mockSecurityService) CreateRole(ctx context.Context, req *connect.Request[adminv2.CreateRoleRequest]) (*connect.Response[adminv2.CreateRoleResponse], error) {
	if m.createRole != nil {
		return m.createRole(ctx, req)
	}
	return m.UnimplementedSecurityServiceHandler.CreateRole(ctx, req)
}

func (m *mockSecurityService) AddRoleMembers(ctx context.Context, req *connect.Request[adminv2.AddRoleMembersRequest]) (*connect.Response[adminv2.AddRoleMembersResponse], error) {
	if m.addRoleMembers != nil {
		return m.addRoleMembers(ctx, req)
	}
	return m.UnimplementedSecurityServiceHandler.AddRoleMembers(ctx, req)
}

func newSecurityTestClient(t *testing.T, svc *mockSecurityService) *AdminAPI {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := adminv2connect.NewSecurityServiceHandler(svc)
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := NewAdminAPI([]string{server.URL}, new(NopAuth), nil)
	require.NoError(t, err)
	return client
}

func TestRoleMemberConversion(t *testing.T) {
	tests := []struct {
		name     string
		input    RoleMember
		wantType string
	}{
		{"user", RoleMember{Name: "alice", PrincipalType: "User"}, "User"},
		{"group", RoleMember{Name: "devs", PrincipalType: "Group"}, "Group"},
		{"group case-insensitive", RoleMember{Name: "ops", PrincipalType: "group"}, "Group"},
		{"unknown type treated as user", RoleMember{Name: "svc", PrincipalType: "service"}, "User"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protos := toProtoRoleMembers([]RoleMember{tt.input})
			require.Len(t, protos, 1)
			back := fromProtoRoleMember(protos[0])
			assert.Equal(t, tt.input.Name, back.Name)
			assert.Equal(t, tt.wantType, back.PrincipalType)
		})
	}
}

func TestRolesFiltering(t *testing.T) {
	client := newSecurityTestClient(t, &mockSecurityService{
		listRoles: func(_ context.Context, _ *connect.Request[adminv2.ListRolesRequest]) (*connect.Response[adminv2.ListRolesResponse], error) {
			return connect.NewResponse(&adminv2.ListRolesResponse{Roles: []*adminv2.Role{
				{Name: "admin", Members: []*adminv2.RoleMember{
					{Member: &adminv2.RoleMember_User{User: &adminv2.RoleUser{Name: "alice"}}},
				}},
				{Name: "viewer", Members: []*adminv2.RoleMember{
					{Member: &adminv2.RoleMember_User{User: &adminv2.RoleUser{Name: "bob"}}},
				}},
				{Name: "admin-readonly"},
			}}), nil
		},
	})

	t.Run("prefix", func(t *testing.T) {
		resp, err := client.Roles(context.Background(), "admin", "", "")
		require.NoError(t, err)
		assert.ElementsMatch(t, []Role{{Name: "admin"}, {Name: "admin-readonly"}}, resp.Roles)
	})

	t.Run("principal and type", func(t *testing.T) {
		resp, err := client.Roles(context.Background(), "", "alice", "User")
		require.NoError(t, err)
		assert.Equal(t, []Role{{Name: "admin"}}, resp.Roles)
	})

	t.Run("principal without principalType errors", func(t *testing.T) {
		_, err := client.Roles(context.Background(), "", "alice", "")
		require.Error(t, err)
	})
}

func TestUpdateRoleMembershipCreateRoleErrors(t *testing.T) {
	add := []RoleMember{{Name: "alice", PrincipalType: "User"}}

	t.Run("AlreadyExists is swallowed", func(t *testing.T) {
		_, err := newSecurityTestClient(t, &mockSecurityService{
			createRole: func(_ context.Context, _ *connect.Request[adminv2.CreateRoleRequest]) (*connect.Response[adminv2.CreateRoleResponse], error) {
				return nil, connect.NewError(connect.CodeAlreadyExists, nil)
			},
			addRoleMembers: func(_ context.Context, _ *connect.Request[adminv2.AddRoleMembersRequest]) (*connect.Response[adminv2.AddRoleMembersResponse], error) {
				return connect.NewResponse(&adminv2.AddRoleMembersResponse{}), nil
			},
		}).UpdateRoleMembership(context.Background(), "r", add, nil, true)
		require.NoError(t, err)
	})

	t.Run("other create error propagates", func(t *testing.T) {
		_, err := newSecurityTestClient(t, &mockSecurityService{
			createRole: func(_ context.Context, _ *connect.Request[adminv2.CreateRoleRequest]) (*connect.Response[adminv2.CreateRoleResponse], error) {
				return nil, connect.NewError(connect.CodePermissionDenied, nil)
			},
		}).UpdateRoleMembership(context.Background(), "r", add, nil, true)
		require.Error(t, err)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})
}
