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
	"net/http"
	"net/url"
)

const (
	baseSecurityEndpoint = "/v1/security/"
	baseRoleEndpoint     = baseSecurityEndpoint + "roles"
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

type patchRoleRequest struct {
	Add    []RoleMember `json:"add,omitempty"`
	Remove []RoleMember `json:"remove,omitempty"`
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

// Roles returns the roles in Redpanda, use 'prefix', 'principal', and
// 'principalType' to filter the results. principalType must be set along with
// principal. It has no effect on its own.
func (a *AdminAPI) Roles(ctx context.Context, prefix, principal, principalType string) (RolesResponse, error) {
	var roles RolesResponse
	u, qs := baseRoleEndpoint, url.Values{}
	if prefix != "" {
		qs.Add("filter", prefix)
	}
	if principal != "" {
		if principalType == "" {
			return RolesResponse{}, errors.New("principalType can not be empty if principal is set")
		}
		qs.Add("principal", principal)
		qs.Add("principal_type", principalType)
	}
	if queryString := qs.Encode(); queryString != "" {
		u += "?" + queryString
	}
	return roles, a.sendAny(ctx, http.MethodGet, u, nil, &roles)
}

// Role returns the specific role in Redpanda.
func (a *AdminAPI) Role(ctx context.Context, roleName string) (RoleDetailResponse, error) {
	var role RoleDetailResponse

	return role, a.sendAny(ctx,
		http.MethodGet,
		fmt.Sprintf("%v/%v", baseRoleEndpoint, roleName),
		nil,
		&role)
}

// CreateRole creates a Role in Redpanda with the given name.
func (a *AdminAPI) CreateRole(ctx context.Context, name string) (CreateRole, error) {
	var res CreateRole
	return res, a.sendAny(ctx, http.MethodPost, baseRoleEndpoint, CreateRole{name}, &res)
}

// DeleteRole deletes a Role in Redpanda with the given name. If deleteACL is
// true, Redpanda will delete ACLs bound to the role.
func (a *AdminAPI) DeleteRole(ctx context.Context, name string, deleteACL bool) error {
	return a.sendAny(
		ctx,
		http.MethodDelete,
		fmt.Sprintf("%v/%v?delete_acls=%v", baseRoleEndpoint, name, deleteACL),
		nil,
		nil,
	)
}

// AssignRole assign the role 'roleName' to the passed members.
func (a *AdminAPI) AssignRole(ctx context.Context, roleName string, add []RoleMember) (PatchRoleResponse, error) {
	var res PatchRoleResponse
	body := patchRoleRequest{
		Add: add,
	}
	return res, a.sendAny(ctx, http.MethodPost, fmt.Sprintf("%v/%v/members", baseRoleEndpoint, roleName), body, &res)
}

// UnassignRole unassigns the role 'roleName' from the passed members.
func (a *AdminAPI) UnassignRole(ctx context.Context, roleName string, remove []RoleMember) (PatchRoleResponse, error) {
	var res PatchRoleResponse
	body := patchRoleRequest{
		Remove: remove,
	}
	return res, a.sendAny(ctx, http.MethodPost, fmt.Sprintf("%v/%v/members", baseRoleEndpoint, roleName), body, &res)
}

// UpdateRoleMembership updates the role membership for 'roleName' adding and removing the passed members.
func (a *AdminAPI) UpdateRoleMembership(ctx context.Context, roleName string, add, remove []RoleMember, createRole bool) (PatchRoleResponse, error) {
	var res PatchRoleResponse
	body := patchRoleRequest{
		Add:    add,
		Remove: remove,
	}
	return res, a.sendAny(ctx,
		http.MethodPost,
		fmt.Sprintf("%v/%v/members?create=%v", baseRoleEndpoint, roleName, createRole),
		body,
		&res)
}

// RoleMembers returns the list of RoleMembers of a given role.
func (a *AdminAPI) RoleMembers(ctx context.Context, roleName string) (RoleMemberResponse, error) {
	var res RoleMemberResponse
	return res, a.sendAny(ctx, http.MethodGet, fmt.Sprintf("%v/%v/members", baseRoleEndpoint, roleName), nil, &res)
}

// SecurityReport describes the security posture of a node's interfaces plus any
// detected alerts.
type SecurityReport struct {
	Interfaces SecurityReportInterfaces `json:"interfaces"`
	Alerts     []SecurityReportAlert    `json:"alerts"`
}

// SecurityReportInterfaces holds the per-interface security posture. Fields are
// only present when the corresponding interface is configured.
type SecurityReportInterfaces struct {
	Kafka                []KafkaInterfaceSecurityReport          `json:"kafka"`
	RPC                  RPCInterfaceSecurityReport              `json:"rpc"`
	Admin                []AdminInterfaceSecurityReport          `json:"admin"`
	Pandaproxy           []PandaproxyInterfaceSecurityReport     `json:"pandaproxy,omitempty"`
	SchemaRegistry       []SchemaRegistryInterfaceSecurityReport `json:"schema_registry,omitempty"`
	SchemaRegistryClient *ClientSecurityReport                   `json:"schema_registry_client,omitempty"`
	AuditLogClient       *ClientSecurityReport                   `json:"audit_log_client,omitempty"`
}

// KafkaInterfaceSecurityReport is the security posture of a Kafka listener.
type KafkaInterfaceSecurityReport struct {
	Name                    string   `json:"name"`
	Host                    string   `json:"host"`
	Port                    int      `json:"port"`
	AdvertisedHost          string   `json:"advertised_host"`
	AdvertisedPort          int      `json:"advertised_port"`
	TLSEnabled              bool     `json:"tls_enabled"`
	MutualTLSEnabled        bool     `json:"mutual_tls_enabled"`
	AuthorizationEnabled    bool     `json:"authorization_enabled"`
	AuthenticationMethod    string   `json:"authentication_method"` // One of: SASL, mTLS, None.
	SupportedSASLMechanisms []string `json:"supported_sasl_mechanisms,omitempty"`
}

// RPCInterfaceSecurityReport is the security posture of the RPC listener.
type RPCInterfaceSecurityReport struct {
	Host             string `json:"host"`
	Port             int    `json:"port"`
	AdvertisedHost   string `json:"advertised_host"`
	AdvertisedPort   int    `json:"advertised_port"`
	TLSEnabled       bool   `json:"tls_enabled"`
	MutualTLSEnabled bool   `json:"mutual_tls_enabled"`
}

// AdminInterfaceSecurityReport is the security posture of an admin listener.
type AdminInterfaceSecurityReport struct {
	Name                  string   `json:"name"`
	Host                  string   `json:"host"`
	Port                  int      `json:"port"`
	TLSEnabled            bool     `json:"tls_enabled"`
	MutualTLSEnabled      bool     `json:"mutual_tls_enabled"`
	AuthorizationEnabled  bool     `json:"authorization_enabled"`
	AuthenticationMethods []string `json:"authentication_methods"` // Values: BASIC, OIDC.
}

// SchemaRegistryInterfaceSecurityReport is the security posture of a schema
// registry listener.
type SchemaRegistryInterfaceSecurityReport struct {
	Name                  string   `json:"name"`
	Host                  string   `json:"host"`
	Port                  int      `json:"port"`
	TLSEnabled            bool     `json:"tls_enabled"`
	MutualTLSEnabled      bool     `json:"mutual_tls_enabled"`
	AuthorizationEnabled  bool     `json:"authorization_enabled"`
	AuthenticationMethods []string `json:"authentication_methods"` // Values: BASIC, OIDC.
}

// PandaproxyInterfaceSecurityReport is the security posture of a pandaproxy
// listener.
type PandaproxyInterfaceSecurityReport struct {
	Name                           string   `json:"name"`
	Host                           string   `json:"host"`
	Port                           int      `json:"port"`
	AdvertisedHost                 string   `json:"advertised_host"`
	AdvertisedPort                 int      `json:"advertised_port"`
	TLSEnabled                     bool     `json:"tls_enabled"`
	MutualTLSEnabled               bool     `json:"mutual_tls_enabled"`
	AuthorizationEnabled           bool     `json:"authorization_enabled"`
	AuthenticationMethods          []string `json:"authentication_methods"`           // Values: BASIC, OIDC.
	ConfiguredAuthenticationMethod string   `json:"configured_authentication_method"` // One of: None, SCRAM_Configured, SCRAM_Proxied.
}

// HostPort is a host and port pair.
type HostPort struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// ClientSecurityReport is the security posture of an internal client (schema
// registry client or audit log client).
type ClientSecurityReport struct {
	KafkaListenerName              string     `json:"kafka_listener_name"`
	Brokers                        []HostPort `json:"brokers"`
	TLSEnabled                     bool       `json:"tls_enabled"`
	MutualTLSEnabled               bool       `json:"mutual_tls_enabled"`
	ConfiguredAuthenticationMethod string     `json:"configured_authentication_method"` // One of: None, SCRAM_Configured, SCRAM_Ephemeral.
}

// SecurityReportAlert is a single security posture alert.
type SecurityReportAlert struct {
	AffectedInterface string `json:"affected_interface,omitempty"` // Absent for cluster-wide alerts.
	ListenerName      string `json:"listener_name,omitempty"`
	Issue             string `json:"issue"`
	Description       string `json:"description"`
}

// SecurityReport returns the security posture report of the target node.
func (a *AdminAPI) SecurityReport(ctx context.Context) (SecurityReport, error) {
	var response SecurityReport
	return response, a.sendAny(ctx, http.MethodGet, "/v1/security/report", nil, &response)
}
