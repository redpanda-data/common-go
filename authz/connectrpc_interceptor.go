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
	"net/http"

	"connectrpc.com/connect"
	"go.uber.org/zap"
)

// ConnectPrincipalExtractor extracts a principal from the request context
// and HTTP headers. Connect RPCs have headers available directly on the
// request, unlike gRPC where metadata is embedded in context.
type ConnectPrincipalExtractor func(ctx context.Context, headers http.Header) (PrincipalID, bool)

// ConnectInterceptorConfig configures the Connect authorization interceptor.
type ConnectInterceptorConfig struct {
	// ExtractPrincipal extracts the caller's principal from request context
	// and headers. If nil, falls back to the Interceptor's PrincipalExtractor
	// (ignoring headers).
	ExtractPrincipal ConnectPrincipalExtractor
}

// ConnectInterceptor returns a connect.Interceptor that enforces
// policy-based authorization on Connect RPCs. It reuses the same
// Interceptor core (proto annotation discovery, policy evaluation,
// atomic policy swaps) as the gRPC interceptor.
func (a *Interceptor) ConnectInterceptor(opts ...ConnectInterceptorConfig) connect.Interceptor {
	var extractor ConnectPrincipalExtractor
	if len(opts) > 0 && opts[0].ExtractPrincipal != nil {
		extractor = opts[0].ExtractPrincipal
	} else {
		ctxExtractor := a.extractPrincipal
		extractor = func(ctx context.Context, _ http.Header) (PrincipalID, bool) {
			return ctxExtractor(ctx)
		}
	}
	return &connectAuthzInterceptor{core: a, extractPrincipal: extractor}
}

type connectAuthzInterceptor struct {
	core             *Interceptor
	extractPrincipal ConnectPrincipalExtractor
}

func (c *connectAuthzInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		procedure := req.Spec().Procedure
		if shouldSkip(procedure) {
			return next(ctx, req)
		}

		principal, ok := c.extractPrincipal(ctx, req.Header())
		if !ok {
			c.core.logger.Warn("No identity in context, denying access", zap.String("method", procedure))
			return nil, connect.NewError(connect.CodeInternal, errors.New("no identity in context"))
		}

		ma := c.core.lookupMethodAuthz(procedure)
		if denial := c.core.checkAccess(ctx, procedure, principal, ma, req.Any()); denial != nil {
			return nil, connectError(denial)
		}

		resp, err := next(ctx, req)
		if err != nil {
			return resp, err
		}

		if ma != nil && ma.isCollection {
			c.core.filterCollection(ma, principal, resp.Any())
		}

		return resp, nil
	}
}

func connectError(d *authzDenial) *connect.Error {
	switch d.kind {
	case authzDenialUnknownMethod, authzDenialForbidden:
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("%s", d.message))
	case authzDenialEmptyResourceID:
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s", d.message))
	default:
		return connect.NewError(connect.CodeInternal, fmt.Errorf("%s", d.message))
	}
}

func (*connectAuthzInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (*connectAuthzInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
