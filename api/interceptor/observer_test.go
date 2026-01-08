// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

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
	var unaryMetadata interceptor.RequestMetadata
	var serverStreamMetadata interceptor.RequestMetadata
	onRequestEnded := func(_ context.Context, requestMetadata *interceptor.RequestMetadata) {
		switch requestMetadata.Procedure() {
		case "/connectrpc.eliza.v1.ElizaService/Say":
			unaryMetadata = *requestMetadata
		case "/connectrpc.eliza.v1.ElizaService/Introduce":
			serverStreamMetadata = *requestMetadata
		default:
			t.Errorf("received request metadata for an unexpected procedure %q", requestMetadata.Procedure())
		}
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

	t.Run("unary", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		_, err := cl.Say(ctx, connect.NewRequest(&elizav1.SayRequest{Sentence: "hello-test"}))
		require.NoError(t, err)

		assert.Equal(t, "connect", unaryMetadata.Protocol())
		assert.Equal(t, "ok", unaryMetadata.StatusCode())
		assert.Equal(t, "/connectrpc.eliza.v1.ElizaService/Say", unaryMetadata.Procedure())
		assert.Equal(t, nil, unaryMetadata.Err())
		assert.Equal(t, int64(36), unaryMetadata.BytesSent())
		assert.Equal(t, int64(12), unaryMetadata.BytesReceived())
	})

	t.Run("server stream", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()

		name := "martin"
		stream, err := cl.Introduce(ctx, connect.NewRequest(&elizav1.IntroduceRequest{Name: name}))
		require.NoError(t, err)

		for stream.Receive() {
			_ = stream.Msg()
		}
		require.Nil(t, stream.Err())
		assert.Nil(t, stream.Close())

		// The introduce handler returns the name len(name) times
		expectedResponses := len(name)

		assert.Equal(t, "connect", serverStreamMetadata.Protocol())
		assert.Equal(t, "ok", serverStreamMetadata.StatusCode())
		assert.Equal(t, "/connectrpc.eliza.v1.ElizaService/Introduce", serverStreamMetadata.Procedure())
		assert.Equal(t, nil, serverStreamMetadata.Err())
		assert.Equal(t, 1, serverStreamMetadata.MessagesReceived())
		assert.Equal(t, expectedResponses, serverStreamMetadata.MessagesSent())
	})
}
