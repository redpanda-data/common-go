// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package authz

import (
	"context"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func noopHandler(_ context.Context, _ any) (any, error) { return "ok", nil }

func info(method string) *grpc.UnaryServerInfo { return &grpc.UnaryServerInfo{FullMethod: method} }

type principalKey struct{}

func testExtractor(ctx context.Context) (PrincipalID, bool) {
	val, ok := ctx.Value(principalKey{}).(PrincipalID)
	return val, ok
}

func ctxWith(email string) context.Context {
	return context.WithValue(context.Background(), principalKey{}, UserPrincipal(email))
}

const testResource ResourceName = "organizations/org1/resourcegroups/rg1/dataplanes/dp1"

func newTestInterceptor(t *testing.T, policy Policy, methods map[string]PermissionName) *Interceptor {
	t.Helper()
	i, err := NewInterceptor(InterceptorConfig{
		Logger:            mustLogger(t),
		ResourceName:      testResource,
		MethodPermissions: methods,
		ExtractPrincipal:  testExtractor,
		Policy:            policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func mustLogger(t *testing.T) *zap.Logger {
	t.Helper()
	l, err := zap.NewDevelopment()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestInterceptor_UnknownMethodDenied(t *testing.T) {
	i := newTestInterceptor(t,
		Policy{Roles: []Role{{ID: "r", Permissions: []PermissionName{"p"}}}},
		map[string]PermissionName{"/svc/Known": "p"},
	)
	h := i.UnaryServerInterceptor()
	_, err := h(ctxWith("u@x.com"), nil, info("/svc/Unknown"), noopHandler)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func TestInterceptor_HealthAndReflectionBypass(t *testing.T) {
	i := newTestInterceptor(t, Policy{}, map[string]PermissionName{})
	h := i.UnaryServerInterceptor()
	for _, m := range []string{
		"/grpc.health.v1.Health/Check",
		"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo",
	} {
		resp, err := h(context.Background(), nil, info(m), noopHandler)
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		if resp != "ok" {
			t.Fatalf("%s: got %v", m, resp)
		}
	}
}

func TestInterceptor_NoIdentity(t *testing.T) {
	i := newTestInterceptor(t,
		Policy{Roles: []Role{{ID: "r", Permissions: []PermissionName{"p"}}}},
		map[string]PermissionName{"/svc/M": "p"},
	)
	h := i.UnaryServerInterceptor()
	_, err := h(context.Background(), nil, info("/svc/M"), noopHandler)
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal, got %v", err)
	}
}

func TestInterceptor_Granted(t *testing.T) {
	i := newTestInterceptor(t,
		Policy{
			Roles:    []Role{{ID: "r", Permissions: []PermissionName{"p"}}},
			Bindings: []RoleBinding{{Role: "r", Principal: UserPrincipal("a@x.com"), Scope: testResource}},
		},
		map[string]PermissionName{"/svc/R": "p"},
	)
	h := i.UnaryServerInterceptor()
	resp, err := h(ctxWith("a@x.com"), nil, info("/svc/R"), noopHandler)
	if err != nil {
		t.Fatal(err)
	}
	if resp != "ok" {
		t.Fatalf("got %v", resp)
	}
}

func TestInterceptor_Denied(t *testing.T) {
	i := newTestInterceptor(t,
		Policy{
			Roles:    []Role{{ID: "r", Permissions: []PermissionName{"read"}}},
			Bindings: []RoleBinding{{Role: "r", Principal: UserPrincipal("a@x.com"), Scope: testResource}},
		},
		map[string]PermissionName{"/svc/W": "write"},
	)
	h := i.UnaryServerInterceptor()
	_, err := h(ctxWith("a@x.com"), nil, info("/svc/W"), noopHandler)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func TestInterceptor_SwapPolicy(t *testing.T) {
	i := newTestInterceptor(t,
		Policy{Roles: []Role{{ID: "r", Permissions: []PermissionName{"p"}}}},
		map[string]PermissionName{"/svc/R": "p"},
	)
	h := i.UnaryServerInterceptor()
	ctx := ctxWith("bob@x.com")

	// Bob has no binding
	if _, err := h(ctx, nil, info("/svc/R"), noopHandler); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}

	// Swap policy to grant bob
	if err := i.SwapPolicy(Policy{
		Roles:    []Role{{ID: "r", Permissions: []PermissionName{"p"}}},
		Bindings: []RoleBinding{{Role: "r", Principal: UserPrincipal("bob@x.com"), Scope: testResource}},
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := h(ctx, nil, info("/svc/R"), noopHandler)
	if err != nil {
		t.Fatal(err)
	}
	if resp != "ok" {
		t.Fatalf("got %v", resp)
	}
}
