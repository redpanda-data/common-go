// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package grpcinterceptor provides a gRPC unary server interceptor
// that enforces policy-based authorization using [authz.Interceptor].
package grpcinterceptor

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/redpanda-data/common-go/authz"
)

// UnaryServerInterceptor returns a gRPC unary server interceptor that
// enforces authorization checks using the given [authz.Interceptor].
func UnaryServerInterceptor(a *authz.Interceptor) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if authz.ShouldSkip(info.FullMethod) {
			return handler(ctx, req)
		}

		principal, ok := a.ExtractPrincipal()(ctx)
		if !ok {
			a.Logger().Warn("No identity in context, denying access", "method", info.FullMethod)
			return nil, status.Error(codes.Internal, "no identity in context")
		}

		ma := a.LookupMethodAuthz(info.FullMethod)
		if denial := a.CheckAccess(ctx, info.FullMethod, principal, ma, req); denial != nil {
			return nil, grpcError(denial)
		}

		resp, err := handler(ctx, req)
		if err != nil {
			return resp, err
		}

		if ma != nil && ma.Collection != nil {
			if err := a.FilterCollection(ma, principal, resp); err != nil {
				a.Logger().Error("collection filtering failed", "method", info.FullMethod, "error", err)
				return nil, status.Error(codes.Internal, "authorization filter error")
			}
		}

		return resp, nil
	}
}

func grpcError(d *authz.Denial) error {
	switch d.Kind {
	case authz.DenialUnknownMethod, authz.DenialForbidden:
		return status.Error(codes.PermissionDenied, d.Message)
	case authz.DenialEmptyResourceID:
		return status.Error(codes.InvalidArgument, d.Message)
	default:
		return status.Error(codes.Internal, d.Message)
	}
}
