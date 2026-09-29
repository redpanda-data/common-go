// Copyright 2026 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gzipcache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestInterop(t *testing.T) {
	c, err := New(WithMinBytes(1 << 10))
	require.NoError(t, err)
	p := payload(256<<10, 'a')
	msg := wrapperspb.Bytes(p) // one message shared by every stream, as a broadcaster does

	const procedure = "/test.v1.Fanout/Watch"
	mux := http.NewServeMux()
	mux.Handle(procedure, connect.NewServerStreamHandler(
		procedure,
		func(_ context.Context, _ *connect.Request[emptypb.Empty], stream *connect.ServerStream[wrapperspb.BytesValue]) error {
			return stream.Send(msg)
		},
		c.WithCompression(),
	))
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	watch := func(t *testing.T, opts ...connect.ClientOption) {
		t.Helper()
		client := connect.NewClient[emptypb.Empty, wrapperspb.BytesValue](srv.Client(), srv.URL+procedure, opts...)
		stream, err := client.CallServerStream(t.Context(), connect.NewRequest(&emptypb.Empty{}))
		require.NoError(t, err)
		require.True(t, stream.Receive(), "receive: %v", stream.Err())
		assert.Equal(t, p, stream.Msg().GetValue())
		assert.False(t, stream.Receive())
		require.NoError(t, stream.Err())
		require.NoError(t, stream.Close())
	}

	t.Run("connect", func(t *testing.T) {
		for range 4 {
			watch(t)
		}
	})
	t.Run("grpc", func(t *testing.T) {
		for range 4 {
			watch(t, connect.WithGRPC())
		}
	})
	s := c.Stats()
	assert.Equal(t, uint64(1), s.Misses)
	assert.Equal(t, uint64(7), s.Hits+s.Waits)

	t.Run("identity", func(t *testing.T) {
		watch(t, connect.WithAcceptCompression(Name, nil, nil))
		assert.Equal(t, s, c.Stats())
	})
}
