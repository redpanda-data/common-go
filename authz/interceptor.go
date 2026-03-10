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
	// ResourceName is the base scope path for policy evaluation,
	// e.g. "organizations/{org}/resourcegroups/{rg}/dataplanes/{dp}".
	ResourceName ResourceName
	// ExtractPrincipal extracts the caller's principal from the request context.
	ExtractPrincipal PrincipalExtractor
	// Policy is the initial authorization policy.
	Policy Policy
}

// methodAuthz holds the resolved authorization info for a single gRPC method.
type methodAuthz struct {
	permission   PermissionName
	resourceType ResourceType // empty = check at base resource level
	idGetterCEL  string       // CEL expression to extract resource ID from request
}

// Interceptor enforces policy-based authorization on gRPC methods.
// It reads required permissions from proto method annotations
// (redpanda.api.common.v1.method_authorization or required_permission)
// and enforces them against the authorization policy.
type Interceptor struct {
	logger           *zap.Logger
	resourcePolicy   *ResourcePolicy
	policyMu         sync.RWMutex
	allPerms         []PermissionName
	resourceName     ResourceName
	extractPrincipal PrincipalExtractor

	// authzCache caches proto descriptor lookups: fullMethod -> *methodAuthz.
	// nil value means "looked up but no annotation found" (deny).
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

		var authorizer Authorizer

		a.policyMu.RLock()
		if ma.resourceType != "" && ma.idGetterCEL != "" {
			// Sub-resource scoping: extract resource ID from request via CEL.
			resourceID := evalCELResourceID(req, ma.idGetterCEL)
			if resourceID != "" {
				authorizer = a.resourcePolicy.SubResourceAuthorizer(ma.resourceType, ResourceID(resourceID), ma.permission)
				span.SetAttributes(
					attribute.String("rp.authz.resource_type", string(ma.resourceType)),
					attribute.String("rp.authz.resource_id", resourceID),
				)
			} else {
				authorizer = a.resourcePolicy.Authorizer(ma.permission)
			}
		} else {
			authorizer = a.resourcePolicy.Authorizer(ma.permission)
		}
		a.policyMu.RUnlock()

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
		ma, _ := v.(*methodAuthz)
		return ma
	}

	ma := resolveMethodAuthz(fullMethod)
	a.authzCache.Store(fullMethod, ma) // may store nil
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
		if authz, ok := ext.(*commonv1.MethodAuthorization); ok && authz != nil && authz.GetPermission() != "" {
			return &methodAuthz{
				permission:   PermissionName(authz.GetPermission()),
				resourceType: ResourceType(authz.GetResourceType()),
				idGetterCEL:  authz.GetIdGetterCel(),
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

// evalCELResourceID evaluates a simple CEL expression to extract a resource ID
// from the request. Supports the common pattern "request.<field_path>".
//
// For now this handles dotted field access on proto messages via protoreflect.
// A full CEL evaluator can be plugged in later if needed.
func evalCELResourceID(req any, celExpr string) string {
	// Strip "request." prefix — the req is the request message itself.
	path := strings.TrimPrefix(celExpr, "request.")
	if path == celExpr {
		// No "request." prefix, can't evaluate.
		return ""
	}

	msg, ok := req.(proto.Message)
	if !ok {
		return ""
	}

	refl := msg.ProtoReflect()
	fields := strings.Split(path, ".")

	for i, fieldName := range fields {
		fd := refl.Descriptor().Fields().ByName(protoreflect.Name(fieldName))
		if fd == nil {
			return ""
		}
		val := refl.Get(fd)
		if i < len(fields)-1 {
			// Intermediate field: must be a message.
			if fd.Kind() != protoreflect.MessageKind {
				return ""
			}
			refl = val.Message()
		} else {
			// Leaf field: return as string.
			return fmt.Sprint(val.Interface())
		}
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
				method := methods.Get(j)
				opts := method.Options()
				if opts == nil {
					continue
				}
				methodOpts, ok := opts.(*descriptorpb.MethodOptions)
				if !ok || methodOpts == nil {
					continue
				}

				// method_authorization
				if ext := proto.GetExtension(methodOpts, commonv1.E_MethodAuthorization); ext != nil {
					if authz, ok := ext.(*commonv1.MethodAuthorization); ok && authz != nil {
						add(authz.GetPermission())
					}
				}

				// collection_authorization
				if ext := proto.GetExtension(methodOpts, commonv1.E_CollectionAuthorization); ext != nil {
					if coll, ok := ext.(*commonv1.CollectionAuthorization); ok && coll != nil && coll.GetEach() != nil {
						add(coll.GetEach().GetPermission())
					}
				}

				// required_permission (simple strings)
				if ext := proto.GetExtension(methodOpts, commonv1.E_RequiredPermission); ext != nil {
					if strs, ok := ext.([]string); ok {
						for _, s := range strs {
							add(s)
						}
					}
				}
			}
		}
		return true
	})

	return perms
}
