// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package grpcauthz provides a gRPC unary server interceptor
// that enforces policy-based authorization using the authz engine.
package grpcauthz

import (
	"context"
	"log/slog"

	commonv1 "buf.build/gen/go/redpandadata/common/protocolbuffers/go/redpanda/api/common/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/redpanda-data/common-go/authz"
)

// PrincipalExtractor extracts a principal from the gRPC request context.
type PrincipalExtractor func(ctx context.Context) (authz.PrincipalID, bool)

// Config configures the gRPC authorization interceptor.
type Config struct {
	// Logger for authorization events. If nil, slog.Default() is used.
	Logger *slog.Logger
	// ResourceName is the base scope path for policy evaluation,
	// e.g. "organizations/{org}/resourcegroups/{rg}/dataplanes/{dp}".
	ResourceName authz.ResourceName
	// ExtractPrincipal extracts the caller's principal from the gRPC
	// request context (typically from metadata). Required.
	ExtractPrincipal PrincipalExtractor
	// Policy is the initial authorization policy. Mutually exclusive with PolicyWatch.
	Policy authz.Policy
	// PolicyWatch loads the initial policy and watches for changes, hot-reloading
	// automatically. Mutually exclusive with Policy. Call [Interceptor.Close] to
	// stop watching.
	PolicyWatch authz.PolicyWatchFunc
	// MethodPermissions provides manual method-to-permission mappings for
	// services whose protos don't yet have authorization annotations.
	// Keys are gRPC full method names (e.g. "/package.Service/Method").
	MethodPermissions map[string]authz.PermissionName
	// Domain is the error domain for structured error details (e.g. "redpanda.com").
	// Defaults to "redpanda.com" if empty.
	Domain string
}

// Interceptor is a gRPC authorization interceptor. Use [New] to create one.
type Interceptor struct {
	engine           *authz.Engine
	extractPrincipal PrincipalExtractor
	logger           *slog.Logger
}

// New creates a gRPC authorization interceptor.
func New(cfg Config) (*Interceptor, error) {
	engine, err := authz.NewEngine(authz.EngineConfig{
		Logger:            cfg.Logger,
		ResourceName:      cfg.ResourceName,
		Policy:            cfg.Policy,
		PolicyWatch:       cfg.PolicyWatch,
		MethodPermissions: cfg.MethodPermissions,
		Domain:            cfg.Domain,
	})
	if err != nil {
		return nil, err
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Interceptor{engine: engine, extractPrincipal: cfg.ExtractPrincipal, logger: logger}, nil
}

// Unary returns a gRPC unary server interceptor.
func (i *Interceptor) Unary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if authz.ShouldSkip(info.FullMethod) {
			return handler(ctx, req)
		}

		principal, ok := i.extractPrincipal(ctx)
		if !ok {
			i.logger.Warn("No identity in context, denying access", "method", info.FullMethod)
			return nil, status.Error(codes.Internal, "no identity in context")
		}

		ma := i.engine.LookupMethodAuthz(info.FullMethod)
		if denial := i.engine.CheckAccess(ctx, info.FullMethod, principal, ma, req); denial != nil {
			return nil, grpcError(i.engine, denial)
		}

		resp, err := handler(ctx, req)
		if err != nil {
			return resp, err
		}

		if ma != nil && ma.Collection != nil {
			if err := i.engine.FilterCollection(ma, principal, resp); err != nil {
				i.logger.Error("collection filtering failed", "method", info.FullMethod, "error", err)
				return nil, status.Error(codes.Internal, "authorization filter error")
			}
		}

		return resp, nil
	}
}

// LookupMethodAuthz resolves a method name to its authorization info.
func (i *Interceptor) LookupMethodAuthz(fullMethod string) *authz.MethodAuthz {
	return i.engine.LookupMethodAuthz(fullMethod)
}

// SwapPolicy replaces the active policy. Safe for concurrent use.
func (i *Interceptor) SwapPolicy(p authz.Policy) error {
	return i.engine.SwapPolicy(p)
}

// Close stops the policy file watcher if one was configured.
func (i *Interceptor) Close() error {
	return i.engine.Close()
}

func grpcError(a *authz.Engine, d *authz.Denial) error {
	var code codes.Code
	var reason string
	switch d.Kind {
	case authz.DenialUnknownMethod, authz.DenialForbidden:
		code = codes.PermissionDenied
		reason = commonv1.Reason_REASON_PERMISSION_DENIED.String()
	case authz.DenialEmptyResourceID:
		code = codes.InvalidArgument
		reason = commonv1.Reason_REASON_INVALID_INPUT.String()
	default:
		code = codes.Internal
		reason = commonv1.Reason_REASON_SERVER_ERROR.String()
	}

	st, err := status.New(code, d.Message).WithDetails(authz.DenialErrorInfo(a.Domain(), reason, d))
	if err != nil {
		return status.Error(code, d.Message)
	}
	return st.Err()
}
