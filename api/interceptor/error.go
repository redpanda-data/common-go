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
