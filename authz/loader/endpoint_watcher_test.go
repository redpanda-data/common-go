// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package loader

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"buf.build/gen/go/redpandadata/common/connectrpc/go/redpanda/policymaterializer/v1/policymaterializerv1connect"
	policymaterializerv1 "buf.build/gen/go/redpandadata/common/protocolbuffers/go/redpanda/policymaterializer/v1"
	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/redpanda-data/common-go/authz"
)

// fakeServer is a controllable PolicyMaterializerServiceHandler for testing.
// Each connected client receives policies sent on the policies channel until
// it is closed (stream ends) or the context is cancelled.
type fakeServer struct {
	policies chan *policymaterializerv1.DataplanePolicy
}

func (f *fakeServer) WatchPolicy(
	ctx context.Context,
	_ *connect.Request[policymaterializerv1.WatchPolicyRequest],
	stream *connect.ServerStream[policymaterializerv1.WatchPolicyResponse],
) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case p, ok := <-f.policies:
			if !ok {
				return nil
			}
			if err := stream.Send(&policymaterializerv1.WatchPolicyResponse{Policy: p}); err != nil {
				return err
			}
		}
	}
}

// startTestServer starts an h2c Connect server backed by svc and returns its base URL.
func startTestServer(t *testing.T, svc policymaterializerv1connect.PolicyMaterializerServiceHandler) string {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := policymaterializerv1connect.NewPolicyMaterializerServiceHandler(svc)
	mux.Handle(path, handler)

	lis, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h2c.NewHandler(mux, &http2.Server{})}
	go srv.Serve(lis) //nolint:errcheck // test server, error irrelevant after t.Cleanup closes it
	t.Cleanup(func() { srv.Close() })
	return "http://" + lis.Addr().String()
}

func makePolicy(roleID, principal string) *policymaterializerv1.DataplanePolicy {
	return &policymaterializerv1.DataplanePolicy{
		Roles: []*policymaterializerv1.DataplaneRole{
			{Id: roleID, Permissions: []string{"read"}},
		},
		Bindings: []*policymaterializerv1.DataplaneRoleBinding{
			{RoleId: roleID, Principal: principal, Scope: "organizations/acme"},
		},
	}
}

func TestWatchPolicyFromEndpoint_InitialPolicy(t *testing.T) {
	policies := make(chan *policymaterializerv1.DataplanePolicy, 1)
	policies <- makePolicy("admin", "User:alice")

	addr := startTestServer(t, &fakeServer{policies: policies})

	got, err := WatchPolicyFromEndpoint(t.Context(), EndpointConfig{Address: addr}, func(authz.Policy, error) {})
	if err != nil {
		t.Fatalf("WatchPolicyFromEndpoint: %v", err)
	}

	if len(got.Roles) != 1 || string(got.Roles[0].ID) != "admin" {
		t.Errorf("unexpected initial policy: %+v", got)
	}
	if len(got.Bindings) != 1 || string(got.Bindings[0].Principal) != "User:alice" {
		t.Errorf("unexpected initial bindings: %+v", got.Bindings)
	}
}

func TestWatchPolicyFromEndpoint_Updates(t *testing.T) {
	policies := make(chan *policymaterializerv1.DataplanePolicy, 2)
	policies <- makePolicy("admin", "User:alice")
	policies <- makePolicy("viewer", "User:bob")

	addr := startTestServer(t, &fakeServer{policies: policies})

	updates := make(chan authz.Policy, 1)
	_, err := WatchPolicyFromEndpoint(t.Context(), EndpointConfig{Address: addr}, func(p authz.Policy, err error) {
		if err == nil {
			updates <- p
		}
	})
	if err != nil {
		t.Fatalf("WatchPolicyFromEndpoint: %v", err)
	}

	select {
	case p := <-updates:
		if len(p.Roles) != 1 || string(p.Roles[0].ID) != "viewer" {
			t.Errorf("unexpected update policy: %+v", p)
		}
	case <-t.Context().Done():
		t.Error("timed out waiting for policy update")
	}
}

// perConnServer dispatches each successive connection to a different handler function.
type perConnServer struct {
	mu       sync.Mutex
	handlers []func(context.Context, *connect.ServerStream[policymaterializerv1.WatchPolicyResponse]) error
	idx      int
}

func (s *perConnServer) WatchPolicy(
	ctx context.Context,
	_ *connect.Request[policymaterializerv1.WatchPolicyRequest],
	stream *connect.ServerStream[policymaterializerv1.WatchPolicyResponse],
) error {
	s.mu.Lock()
	i := s.idx
	s.idx++
	s.mu.Unlock()
	if i < len(s.handlers) {
		return s.handlers[i](ctx, stream)
	}
	<-ctx.Done()
	return nil
}

func TestWatchPolicyFromEndpoint_Timeout(t *testing.T) {
	old := initTimeout
	initTimeout = 50 * time.Millisecond
	t.Cleanup(func() { initTimeout = old })

	// Server that never sends anything.
	addr := startTestServer(t, &perConnServer{handlers: []func(context.Context, *connect.ServerStream[policymaterializerv1.WatchPolicyResponse]) error{
		func(ctx context.Context, _ *connect.ServerStream[policymaterializerv1.WatchPolicyResponse]) error {
			<-ctx.Done()
			return nil
		},
	}})

	_, err := WatchPolicyFromEndpoint(t.Context(), EndpointConfig{Address: addr}, func(authz.Policy, error) {})
	var initErr *InitializeWatchError
	if !errors.As(err, &initErr) {
		t.Fatalf("expected *InitializeWatchError, got %T: %v", err, err)
	}
}

