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
	// Policy is the initial authorization policy.
	Policy Policy
}

// Interceptor enforces policy-based authorization on gRPC methods.
// It reads required permissions from proto method annotations
// (redpanda.api.common.v1.required_permission) and enforces them
// against the authorization policy.
type Interceptor struct {
	logger           *zap.Logger
	resourcePolicy   *ResourcePolicy
	policyMu         sync.RWMutex
	allPerms         []PermissionName
	resourceName     ResourceName
	extractPrincipal PrincipalExtractor

	// permCache caches the proto descriptor lookup: fullMethod -> permission.
	// Empty string means "looked up but no annotation found" (deny).
	permCache sync.Map // map[string]PermissionName
}

// NewInterceptor creates a new policy-based gRPC authorization interceptor.
//
// Required permissions are read from proto method annotations
// (redpanda.api.common.v1.required_permission). Methods without
// annotations are denied (fail-closed).
//
// Use [Interceptor.SwapPolicy] to hot-reload the policy (e.g. from
// loader.WatchPolicyFile).
func NewInterceptor(cfg InterceptorConfig) (*Interceptor, error) {
	if cfg.ExtractPrincipal == nil {
		return nil, errors.New("principal extractor must not be nil")
	}

	// Pre-scan all registered protos for permissions so we can build
	// the ResourcePolicy with the full set of known permissions.
	allPerms := discoverAllPermissions()

	rp, err := NewResourcePolicy(cfg.Policy, cfg.ResourceName, allPerms)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource policy: %w", err)
	}

	i := &Interceptor{
		logger:           cfg.Logger,
		allPerms:         allPerms,
		resourceName:     cfg.ResourceName,
		extractPrincipal: cfg.ExtractPrincipal,
		resourcePolicy:   rp,
	}

	cfg.Logger.Info("Authorization interceptor initialized",
		zap.String("resource", string(cfg.ResourceName)),
		zap.Int("permissions", len(allPerms)))

	return i, nil
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

// sentinel for "looked up, no annotation found".
const noPermission PermissionName = ""

// UnaryServerInterceptor returns a gRPC unary server interceptor that
// enforces authorization checks.
func (a *Interceptor) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		for _, prefix := range skipPrefixes {
			if strings.HasPrefix(info.FullMethod, prefix) {
				return handler(ctx, req)
			}
		}

		perm := a.lookupPermission(info.FullMethod)
		if perm == noPermission {
			a.logger.Warn("No permission annotation, denying access (fail-closed)", zap.String("method", info.FullMethod))
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

// lookupPermission resolves a gRPC full method name to its required permission
// via proto descriptor reflection. Results are cached.
func (a *Interceptor) lookupPermission(fullMethod string) PermissionName {
	if v, ok := a.permCache.Load(fullMethod); ok {
		return v.(PermissionName)
	}

	perm := resolvePermission(fullMethod)
	a.permCache.Store(fullMethod, perm)
	return perm
}

// resolvePermission looks up the required_permission annotation for a gRPC method
// by parsing the full method name and walking the proto descriptor registry.
func resolvePermission(fullMethod string) PermissionName {
	// fullMethod is "/<package.ServiceName>/<MethodName>"
	parts := strings.Split(strings.TrimPrefix(fullMethod, "/"), "/")
	if len(parts) != 2 {
		return noPermission
	}

	serviceName := parts[0]
	methodName := parts[1]

	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(serviceName))
	if err != nil || desc == nil {
		return noPermission
	}

	svc, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return noPermission
	}

	method := svc.Methods().ByName(protoreflect.Name(methodName))
	if method == nil {
		return noPermission
	}

	perms := extractProtoPermissions(method)
	if len(perms) == 0 {
		return noPermission
	}
	return PermissionName(perms[0])
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

// discoverAllPermissions scans all registered proto files for
// required_permission annotations and returns the deduplicated set.
func discoverAllPermissions() []PermissionName {
	seen := make(map[PermissionName]struct{})
	var perms []PermissionName

	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		services := fd.Services()
		for i := range services.Len() {
			svc := services.Get(i)
			methods := svc.Methods()
			for j := range methods.Len() {
				for _, p := range extractProtoPermissions(methods.Get(j)) {
					pn := PermissionName(p)
					if _, ok := seen[pn]; !ok {
						seen[pn] = struct{}{}
						perms = append(perms, pn)
					}
				}
			}
		}
		return true
	})

	return perms
}
