// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package authz

import (
	commonv1 "buf.build/gen/go/redpandadata/common/protocolbuffers/go/redpanda/api/common/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// PolicyWatchFunc loads the initial policy and watches for changes.
// It returns the initial policy, an unwatch function, and any error.
// The callback is invoked on policy changes after the initial load.
// This matches the signature of [loader.WatchPolicyFile].
type PolicyWatchFunc func(callback func(Policy, error)) (Policy, func() error, error)

// StaticPolicy wraps a static [Policy] into a [PolicyWatchFunc] that returns
// the policy immediately with no file watching. Useful for tests and
// configurations where hot-reloading is not needed.
func StaticPolicy(p Policy) PolicyWatchFunc {
	return func(_ func(Policy, error)) (Policy, func() error, error) {
		return p, func() error { return nil }, nil
	}
}

// MethodAuthz holds the resolved authorization info for a gRPC/Connect method.
// For single-resource RPCs, Auth is populated directly.
// For collection RPCs (List), Collection is populated and Auth points to Collection.Each.
//
// When CollectionOnly is true, the interceptor skips the pre-call permission
// check and defers to per-item filtering via FilterCollection. When both
// method_authorization and collection_authorization are set on the same RPC,
// CollectionOnly is false — the pre-call check runs AND the response is filtered.
type MethodAuthz struct {
	// Auth is the per-method authorization from the proto annotation.
	Auth *commonv1.MethodAuthorization
	// Collection is set for List RPCs with collection_authorization.
	Collection *commonv1.CollectionAuthorization
	// CollectionOnly is true when only collection_authorization is set
	// (no method_authorization). The pre-call check is skipped.
	CollectionOnly bool
	// Skip is set when the method has skip: true in method_authorization.
	// The interceptor passes through without any permission check.
	Skip bool
}

// DenialKind classifies authorization failures.
type DenialKind int

const (
	// DenialUnknown is the zero value and should not be used.
	DenialUnknown DenialKind = iota
	// DenialUnknownMethod indicates no annotation was found for the method.
	DenialUnknownMethod
	// DenialEmptyResourceID indicates a required resource ID was empty.
	DenialEmptyResourceID
	// DenialForbidden indicates the principal lacks the required permission.
	DenialForbidden
)

// Denial is a structured authorization failure returned by CheckAccess.
type Denial struct {
	Kind         DenialKind
	Method       string
	Message      string
	Permission   string
	Principal    PrincipalID
	ResourceType string
	ResourceID   string
}

// DenialErrorInfo builds an [errdetails.ErrorInfo] from a Denial.
// Used by transport adapters to attach structured error details.
func DenialErrorInfo(domain, reason string, d *Denial) *errdetails.ErrorInfo {
	md := make(map[string]string)
	if d.Method != "" {
		md["method"] = d.Method
	}
	if d.Permission != "" {
		md["permission"] = d.Permission
	}
	if d.Principal != "" {
		md["principal"] = string(d.Principal)
	}
	if d.ResourceType != "" {
		md["resource_type"] = d.ResourceType
	}
	if d.ResourceID != "" {
		md["resource_id"] = d.ResourceID
	}
	return &errdetails.ErrorInfo{
		Reason:   reason,
		Domain:   domain,
		Metadata: md,
	}
}
