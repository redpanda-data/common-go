// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package connectinterceptor provides a Connect interceptor that enforces
// policy-based authorization using [authz.Interceptor].
package connectinterceptor

import (
	"context"
	"errors"
	"fmt"
	"net/http"

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
	// and headers. If nil, falls back to the Interceptor's PrincipalExtractor
	// (ignoring headers).
	ExtractPrincipal PrincipalExtractor
}

// New returns a connect.Interceptor that enforces policy-based
// authorization on Connect RPCs. It reuses the same Interceptor core
// (proto annotation discovery, policy evaluation, atomic policy swaps)
// as the gRPC interceptor.
func New(a *authz.Interceptor, opts ...Config) connect.Interceptor {
	var extractor PrincipalExtractor
	if len(opts) > 0 && opts[0].ExtractPrincipal != nil {
		extractor = opts[0].ExtractPrincipal
	} else {
		ctxExtractor := a.ExtractPrincipal()
		extractor = func(ctx context.Context, _ http.Header) (authz.PrincipalID, bool) {
			return ctxExtractor(ctx)
		}
	}
	return &connectAuthzInterceptor{core: a, extractPrincipal: extractor}
}

type connectAuthzInterceptor struct {
	core             *authz.Interceptor
	extractPrincipal PrincipalExtractor
}

func (c *connectAuthzInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		procedure := req.Spec().Procedure
		if authz.ShouldSkip(procedure) {
			return next(ctx, req)
		}

		principal, ok := c.extractPrincipal(ctx, req.Header())
		if !ok {
			c.core.Logger().Warn("No identity in context, denying access", "method", procedure)
			return nil, connect.NewError(connect.CodeInternal, errors.New("no identity in context"))
		}

		ma := c.core.LookupMethodAuthz(procedure)
		if denial := c.core.CheckAccess(ctx, procedure, principal, ma, req.Any()); denial != nil {
			return nil, connectError(denial)
		}

		resp, err := next(ctx, req)
		if err != nil {
			return resp, err
		}

		if ma != nil && ma.Collection != nil {
			c.core.FilterCollection(ma, principal, resp.Any())
		}

		return resp, nil
	}
}

func connectError(d *authz.Denial) *connect.Error {
	switch d.Kind {
	case authz.DenialUnknownMethod, authz.DenialForbidden:
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("%s", d.Message))
	case authz.DenialEmptyResourceID:
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s", d.Message))
	default:
		return connect.NewError(connect.CodeInternal, fmt.Errorf("%s", d.Message))
	}
}

func (*connectAuthzInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (*connectAuthzInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
