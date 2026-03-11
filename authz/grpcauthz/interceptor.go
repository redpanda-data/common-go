// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package grpcauthz provides a gRPC unary server interceptor
// that enforces policy-based authorization using [authz.Interceptor].
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
	// ExtractPrincipal extracts the caller's principal from the gRPC
	// request context (typically from metadata). Required.
	ExtractPrincipal PrincipalExtractor
	// Logger for authorization events. If nil, slog.Default() is used.
	Logger *slog.Logger
}

// UnaryServerInterceptor returns a gRPC unary server interceptor that
// enforces authorization checks using the given [authz.Interceptor].
func UnaryServerInterceptor(a *authz.Interceptor, cfg Config) grpc.UnaryServerInterceptor {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if authz.ShouldSkip(info.FullMethod) {
			return handler(ctx, req)
		}

		principal, ok := cfg.ExtractPrincipal(ctx)
		if !ok {
			logger.Warn("No identity in context, denying access", "method", info.FullMethod)
			return nil, status.Error(codes.Internal, "no identity in context")
		}

		ma := a.LookupMethodAuthz(info.FullMethod)
		if denial := a.CheckAccess(ctx, info.FullMethod, principal, ma, req); denial != nil {
			return nil, grpcError(a, denial)
		}

		resp, err := handler(ctx, req)
		if err != nil {
			return resp, err
		}

		if ma != nil && ma.Collection != nil {
			if err := a.FilterCollection(ma, principal, resp); err != nil {
				logger.Error("collection filtering failed", "method", info.FullMethod, "error", err)
				return nil, status.Error(codes.Internal, "authorization filter error")
			}
		}

		return resp, nil
	}
}

func grpcError(a *authz.Interceptor, d *authz.Denial) error {
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
