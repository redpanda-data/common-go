// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package rpsr provides a client to interact with the Redpanda Schema Registry.
package rpsr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/twmb/franz-go/pkg/sr"
	"golang.org/x/sync/errgroup"
)

// ResourceType defines the type of resource an ACL applies to.
type ResourceType string

// PatternType defines how the resource name is matched.
type PatternType string

// Operation defines the action allowed or denied by an ACL.
type Operation string

// Permission defines whether an ACL allows or denies an operation.
type Permission string

const (
	// ResourceTypeAny matches any resource type, either literal or prefixed.
	ResourceTypeAny ResourceType = "ANY"
	// ResourceTypeRegistry represents a registry resource.
	ResourceTypeRegistry ResourceType = "REGISTRY"
	// ResourceTypeSubject represents a subject (schema) resource.
	ResourceTypeSubject ResourceType = "SUBJECT"

	// PatternTypeLiteral matches a resource name exactly.
	PatternTypeLiteral PatternType = "LITERAL"
	// PatternTypePrefix matches resource names by prefix.
	PatternTypePrefix PatternType = "PREFIXED"
	// PatternTypeAny matches any resource name, either literal or prefixed.
	PatternTypeAny PatternType = "ANY"

	// OperationAll represents ALL operations.
	OperationAll Operation = "ALL"
	// OperationAny matches any operation, used for filtering (listing and deleting).
	OperationAny Operation = "ANY"
	// OperationRead is the READ operation.
	OperationRead Operation = "READ"
	// OperationWrite is the WRITE operation.
	OperationWrite Operation = "WRITE"
	// OperationDelete is the DELETE operation.
	OperationDelete Operation = "DELETE"
	// OperationDescribe is the DESCRIBE operation.
	OperationDescribe Operation = "DESCRIBE"
	// OperationDescribeConfig is the DESCRIBE_CONFIG operation.
	OperationDescribeConfig Operation = "DESCRIBE_CONFIGS"
	// OperationAlter is the ALTER operation.
	OperationAlter Operation = "ALTER"
	// OperationAlterConfig is the ALTER_CONFIG operation.
	OperationAlterConfig Operation = "ALTER_CONFIGS"

	// PermissionAny matches any permission, either ALLOW or DENY.
	PermissionAny Permission = "ANY"
	// PermissionAllow permits the operation.
	PermissionAllow Permission = "ALLOW"
	// PermissionDeny denies the operation.
	PermissionDeny Permission = "DENY"
)

// ErrNotFound is returned when no ACLs match the provided filter.
//
//nolint:staticcheck // this comes from Redpanda.
var ErrNotFound = errors.New("Not found")

// ACL represents an individual access control rule.
type ACL struct {
	Principal    string       `json:"principal"`
	Resource     string       `json:"resource"`
	ResourceType ResourceType `json:"resource_type"`
	PatternType  PatternType  `json:"pattern_type"` // LITERAL or PREFIXED
	Host         string       `json:"host"`
	Operation    Operation    `json:"operation"`  // READ, WRITE, etc.
	Permission   Permission   `json:"permission"` // ALLOW or DENY
}

// AddQueryToContext converts the filter into a URL query params and adds it to
// the context using sr.WithParams.
func (f *ACL) AddQueryToContext(ctx context.Context) context.Context {
	v := url.Values{}
	if f.Principal != "" {
		v.Set("principal", f.Principal)
	}
	if f.Resource != "" {
		v.Set("resource", f.Resource)
	}
	if f.ResourceType != "" {
		v.Set("resource_type", string(f.ResourceType))
	}
	if f.PatternType != "" {
		v.Set("pattern_type", string(f.PatternType))
	}
	if f.Host != "" {
		v.Set("host", f.Host)
	}
	if f.Operation != "" {
		v.Set("operation", string(f.Operation))
	}
	if f.Permission != "" {
		v.Set("permission", string(f.Permission))
	}
	if len(v) == 0 {
		return ctx
	}
	return sr.WithParams(ctx, sr.RawParams(v))
}

// ACLClient defines methods to manage Redpanda's Schema Registry ACLs.
type ACLClient interface {
	CreateACLs(ctx context.Context, acls []ACL) error
	DeleteACLs(ctx context.Context, acls []ACL) error
	ListACLs(ctx context.Context, filter *ACL) ([]ACL, error)
	ListACLsBatch(ctx context.Context, filter []ACL) ([]ACL, error)
}

