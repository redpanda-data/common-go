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
	"errors"
	"fmt"
	"strings"

	"buf.build/gen/go/redpandadata/core/connectrpc/go/redpanda/core/admin/v2/adminv2connect"
	adminv2 "buf.build/gen/go/redpandadata/core/protocolbuffers/go/redpanda/core/admin/v2"
	"connectrpc.com/connect"
)

// Role is a representation of a Role as returned by the Admin API.
type Role struct {
	Name string `json:"name" yaml:"name"`
}

// RoleMember is a representation of a principal.
type RoleMember struct {
	Name          string `json:"name" yaml:"name"`
	PrincipalType string `json:"principal_type" yaml:"principal_type"`
}

// RolesResponse represent the response from Roles method.
type RolesResponse struct {
	Roles []Role `json:"roles" yaml:"roles"`
}

// CreateRole is both the request and response from the CreateRole method.
type CreateRole struct {
	RoleName string `json:"role" yaml:"role"`
}

// PatchRoleResponse is the response of the PatchRole method.
type PatchRoleResponse struct {
	RoleName string       `json:"role" yaml:"role"`
	Added    []RoleMember `json:"added" yaml:"added"`
	Removed  []RoleMember `json:"removed" yaml:"removed"`
}

// RoleMemberResponse is the response of the RoleMembers method.
type RoleMemberResponse struct {
	Members []RoleMember `json:"members" yaml:"members"`
}

// RoleDetailResponse is the response of the Role method.
type RoleDetailResponse struct {
	RoleName string       `json:"name" yaml:"name"`
	Members  []RoleMember `json:"members" yaml:"members"`
}

func (m RoleMember) toProto() *adminv2.RoleMember {
	switch m.PrincipalType {
	case "User":
		return &adminv2.RoleMember{
			Member: &adminv2.RoleMember_User{
				User: &adminv2.RoleUser{Name: m.Name},
			},
		}
	case "Group":
		return &adminv2.RoleMember{
			Member: &adminv2.RoleMember_Group{
				Group: &adminv2.RoleGroup{Name: m.Name},
			},
		}
	}
	return nil
}

// roleMemberFromProto converts a proto RoleMember to a Go RoleMember.
func roleMemberFromProto(m *adminv2.RoleMember) RoleMember {
	if m.HasUser() {
		return RoleMember{Name: m.GetUser().Name, PrincipalType: "User"}
	}
	if m.HasGroup() {
		return RoleMember{Name: m.GetGroup().Name, PrincipalType: "Group"}
	}
	return RoleMember{}
}

// Roles returns the roles in Redpanda, use 'prefix', 'principal', and
// 'principalType' to filter the results. principalType must be set along with
// principal. It has no effect on its own.
func (a *AdminAPI) Roles(ctx context.Context, prefix, principal, principalType string) (RolesResponse, error) {
	if principal != "" {
		if principalType == "" {
			return RolesResponse{}, errors.New("principalType can not be empty if principal is set")
		}
	}

	// Validate that the principalType is one of the ones we're expecting
	if principalType != "" && principalType != "User" && principalType != "Group" {
		return RolesResponse{}, fmt.Errorf("unexpected principalType value %q", principalType)
	}

	resp, err := adminv2connect.
		NewSecurityServiceClient(a, "/").
		ListRoles(ctx, connect.NewRequest(&adminv2.ListRolesRequest{}))
	if err != nil {
		return RolesResponse{}, err
	}

	var filtered []Role
	for _, role := range resp.Msg.Roles {
		// Check if a given principal is assigned to the current role
		hasPrincipal := principal == ""
		if !hasPrincipal {
			for _, member := range role.Members {
				if (principalType == "User" && member.HasUser() && principal == member.GetUser().Name) ||
					(principalType == "Group" && member.HasGroup() && principal == member.GetGroup().Name) {
					hasPrincipal = true
					break
				}
			}
		}

		if hasPrincipal && strings.HasPrefix(role.Name, prefix) {
			filtered = append(filtered, Role{Name: role.Name})
		}
	}

	return RolesResponse{Roles: filtered}, nil
}

// Role returns the specific role in Redpanda.
func (a *AdminAPI) Role(ctx context.Context, roleName string) (RoleDetailResponse, error) {
	resp, err := adminv2connect.
		NewSecurityServiceClient(a, "/").
		GetRole(ctx, connect.NewRequest(&adminv2.GetRoleRequest{Name: roleName}))
	if err != nil {
		return RoleDetailResponse{}, err
	}

	var members []RoleMember
	for _, m := range resp.Msg.Role.Members {
		members = append(members, roleMemberFromProto(m))
	}
	return RoleDetailResponse{RoleName: resp.Msg.Role.Name, Members: members}, nil
}

