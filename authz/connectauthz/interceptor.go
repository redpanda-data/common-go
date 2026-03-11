// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package connectauthz provides a Connect interceptor that enforces
// policy-based authorization using [authz.Interceptor].
package connectauthz

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

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
	// ExtractPrincipal extracts the caller's principal from request context
	// and headers. Required.
	ExtractPrincipal PrincipalExtractor
	// Logger for authorization events. If nil, slog.Default() is used.
	Logger *slog.Logger
}

// New returns a connect.Interceptor that enforces policy-based
// authorization on Connect RPCs. It reuses the same Interceptor core
// (proto annotation discovery, policy evaluation, atomic policy swaps)
// as the gRPC interceptor.
func New(a *authz.Interceptor, cfg Config) connect.Interceptor {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &connectAuthzInterceptor{core: a, extractPrincipal: cfg.ExtractPrincipal, logger: logger}
}

type connectAuthzInterceptor struct {
	core             *authz.Interceptor
	extractPrincipal PrincipalExtractor
	logger           *slog.Logger
}

func (c *connectAuthzInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		procedure := req.Spec().Procedure
		if authz.ShouldSkip(procedure) {
			return next(ctx, req)
		}

		principal, ok := c.extractPrincipal(ctx, req.Header())
		if !ok {
			c.logger.Warn("No identity in context, denying access", "method", procedure)
			return nil, connect.NewError(connect.CodeInternal, errors.New("no identity in context"))
		}

		ma := c.core.LookupMethodAuthz(procedure)
		if denial := c.core.CheckAccess(ctx, procedure, principal, ma, req.Any()); denial != nil {
			return nil, connectError(c.core, denial)
		}

		resp, err := next(ctx, req)
		if err != nil {
			return resp, err
		}

		if ma != nil && ma.Collection != nil {
			if err := c.core.FilterCollection(ma, principal, resp.Any()); err != nil {
				c.logger.Error("collection filtering failed", "method", procedure, "error", err)
				return nil, connect.NewError(connect.CodeInternal, errors.New("authorization filter error"))
			}
		}

		return resp, nil
	}
}

func connectError(a *authz.Interceptor, d *authz.Denial) *connect.Error {
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

func (*connectAuthzInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (*connectAuthzInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
