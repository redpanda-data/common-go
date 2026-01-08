// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package grpcgateway

import (
	"net/http"

	"connectrpc.com/connect"
)

// ConnectCodeToHTTPStatus converts a connect error code into the corresponding
// HTTP response status.
func ConnectCodeToHTTPStatus(code connect.Code) int {
	switch code {
	case 0:
		return http.StatusOK
	case connect.CodeCanceled:
		return 499

	// Note, CodeFailedPrecondition deliberately doesn't translate to the similarly named '412 Precondition Failed' HTTP response status.
	case connect.CodeInvalidArgument, connect.CodeFailedPrecondition, connect.CodeOutOfRange:
		return http.StatusBadRequest
	case connect.CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	case connect.CodeNotFound:
		return http.StatusNotFound
	case connect.CodeAlreadyExists, connect.CodeAborted:
		return http.StatusConflict
	case connect.CodePermissionDenied:
		return http.StatusForbidden
	case connect.CodeUnauthenticated:
		return http.StatusUnauthorized
	case connect.CodeResourceExhausted:
		return http.StatusTooManyRequests
	case connect.CodeUnimplemented:
		return http.StatusNotImplemented
	case connect.CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		// includes CodeInternal, CodeDataLoss, CodeUnknown
		return http.StatusInternalServerError
	}
}

// HTTPStatusCodeToConnectCode converts an HTTP status code into the
// corresponding connect response code.
func HTTPStatusCodeToConnectCode(code int) connect.Code {
	switch code {
	case http.StatusOK:
		return 0 // OK
	case http.StatusBadRequest:
		return connect.CodeInternal
	case http.StatusUnauthorized:
		return connect.CodeUnauthenticated
	case http.StatusForbidden:
		return connect.CodePermissionDenied
	case http.StatusNotFound:
		return connect.CodeUnimplemented
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return connect.CodeUnavailable
	default:
		return connect.CodeUnknown
	}
}
