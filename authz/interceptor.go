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
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	commonv1 "buf.build/gen/go/redpandadata/common/protocolbuffers/go/redpanda/api/common/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

var tracer = otel.Tracer("github.com/redpanda-data/common-go/authz")

// PrincipalExtractor extracts a principal from the request context.
type PrincipalExtractor func(ctx context.Context) (PrincipalID, bool)

// PolicyWatchFunc loads the initial policy and watches for changes.
// It returns the initial policy, an unwatch function, and any error.
// The callback is invoked on policy changes after the initial load.
// This matches the signature of [loader.WatchPolicyFile].
type PolicyWatchFunc func(callback func(Policy, error)) (Policy, func() error, error)

// InterceptorConfig configures the policy-based authorization interceptor.
type InterceptorConfig struct {
	// Logger for authorization events. If nil, slog.Default() is used.
	Logger *slog.Logger
	// ResourceName is the base scope path for policy evaluation,
	// e.g. "organizations/{org}/resourcegroups/{rg}/dataplanes/{dp}".
	ResourceName ResourceName
	// ExtractPrincipal extracts the caller's principal from the request context.
	ExtractPrincipal PrincipalExtractor
	// Policy is the initial authorization policy. Mutually exclusive with PolicyWatch.
	Policy Policy
	// PolicyWatch loads the initial policy and watches for changes, hot-reloading
	// automatically. Mutually exclusive with Policy. Call [Interceptor.Close] to
	// stop watching.
	//
	// Example using loader.WatchPolicyFile:
	//
	//   PolicyWatch: func(cb func(authz.Policy, error)) (authz.Policy, func() error, error) {
	//       return loader.WatchPolicyFile("/path/to/policy.yaml", cb)
	//   },
	PolicyWatch PolicyWatchFunc
	// MethodPermissions provides manual method-to-permission mappings for
	// services whose protos don't yet have authorization annotations.
	// These are merged with any permissions discovered from proto annotations.
	// Keys are gRPC full method names (e.g. "/package.Service/Method").
	MethodPermissions map[string]PermissionName
}

// MethodAuthz holds the resolved authorization info for a gRPC/Connect method.
// For single-resource RPCs, Auth is populated directly.
// For collection RPCs (List), Collection is populated and Auth points to Collection.Each.
type MethodAuthz struct {
	// Auth is the per-method authorization from the proto annotation.
	Auth *commonv1.MethodAuthorization
	// Collection is set for List RPCs with collection_authorization.
	// When non-nil, the interceptor skips pre-call authorization and
	// filters the response per-item instead.
	Collection *commonv1.CollectionAuthorization
}

// Interceptor enforces policy-based authorization on gRPC/Connect methods.
// It reads required permissions from proto method annotations
// (method_authorization or required_permission) and enforces them
// against the authorization policy.
type Interceptor struct {
	logger           *slog.Logger
	resourcePolicy   atomic.Pointer[ResourcePolicy]
	allPerms         []PermissionName
	resourceName     ResourceName
	extractPrincipal PrincipalExtractor
	unwatch          func() error // non-nil when PolicyFile is used

	// authzCache caches proto descriptor lookups: fullMethod -> *MethodAuthz.
	// nil means "looked up but no annotation found" (deny).
	authzCache sync.Map
}

// Logger returns the interceptor's logger.
func (a *Interceptor) Logger() *slog.Logger {
	return a.logger
}

// ExtractPrincipal returns the interceptor's principal extractor.
func (a *Interceptor) ExtractPrincipal() PrincipalExtractor {
	return a.extractPrincipal
}

