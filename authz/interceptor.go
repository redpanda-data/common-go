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
	"sync/atomic"

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
	// ResourceName is the base scope path for policy evaluation,
	// e.g. "organizations/{org}/resourcegroups/{rg}/dataplanes/{dp}".
	ResourceName ResourceName
	// ExtractPrincipal extracts the caller's principal from the request context.
	ExtractPrincipal PrincipalExtractor
	// Policy is the initial authorization policy.
	Policy Policy
}

// methodAuthz holds the resolved authorization info for a gRPC method.
type methodAuthz struct {
	permission   PermissionName
	resourceType ResourceType // empty = check at base resource level
	idGetterCEL  string       // CEL path to extract resource ID from request
}

// Interceptor enforces policy-based authorization on gRPC methods.
// It reads required permissions from proto method annotations
// (method_authorization or required_permission) and enforces them
// against the authorization policy.
type Interceptor struct {
	logger           *zap.Logger
	resourcePolicy   atomic.Pointer[ResourcePolicy]
	allPerms         []PermissionName
	resourceName     ResourceName
	extractPrincipal PrincipalExtractor

	// authzCache caches proto descriptor lookups: fullMethod -> *methodAuthz.
	// nil means "looked up but no annotation found" (deny).
	authzCache sync.Map
}

// NewInterceptor creates a new policy-based gRPC authorization interceptor.
//
// Required permissions are read from proto method annotations:
//   - redpanda.api.common.v1.method_authorization (with resource scoping)
//   - redpanda.api.common.v1.required_permission (simple permission string)
//
// Methods without annotations are denied (fail-closed).
//
// Use [Interceptor.SwapPolicy] to hot-reload the policy (e.g. from
// loader.WatchPolicyFile).
func NewInterceptor(cfg InterceptorConfig) (*Interceptor, error) {
	if cfg.ExtractPrincipal == nil {
		return nil, errors.New("principal extractor must not be nil")
	}

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
	}
	i.resourcePolicy.Store(rp)

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
	a.resourcePolicy.Store(rp)
	return nil
}

// skipPrefixes contains gRPC service prefixes that bypass authorization.
var skipPrefixes = []string{
	"/grpc.health.v1.Health/",
	"/grpc.reflection.",
}

// UnaryServerInterceptor returns a gRPC unary server interceptor that
// enforces authorization checks.
func (a *Interceptor) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		for _, prefix := range skipPrefixes {
			if strings.HasPrefix(info.FullMethod, prefix) {
				return handler(ctx, req)
			}
		}

		ma := a.lookupMethodAuthz(info.FullMethod)
		if ma == nil {
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
				attribute.String("rp.authz.permission", string(ma.permission)),
				attribute.String("rp.authz.method", info.FullMethod),
			),
		)
		defer span.End()

		// Extract resource ID before taking the lock.
		var resourceID string
		if ma.idGetterCEL != "" {
			resourceID = evalFieldPath(req, ma.idGetterCEL)
			if resourceID == "" {
				span.SetAttributes(attribute.String("rp.authz.decision", "denied"))
				span.SetStatus(otelcodes.Error, "empty resource ID")
				a.logger.Warn("Empty resource ID from request, denying",
					zap.String("method", info.FullMethod),
					zap.String("id_getter_cel", ma.idGetterCEL))
				return nil, status.Errorf(codes.InvalidArgument, "resource ID is required")
			}
			span.SetAttributes(
				attribute.String("rp.authz.resource_type", string(ma.resourceType)),
				attribute.String("rp.authz.resource_id", resourceID),
			)
		}

		rp := a.resourcePolicy.Load()
		var authorizer Authorizer
		if resourceID != "" {
			authorizer = rp.SubResourceAuthorizer(ma.resourceType, ResourceID(resourceID), ma.permission)
		} else {
			authorizer = rp.Authorizer(ma.permission)
		}

		if !authorizer.Check(principal) {
			span.SetAttributes(attribute.String("rp.authz.decision", "denied"))
			span.SetStatus(otelcodes.Error, "permission denied")

			a.logger.Warn("Authorization denied",
				zap.String("method", info.FullMethod),
				zap.String("permission", string(ma.permission)),
				zap.String("principal", string(principal)))
			return nil, status.Errorf(codes.PermissionDenied, "principal %s lacks permission %s", principal, ma.permission)
		}

		span.SetAttributes(attribute.String("rp.authz.decision", "granted"))
		return handler(ctx, req)
	}
}

