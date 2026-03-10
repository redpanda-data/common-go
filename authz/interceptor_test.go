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

func srvInfo(method string) *grpc.UnaryServerInfo { return &grpc.UnaryServerInfo{FullMethod: method} }

type principalKey struct{}

func testExtractor(ctx context.Context) (PrincipalID, bool) {
	val, ok := ctx.Value(principalKey{}).(PrincipalID)
	return val, ok
}

func ctxWith(email string) context.Context {
	return context.WithValue(context.Background(), principalKey{}, UserPrincipal(email))
}

const testResource ResourceName = "organizations/org1/resourcegroups/rg1/dataplanes/dp1"

func newTestInterceptor(t *testing.T, policy Policy) *Interceptor {
	t.Helper()
	l, _ := zap.NewDevelopment()
	i, err := NewInterceptor(InterceptorConfig{
		Logger:           l,
		ResourceName:     testResource,
		ExtractPrincipal: testExtractor,
		Policy:           policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestInterceptor_UnannotatedMethodDenied(t *testing.T) {
	// No protos with annotations are registered in this test binary,
	// so any method lookup returns noPermission -> denied.
	i := newTestInterceptor(t, Policy{})
	h := i.UnaryServerInterceptor()
	_, err := h(ctxWith("u@x.com"), nil, srvInfo("/some.Service/SomeMethod"), noopHandler)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func TestInterceptor_HealthAndReflectionBypass(t *testing.T) {
	i := newTestInterceptor(t, Policy{})
	h := i.UnaryServerInterceptor()
	for _, m := range []string{
		"/grpc.health.v1.Health/Check",
		"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo",
	} {
		resp, err := h(context.Background(), nil, srvInfo(m), noopHandler)
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		if resp != "ok" {
			t.Fatalf("%s: got %v", m, resp)
		}
	}
}

func TestInterceptor_ResolvePermission(t *testing.T) {
	// Direct test of the resolver with a method that doesn't exist in registry.
	perm := resolvePermission("/no.such.Service/Method")
	if perm != noPermission {
		t.Fatalf("expected noPermission, got %q", perm)
	}
}

func TestInterceptor_CachesLookup(t *testing.T) {
	i := newTestInterceptor(t, Policy{})

	// First lookup populates cache
	p1 := i.lookupPermission("/test.Svc/M")
	// Second lookup hits cache
	p2 := i.lookupPermission("/test.Svc/M")

	if p1 != p2 {
		t.Fatalf("cache inconsistency: %q vs %q", p1, p2)
	}
}

func TestInterceptor_SwapPolicy(t *testing.T) {
	// SwapPolicy should not error on valid policies.
	i := newTestInterceptor(t, Policy{
		Roles: []Role{{ID: "r", Permissions: []PermissionName{"p"}}},
	})

	err := i.SwapPolicy(Policy{
		Roles:    []Role{{ID: "r", Permissions: []PermissionName{"p"}}},
		Bindings: []RoleBinding{{Role: "r", Principal: UserPrincipal("bob@x.com"), Scope: testResource}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// newTestInterceptorWithPerms creates an interceptor with pre-seeded permissions
// (since no annotated protos are registered in this test binary).
func newTestInterceptorWithPerms(t *testing.T, policy Policy, perms []PermissionName, cache map[string]PermissionName) *Interceptor {
	t.Helper()
	l, _ := zap.NewDevelopment()

	rp, err := NewResourcePolicy(policy, testResource, perms)
	if err != nil {
		t.Fatal(err)
	}

	i := &Interceptor{
		logger:           l,
		allPerms:         perms,
		resourceName:     testResource,
		extractPrincipal: testExtractor,
		resourcePolicy:   rp,
	}
	for method, perm := range cache {
		i.permCache.Store(method, perm)
	}
	return i
}

func TestInterceptor_NoIdentity(t *testing.T) {
	i := newTestInterceptorWithPerms(t,
		Policy{Roles: []Role{{ID: "r", Permissions: []PermissionName{"test_perm"}}}},
		[]PermissionName{"test_perm"},
		map[string]PermissionName{"/test.Svc/M": "test_perm"},
	)
	h := i.UnaryServerInterceptor()
	_, err := h(context.Background(), nil, srvInfo("/test.Svc/M"), noopHandler)
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal, got %v", err)
	}
}

func TestInterceptor_GrantedViaCachedPerm(t *testing.T) {
	i := newTestInterceptorWithPerms(t,
		Policy{
			Roles:    []Role{{ID: "r", Permissions: []PermissionName{"test_perm"}}},
			Bindings: []RoleBinding{{Role: "r", Principal: UserPrincipal("a@x.com"), Scope: testResource}},
		},
		[]PermissionName{"test_perm"},
		map[string]PermissionName{"/test.Svc/R": "test_perm"},
	)
	h := i.UnaryServerInterceptor()
	resp, err := h(ctxWith("a@x.com"), nil, srvInfo("/test.Svc/R"), noopHandler)
	if err != nil {
		t.Fatal(err)
	}
	if resp != "ok" {
		t.Fatalf("got %v", resp)
	}
}

func TestInterceptor_DeniedViaCachedPerm(t *testing.T) {
	i := newTestInterceptorWithPerms(t,
		Policy{
			Roles:    []Role{{ID: "r", Permissions: []PermissionName{"read"}}},
			Bindings: []RoleBinding{{Role: "r", Principal: UserPrincipal("a@x.com"), Scope: testResource}},
		},
		[]PermissionName{"read", "write"},
		map[string]PermissionName{"/test.Svc/W": "write"},
	)
	h := i.UnaryServerInterceptor()
	_, err := h(ctxWith("a@x.com"), nil, srvInfo("/test.Svc/W"), noopHandler)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}