// NewInterceptor creates a new policy-based authorization interceptor.
//
// Required permissions are read from proto method annotations:
//   - redpanda.api.common.v1.method_authorization (with resource scoping)
//   - redpanda.api.common.v1.required_permission (simple permission string)
//
// Methods without annotations are denied (fail-closed).
//
// When PolicyWatch is set, the interceptor watches for policy changes and
// hot-reloads automatically. Call [Interceptor.Close] to stop watching.
func NewInterceptor(cfg InterceptorConfig) (*Interceptor, error) {
	if cfg.ExtractPrincipal == nil {
		return nil, errors.New("principal extractor must not be nil")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	allPerms := discoverAllPermissions()

	// Merge manual method permissions into the permission set and pre-seed cache.
	var manualEntries []struct {
		method string
		ma     *MethodAuthz
	}
	for method, perm := range cfg.MethodPermissions {
		ma := &MethodAuthz{Auth: &commonv1.MethodAuthorization{Permission: string(perm)}}
		manualEntries = append(manualEntries, struct {
			method string
			ma     *MethodAuthz
		}{method, ma})
		allPerms = append(allPerms, perm)
	}

	// Deduplicate permissions.
	seen := make(map[PermissionName]struct{}, len(allPerms))
	deduped := make([]PermissionName, 0, len(allPerms))
	for _, p := range allPerms {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			deduped = append(deduped, p)
		}
	}
	allPerms = deduped

	// Load initial policy — either static or from a watched file.
	// The callback captures `iptr` which is set after NewInterceptor returns
	// the interceptor. The watch callback only fires on file changes after
	// WatchPolicyFile returns, so `*iptr` is always initialized.
	var (
		policy  Policy
		unwatch func() error
		iptr    *Interceptor
	)
	if cfg.PolicyWatch != nil {
		var err error
		policy, unwatch, err = cfg.PolicyWatch(func(p Policy, watchErr error) {
			if watchErr != nil {
				logger.Error("Failed to reload authorization policy", "error", watchErr)
				return
			}
			if swapErr := iptr.SwapPolicy(p); swapErr != nil {
				logger.Error("Failed to apply reloaded authorization policy", "error", swapErr)
				return
			}
			logger.Info("Authorization policy reloaded")
		})
		if err != nil {
			return nil, fmt.Errorf("failed to load policy: %w", err)
		}
	} else {
		policy = cfg.Policy
	}

	rp, err := NewResourcePolicy(policy, cfg.ResourceName, allPerms)
	if err != nil {
		if unwatch != nil {
			_ = unwatch()
		}
		return nil, fmt.Errorf("failed to create resource policy: %w", err)
	}

	i := &Interceptor{
		logger:           logger,
		allPerms:         allPerms,
		resourceName:     cfg.ResourceName,
		extractPrincipal: cfg.ExtractPrincipal,
		unwatch:          unwatch,
	}
	i.resourcePolicy.Store(rp)
	iptr = i // Wire up the callback's reference.

	// Pre-seed cache with manual mappings.
	for _, e := range manualEntries {
		i.authzCache.Store(e.method, e.ma)
	}

	logger.Info("Authorization interceptor initialized",
		"resource", string(cfg.ResourceName),
		"permissions", len(allPerms))

	return i, nil
}

// SwapPolicy replaces the active policy. Safe for concurrent use.
func (a *Interceptor) SwapPolicy(p Policy) error {
	rp, err := NewResourcePolicy(p, a.resourceName, a.allPerms)
	if err != nil {
		return fmt.Errorf("failed to create resource policy: %w", err)
	}
	a.resourcePolicy.Store(rp)
	return nil
}

// Close stops the policy file watcher if one was configured via PolicyWatch.
// Safe to call multiple times or if no watcher is active.
func (a *Interceptor) Close() error {
	if a.unwatch != nil {
		return a.unwatch()
	}
	return nil
}

// skipPrefixes contains gRPC service prefixes that bypass authorization.
var skipPrefixes = []string{
	"/grpc.health.v1.Health/",
	"/grpc.reflection.",
}

// DenialKind classifies authorization failures.
type DenialKind int

const (
	// DenialUnknownMethod indicates no annotation was found for the method.
	DenialUnknownMethod DenialKind = iota
	// DenialEmptyResourceID indicates a required resource ID was empty.
	DenialEmptyResourceID
	// DenialForbidden indicates the principal lacks the required permission.
	DenialForbidden
)

