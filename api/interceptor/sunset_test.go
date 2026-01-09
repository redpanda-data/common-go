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

func TestNewSunsetInterceptor(t *testing.T) {
	// Setup test date
	sunsetDate := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)
	deprecationDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Test with all options
	interceptor := interceptor.NewSunset(
		sunsetDate,
		interceptor.WithDeprecationDate(deprecationDate),
		interceptor.WithLink("https://example.com/api/deprecation"),
	)

	assert.Equal(t, "Wed, 31 Dec 2025 23:59:59 UTC", interceptor.SunsetDate())
	assert.Equal(t, "@1704067200", interceptor.DeprecationDate())
	assert.Equal(t, "<https://example.com/api/deprecation>; rel=\"sunset\"", interceptor.LinkValue())
}

// TestNilResponse ensures that the interceptor doesn't panic if the response is nil.
func TestNilResponse(t *testing.T) {
	sunsetDate := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)
	interceptor := interceptor.NewSunset(sunsetDate)

	// Create a handler that returns a nil response
	handler := func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, nil
	}

	// Wrap the handler with the interceptor
	wrappedHandler := interceptor.WrapUnary(handler)

	// Call the wrapped handler
	_, err := wrappedHandler(context.Background(), connect.NewRequest(&elizav1.SayRequest{}))

	// Assert that no error occurred
	assert.NoError(t, err)
}

func TestSunset(t *testing.T) {
	// Setup test date
	sunsetDate := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)
	deprecationDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Test with all options
	i := interceptor.NewSunset(
		sunsetDate,
		interceptor.WithDeprecationDate(deprecationDate),
		interceptor.WithLink("https://example.com/api/deprecation"),
	)

	mux := http.NewServeMux()
	mux.Handle(elizav1connect.NewElizaServiceHandler(
		elizaServerHandler{},
		connect.WithInterceptors(i),
	))

	// Start HTTP server
	h2s := &http2.Server{}
	handler := h2c.NewHandler(mux, h2s)
	httpServer := http.Server{
		Handler: handler,
	}
	lis := bufconn.Listen(1024 * 1024)
	ready := make(chan struct{})
	go func() {
		close(ready) // Signal that the server is ready
		err := httpServer.Serve(lis)
		require.NoError(t, err)
	}()
	<-ready // Wait for the server to signal readiness

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
		res, err := cl.Say(ctx, connect.NewRequest(&elizav1.SayRequest{Sentence: "hello-test"}))
		require.NoError(t, err)
		require.NotNil(t, res)
		// Verify headers set by server-side interceptor
		assert.Equal(t, "Wed, 31 Dec 2025 23:59:59 UTC", res.Header().Get(interceptor.SunsetHeaderName))
		assert.Equal(t, "@1704067200", res.Header().Get(interceptor.DeprecationHeaderName))
		assert.Equal(t, "<https://example.com/api/deprecation>; rel=\"sunset\"", res.Header().Get(interceptor.LinkHeaderName))
	})

	t.Run("server stream", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()

		name := "Santi"
		stream, err := cl.Introduce(ctx, connect.NewRequest(&elizav1.IntroduceRequest{Name: name}))
		require.NoError(t, err)

		for stream.Receive() {
			_ = stream.Msg()
		}
		require.Nil(t, stream.Err())
		assert.Nil(t, stream.Close())

		assert.Equal(t, "Wed, 31 Dec 2025 23:59:59 UTC", stream.ResponseHeader().Get(interceptor.SunsetHeaderName))
		assert.Equal(t, "@1704067200", stream.ResponseHeader().Get(interceptor.DeprecationHeaderName))
		assert.Equal(t, "<https://example.com/api/deprecation>; rel=\"sunset\"", stream.ResponseHeader().Get(interceptor.LinkHeaderName))
	})

	t.Run("unary error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		_, err := cl.Say(ctx, connect.NewRequest(&elizav1.SayRequest{Sentence: "error"}))
		require.Error(t, err)
		assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
	})

	t.Run("server stream error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()

		stream, err := cl.Introduce(ctx, connect.NewRequest(&elizav1.IntroduceRequest{Name: "error"}))
		require.NoError(t, err)

		for stream.Receive() {
			_ = stream.Msg()
		}
		require.Error(t, stream.Err())
		assert.Equal(t, connect.CodeInternal, connect.CodeOf(stream.Err()))

		assert.Equal(t, "Wed, 31 Dec 2025 23:59:59 UTC", stream.ResponseHeader().Get(interceptor.SunsetHeaderName))
		assert.Equal(t, "@1704067200", stream.ResponseHeader().Get(interceptor.DeprecationHeaderName))
		assert.Equal(t, "<https://example.com/api/deprecation>; rel=\"sunset\"", stream.ResponseHeader().Get(interceptor.LinkHeaderName))
	})
}