// Client is a Redpanda Schema Registry client that embeds the franz-go schema
// registry client. It expects all the configurations to be set up in the
// franz-go sr.Client.
type Client struct {
	*sr.Client
}

// Ensure that Client implements the ACLClient interface.
var _ ACLClient = (*Client)(nil)

// NewClient creates a new ACL-aware Schema Registry client. srCL must not be
// nil.
func NewClient(srCl *sr.Client) (*Client, error) {
	if srCl == nil {
		return nil, errors.New("schema registry client cannot be nil")
	}
	return &Client{Client: srCl}, nil
}

// CreateACLs creates new ACL entries.
func (c *Client) CreateACLs(ctx context.Context, acls []ACL) error {
	return c.Do(ctx, http.MethodPost, "/security/acls", acls, nil)
}

// DeleteACLs deletes existing ACL entries.
func (c *Client) DeleteACLs(ctx context.Context, acls []ACL) error {
	return c.Do(ctx, http.MethodDelete, "/security/acls", acls, nil)
}

// ListACLs retrieves all ACL entries that match the provided filter. Empty
// fields will be ignored, meaning they will match any value.
func (c *Client) ListACLs(ctx context.Context, filter *ACL) ([]ACL, error) {
	var result []ACL
	if filter != nil {
		ctx = filter.AddQueryToContext(ctx)
	}
	return result, c.Do(ctx, http.MethodGet, "/security/acls", nil, &result)
}

// ListACLsBatch retrieves all ACL entries that match any of the provided
// filters. It performs concurrent requests for each filter and combines the
// results, trimming repeated ACLs.
func (c *Client) ListACLsBatch(ctx context.Context, filters []ACL) ([]ACL, error) {
	var (
		allACLs []ACL
		mu      sync.Mutex
	)
	g, egCtx := errgroup.WithContext(ctx)
	for _, f := range filters {
		g.Go(func() error {
			acls, err := c.ListACLs(egCtx, &f)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					// No ACLs found means no matches. Continue.
					return nil
				}
				return err
			}
			mu.Lock()
			allACLs = append(allACLs, acls...)
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	allACLs = trimRepeatedACLs(allACLs)
	return allACLs, nil
}

// ParseOperation takes a string representation of an operation and returns the
// corresponding Operation constant.
func ParseOperation(op string) (Operation, error) {
	switch strnorm(op) {
	case "all":
		return OperationAll, nil
	case "read":
		return OperationRead, nil
	case "write":
		return OperationWrite, nil
	case "delete":
		return OperationDelete, nil
	case "describe":
		return OperationDescribe, nil
	case "describeconfigs":
		return OperationDescribeConfig, nil
	case "alter":
		return OperationAlter, nil
	case "alterconfigs":
		return OperationAlterConfig, nil
	default:
		return "", fmt.Errorf("unable to parse, unknown operation: %s", op)
	}
}

// ParsePatternType takes a string representation of a pattern type and
// returns the corresponding PatternType constant.
func ParsePatternType(pt string) (PatternType, error) {
	switch strnorm(pt) {
	case "literal":
		return PatternTypeLiteral, nil
	case "prefixed":
		return PatternTypePrefix, nil
	case "any":
		return PatternTypeAny, nil
	default:
		return "", fmt.Errorf("unable to parse, unknown pattern type: %s", pt)
	}
}

func trimRepeatedACLs(acls []ACL) []ACL {
	seen := make(map[string]struct{})
	var uniqueACLs []ACL
	for _, acl := range acls {
		key := aclKey(acl)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			uniqueACLs = append(uniqueACLs, acl)
		}
	}
	return uniqueACLs
}

// aclKey generates a string key for an ACL based on its fields. ACLs don't have
// an ID, so we use this to differentiate between them.
func aclKey(acl ACL) string {
	sep := "\x00"
	return acl.Principal + sep +
		acl.Resource + sep +
		string(acl.ResourceType) + sep +
		string(acl.PatternType) + sep +
		acl.Host + sep +
		string(acl.Operation) + sep +
		string(acl.Permission)
}

// strnorm normalizes a string by removing ".", "_", and "-" characters, trimming
// whitespace, and converting it to lowercase. This is the normalization that
// franz-go uses for parsing ACLs property strings.
func strnorm(s string) string {
	s = strings.ReplaceAll(s, ".", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	return s
}
