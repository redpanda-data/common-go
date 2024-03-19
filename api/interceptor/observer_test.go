package interceptor_test

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"testing"
	"time"

	"buf.build/gen/go/connectrpc/eliza/connectrpc/go/connectrpc/eliza/v1/elizav1connect"
	elizav1 "buf.build/gen/go/connectrpc/eliza/protocolbuffers/go/connectrpc/eliza/v1"
	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc/test/bufconn"

	"github.com/redpanda-data/common-go/api/interceptor"
)

func TestObserver(t *testing.T) {
	// Create dummy handler with observer middleware
	var lastMetadata interceptor.RequestMetadata
	onRequestEnded := func(_ context.Context, requestMetadata *interceptor.RequestMetadata) {
		lastMetadata = *requestMetadata
	}
	observerMiddleware := interceptor.NewObserver(onRequestEnded)

	mux := http.NewServeMux()
	mux.Handle(elizav1connect.NewElizaServiceHandler(
		elizaServerHandler{},
		connect.WithInterceptors(observerMiddleware),
	))

	// Below boilerplate can be simplified by using the connect-go
	// memhttp package once exported. See following
	// issue: https://github.com/connectrpc/connect-go/issues/694

	// Start HTTP server
	h2s := &http2.Server{}
	handler := h2c.NewHandler(observerMiddleware.WrapHandler(mux), h2s)
	httpServer := http.Server{
		Handler: handler,
	}
	lis := bufconn.Listen(1024 * 1024)
	go func() {
		err := httpServer.Serve(lis)
		require.NoError(t, err)
	}()
	time.Sleep(time.Second)

	// Create client
	httpCl := &http.Client{
		Transport: &http2.Transport{
			DialTLSContext: func(ctx context.Context, _ string, _ string, _ *tls.Config) (net.Conn, error) {
				return lis.DialContext(ctx)
			},
			AllowHTTP: true,
		},
	}
	cl := elizav1connect.NewElizaServiceClient(httpCl, "http://"+lis.Addr().String())

	// Send request
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	_, err := cl.Say(ctx, connect.NewRequest(&elizav1.SayRequest{Sentence: "hello-test"}))
	require.NoError(t, err)

	assert.Equal(t, "connect", lastMetadata.Protocol())
	assert.Equal(t, "ok", lastMetadata.StatusCode())
	assert.Equal(t, "/connectrpc.eliza.v1.ElizaService/Say", lastMetadata.Procedure())
	assert.Equal(t, nil, lastMetadata.Err())
	assert.Equal(t, int64(36), lastMetadata.BytesSent())
	assert.Equal(t, int64(12), lastMetadata.BytesReceived())
}

type elizaServerHandler struct {
	elizav1connect.UnimplementedElizaServiceHandler
}

func (elizaServerHandler) Say(_ context.Context, req *connect.Request[elizav1.SayRequest]) (*connect.Response[elizav1.SayResponse], error) {
	return connect.NewResponse(&elizav1.SayResponse{
		Sentence: req.Msg.Sentence, // Just echo request string
	}), nil
}