// CreateRole creates a Role in Redpanda with the given name.
func (a *AdminAPI) CreateRole(ctx context.Context, name string) (CreateRole, error) {
	resp, err := adminv2connect.
		NewSecurityServiceClient(a, "/").
		CreateRole(ctx, connect.NewRequest(&adminv2.CreateRoleRequest{Role: &adminv2.Role{Name: name}}))
	if err != nil {
		return CreateRole{}, err
	}

	return CreateRole{resp.Msg.Role.Name}, nil
}

// DeleteRole deletes a Role in Redpanda with the given name. If deleteACL is
// true, Redpanda will delete ACLs bound to the role.
func (a *AdminAPI) DeleteRole(ctx context.Context, name string, deleteACL bool) error {
	_, err := adminv2connect.
		NewSecurityServiceClient(a, "/").
		DeleteRole(ctx, connect.NewRequest(&adminv2.DeleteRoleRequest{Name: name, DeleteAcls: deleteACL}))
	return err
}

// AssignRole adds the given RoleMembers to the role
func (a *AdminAPI) AssignRole(ctx context.Context, roleName string, members []RoleMember) (PatchRoleResponse, error) {
	protoMembers := make([]*adminv2.RoleMember, len(members))
	for i, m := range members {
		protoMembers[i] = m.toProto()
	}

	_, err := adminv2connect.
		NewSecurityServiceClient(a, "/").
		AddRoleMembers(ctx, connect.NewRequest(&adminv2.AddRoleMembersRequest{
			RoleName: roleName,
			Members:  protoMembers,
		}))
	if err != nil {
		return PatchRoleResponse{}, err
	}
	return PatchRoleResponse{RoleName: roleName, Added: members}, nil
}

// UnassignRole removes the given users from the role
func (a *AdminAPI) UnassignRole(ctx context.Context, roleName string, members []RoleMember) (PatchRoleResponse, error) {
	protoMembers := make([]*adminv2.RoleMember, len(members))
	for i, m := range members {
		protoMembers[i] = m.toProto()
	}

	_, err := adminv2connect.
		NewSecurityServiceClient(a, "/").
		RemoveRoleMembers(ctx, connect.NewRequest(&adminv2.RemoveRoleMembersRequest{
			RoleName: roleName,
			Members:  protoMembers,
		}))
	if err != nil {
		return PatchRoleResponse{}, err
	}
	return PatchRoleResponse{RoleName: roleName, Removed: members}, nil
}

// UpdateRoleMembership updates the role membership for the role, adding and removing members.
// If createRole is true, the role is created first (ignoring "already exists" errors).
// Note: create, add and remove are separate RPCs and are _not_ atomic.
func (a *AdminAPI) UpdateRoleMembership(ctx context.Context, roleName string, add, remove []RoleMember, createRole bool) (PatchRoleResponse, error) {
	client := adminv2connect.NewSecurityServiceClient(a, "/")

	if createRole {
		_, err := client.CreateRole(ctx, connect.NewRequest(&adminv2.CreateRoleRequest{
			Role: &adminv2.Role{Name: roleName},
		}))
		if err != nil && connect.CodeOf(err) != connect.CodeAlreadyExists {
			return PatchRoleResponse{}, err
		}
	}

	// When no members are being added or removed and the role isn't being
	// created, validate the role exists by fetching it.
	if !createRole && len(add) == 0 && len(remove) == 0 {
		_, err := client.GetRole(ctx, connect.NewRequest(&adminv2.GetRoleRequest{Name: roleName}))
		return PatchRoleResponse{RoleName: roleName}, err
	}

	if len(add) > 0 {
		if _, err := a.AssignRole(ctx, roleName, add); err != nil {
			return PatchRoleResponse{}, err
		}
	}

	if len(remove) > 0 {
		if _, err := a.UnassignRole(ctx, roleName, remove); err != nil {
			return PatchRoleResponse{}, err
		}
	}

	return PatchRoleResponse{RoleName: roleName, Added: add, Removed: remove}, nil
}

// RoleMembers returns the list of RoleMembers of a given role.
func (a *AdminAPI) RoleMembers(ctx context.Context, roleName string) (RoleMemberResponse, error) {
	resp, err := adminv2connect.
		NewSecurityServiceClient(a, "/").
		GetRole(ctx, connect.NewRequest(&adminv2.GetRoleRequest{Name: roleName}))
	if err != nil {
		return RoleMemberResponse{}, err
	}

	var members []RoleMember
	for _, m := range resp.Msg.Role.Members {
		members = append(members, roleMemberFromProto(m))
	}
	return RoleMemberResponse{Members: members}, nil
}
