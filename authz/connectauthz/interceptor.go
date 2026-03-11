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
	"strings"

	commonv1 "buf.build/gen/go/redpandadata/common/protocolbuffers/go/redpanda/api/common/v1"
	"connectrpc.com/connect"

	"github.com/redpanda-data/common-go/authz"
)

// PrincipalExtractor extracts a principal from the request context
// and HTTP headers. Connect RPCs have headers available directly on the
// request, unlike gRPC where metadata is embedded in context.
type PrincipalExtractor func(ctx context.Context, headers http.Header) (authz.PrincipalID, bool)

// Config configures the Connect authorization interceptor.
type Config struct {
	// Logger for authorization events. If nil, slog.Default() is used.
	Logger *slog.Logger
	// ResourceName is the base scope path for policy evaluation,
	// e.g. "organizations/{org}/resourcegroups/{rg}/dataplanes/{dp}".
	ResourceName authz.ResourceName
	// ExtractPrincipal extracts the caller's principal from request context
	// and headers. Required.
	ExtractPrincipal PrincipalExtractor
	// Policy is the initial authorization policy. Mutually exclusive with PolicyWatch.
	Policy authz.Policy
	// PolicyWatch loads the initial policy and watches for changes, hot-reloading
	// automatically. Mutually exclusive with Policy. Call [Interceptor.Close] to
	// stop watching.
	PolicyWatch authz.PolicyWatchFunc
	// Domain is the error domain for structured error details (e.g. "redpanda.com").
	// Defaults to "redpanda.com" if empty.
	Domain string
	// SkipPrefixes is a list of procedure prefixes that bypass authorization
	// entirely (e.g. "/grpc.health.v1.Health/", "/grpc.reflection.").
	SkipPrefixes []string
}

// Interceptor is a Connect authorization interceptor. Use [New] to create one.
type Interceptor struct {
	engine           *authz.Engine
	extractPrincipal PrincipalExtractor
	logger           *slog.Logger
	skipPrefixes     []string
}

// New creates a Connect authorization interceptor.
func New(cfg Config) (*Interceptor, error) {
	engine, err := authz.NewEngine(authz.EngineConfig{
		Logger:            cfg.Logger,
		ResourceName:      cfg.ResourceName,
		Policy:            cfg.Policy,
		PolicyWatch: cfg.PolicyWatch,
		Domain:      cfg.Domain,
	})
	if err != nil {
		return nil, err
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Interceptor{engine: engine, extractPrincipal: cfg.ExtractPrincipal, logger: logger, skipPrefixes: cfg.SkipPrefixes}, nil
}

// SwapPolicy replaces the active policy. Safe for concurrent use.
func (i *Interceptor) SwapPolicy(p authz.Policy) error {
	return i.engine.SwapPolicy(p)
}

// Close stops the policy file watcher if one was configured.
func (i *Interceptor) Close() error {
	return i.engine.Close()
}

// WrapUnary implements [connect.Interceptor].
func (i *Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		procedure := req.Spec().Procedure
		if i.shouldSkip(procedure) {
			return next(ctx, req)
		}

		ma := i.engine.LookupMethodAuthz(procedure)
		if ma != nil && ma.Skip {
			return next(ctx, req)
		}

		principal, ok := i.extractPrincipal(ctx, req.Header())
		if !ok {
			i.logger.Warn("No identity in context, denying access", "method", procedure)
			return nil, connect.NewError(connect.CodeInternal, errors.New("no identity in context"))
		}

		if denial := i.engine.CheckAccess(ctx, procedure, principal, ma, req.Any()); denial != nil {
			return nil, connectError(i.engine, denial)
		}

		resp, err := next(ctx, req)
		if err != nil {
			return resp, err
		}

		if ma != nil && ma.Collection != nil {
			if err := i.engine.FilterCollection(ma, principal, resp.Any()); err != nil {
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
		if i.shouldSkip(procedure) {
			return next(ctx, conn)
		}

		ma := i.engine.LookupMethodAuthz(procedure)
		if ma != nil && ma.Skip {
			return next(ctx, conn)
		}

		principal, ok := i.extractPrincipal(ctx, conn.RequestHeader())
		if !ok {
			i.logger.Warn("No identity in context, denying access", "method", procedure)
			return connect.NewError(connect.CodeInternal, errors.New("no identity in context"))
		}

		if denial := i.engine.CheckAccess(ctx, procedure, principal, ma, nil); denial != nil {
			return connectError(i.engine, denial)
		}

		return next(ctx, conn)
	}
}

func (i *Interceptor) shouldSkip(procedure string) bool {
	for _, prefix := range i.skipPrefixes {
		if strings.HasPrefix(procedure, prefix) {
			return true
		}
	}
	return false
}

func connectError(a *authz.Engine, d *authz.Denial) *connect.Error {
	var code connect.Code
	var reason string
	switch d.Kind {
	case authz.DenialUnknownMethod, authz.DenialForbidden:
		code = connect.CodePermissionDenied
		reason = commonv1.Reason_REASON_PERMISSION_DENIED.String()
	case authz.DenialEmptyResourceID:
		code = connect.CodeInvalidArgument
		reason = commonv1.Reason_REASON_INVALID_INPUT.String()
	default:
		code = connect.CodeInternal
		reason = commonv1.Reason_REASON_SERVER_ERROR.String()
	}

	connectErr := connect.NewError(code, errors.New(d.Message))
	info := authz.DenialErrorInfo(a.Domain(), reason, d)
	if detail, err := connect.NewErrorDetail(info); err == nil {
		connectErr.AddDetail(detail)
	}
	return connectErr
}
