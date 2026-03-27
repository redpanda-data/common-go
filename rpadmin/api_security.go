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
	"strings"

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

func toProtoRoleMembers(members []RoleMember) []*adminv2.RoleMember {
	result := make([]*adminv2.RoleMember, len(members))
	for i, m := range members {
		if strings.EqualFold(m.PrincipalType, "Group") {
			result[i] = &adminv2.RoleMember{Member: &adminv2.RoleMember_Group{Group: &adminv2.RoleGroup{Name: m.Name}}}
		} else {
			result[i] = &adminv2.RoleMember{Member: &adminv2.RoleMember_User{User: &adminv2.RoleUser{Name: m.Name}}}
		}
	}
	return result
}

func fromProtoRoleMember(m *adminv2.RoleMember) RoleMember {
	if m.HasUser() {
		return RoleMember{Name: m.GetUser().GetName(), PrincipalType: "User"}
	}
	if m.HasGroup() {
		return RoleMember{Name: m.GetGroup().GetName(), PrincipalType: "Group"}
	}
	return RoleMember{}
}

func fromProtoRoleMembers(members []*adminv2.RoleMember) []RoleMember {
	result := make([]RoleMember, len(members))
	for i, m := range members {
		result[i] = fromProtoRoleMember(m)
	}
	return result
}

// Roles returns the roles in Redpanda, use 'prefix', 'principal', and
// 'principalType' to filter the results. principalType must be set along with
// principal. It has no effect on its own.
func (a *AdminAPI) Roles(ctx context.Context, prefix, principal, principalType string) (RolesResponse, error) {
	if principal != "" && principalType == "" {
		return RolesResponse{}, errors.New("principalType can not be empty if principal is set")
	}
	resp, err := a.SecurityService().ListRoles(ctx, connect.NewRequest(&adminv2.ListRolesRequest{}))
	if err != nil {
		return RolesResponse{}, err
	}
	var roles []Role
	for _, r := range resp.Msg.GetRoles() {
		if prefix != "" && !strings.HasPrefix(r.GetName(), prefix) {
			continue
		}
		if principal != "" {
			found := false
			for _, m := range r.GetMembers() {
				member := fromProtoRoleMember(m)
				if member.Name == principal && strings.EqualFold(member.PrincipalType, principalType) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		roles = append(roles, Role{Name: r.GetName()})
	}
	return RolesResponse{Roles: roles}, nil
}

// Role returns the specific role in Redpanda.
func (a *AdminAPI) Role(ctx context.Context, roleName string) (RoleDetailResponse, error) {
	resp, err := a.SecurityService().GetRole(ctx, connect.NewRequest(&adminv2.GetRoleRequest{Name: roleName}))
	if err != nil {
		return RoleDetailResponse{}, err
	}
	r := resp.Msg.GetRole()
	return RoleDetailResponse{
		RoleName: r.GetName(),
		Members:  fromProtoRoleMembers(r.GetMembers()),
	}, nil
}

// CreateRole creates a Role in Redpanda with the given name.
func (a *AdminAPI) CreateRole(ctx context.Context, name string) (CreateRole, error) {
	resp, err := a.SecurityService().CreateRole(ctx, connect.NewRequest(
		&adminv2.CreateRoleRequest{Role: &adminv2.Role{Name: name}},
	))
	if err != nil {
		return CreateRole{}, err
	}
	return CreateRole{RoleName: resp.Msg.GetRole().GetName()}, nil
}

// DeleteRole deletes a Role in Redpanda with the given name. If deleteACL is
// true, Redpanda will delete ACLs bound to the role.
func (a *AdminAPI) DeleteRole(ctx context.Context, name string, deleteACL bool) error {
	_, err := a.SecurityService().DeleteRole(ctx, connect.NewRequest(&adminv2.DeleteRoleRequest{Name: name, DeleteAcls: deleteACL}))
	return err
}

// AssignRole assigns the role 'roleName' to the passed members.
func (a *AdminAPI) AssignRole(ctx context.Context, roleName string, add []RoleMember) (PatchRoleResponse, error) {
	_, err := a.SecurityService().AddRoleMembers(ctx, connect.NewRequest(
		&adminv2.AddRoleMembersRequest{RoleName: roleName, Members: toProtoRoleMembers(add)},
	))
	if err != nil {
		return PatchRoleResponse{}, err
	}
	return PatchRoleResponse{RoleName: roleName, Added: add}, nil
}

// UnassignRole unassigns the role 'roleName' from the passed members.
func (a *AdminAPI) UnassignRole(ctx context.Context, roleName string, remove []RoleMember) (PatchRoleResponse, error) {
	_, err := a.SecurityService().RemoveRoleMembers(ctx, connect.NewRequest(
		&adminv2.RemoveRoleMembersRequest{RoleName: roleName, Members: toProtoRoleMembers(remove)},
	))
	if err != nil {
		return PatchRoleResponse{}, err
	}
	return PatchRoleResponse{RoleName: roleName, Removed: remove}, nil
}

// UpdateRoleMembership updates the role membership for 'roleName' adding and removing the passed members.
// If createRole is true, the role is created first (existing roles are not an error).
func (a *AdminAPI) UpdateRoleMembership(ctx context.Context, roleName string, add, remove []RoleMember, createRole bool) (PatchRoleResponse, error) {
	svc := a.SecurityService()
	if createRole {
		_, err := svc.CreateRole(ctx, connect.NewRequest(
			&adminv2.CreateRoleRequest{Role: &adminv2.Role{Name: roleName}},
		))
		if err != nil && connect.CodeOf(err) != connect.CodeAlreadyExists {
			return PatchRoleResponse{}, err
		}
	}
	if len(add) > 0 {
		_, err := svc.AddRoleMembers(ctx, connect.NewRequest(
			&adminv2.AddRoleMembersRequest{RoleName: roleName, Members: toProtoRoleMembers(add)},
		))
		if err != nil {
			return PatchRoleResponse{}, err
		}
	}
	if len(remove) > 0 {
		_, err := svc.RemoveRoleMembers(ctx, connect.NewRequest(
			&adminv2.RemoveRoleMembersRequest{RoleName: roleName, Members: toProtoRoleMembers(remove)},
		))
		if err != nil {
			return PatchRoleResponse{}, err
		}
	}
	return PatchRoleResponse{RoleName: roleName, Added: add, Removed: remove}, nil
}

// RoleMembers returns the list of RoleMembers of a given role.
func (a *AdminAPI) RoleMembers(ctx context.Context, roleName string) (RoleMemberResponse, error) {
	resp, err := a.SecurityService().GetRole(ctx, connect.NewRequest(
		&adminv2.GetRoleRequest{Name: roleName},
	))
	if err != nil {
		return RoleMemberResponse{}, err
	}
	return RoleMemberResponse{Members: fromProtoRoleMembers(resp.Msg.GetRole().GetMembers())}, nil
}