// Denial is a structured authorization failure returned by CheckAccess.
type Denial struct {
	Kind    DenialKind
	Method  string
	Message string
}

// ShouldSkip returns true if the method should bypass authorization.
func ShouldSkip(method string) bool {
	for _, prefix := range skipPrefixes {
		if strings.HasPrefix(method, prefix) {
			return true
		}
	}
	return false
}

// CheckAccess is the shared authorization core used by both gRPC and Connect
// interceptors. It returns nil on success, or a *Denial on failure.
// The ma parameter may be nil (looked up externally to allow reuse).
func (a *Interceptor) CheckAccess(ctx context.Context, method string, principal PrincipalID, ma *MethodAuthz, reqMsg any) *Denial {
	if ma == nil {
		a.logger.Warn("No permission annotation, denying access (fail-closed)", "method", method)
		return &Denial{Kind: DenialUnknownMethod, Method: method, Message: fmt.Sprintf("unknown method %s", method)}
	}

	// Collection methods do no pre-call authorization — the principal may only
	// have permission on specific resources, not at the parent level. The
	// response is filtered per-item by FilterCollection after the call.
	if ma.Collection != nil {
		return nil
	}

	perm := ma.Auth.GetPermission()

	_, span := tracer.Start(ctx, "authz.check",
		trace.WithAttributes(
			attribute.String("rp.authz.principal", string(principal)),
			attribute.String("rp.authz.permission", perm),
			attribute.String("rp.authz.method", method),
		),
	)
	defer span.End()

	var resourceID string
	if cel := ma.Auth.GetIdGetterCel(); cel != "" {
		resourceID = EvalFieldPath(reqMsg, cel)
		if resourceID == "" {
			span.SetAttributes(attribute.String("rp.authz.decision", "denied"))
			span.SetStatus(otelcodes.Error, "empty resource ID")
			a.logger.Warn("Empty resource ID from request, denying",
				"method", method,
				"id_getter_cel", cel)
			return &Denial{Kind: DenialEmptyResourceID, Method: method, Message: "resource ID is required"}
		}
		span.SetAttributes(
			attribute.String("rp.authz.resource_type", ma.Auth.GetResourceType()),
			attribute.String("rp.authz.resource_id", resourceID),
		)
	}

	rp := a.resourcePolicy.Load()
	var authorizer Authorizer
	if resourceID != "" {
		authorizer = rp.SubResourceAuthorizer(ResourceType(ma.Auth.GetResourceType()), ResourceID(resourceID), PermissionName(perm))
	} else {
		authorizer = rp.Authorizer(PermissionName(perm))
	}

	if !authorizer.Check(principal) {
		span.SetAttributes(attribute.String("rp.authz.decision", "denied"))
		span.SetStatus(otelcodes.Error, "permission denied")
		a.logger.Warn("Authorization denied",
			"method", method,
			"permission", perm,
			"principal", string(principal))
		return &Denial{
			Kind:    DenialForbidden,
			Method:  method,
			Message: fmt.Sprintf("principal %s lacks permission %s", principal, perm),
		}
	}

	span.SetAttributes(attribute.String("rp.authz.decision", "granted"))
	return nil
}

