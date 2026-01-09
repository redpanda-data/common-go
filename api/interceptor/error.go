// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package interceptor

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	apierrors "github.com/redpanda-data/common-go/api/errors"
)

// NewSafeErrorInterceptor is a client interceptor that strips error messages
// responses from internal microservices, and extracts details from the
// ExternalError detail.
func NewSafeErrorInterceptor() connect.UnaryInterceptorFunc {
	interceptor := func(next connect.UnaryFunc) connect.UnaryFunc {
		return connect.UnaryFunc(func(
			ctx context.Context,
			req connect.AnyRequest,
		) (connect.AnyResponse, error) {
			resp, err := next(ctx, req)
			if err != nil && req.Spec().IsClient {
				var connectErr *connect.Error
				if !errors.As(err, &connectErr) {
					return resp, connect.NewError(connect.CodeInternal, nil)
				}

				err = apierrors.NewSafePublicErrorConnect(connectErr)
			}
			return resp, err
		})
	}
	return connect.UnaryInterceptorFunc(interceptor)
}
