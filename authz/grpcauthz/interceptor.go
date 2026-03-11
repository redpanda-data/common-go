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
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/redpanda-data/common-go/authz"
	"github.com/redpanda-data/common-go/authz/authzcore"
)

// PrincipalExtractor extracts a principal from the gRPC request context.
type PrincipalExtractor func(ctx context.Context) (authz.PrincipalID, bool)

// Option configures optional behavior of the authzcore.
type Option func(*options)

type options struct {
	logger         *slog.Logger
	domain         string
	files          *protoregistry.Files
	skipPrefixes   []string
	tracerProvider trace.TracerProvider
}

// WithLogger sets the logger for authorization events.
// Defaults to slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(o *options) { o.logger = l }
}

// WithDomain sets the error domain for structured error details.
// Defaults to "redpanda.com".
func WithDomain(d string) Option {
	return func(o *options) { o.domain = d }
}

// WithFiles sets the proto file registry for resolving method annotations.
// Defaults to protoregistry.GlobalFiles.
func WithFiles(f *protoregistry.Files) Option {
	return func(o *options) { o.files = f }
}

// WithSkipPrefixes sets method prefixes that bypass authorization entirely
// (e.g. "/grpc.health.v1.Health/", "/grpc.reflection.").
func WithSkipPrefixes(prefixes ...string) Option {
	return func(o *options) { o.skipPrefixes = prefixes }
}

// WithTracerProvider sets the OpenTelemetry tracer provider for authorization spans.
// Defaults to the global provider.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(o *options) { o.tracerProvider = tp }
}

// Interceptor is a gRPC authorization interceptor. Use [New] to create one.
type Interceptor struct {
	base             *authzcore.Base
	extractPrincipal PrincipalExtractor
	logger           *slog.Logger
}

// New creates a gRPC authorization interceptor.
//
// resourceName is the base scope path for policy evaluation
// (e.g. "organizations/{org}/resourcegroups/{rg}/dataplanes/{dp}").
//
// extractPrincipal extracts the caller's identity from the gRPC request context.
//
// policy provides the initial authorization policy. Use [authz.PolicyWatchFunc]
// wrapped in a closure for hot-reloading, or pass a static [authz.Policy] via
// [authz.StaticPolicy].
func New(
	resourceName authz.ResourceName,
	extractPrincipal PrincipalExtractor,
	policy authz.PolicyWatchFunc,
	opts ...Option,
) (*Interceptor, error) {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	base, err := authzcore.New(authzcore.Config{
		Logger:         o.logger,
		ResourceName:   resourceName,
		PolicyWatch:    policy,
		Domain:         o.domain,
		Files:          o.files,
		SkipPrefixes:   o.skipPrefixes,
		TracerProvider: o.tracerProvider,
	})
	if err != nil {
		return nil, err
	}
	logger := o.logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Interceptor{base: base, extractPrincipal: extractPrincipal, logger: logger}, nil
}

// Unary returns a gRPC unary server interceptor.
func (i *Interceptor) Unary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if i.base.ShouldSkip(info.FullMethod) {
			return handler(ctx, req)
		}

		ma := i.base.LookupMethodAuthz(info.FullMethod)
		if ma != nil && ma.Skip {
			return handler(ctx, req)
		}

		principal, ok := i.extractPrincipal(ctx)
		if !ok {
			i.logger.Warn("Authorization denied", "method", info.FullMethod, "reason", "no_identity")
			return nil, status.Error(codes.Internal, "no identity in context")
		}

		if denial := i.base.CheckAccess(ctx, info.FullMethod, principal, ma, req); denial != nil {
			return nil, grpcError(i.base, denial)
		}

		resp, err := handler(ctx, req)
		if err != nil {
			return resp, err
		}

		if ma != nil && ma.Collection != nil {
			if err := i.base.FilterCollection(ma, principal, resp); err != nil {
				i.logger.Error("collection filtering failed", "method", info.FullMethod, "error", err)
				return nil, status.Error(codes.Internal, "authorization filter error")
			}
		}

		return resp, nil
	}
}

// Stream returns a gRPC stream server interceptor. It performs the same
// pre-call authorization check as Unary. Collection filtering is not
// applicable to streaming RPCs.
func (i *Interceptor) Stream() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if i.base.ShouldSkip(info.FullMethod) {
			return handler(srv, ss)
		}

		ma := i.base.LookupMethodAuthz(info.FullMethod)
		if ma != nil && ma.Skip {
			return handler(srv, ss)
		}

		principal, ok := i.extractPrincipal(ss.Context())
		if !ok {
			i.logger.Warn("Authorization denied", "method", info.FullMethod, "reason", "no_identity")
			return status.Error(codes.Internal, "no identity in context")
		}

		if denial := i.base.CheckAccess(ss.Context(), info.FullMethod, principal, ma, nil); denial != nil {
			return grpcError(i.base, denial)
		}

		return handler(srv, ss)
	}
}

// LookupMethodAuthz resolves a method name to its authorization info.
func (i *Interceptor) LookupMethodAuthz(fullMethod string) *authz.MethodAuthz {
	return i.base.LookupMethodAuthz(fullMethod)
}

// SwapPolicy replaces the active policy. Safe for concurrent use.
func (i *Interceptor) SwapPolicy(p authz.Policy) error {
	return i.base.SwapPolicy(p)
}

// Close stops the policy file watcher if one was configured.
func (i *Interceptor) Close() error {
	return i.base.Close()
}

func grpcError(b *authzcore.Base, d *authz.Denial) error {
	var code codes.Code
	var reason string
	switch d.Kind {
	case authz.DenialUnknownMethod:
		code = codes.FailedPrecondition
		reason = commonv1.Reason_REASON_INVALID_INPUT.String()
	case authz.DenialForbidden:
		code = codes.PermissionDenied
		reason = commonv1.Reason_REASON_PERMISSION_DENIED.String()
	case authz.DenialEmptyResourceID:
		code = codes.InvalidArgument
		reason = commonv1.Reason_REASON_INVALID_INPUT.String()
	default:
		code = codes.Internal
		reason = commonv1.Reason_REASON_SERVER_ERROR.String()
	}

	st, err := status.New(code, d.Message).WithDetails(authz.DenialErrorInfo(b.Domain(), reason, d))
	if err != nil {
		return status.Error(code, d.Message)
	}
	return st.Err()
}
