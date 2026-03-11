// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package connectauthz provides a Connect interceptor that enforces
// policy-based authorization using the authz engine.
package connectauthz

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	commonv1 "buf.build/gen/go/redpandadata/common/protocolbuffers/go/redpanda/api/common/v1"
	"connectrpc.com/connect"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/redpanda-data/common-go/authz"
	"github.com/redpanda-data/common-go/authz/authzcore"
)

// PrincipalExtractor extracts a principal from the request context
// and HTTP headers. Connect RPCs have headers available directly on the
// request, unlike gRPC where metadata is embedded in context.
type PrincipalExtractor func(ctx context.Context, headers http.Header) (authz.PrincipalID, bool)

// Option configures optional behavior of the interceptor.
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

// WithSkipPrefixes sets procedure prefixes that bypass authorization entirely
// (e.g. "/grpc.health.v1.Health/", "/grpc.reflection.").
func WithSkipPrefixes(prefixes ...string) Option {
	return func(o *options) { o.skipPrefixes = prefixes }
}

// WithTracerProvider sets the OpenTelemetry tracer provider for authorization spans.
// Defaults to the global provider.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(o *options) { o.tracerProvider = tp }
}

// Interceptor is a Connect authorization interceptor. Use [New] to create one.
type Interceptor struct {
	base             *authzcore.Base
	extractPrincipal PrincipalExtractor
	logger           *slog.Logger
}

// New creates a Connect authorization interceptor.
//
// resourceName is the base scope path for policy evaluation
// (e.g. "organizations/{org}/resourcegroups/{rg}/dataplanes/{dp}").
//
// extractPrincipal extracts the caller's identity from the request context and headers.
//
// policy provides the initial authorization policy. Use [authzcore.PolicyWatchFunc]
// wrapped in a closure for hot-reloading, or pass a static [authz.Policy] via
// [authzcore.StaticPolicy].
func New(
	resourceName authz.ResourceName,
	extractPrincipal PrincipalExtractor,
	policy authzcore.PolicyWatchFunc,
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

// LookupMethodAuthz resolves a method name to its authorization info.
func (i *Interceptor) LookupMethodAuthz(procedure string) *authzcore.MethodAuthz {
	return i.base.LookupMethodAuthz(procedure)
}

// SwapPolicy replaces the active policy. Safe for concurrent use.
// On error, the previous policy remains in effect.
func (i *Interceptor) SwapPolicy(p authz.Policy) error {
	return i.base.SwapPolicy(p)
}

// Close stops the policy file watcher if one was configured.
func (i *Interceptor) Close() error {
	return i.base.Close()
}

// WrapUnary implements [connect.Interceptor].
func (i *Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		procedure := req.Spec().Procedure
		if i.base.ShouldSkip(procedure) {
			return next(ctx, req)
		}

		ma := i.base.LookupMethodAuthz(procedure)
		if ma != nil && ma.Skip {
			return next(ctx, req)
		}

		principal, ok := i.extractPrincipal(ctx, req.Header())
		if !ok {
			i.logger.Warn("Authorization denied", "method", procedure, "reason", "no_identity")
			return nil, connect.NewError(connect.CodeInternal, errors.New("no identity in context"))
		}

		if denial := i.base.CheckAccess(ctx, procedure, principal, ma, req.Any()); denial != nil {
			return nil, connectError(i.base, denial)
		}

		resp, err := next(ctx, req)
		if err != nil {
			return resp, err
		}

		if ma != nil && ma.Collection != nil {
			if err := i.base.FilterCollection(ma, principal, resp.Any()); err != nil {
				i.logger.Error("collection filtering failed", "method", procedure, "error", err)
				return nil, connect.NewError(connect.CodeInternal, errors.New("authorization filter error"))
			}
		}

		return resp, nil
	}
}

// WrapStreamingClient implements [connect.Interceptor]. No-op for server-side interceptors.
func (*Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements [connect.Interceptor]. It performs the same
// pre-call authorization check as WrapUnary. Collection filtering is not
// applicable to streaming RPCs.
func (i *Interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		procedure := conn.Spec().Procedure
		if i.base.ShouldSkip(procedure) {
			return next(ctx, conn)
		}

		ma := i.base.LookupMethodAuthz(procedure)
		if ma != nil && ma.Skip {
			return next(ctx, conn)
		}

		principal, ok := i.extractPrincipal(ctx, conn.RequestHeader())
		if !ok {
			i.logger.Warn("Authorization denied", "method", procedure, "reason", "no_identity")
			return connect.NewError(connect.CodeInternal, errors.New("no identity in context"))
		}

		if denial := i.base.CheckAccess(ctx, procedure, principal, ma, nil); denial != nil {
			return connectError(i.base, denial)
		}

		return next(ctx, conn)
	}
}

func connectError(b *authzcore.Base, d *authzcore.Denial) *connect.Error {
	var code connect.Code
	var reason string
	switch d.Kind {
	case authzcore.DenialUnknownMethod:
		code = connect.CodeFailedPrecondition
		reason = commonv1.Reason_REASON_INVALID_INPUT.String()
	case authzcore.DenialForbidden:
		code = connect.CodePermissionDenied
		reason = commonv1.Reason_REASON_PERMISSION_DENIED.String()
	case authzcore.DenialEmptyResourceID:
		code = connect.CodeInvalidArgument
		reason = commonv1.Reason_REASON_INVALID_INPUT.String()
	default:
		code = connect.CodeInternal
		reason = commonv1.Reason_REASON_SERVER_ERROR.String()
	}

	connectErr := connect.NewError(code, errors.New(d.Message))
	info := authzcore.DenialErrorInfo(b.Domain(), reason, d)
	if detail, err := connect.NewErrorDetail(info); err == nil {
		connectErr.AddDetail(detail)
	}
	return connectErr
}
