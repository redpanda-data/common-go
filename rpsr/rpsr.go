// Package rpsr provides a client to interact with the Redpanda Schema Registry.
package rpsr

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/twmb/franz-go/pkg/sr"
)

// ACLClient defines methods to manage Redpanda's Schema Registry ACLs.
type ACLClient interface {
	ListACLs(ctx context.Context, filter ACLFilter) ([]ACL, error)
	CreateACLs(ctx context.Context, acls []ACL) error
	DeleteACLs(ctx context.Context, acls []ACL) error
}

// Client is a Redpanda Schema Registry client that embeds the franz-go schema
// registry client. It expects all the configurations to be set up in the
// franz-go sr.Client.
type Client struct {
	*sr.Client
}

// NewClient creates a new ACL-aware Schema Registry client. srCL must not be
// nil.
func NewClient(srCl *sr.Client) (*Client, error) {
	if srCl == nil {
		return nil, errors.New("schema registry client cannot be nil")
	}
	return &Client{Client: srCl}, nil
}

// ListACLs retrieves ACL entries matching the provided filter.
func (c *Client) ListACLs(ctx context.Context, filter ACLFilter) ([]ACL, error) {
	var result []ACL
	return result, c.Do(ctx, http.MethodGet, "/security/acls"+filter.ToQuery(), nil, &result)
}

// CreateACLs creates new ACL entries.
func (c *Client) CreateACLs(ctx context.Context, acls []ACL) error {
	return c.Do(ctx, http.MethodPost, "/security/acls", acls, nil)
}

// DeleteACLs deletes existing ACL entries.
func (c *Client) DeleteACLs(ctx context.Context, acls []ACL) error {
	return c.Do(ctx, http.MethodDelete, "/security/acls", acls, nil)
}

// PrincipalType defines the type of principal (USER or REDPANDA_ROLE).
type PrincipalType string

// ResourceType defines the type of resource an ACL applies to.
type ResourceType string

// PatternType defines how the resource name is matched.
type PatternType string

// Operation defines the action allowed or denied by an ACL.
type Operation string

// Permission defines whether an ACL allows or denies an operation.
type Permission string

const (
	// PrincipalTypeUser is a principal representing a user.
	PrincipalTypeUser PrincipalType = "USER"
	// PrincipalTypeRedpandaRole is a principal representing a Redpanda role.
	PrincipalTypeRedpandaRole PrincipalType = "REDPANDA_ROLE"

	// ResourceTypeRegistry represents a registry resource.
	ResourceTypeRegistry ResourceType = "REGISTRY"
	// ResourceTypeSubject represents a subject (schema) resource.
	ResourceTypeSubject ResourceType = "SUBJECT"

	// PatternTypeLiteral matches a resource name exactly.
	PatternTypeLiteral PatternType = "LITERAL"
	// PatternTypePrefix matches resource names by prefix.
	PatternTypePrefix PatternType = "PREFIX"

	// OperationAll represents ALL operations.
	OperationAll Operation = "ALL"
	// OperationRead is the READ operation.
	OperationRead Operation = "READ"
	// OperationWrite is the WRITE operation.
	OperationWrite Operation = "WRITE"
	// OperationRemove is the REMOVE operation.
	OperationRemove Operation = "REMOVE"
	// OperationDescribe is the DESCRIBE operation.
	OperationDescribe Operation = "DESCRIBE"
	// OperationDescribeConfig is the DESCRIBE_CONFIG operation.
	OperationDescribeConfig Operation = "DESCRIBE_CONFIGS"
	// OperationAlter is the ALTER operation.
	OperationAlter Operation = "ALTER"
	// OperationAlterConfig is the ALTER_CONFIG operation.
	OperationAlterConfig Operation = "ALTER_CONFIGS"

	// PermissionAllow permits the operation.
	PermissionAllow Permission = "ALLOW"
	// PermissionDeny denies the operation.
	PermissionDeny Permission = "DENY"
)

// ACL represents an individual access control rule.
type ACL struct {
	Principal     string        `json:"principal"`
	PrincipalType PrincipalType `json:"principal_type"` // USER or REDPANDA_ROLE
	Resource      string        `json:"resource"`
	ResourceType  ResourceType  `json:"resource_type"`
	PatternType   PatternType   `json:"pattern_type"` // LITERAL or PREFIX
	Host          string        `json:"host"`
	Operation     Operation     `json:"operation"`  // READ, WRITE, etc.
	Permission    Permission    `json:"permission"` // ALLOW or DENY
}

// ACLFilter builds query parameters for listing ACLs. Empty fields are ignored.
type ACLFilter struct {
	Principal     string
	PrincipalType string
	Resource      string
	ResourceType  string
	PatternType   string
	Host          string
	Operation     string
	Permission    string
}

// ToQuery converts the filter into a URL query string.
func (f *ACLFilter) ToQuery() string {
	v := url.Values{}
	if f.Principal != "" {
		v.Set("principal", f.Principal)
	}
	if f.PrincipalType != "" {
		v.Set("principal_type", f.PrincipalType)
	}
	if f.Resource != "" {
		v.Set("resource", f.Resource)
	}
	if f.ResourceType != "" {
		v.Set("resource_type", f.ResourceType)
	}
	if f.PatternType != "" {
		v.Set("pattern_type", f.PatternType)
	}
	if f.Host != "" {
		v.Set("host", f.Host)
	}
	if f.Operation != "" {
		v.Set("operation", f.Operation)
	}
	if f.Permission != "" {
		v.Set("permission", f.Permission)
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}