// TestWatchPolicyFromEndpoint_TimeoutCancelsGoroutine verifies that the background
// goroutine is torn down when WatchPolicyFromEndpoint returns due to a timeout,
// not left running until the caller eventually cancels their own context.
func TestWatchPolicyFromEndpoint_TimeoutCancelsGoroutine(t *testing.T) {
	old := initTimeout
	initTimeout = 50 * time.Millisecond
	t.Cleanup(func() { initTimeout = old })

	// serverDone is closed when the server handler's ctx is cancelled.
	// If the goroutine leak fix is working, this happens promptly after
	// WatchPolicyFromEndpoint returns (innerCtx cancelled). Without the fix,
	// it would only happen when the test's own context is cancelled.
	serverDone := make(chan struct{})
	addr := startTestServer(t, &perConnServer{handlers: []func(context.Context, *connect.ServerStream[policymaterializerv1.WatchPolicyResponse]) error{
		func(ctx context.Context, _ *connect.ServerStream[policymaterializerv1.WatchPolicyResponse]) error {
			defer close(serverDone)
			<-ctx.Done()
			return nil
		},
	}})

	// Use a long-lived context to make a leaked goroutine observable.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := WatchPolicyFromEndpoint(ctx, EndpointConfig{Address: addr}, func(authz.Policy, error) {})
	var initErr *InitializeWatchError
	if !errors.As(err, &initErr) {
		t.Fatalf("expected *InitializeWatchError, got %T: %v", err, err)
	}

	select {
	case <-serverDone:
		// innerCtx was cancelled — goroutine cleaned up promptly.
	case <-time.After(500 * time.Millisecond):
		t.Error("background goroutine was not cancelled after WatchPolicyFromEndpoint returned")
	}
}

func TestWatchPolicyFromEndpoint_Reconnect(t *testing.T) {
	old := reconnectBackoffInitial
	reconnectBackoffInitial = 10 * time.Millisecond
	t.Cleanup(func() { reconnectBackoffInitial = old })

	secondPolicy := make(chan *policymaterializerv1.DataplanePolicy, 1)
	secondPolicy <- makePolicy("viewer", "User:bob")

	addr := startTestServer(t, &perConnServer{handlers: []func(context.Context, *connect.ServerStream[policymaterializerv1.WatchPolicyResponse]) error{
		// First connection: send initial policy then close.
		func(_ context.Context, stream *connect.ServerStream[policymaterializerv1.WatchPolicyResponse]) error {
			_ = stream.Send(&policymaterializerv1.WatchPolicyResponse{Policy: makePolicy("admin", "User:alice")})
			return nil
		},
		// Second connection (after reconnect): send updated policy.
		func(ctx context.Context, stream *connect.ServerStream[policymaterializerv1.WatchPolicyResponse]) error {
			select {
			case p := <-secondPolicy:
				_ = stream.Send(&policymaterializerv1.WatchPolicyResponse{Policy: p})
			case <-ctx.Done():
			}
			<-ctx.Done()
			return nil
		},
	}})

	updates := make(chan authz.Policy, 1)
	_, err := WatchPolicyFromEndpoint(t.Context(), EndpointConfig{Address: addr}, func(p authz.Policy, _ error) {
		updates <- p
	})
	if err != nil {
		t.Fatalf("WatchPolicyFromEndpoint: %v", err)
	}

	select {
	case p := <-updates:
		if len(p.Roles) != 1 || string(p.Roles[0].ID) != "viewer" {
			t.Errorf("unexpected reconnect policy: %+v", p)
		}
	case <-t.Context().Done():
		t.Error("timed out waiting for reconnect policy")
	}
}

func TestToAuthzPolicy_Empty(t *testing.T) {
	// Empty proto — should return an empty policy without panicking.
	p := toAuthzPolicy(&policymaterializerv1.DataplanePolicy{})
	if len(p.Roles) != 0 || len(p.Bindings) != 0 {
		t.Errorf("expected empty policy, got %+v", p)
	}

	// Role with no permissions.
	p = toAuthzPolicy(&policymaterializerv1.DataplanePolicy{
		Roles: []*policymaterializerv1.DataplaneRole{
			{Id: "admin"},
		},
	})
	if len(p.Roles) != 1 || len(p.Roles[0].Permissions) != 0 {
		t.Errorf("unexpected policy: %+v", p)
	}
}

func TestWatchPolicyFromEndpoint_ContextCancelledDuringInit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Server that never sends anything; cancel the context after it connects.
	addr := startTestServer(t, &perConnServer{handlers: []func(context.Context, *connect.ServerStream[policymaterializerv1.WatchPolicyResponse]) error{
		func(_ context.Context, _ *connect.ServerStream[policymaterializerv1.WatchPolicyResponse]) error {
			cancel()
			// Block until our own ctx is cancelled so the stream stays open.
			<-ctx.Done()
			return nil
		},
	}})

	_, err := WatchPolicyFromEndpoint(ctx, EndpointConfig{Address: addr}, func(authz.Policy, error) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %T: %v", err, err)
	}
}

func TestWatchPolicyFromEndpoint_Cancel(t *testing.T) {
	policies := make(chan *policymaterializerv1.DataplanePolicy, 1)
	policies <- makePolicy("admin", "User:alice")

	addr := startTestServer(t, &fakeServer{policies: policies})

	ctx, cancel := context.WithCancel(t.Context())
	_, err := WatchPolicyFromEndpoint(ctx, EndpointConfig{Address: addr}, func(authz.Policy, error) {})
	if err != nil {
		t.Fatalf("WatchPolicyFromEndpoint: %v", err)
	}
	cancel()
}
