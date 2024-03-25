package interceptor

import (
	"context"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"

	"github.com/redpanda-data/common-go/api/grpcgateway"
)

var _ connect.Interceptor = &Observer{}

// Observer wraps all requests/responses and emits events that inform about
// request duration, size and status code.
type Observer struct {
	onRequestEnded func(context.Context, *RequestMetadata)
}

// NewObserver creates a new Observer.
func NewObserver(onRequestEnded func(context.Context, *RequestMetadata)) *Observer {
	return &Observer{
		onRequestEnded: onRequestEnded,
	}
}

// WrapHandler mounts an HTTP middleware to gather metrics at the
// HTTP level.
func (in *Observer) WrapHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Inject request metadata into request context
		var procedure string
		switch {
		case strings.HasPrefix(r.RequestURI, "/grpc.reflection.v1.ServerReflection"),
			strings.HasPrefix(r.RequestURI, "/grpc.reflection.v1alpha.ServerReflection"):
			procedure = r.RequestURI
		default:
			procedure = "none"
		}
		rm := &RequestMetadata{
			startAt:     time.Now(),
			peerAddress: r.RemoteAddr,
			protocol:    "http",
			procedure:   procedure,
			requestURI:  r.RequestURI,
			method:      r.Method,
			// Other properties are set by wrapped body ready, response writer
			// as well as connectrpc interceptors.
		}
		ctx := setRequestMetadata(r.Context(), rm)
		r = r.WithContext(ctx)

		// 2. Inject custom body reader that counts the bytes as part of the request
		r.Body = bodyReader{
			base:      r.Body,
			bytesRead: &rm.bytesReceived,
		}

		// 3. Wrap a response writer to collect metrics while writing the response
		wrappedResponseWriter := responseController{
			base: w.(responseWriteFlusher),
			rm:   rm,
		}

		// 4. Let handler(s) process the request
		next.ServeHTTP(wrappedResponseWriter, r)

		// 5. Inform RequestMetadata that we can stop measuring duration
		rm.end()
		in.onRequestEnded(r.Context(), rm)
	})
}

// WrapUnary creates an interceptor to validate Connect requests.
func (*Observer) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if req.Spec().IsClient {
			return next(ctx, req)
		}

		rm := getRequestMetadata(ctx)

		// For HTTP paths invoked via gRPC gateway, procedure is expected not to be set.
		// In that case, let's try to retrieve it with gRPC gateway's runtime pkg.
		procedure := req.Spec().Procedure
		if procedure == "" {
			path, ok := runtime.RPCMethod(ctx)
			if !ok {
				procedure = "unknown"
			} else {
				procedure = path
			}
		}
		rm.procedure = procedure

		protocol := req.Peer().Protocol
		if protocol == "" {
			protocol = "http"
		}
		rm.protocol = protocol

		// 2. Execute request
		response, err := next(ctx, req)
		rm.err = err

		return response, err
	}
}

// WrapStreamingClient is the middleware handler for bidirectional requests from
// the client perspective.
func (*Observer) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler is the middleware handler for bidirectional requests from
// the server handling perspective.
func (*Observer) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, stream connect.StreamingHandlerConn) error {
		rm := getRequestMetadata(ctx)
		rm.procedure = stream.Spec().Procedure
		rm.protocol = stream.Peer().Protocol

		wrapped := streamingHandlerConn{
			base: stream,
			rm:   rm,
		}
		if err := next(ctx, wrapped); err != nil {
			rm.err = err
			return err
		}
		return nil
	}
}

// RequestMetadata represents the metadata gathered during the entire lifecycle
// of an incoming API request.
type RequestMetadata struct {
	startAt          time.Time
	finishedAt       time.Time
	duration         time.Duration
	procedure        string
	requestURI       string
	method           string
	protocol         string
	peerAddress      string
	messagesReceived int
	messagesSent     int
	bytesReceived    int64
	bytesSent        int64
	httpStatusCode   int
	err              error
}

// StartAt retrieves the timestamp indicating when the server received the
// request.
func (r *RequestMetadata) StartAt() time.Time {
	return r.startAt
}

// FinishedAt retrieves the timestamp indicating when the server completed
// processing the request.
func (r *RequestMetadata) FinishedAt() time.Time {
	return r.finishedAt
}

// Duration returns the duration of the request processing.
func (r *RequestMetadata) Duration() time.Duration {
	return r.duration
}

// Procedure returns the RPC name.
func (r *RequestMetadata) Procedure() string {
	return r.procedure
}

// RequestURI returns the HTTP request uri.
func (r *RequestMetadata) RequestURI() string {
	return r.requestURI
}

// Method returns the HTTP method.
func (r *RequestMetadata) Method() string {
	return r.method
}

// Protocol returns the protocol that was used (e.g. grpc, connect, http).
func (r *RequestMetadata) Protocol() string {
	return r.protocol
}

// PeerAddress returns the IP address of the requester.
func (r *RequestMetadata) PeerAddress() string {
	return r.peerAddress
}

// MessagesReceived returns the number of messages received
// in a streaming request.
func (r *RequestMetadata) MessagesReceived() int {
	return r.messagesReceived
}

// MessagesSent returns the number of messages sent
// in a streaming request.
func (r *RequestMetadata) MessagesSent() int {
	return r.messagesSent
}

// BytesReceived is the number of bytes we read via HTTP.
func (r *RequestMetadata) BytesReceived() int64 {
	return r.bytesReceived
}

// BytesSent is the number of bytes we sent via HTTP.
func (r *RequestMetadata) BytesSent() int64 {
	return r.bytesSent
}

// HTTPStatusCode returns the HTTP status code. This is expected to be 200 for
// most gRPC and connectrpc requests.
func (r *RequestMetadata) HTTPStatusCode() int {
	return r.httpStatusCode
}

// StatusCode returns the response status code in a string presentation.
// If there was no error the status code "ok" will be returned.
func (r *RequestMetadata) StatusCode() string {
	if r.procedure == "none" && r.protocol == "http" {
		connectCode := grpcgateway.HTTPStatusCodeToConnectCode(r.httpStatusCode)
		if connectCode == 0 {
			return "ok"
		}
		return connectCode.String()
	}

	if r.err == nil {
		return "ok"
	}
	return connect.CodeOf(r.err).String()
}

// Err returns the error that was returned by a handler.
func (r *RequestMetadata) Err() error {
	return r.err
}

func (r *RequestMetadata) end() {
	r.finishedAt = time.Now()
	r.duration = time.Since(r.startAt)
}

type requestMetadataCtxKey struct{}

func setRequestMetadata(ctx context.Context, rm *RequestMetadata) context.Context {
	return context.WithValue(ctx, requestMetadataCtxKey{}, rm)
}

func getRequestMetadata(ctx context.Context) *RequestMetadata {
	//nolint:revive // We are certain that this assertion is okay
	return ctx.Value(requestMetadataCtxKey{}).(*RequestMetadata)
}