// FilterCollection removes items from a response collection that the principal
// lacks permission for. Returns an error if the collection annotation is
// misconfigured (fail-closed: caller must not return the unfiltered response).
func (a *Interceptor) FilterCollection(ma *MethodAuthz, principal PrincipalID, resp any) error {
	if ma.Collection == nil || ma.Collection.GetEach() == nil {
		return errors.New("authz: FilterCollection called on non-collection method")
	}

	msg, ok := resp.(proto.Message)
	if !ok {
		return errors.New("authz: response is not a proto.Message")
	}

	each := ma.Collection.GetEach()
	collCEL := ma.Collection.GetCollectionGetterCel()
	idCEL := each.GetIdGetterCel()

	// Parse "response.field" -> "field"
	collField := strings.TrimPrefix(collCEL, "response.")
	if collField == collCEL || collField == "" {
		return fmt.Errorf("authz: invalid collection_getter_cel %q (must start with 'response.')", collCEL)
	}

	// Parse "each.field" -> "field"
	idField := strings.TrimPrefix(idCEL, "each.")
	if idField == idCEL || idField == "" {
		return fmt.Errorf("authz: invalid each.id_getter_cel %q (must start with 'each.')", idCEL)
	}

	refl := msg.ProtoReflect()
	fd := refl.Descriptor().Fields().ByName(protoreflect.Name(collField))
	if fd == nil {
		return fmt.Errorf("authz: response has no field %q", collField)
	}
	if !fd.IsList() {
		return fmt.Errorf("authz: response field %q is not a repeated field", collField)
	}

	perm := PermissionName(each.GetPermission())
	resType := ResourceType(each.GetResourceType())
	rp := a.resourcePolicy.Load()
	list := refl.Mutable(fd).List()

	// Build a filtered copy preserving order.
	var kept []protoreflect.Value
	for i := range list.Len() {
		item := list.Get(i)
		if item.Message() == nil {
			continue
		}
		itemRefl := item.Message()
		idFd := itemRefl.Descriptor().Fields().ByName(protoreflect.Name(idField))
		if idFd == nil {
			return fmt.Errorf("authz: collection item has no field %q", idField)
		}
		resourceID := fmt.Sprint(itemRefl.Get(idFd).Interface())
		if resourceID == "" {
			continue
		}

		authorizer := rp.SubResourceAuthorizer(resType, ResourceID(resourceID), perm)
		if authorizer.Check(principal) {
			kept = append(kept, item)
		}
	}

	// Replace the list contents with the filtered items.
	list.Truncate(0)
	for _, v := range kept {
		list.Append(v)
	}

	return nil
}

// LookupMethodAuthz resolves a gRPC full method name to its authorization info.
// Results are cached. Returns nil if no annotation found.
func (a *Interceptor) LookupMethodAuthz(fullMethod string) *MethodAuthz {
	if v, ok := a.authzCache.Load(fullMethod); ok {
		if ma, ok := v.(*MethodAuthz); ok {
			return ma
		}
		return nil
	}
	ma := resolveMethodAuthz(fullMethod)
	a.authzCache.Store(fullMethod, ma)
	return ma
}

// resolveMethodAuthz looks up authorization annotations for a gRPC method.
func resolveMethodAuthz(fullMethod string) *MethodAuthz {
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

	return extractMethodAuthz(methodOpts)
}

// extractMethodAuthz reads authorization annotations from method options.
func extractMethodAuthz(methodOpts *descriptorpb.MethodOptions) *MethodAuthz {
	// Prefer method_authorization (structured, with resource scoping).
	if ext := proto.GetExtension(methodOpts, commonv1.E_MethodAuthorization); ext != nil {
		if ma, ok := ext.(*commonv1.MethodAuthorization); ok && ma != nil && ma.GetPermission() != "" {
			return &MethodAuthz{Auth: ma}
		}
	}

	// collection_authorization: List RPCs that require per-item filtering on the response.
	if ext := proto.GetExtension(methodOpts, commonv1.E_CollectionAuthorization); ext != nil {
		if ca, ok := ext.(*commonv1.CollectionAuthorization); ok && ca != nil && ca.GetEach() != nil && ca.GetEach().GetPermission() != "" {
			return &MethodAuthz{Auth: ca.GetEach(), Collection: ca}
		}
	}

	// Fall back to simple required_permission string.
	if ext := proto.GetExtension(methodOpts, commonv1.E_RequiredPermission); ext != nil {
		if perms, ok := ext.([]string); ok && len(perms) > 0 {
			return &MethodAuthz{Auth: &commonv1.MethodAuthorization{Permission: perms[0]}}
		}
	}

	return nil
}

// EvalFieldPath evaluates a dotted field path on a proto message to extract
// a string value. Handles the common "request.<field>.<field>" pattern used
// in id_getter_cel annotations.
func EvalFieldPath(req any, celExpr string) string {
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