// lookupMethodAuthz resolves a gRPC full method name to its authorization info.
// Results are cached. Returns nil if no annotation found.
func (a *Interceptor) lookupMethodAuthz(fullMethod string) *methodAuthz {
	if v, ok := a.authzCache.Load(fullMethod); ok {
		if ma, ok := v.(*methodAuthz); ok {
			return ma
		}
		return nil
	}
	ma := resolveMethodAuthz(fullMethod)
	a.authzCache.Store(fullMethod, ma)
	return ma
}

// resolveMethodAuthz looks up authorization annotations for a gRPC method.
func resolveMethodAuthz(fullMethod string) *methodAuthz {
	parts := strings.Split(strings.TrimPrefix(fullMethod, "/"), "/")
	if len(parts) != 2 {
		return nil
	}

	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(parts[0]))
	if err != nil || desc == nil {
		return nil
	}

	svc, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil
	}

	method := svc.Methods().ByName(protoreflect.Name(parts[1]))
	if method == nil {
		return nil
	}

	opts := method.Options()
	if opts == nil {
		return nil
	}
	methodOpts, ok := opts.(*descriptorpb.MethodOptions)
	if !ok || methodOpts == nil {
		return nil
	}

	// Prefer method_authorization (structured, with resource scoping).
	if ext := proto.GetExtension(methodOpts, commonv1.E_MethodAuthorization); ext != nil {
		if ma, ok := ext.(*commonv1.MethodAuthorization); ok && ma != nil && ma.GetPermission() != "" {
			return &methodAuthz{
				permission:   PermissionName(ma.GetPermission()),
				resourceType: ResourceType(ma.GetResourceType()),
				idGetterCEL:  ma.GetIdGetterCel(),
			}
		}
	}

	// Fall back to simple required_permission string.
	if ext := proto.GetExtension(methodOpts, commonv1.E_RequiredPermission); ext != nil {
		if perms, ok := ext.([]string); ok && len(perms) > 0 {
			return &methodAuthz{permission: PermissionName(perms[0])}
		}
	}

	return nil
}

// evalFieldPath evaluates a dotted field path on a proto message to extract
// a string value. Handles the common "request.<field>.<field>" pattern used
// in id_getter_cel annotations.
func evalFieldPath(req any, celExpr string) string {
	path := strings.TrimPrefix(celExpr, "request.")
	if path == celExpr {
		return ""
	}

	msg, ok := req.(proto.Message)
	if !ok {
		return ""
	}

	refl := msg.ProtoReflect()
	for _, fieldName := range strings.Split(path, ".") {
		fd := refl.Descriptor().Fields().ByName(protoreflect.Name(fieldName))
		if fd == nil {
			return ""
		}
		val := refl.Get(fd)
		if fd.Kind() == protoreflect.MessageKind {
			refl = val.Message()
			continue
		}
		return fmt.Sprint(val.Interface())
	}
	return ""
}

// discoverAllPermissions scans all registered proto files for
// authorization annotations and returns the deduplicated set of permissions.
func discoverAllPermissions() []PermissionName {
	seen := make(map[PermissionName]struct{})
	var perms []PermissionName

	add := func(p string) {
		pn := PermissionName(p)
		if _, ok := seen[pn]; !ok && p != "" {
			seen[pn] = struct{}{}
			perms = append(perms, pn)
		}
	}

	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		services := fd.Services()
		for i := range services.Len() {
			svc := services.Get(i)
			methods := svc.Methods()
			for j := range methods.Len() {
				collectMethodPermissions(methods.Get(j), add)
			}
		}
		return true
	})

	return perms
}

// collectMethodPermissions extracts all permission strings from a method's annotations.
func collectMethodPermissions(method protoreflect.MethodDescriptor, add func(string)) {
	opts := method.Options()
	if opts == nil {
		return
	}
	methodOpts, ok := opts.(*descriptorpb.MethodOptions)
	if !ok || methodOpts == nil {
		return
	}

	if ext := proto.GetExtension(methodOpts, commonv1.E_MethodAuthorization); ext != nil {
		if ma, ok := ext.(*commonv1.MethodAuthorization); ok && ma != nil {
			add(ma.GetPermission())
		}
	}

	if ext := proto.GetExtension(methodOpts, commonv1.E_CollectionAuthorization); ext != nil {
		if ca, ok := ext.(*commonv1.CollectionAuthorization); ok && ca != nil && ca.GetEach() != nil {
			add(ca.GetEach().GetPermission())
		}
	}

	if ext := proto.GetExtension(methodOpts, commonv1.E_RequiredPermission); ext != nil {
		if strs, ok := ext.([]string); ok {
			for _, s := range strs {
				add(s)
			}
		}
	}
}
