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
	"net"
	"net/http"
	"testing"

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

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h2c.NewHandler(mux, &http2.Server{})}
	go srv.Serve(lis) //nolint:errcheck // test server, error irrelevant after t.Cleanup closes it
	t.Cleanup(func() { srv.Close() })
	return "http://" + lis.Addr().String()
}

func makePolicy(roleID, principal, scope string) *policymaterializerv1.DataplanePolicy {
	return &policymaterializerv1.DataplanePolicy{
		Roles: []*policymaterializerv1.DataplaneRole{
			{Id: roleID, Permissions: []string{"read"}},
		},
		Bindings: []*policymaterializerv1.DataplaneRoleBinding{
			{RoleId: roleID, Principal: principal, Scope: scope},
		},
	}
}

func TestWatchPolicyFromEndpoint_InitialPolicy(t *testing.T) {
	policies := make(chan *policymaterializerv1.DataplanePolicy, 1)
	policies <- makePolicy("admin", "User:alice", "organizations/acme")

	addr := startTestServer(t, &fakeServer{policies: policies})

	got, unwatch, err := WatchPolicyFromEndpoint(EndpointConfig{Address: addr}, func(authz.Policy, error) {})
	if err != nil {
		t.Fatalf("WatchPolicyFromEndpoint: %v", err)
	}
	defer unwatch() //nolint:errcheck // unwatch always returns nil

	if len(got.Roles) != 1 || string(got.Roles[0].ID) != "admin" {
		t.Errorf("unexpected initial policy: %+v", got)
	}
	if len(got.Bindings) != 1 || string(got.Bindings[0].Principal) != "User:alice" {
		t.Errorf("unexpected initial bindings: %+v", got.Bindings)
	}
}

func TestWatchPolicyFromEndpoint_Updates(t *testing.T) {
	policies := make(chan *policymaterializerv1.DataplanePolicy, 2)
	policies <- makePolicy("admin", "User:alice", "organizations/acme")
	policies <- makePolicy("viewer", "User:bob", "organizations/acme")

	addr := startTestServer(t, &fakeServer{policies: policies})

	updates := make(chan authz.Policy, 1)
	_, unwatch, err := WatchPolicyFromEndpoint(EndpointConfig{Address: addr}, func(p authz.Policy, err error) {
		if err == nil {
			updates <- p
		}
	})
	if err != nil {
		t.Fatalf("WatchPolicyFromEndpoint: %v", err)
	}
	defer unwatch() //nolint:errcheck // unwatch always returns nil

	select {
	case p := <-updates:
		if len(p.Roles) != 1 || string(p.Roles[0].ID) != "viewer" {
			t.Errorf("unexpected update policy: %+v", p)
		}
	case <-t.Context().Done():
		t.Error("timed out waiting for policy update")
	}
}

func TestWatchPolicyFromEndpoint_Unwatch(t *testing.T) {
	policies := make(chan *policymaterializerv1.DataplanePolicy, 1)
	policies <- makePolicy("admin", "User:alice", "organizations/acme")

	addr := startTestServer(t, &fakeServer{policies: policies})

	_, unwatch, err := WatchPolicyFromEndpoint(EndpointConfig{Address: addr}, func(authz.Policy, error) {})
	if err != nil {
		t.Fatalf("WatchPolicyFromEndpoint: %v", err)
	}
	if err := unwatch(); err != nil {
		t.Errorf("unwatch returned error: %v", err)
	}
}
