package grpcgateway

import (
	"net/http"

	"connectrpc.com/connect"
)

// ConnectCodeToHTTPStatus converts a connect error code into the corresponding
// HTTP response status.
//
//nolint:cyclop // It's easy enough to understand, just a bunch of switch case.
func ConnectCodeToHTTPStatus(code connect.Code) int {
	switch code {
	case 0:
		return http.StatusOK
	case connect.CodeCanceled:
		return 499
	case connect.CodeUnknown:
		return http.StatusInternalServerError
	case connect.CodeInvalidArgument:
		return http.StatusBadRequest
	case connect.CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	case connect.CodeNotFound:
		return http.StatusNotFound
	case connect.CodeAlreadyExists:
		return http.StatusConflict
	case connect.CodePermissionDenied:
		return http.StatusForbidden
	case connect.CodeUnauthenticated:
		return http.StatusUnauthorized
	case connect.CodeResourceExhausted:
		return http.StatusTooManyRequests
	case connect.CodeFailedPrecondition:
		// Note, this deliberately doesn't translate to the similarly named '412 Precondition Failed' HTTP response status.
		return http.StatusBadRequest
	case connect.CodeAborted:
		return http.StatusConflict
	case connect.CodeOutOfRange:
		return http.StatusBadRequest
	case connect.CodeUnimplemented:
		return http.StatusNotImplemented
	case connect.CodeInternal:
		return http.StatusInternalServerError
	case connect.CodeUnavailable:
		return http.StatusServiceUnavailable
	case connect.CodeDataLoss:
		return http.StatusInternalServerError
	default:
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
