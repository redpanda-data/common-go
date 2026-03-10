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
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	commonv1 "buf.build/gen/go/redpandadata/common/protocolbuffers/go/redpanda/api/common/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

var tracer = otel.Tracer("github.com/redpanda-data/common-go/authz")

// PrincipalExtractor extracts a principal from the request context.
type PrincipalExtractor func(ctx context.Context) (PrincipalID, bool)

// InterceptorConfig configures the policy-based gRPC authorization interceptor.
type InterceptorConfig struct {
	// Logger for authorization events.
	Logger *zap.Logger
	// ResourceName is the scope path for policy evaluation,
	// e.g. "organizations/{org}/resourcegroups/{rg}/dataplanes/{dp}".
	ResourceName ResourceName
	// ExtractPrincipal extracts the caller's principal from the request context.
	ExtractPrincipal PrincipalExtractor
	// MethodPermissions provides an explicit method-to-permission mapping.
	// These take precedence over proto annotations. Methods not found here
	// fall back to proto annotations (redpanda.api.common.v1.required_permission).
	MethodPermissions map[string]PermissionName
	// Policy is the initial authorization policy.
	Policy Policy
}

// Interceptor enforces policy-based authorization on gRPC methods.
type Interceptor struct {
	logger           *zap.Logger
	resourcePolicy   *ResourcePolicy
	policyMu         sync.RWMutex
	methods          map[string]PermissionName
	allPerms         []PermissionName
	resourceName     ResourceName
	extractPrincipal PrincipalExtractor
}

// NewInterceptor creates a new policy-based gRPC authorization interceptor.
//
// It merges permissions from two sources:
//  1. Explicit MethodPermissions in the config (highest priority)
//  2. Proto method annotations via redpanda.api.common.v1.required_permission
//
// Use [Interceptor.SwapPolicy] to hot-reload the policy (e.g. from
// loader.WatchPolicyFile).
func NewInterceptor(cfg InterceptorConfig) (*Interceptor, error) {
	if cfg.ExtractPrincipal == nil {
		return nil, errors.New("principal extractor must not be nil")
	}

	methods := buildMethodMap(cfg.MethodPermissions)
	perms := uniquePermissions(methods)

	rp, err := NewResourcePolicy(cfg.Policy, cfg.ResourceName, perms)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource policy: %w", err)
	}

	interceptor := &Interceptor{
		logger:           cfg.Logger,
		methods:          methods,
		allPerms:         perms,
		resourceName:     cfg.ResourceName,
		extractPrincipal: cfg.ExtractPrincipal,
		resourcePolicy:   rp,
	}

	cfg.Logger.Info("Authorization interceptor initialized",
		zap.String("resource", string(cfg.ResourceName)),
		zap.Int("methods", len(methods)))

	return interceptor, nil
}

// SwapPolicy replaces the active policy. Safe for concurrent use.
// Intended to be called from a loader.WatchPolicyFile callback.
func (a *Interceptor) SwapPolicy(p Policy) error {
	rp, err := NewResourcePolicy(p, a.resourceName, a.allPerms)
	if err != nil {
		return fmt.Errorf("failed to create resource policy: %w", err)
	}
	a.policyMu.Lock()
	a.resourcePolicy = rp
	a.policyMu.Unlock()
	return nil
}

// skipPrefixes contains gRPC service prefixes that bypass authorization.
var skipPrefixes = []string{
	"/grpc.health.v1.Health/",
	"/grpc.reflection.",
}

// UnaryServerInterceptor returns a gRPC unary server interceptor that
// enforces authorization checks. Methods not in the permission map
// (and without proto annotations) are denied.
func (a *Interceptor) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		for _, prefix := range skipPrefixes {
			if strings.HasPrefix(info.FullMethod, prefix) {
				return handler(ctx, req)
			}
		}

		perm, ok := a.methods[info.FullMethod]
		if !ok {
			a.logger.Warn("Unknown method, denying access (fail-closed)", zap.String("method", info.FullMethod))
			return nil, status.Errorf(codes.PermissionDenied, "unknown method %s", info.FullMethod)
		}

		principal, ok := a.extractPrincipal(ctx)
		if !ok {
			a.logger.Warn("No identity in context, denying access", zap.String("method", info.FullMethod))
			return nil, status.Error(codes.Internal, "no identity in context")
		}

		_, span := tracer.Start(ctx, "authz.check",
			trace.WithAttributes(
				attribute.String("rp.authz.principal", string(principal)),
				attribute.String("rp.authz.permission", string(perm)),
				attribute.String("rp.authz.method", info.FullMethod),
			),
		)
		defer span.End()

		a.policyMu.RLock()
		authorizer := a.resourcePolicy.Authorizer(perm)
		a.policyMu.RUnlock()

		if !authorizer.Check(principal) {
			span.SetAttributes(attribute.String("rp.authz.decision", "denied"))
			span.SetStatus(otelcodes.Error, "permission denied")

			a.logger.Warn("Authorization denied",
				zap.String("method", info.FullMethod),
				zap.String("permission", string(perm)),
				zap.String("principal", string(principal)))
			return nil, status.Errorf(codes.PermissionDenied, "principal %s lacks permission %s", principal, perm)
		}

		span.SetAttributes(attribute.String("rp.authz.decision", "granted"))
		return handler(ctx, req)
	}
}

// buildMethodMap merges explicit method permissions with proto annotations.
func buildMethodMap(explicit map[string]PermissionName) map[string]PermissionName {
	methods := make(map[string]PermissionName)

	// Discover permissions from proto annotations.
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		services := fd.Services()
		for i := range services.Len() {
			svc := services.Get(i)
			svcMethods := svc.Methods()
			for j := range svcMethods.Len() {
				method := svcMethods.Get(j)
				perms := extractProtoPermissions(method)
				if len(perms) > 0 {
					fullMethod := fmt.Sprintf("/%s/%s", svc.FullName(), method.Name())
					methods[fullMethod] = PermissionName(perms[0])
				}
			}
		}
		return true
	})

	// Explicit entries override proto annotations.
	for method, perm := range explicit {
		methods[method] = perm
	}

	return methods
}

// extractProtoPermissions reads the required_permission annotation from a method descriptor.
func extractProtoPermissions(method protoreflect.MethodDescriptor) []string {
	opts := method.Options()
	if opts == nil {
		return nil
	}
	methodOpts, ok := opts.(*descriptorpb.MethodOptions)
	if !ok || methodOpts == nil {
		return nil
	}
	ext := proto.GetExtension(methodOpts, commonv1.E_RequiredPermission)
	if ext == nil {
		return nil
	}
	perms, ok := ext.([]string)
	if !ok {
		return nil
	}
	return perms
}

// uniquePermissions deduplicates permission values.
func uniquePermissions(methods map[string]PermissionName) []PermissionName {
	seen := make(map[PermissionName]struct{})
	var perms []PermissionName
	for _, p := range methods {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			perms = append(perms, p)
		}
	}
	return perms
}
