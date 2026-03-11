// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package grpcauthz_test

import (
	"context"
	"net"
	"sync"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/redpanda-data/common-go/authz"
	"github.com/redpanda-data/common-go/authz/authzcore"
	"github.com/redpanda-data/common-go/authz/grpcauthz"
	testv1 "github.com/redpanda-data/common-go/authz/testdata/gen"
)

func noopHandler(_ context.Context, _ any) (any, error) { return "ok", nil }

func srvInfo(method string) *grpc.UnaryServerInfo { return &grpc.UnaryServerInfo{FullMethod: method} }

type testServer struct {
	testv1.UnimplementedTestServiceServer
}

func (*testServer) SimpleMethod(context.Context, *testv1.SimpleRequest) (*testv1.SimpleResponse, error) {
	return &testv1.SimpleResponse{}, nil
}

func (*testServer) GetWidget(context.Context, *testv1.GetWidgetRequest) (*testv1.GetWidgetResponse, error) {
	return &testv1.GetWidgetResponse{}, nil
}

func (*testServer) CreateWidget(context.Context, *testv1.CreateWidgetRequest) (*testv1.CreateWidgetResponse, error) {
	return &testv1.CreateWidgetResponse{}, nil
}

func (*testServer) UpdateWidget(context.Context, *testv1.UpdateWidgetRequest) (*testv1.UpdateWidgetResponse, error) {
	return &testv1.UpdateWidgetResponse{}, nil
}

func (*testServer) ListWidgets(context.Context, *testv1.ListWidgetsRequest) (*testv1.ListWidgetsResponse, error) {
	return &testv1.ListWidgetsResponse{
		Widgets: []*testv1.Widget{
			{Id: "widget-1", Name: "one"},
			{Id: "widget-2", Name: "two"},
			{Id: "widget-3", Name: "three"},
		},
	}, nil
}

func (*testServer) SkippedMethod(context.Context, *testv1.SimpleRequest) (*testv1.SimpleResponse, error) {
	return &testv1.SimpleResponse{}, nil
}

func (*testServer) ListWidgetsWithPreCheck(context.Context, *testv1.ListWidgetsRequest) (*testv1.ListWidgetsResponse, error) {
	return &testv1.ListWidgetsResponse{
		Widgets: []*testv1.Widget{
			{Id: "widget-1", Name: "one"},
			{Id: "widget-2", Name: "two"},
			{Id: "widget-3", Name: "three"},
		},
	}, nil
}

func (*testServer) UnannotatedMethod(context.Context, *testv1.SimpleRequest) (*testv1.SimpleResponse, error) {
	return &testv1.SimpleResponse{}, nil
}

const testPrincipalMDKey = "x-test-principal"

func testExtractor(ctx context.Context) (authz.PrincipalID, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false
	}
	vals := md.Get(testPrincipalMDKey)
	if len(vals) == 0 {
		return "", false
	}
	return authz.UserPrincipal(vals[0]), true
}

const testDataplane authz.ResourceName = "organizations/a845616f-0484-4506-9638-45fe28f34865/resourcegroups/a098bb32-55a0-4783-9eef-873826987d58/dataplanes/d5tp5kntujt599ksadgg"

// Mirrors a real dataplane authorization ConfigMap with Admin/Writer/Reader roles.
var realisticPolicy = authz.Policy{
	Roles: []authz.Role{
		{
			ID: "Admin",
			Permissions: []authz.PermissionName{
				"test_simple_perm",
				"test_scoped_perm",
				"test_create_perm",
				"test_list_perm",
			},
		},
		{
			ID: "Writer",
			Permissions: []authz.PermissionName{
				"test_simple_perm",
				"test_scoped_perm",
				"test_create_perm",
				"test_list_perm",
			},
		},
		{
			ID: "Reader",
			Permissions: []authz.PermissionName{
				"test_simple_perm",
				"test_list_perm",
			},
		},
	},
	Bindings: []authz.RoleBinding{
		{Role: "Admin", Principal: authz.UserPrincipal("stephan@redpanda.com"), Scope: testDataplane},
		{Role: "Writer", Principal: authz.UserPrincipal("tyler@redpanda.com"), Scope: testDataplane},
		{Role: "Reader", Principal: authz.UserPrincipal("intern@redpanda.com"), Scope: testDataplane},
	},
}

func newTestInterceptor(t testing.TB, policy authz.Policy) *grpcauthz.Interceptor {
	t.Helper()
	interceptor, err := grpcauthz.New(
		testDataplane,
		testExtractor,
		authzcore.StaticPolicy(policy),
	)
	if err != nil {
		t.Fatal(err)
	}
	return interceptor
}

func startTestServer(t *testing.T, policy authz.Policy) testv1.TestServiceClient {
	t.Helper()
	client, _ := startTestServerWithInterceptor(t, policy)
	return client
}

func startTestServerWithInterceptor(t *testing.T, policy authz.Policy) (testv1.TestServiceClient, *grpcauthz.Interceptor) {
	t.Helper()
	interceptor := newTestInterceptor(t, policy)

	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(interceptor.Unary()))
	testv1.RegisterTestServiceServer(srv, &testServer{})
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.GracefulStop)

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return testv1.NewTestServiceClient(conn), interceptor
}

// ctxAs creates a context with outgoing metadata (for gRPC client calls).
func ctxAs(email string) context.Context {
	return metadata.NewOutgoingContext(context.Background(), metadata.Pairs(testPrincipalMDKey, email))
}

// ctxAsIncoming creates a context with incoming metadata (for direct interceptor calls in benchmarks).
func ctxAsIncoming(email string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(testPrincipalMDKey, email))
}

// --- Proto annotation discovery ---

func TestDiscoverPermissions(t *testing.T) {
	// Create an interceptor to trigger proto registration, then verify
	// that the core module can discover permissions from annotations.
	interceptor := newTestInterceptor(t, realisticPolicy)

	// Verify annotations resolve for known methods.
	for _, method := range []string{
		"/authz.test.v1.TestService/SimpleMethod",
		"/authz.test.v1.TestService/GetWidget",
		"/authz.test.v1.TestService/CreateWidget",
		"/authz.test.v1.TestService/ListWidgets",
	} {
		ma := interceptor.LookupMethodAuthz(method)
		if ma == nil {
			t.Errorf("expected annotation for %s, got nil", method)
		}
	}
}

func TestResolveAnnotations(t *testing.T) {
	interceptor := newTestInterceptor(t, realisticPolicy)

	tests := []struct {
		method   string
		wantPerm string
		wantType string
		wantCEL  string
		wantNil  bool
	}{
		{"/authz.test.v1.TestService/SimpleMethod", "test_simple_perm", "", "", false},
		{"/authz.test.v1.TestService/GetWidget", "test_scoped_perm", "widgets", "request.id", false},
		{"/authz.test.v1.TestService/CreateWidget", "test_create_perm", "widgets", "", false},
		{"/authz.test.v1.TestService/UpdateWidget", "test_scoped_perm", "widgets", "request.widget.id", false},
		{"/authz.test.v1.TestService/ListWidgets", "test_list_perm", "widgets", "each.id", false},
		{"/authz.test.v1.TestService/UnannotatedMethod", "", "", "", true},
		{"/no.such.Service/Method", "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			ma := interceptor.LookupMethodAuthz(tt.method)
			if tt.wantNil {
				if ma != nil {
					t.Fatalf("expected nil, got %+v", ma)
				}
				return
			}
			if ma == nil || ma.Auth == nil {
				t.Fatal("expected non-nil")
			}
			if ma.Auth.GetPermission() != tt.wantPerm {
				t.Errorf("permission: got %s, want %s", ma.Auth.GetPermission(), tt.wantPerm)
			}
			if ma.Auth.GetResourceType() != tt.wantType {
				t.Errorf("resourceType: got %s, want %s", ma.Auth.GetResourceType(), tt.wantType)
			}
			if ma.Auth.GetIdGetterCel() != tt.wantCEL {
				t.Errorf("idGetterCEL: got %s, want %s", ma.Auth.GetIdGetterCel(), tt.wantCEL)
			}
		})
	}
}

// --- Fail-closed and bypass ---

func TestGRPC_UnannotatedMethodDenied(t *testing.T) {
	client := startTestServer(t, realisticPolicy)
	// Even admin is denied on unannotated methods — FailedPrecondition
	// indicates a server-side configuration error, not a caller auth failure.
	_, err := client.UnannotatedMethod(ctxAs("stephan@redpanda.com"), &testv1.SimpleRequest{})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v", err)
	}
}

func TestGRPC_NoIdentityReturnsInternal(t *testing.T) {
	client := startTestServer(t, realisticPolicy)
	_, err := client.SimpleMethod(context.Background(), &testv1.SimpleRequest{})
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal, got %v", err)
	}
}

func TestGRPC_UnboundUserDenied(t *testing.T) {
	client := startTestServer(t, realisticPolicy)
	_, err := client.SimpleMethod(ctxAs("random@attacker.com"), &testv1.SimpleRequest{})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

// --- Role-based access at dataplane level ---

func TestGRPC_AdminGrantedSimple(t *testing.T) {
	client := startTestServer(t, realisticPolicy)
	_, err := client.SimpleMethod(ctxAs("stephan@redpanda.com"), &testv1.SimpleRequest{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGRPC_ReaderGrantedSimple(t *testing.T) {
	client := startTestServer(t, realisticPolicy)
	_, err := client.SimpleMethod(ctxAs("intern@redpanda.com"), &testv1.SimpleRequest{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGRPC_ReaderDeniedGetWidget(t *testing.T) {
	client := startTestServer(t, realisticPolicy)
	// Reader doesn't have test_scoped_perm.
	_, err := client.GetWidget(ctxAs("intern@redpanda.com"), &testv1.GetWidgetRequest{Id: "w1"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func TestGRPC_ReaderDeniedCreateWidget(t *testing.T) {
	client := startTestServer(t, realisticPolicy)
	// Reader doesn't have test_create_perm.
	_, err := client.CreateWidget(ctxAs("intern@redpanda.com"), &testv1.CreateWidgetRequest{Name: "new"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

// --- Collection authorization (List RPCs) ---

func TestGRPC_ListWidgets_DataplaneScopeReturnsAll(t *testing.T) {
	// Admin bound at dataplane level sees all widgets.
	client := startTestServer(t, realisticPolicy)
	resp, err := client.ListWidgets(ctxAs("stephan@redpanda.com"), &testv1.ListWidgetsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Widgets) != 3 {
		t.Fatalf("expected 3 widgets, got %d", len(resp.Widgets))
	}
}

func TestGRPC_ListWidgets_PerResourceReturnsSubset(t *testing.T) {
	// Alice has permission only on widget-1 and widget-3.
	policy := authz.Policy{
		Roles: []authz.Role{{ID: "Lister", Permissions: []authz.PermissionName{"test_list_perm"}}},
		Bindings: []authz.RoleBinding{
			{Role: "Lister", Principal: authz.UserPrincipal("alice@redpanda.com"), Scope: testDataplane + "/widgets/widget-1"},
			{Role: "Lister", Principal: authz.UserPrincipal("alice@redpanda.com"), Scope: testDataplane + "/widgets/widget-3"},
		},
	}
	client := startTestServer(t, policy)
	resp, err := client.ListWidgets(ctxAs("alice@redpanda.com"), &testv1.ListWidgetsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Widgets) != 2 {
		t.Fatalf("expected 2 widgets, got %d", len(resp.Widgets))
	}
	ids := map[string]bool{}
	for _, w := range resp.Widgets {
		ids[w.Id] = true
	}
	if !ids["widget-1"] || !ids["widget-3"] {
		t.Fatalf("expected widget-1 and widget-3, got %v", ids)
	}
}

func TestGRPC_ListWidgets_NoPermissionReturnsEmpty(t *testing.T) {
	// Bob has no bindings at all — gets empty list, not an error.
	policy := authz.Policy{
		Roles: []authz.Role{{ID: "Lister", Permissions: []authz.PermissionName{"test_list_perm"}}},
	}
	client := startTestServer(t, policy)
	resp, err := client.ListWidgets(ctxAs("bob@redpanda.com"), &testv1.ListWidgetsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Widgets) != 0 {
		t.Fatalf("expected 0 widgets, got %d", len(resp.Widgets))
	}
}

func TestGRPC_ListWidgets_WildcardReturnsAll(t *testing.T) {
	// Wildcard binding on widgets/* sees everything.
	policy := authz.Policy{
		Roles: []authz.Role{{ID: "Lister", Permissions: []authz.PermissionName{"test_list_perm"}}},
		Bindings: []authz.RoleBinding{
			{Role: "Lister", Principal: authz.UserPrincipal("carol@redpanda.com"), Scope: testDataplane + "/widgets/*"},
		},
	}
	client := startTestServer(t, policy)
	resp, err := client.ListWidgets(ctxAs("carol@redpanda.com"), &testv1.ListWidgetsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Widgets) != 3 {
		t.Fatalf("expected 3 widgets, got %d", len(resp.Widgets))
	}
}

func TestGRPC_ListWidgets_NoIdentityDenied(t *testing.T) {
	client := startTestServer(t, realisticPolicy)
	_, err := client.ListWidgets(context.Background(), &testv1.ListWidgetsRequest{})
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal, got %v", err)
	}
}

// --- Skip authorization ---

func TestGRPC_SkippedMethodAllowsAnyone(t *testing.T) {
	client := startTestServer(t, realisticPolicy)
	// No identity at all — still allowed because skip: true.
	_, err := client.SkippedMethod(context.Background(), &testv1.SimpleRequest{})
	if err != nil {
		t.Fatalf("skipped method should allow unauthenticated: %v", err)
	}
}

func TestGRPC_SkippedMethodResolves(t *testing.T) {
	interceptor := newTestInterceptor(t, realisticPolicy)
	ma := interceptor.LookupMethodAuthz("/authz.test.v1.TestService/SkippedMethod")
	if ma == nil {
		t.Fatal("expected non-nil MethodAuthz for skipped method")
	}
	if !ma.Skip {
		t.Fatal("expected Skip to be true")
	}
}

// --- Collection with pre-check (both method_authorization + collection_authorization) ---

func TestGRPC_ListWithPreCheck_AdminSeesAll(t *testing.T) {
	// Admin has test_list_perm at dataplane level — passes pre-check
	// and all items pass per-item filter.
	client := startTestServer(t, realisticPolicy)
	resp, err := client.ListWidgetsWithPreCheck(ctxAs("stephan@redpanda.com"), &testv1.ListWidgetsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Widgets) != 3 {
		t.Fatalf("expected 3 widgets, got %d", len(resp.Widgets))
	}
}

func TestGRPC_ListWithPreCheck_NoPerm_Denied(t *testing.T) {
	// User with no bindings — pre-check fails with PermissionDenied,
	// never reaches the handler.
	policy := authz.Policy{
		Roles: []authz.Role{{ID: "Lister", Permissions: []authz.PermissionName{"test_list_perm"}}},
	}
	client := startTestServer(t, policy)
	_, err := client.ListWidgetsWithPreCheck(ctxAs("nobody@redpanda.com"), &testv1.ListWidgetsRequest{})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied from pre-check, got %v", err)
	}
}

func TestGRPC_ListWithPreCheck_PerResource_FiltersAfterPreCheck(t *testing.T) {
	// User has test_list_perm at dataplane level (passes pre-check)
	// but per-item bindings only on widget-1 and widget-3.
	policy := authz.Policy{
		Roles: []authz.Role{{ID: "Lister", Permissions: []authz.PermissionName{"test_list_perm"}}},
		Bindings: []authz.RoleBinding{
			// Dataplane-level binding — passes the method_authorization pre-check.
			{Role: "Lister", Principal: authz.UserPrincipal("alice@redpanda.com"), Scope: testDataplane},
		},
	}
	client := startTestServer(t, policy)
	resp, err := client.ListWidgetsWithPreCheck(ctxAs("alice@redpanda.com"), &testv1.ListWidgetsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	// Dataplane-level binding cascades to all sub-resources, so all 3 pass the per-item filter too.
	if len(resp.Widgets) != 3 {
		t.Fatalf("expected 3 widgets (dataplane binding cascades), got %d", len(resp.Widgets))
	}
}

func TestGRPC_ListWithPreCheck_ResolvesBothAnnotations(t *testing.T) {
	interceptor := newTestInterceptor(t, realisticPolicy)
	ma := interceptor.LookupMethodAuthz("/authz.test.v1.TestService/ListWidgetsWithPreCheck")
	if ma == nil {
		t.Fatal("expected non-nil MethodAuthz")
	}
	if ma.Auth == nil {
		t.Fatal("expected Auth (from method_authorization)")
	}
	if ma.Collection == nil {
		t.Fatal("expected Collection (from collection_authorization)")
	}
	// Auth should NOT point to Collection.Each — they're from different annotations.
	if ma.Auth == ma.Collection.GetEach() {
		t.Fatal("Auth should be from method_authorization, not Collection.Each")
	}
	if ma.Auth.GetPermission() != "test_list_perm" {
		t.Errorf("method_authorization permission: got %s, want test_list_perm", ma.Auth.GetPermission())
	}
	if ma.Collection.GetEach().GetPermission() != "test_list_perm" {
		t.Errorf("collection_authorization permission: got %s, want test_list_perm", ma.Collection.GetEach().GetPermission())
	}
}

// --- Collection only (no pre-check): existing ListWidgets behavior ---

func TestGRPC_ListWidgets_CollectionOnly_PerResourceAllowed(t *testing.T) {
	// collection_authorization only — no pre-check. User has per-resource
	// binding only, not at dataplane level. Should still work (handler runs,
	// then items are filtered).
	policy := authz.Policy{
		Roles: []authz.Role{{ID: "Lister", Permissions: []authz.PermissionName{"test_list_perm"}}},
		Bindings: []authz.RoleBinding{
			{Role: "Lister", Principal: authz.UserPrincipal("alice@redpanda.com"), Scope: testDataplane + "/widgets/widget-2"},
		},
	}
	client := startTestServer(t, policy)
	resp, err := client.ListWidgets(ctxAs("alice@redpanda.com"), &testv1.ListWidgetsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Widgets) != 1 || resp.Widgets[0].Id != "widget-2" {
		t.Fatalf("expected [widget-2], got %v", resp.Widgets)
	}
}

// --- Sub-resource scoping: dataplane binding cascades to children ---

func TestGRPC_DataplaneBindingGrantsGetWidget(t *testing.T) {
	// Admin bound at dataplane level can get any widget.
	client := startTestServer(t, realisticPolicy)
	_, err := client.GetWidget(ctxAs("stephan@redpanda.com"), &testv1.GetWidgetRequest{Id: "any-widget"})
	if err != nil {
		t.Fatalf("dataplane admin should access any widget: %v", err)
	}
}

func TestGRPC_DataplaneBindingGrantsUpdateWidget(t *testing.T) {
	// Writer bound at dataplane level can update any widget (nested field path).
	client := startTestServer(t, realisticPolicy)
	_, err := client.UpdateWidget(ctxAs("tyler@redpanda.com"), &testv1.UpdateWidgetRequest{
		Widget: &testv1.Widget{Id: "widget-abc", Name: "updated"},
	})
	if err != nil {
		t.Fatalf("dataplane writer should update any widget: %v", err)
	}
}

func TestGRPC_DataplaneBindingGrantsCreateWidget(t *testing.T) {
	// Create has no id_getter_cel — checks at base resource (dataplane) level.
	client := startTestServer(t, realisticPolicy)
	_, err := client.CreateWidget(ctxAs("tyler@redpanda.com"), &testv1.CreateWidgetRequest{Name: "new-widget"})
	if err != nil {
		t.Fatalf("dataplane writer should create: %v", err)
	}
}

// --- Sub-resource scoping: per-resource binding ---

func TestGRPC_PerResourceBindingGrantsSpecificWidget(t *testing.T) {
	// Binding scoped to widgets/widget-123 — grants only that widget.
	policy := authz.Policy{
		Roles: []authz.Role{{ID: "WidgetEditor", Permissions: []authz.PermissionName{"test_scoped_perm"}}},
		Bindings: []authz.RoleBinding{{
			Role:      "WidgetEditor",
			Principal: authz.UserPrincipal("alice@redpanda.com"),
			Scope:     testDataplane + "/widgets/widget-123",
		}},
	}
	client := startTestServer(t, policy)

	_, err := client.GetWidget(ctxAs("alice@redpanda.com"), &testv1.GetWidgetRequest{Id: "widget-123"})
	if err != nil {
		t.Fatalf("should grant widget-123: %v", err)
	}
}

func TestGRPC_PerResourceBindingDeniesOtherWidget(t *testing.T) {
	// Binding scoped to widgets/widget-123 — must NOT grant widget-456.
	policy := authz.Policy{
		Roles: []authz.Role{{ID: "WidgetEditor", Permissions: []authz.PermissionName{"test_scoped_perm"}}},
		Bindings: []authz.RoleBinding{{
			Role:      "WidgetEditor",
			Principal: authz.UserPrincipal("alice@redpanda.com"),
			Scope:     testDataplane + "/widgets/widget-123",
		}},
	}
	client := startTestServer(t, policy)

	_, err := client.GetWidget(ctxAs("alice@redpanda.com"), &testv1.GetWidgetRequest{Id: "widget-456"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for wrong widget, got %v", err)
	}
}

func TestGRPC_PerResourceBindingNestedFieldPath(t *testing.T) {
	// UpdateWidget uses request.widget.id — test that nested extraction works.
	policy := authz.Policy{
		Roles: []authz.Role{{ID: "WidgetEditor", Permissions: []authz.PermissionName{"test_scoped_perm"}}},
		Bindings: []authz.RoleBinding{{
			Role:      "WidgetEditor",
			Principal: authz.UserPrincipal("alice@redpanda.com"),
			Scope:     testDataplane + "/widgets/widget-999",
		}},
	}
	client := startTestServer(t, policy)

	// Correct widget ID in nested field — granted.
	_, err := client.UpdateWidget(ctxAs("alice@redpanda.com"), &testv1.UpdateWidgetRequest{
		Widget: &testv1.Widget{Id: "widget-999", Name: "x"},
	})
	if err != nil {
		t.Fatalf("should grant update on widget-999: %v", err)
	}

	// Wrong widget ID in nested field — denied.
	_, err = client.UpdateWidget(ctxAs("alice@redpanda.com"), &testv1.UpdateWidgetRequest{
		Widget: &testv1.Widget{Id: "widget-000", Name: "x"},
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for wrong widget, got %v", err)
	}
}

// --- Wildcard bindings ---

func TestGRPC_WildcardBindingGrantsAllWidgets(t *testing.T) {
	// Binding with wildcard scope: widgets/* — grants all widgets.
	policy := authz.Policy{
		Roles: []authz.Role{{ID: "WidgetEditor", Permissions: []authz.PermissionName{"test_scoped_perm"}}},
		Bindings: []authz.RoleBinding{{
			Role:      "WidgetEditor",
			Principal: authz.UserPrincipal("bob@redpanda.com"),
			Scope:     testDataplane + "/widgets/*",
		}},
	}
	client := startTestServer(t, policy)

	_, err := client.GetWidget(ctxAs("bob@redpanda.com"), &testv1.GetWidgetRequest{Id: "any-widget"})
	if err != nil {
		t.Fatalf("wildcard binding should grant any widget: %v", err)
	}

	_, err = client.GetWidget(ctxAs("bob@redpanda.com"), &testv1.GetWidgetRequest{Id: "another-widget"})
	if err != nil {
		t.Fatalf("wildcard binding should grant another widget: %v", err)
	}
}

// --- Empty resource ID when id_getter_cel is set ---

func TestGRPC_EmptyResourceIDDenied(t *testing.T) {
	// GetWidget has id_getter_cel: "request.id". Sending empty ID must be
	// rejected with InvalidArgument, not silently checked at dataplane scope.
	client := startTestServer(t, realisticPolicy)
	_, err := client.GetWidget(ctxAs("stephan@redpanda.com"), &testv1.GetWidgetRequest{Id: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for empty resource ID, got %v", err)
	}
}

// --- Policy hot-reload ---

func TestGRPC_SwapPolicy(t *testing.T) {
	client, interceptor := startTestServerWithInterceptor(t, authz.Policy{
		Roles: []authz.Role{{ID: "r", Permissions: []authz.PermissionName{"test_simple_perm"}}},
	})

	// Bob has no binding — denied.
	_, err := client.SimpleMethod(ctxAs("bob@redpanda.com"), &testv1.SimpleRequest{})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied before swap, got %v", err)
	}

	// Hot-reload to grant bob.
	if err := interceptor.SwapPolicy(authz.Policy{
		Roles:    []authz.Role{{ID: "r", Permissions: []authz.PermissionName{"test_simple_perm"}}},
		Bindings: []authz.RoleBinding{{Role: "r", Principal: authz.UserPrincipal("bob@redpanda.com"), Scope: testDataplane}},
	}); err != nil {
		t.Fatal(err)
	}

	_, err = client.SimpleMethod(ctxAs("bob@redpanda.com"), &testv1.SimpleRequest{})
	if err != nil {
		t.Fatalf("bob should be granted after swap: %v", err)
	}
}

// --- Race: concurrent requests + policy swaps ---

func TestGRPC_ConcurrentRequestsAndSwaps(t *testing.T) {
	policyGranted := authz.Policy{
		Roles:    []authz.Role{{ID: "r", Permissions: []authz.PermissionName{"test_simple_perm", "test_scoped_perm", "test_create_perm"}}},
		Bindings: []authz.RoleBinding{{Role: "r", Principal: authz.UserPrincipal("racer@redpanda.com"), Scope: testDataplane}},
	}
	policyDenied := authz.Policy{
		Roles: []authz.Role{{ID: "r", Permissions: []authz.PermissionName{"test_simple_perm", "test_scoped_perm", "test_create_perm"}}},
		// No bindings — everyone denied.
	}

	client, interceptor := startTestServerWithInterceptor(t, policyGranted)

	const goroutines = 20
	const iterations = 100

	var wg sync.WaitGroup

	// Hammer requests from multiple goroutines.
	for i := range goroutines {
		wg.Go(func() {
			for range iterations {
				switch i % 3 {
				case 0:
					client.SimpleMethod(ctxAs("racer@redpanda.com"), &testv1.SimpleRequest{}) //nolint:errcheck // race test: errors expected
				case 1:
					client.GetWidget(ctxAs("racer@redpanda.com"), &testv1.GetWidgetRequest{Id: "w1"}) //nolint:errcheck // race test: errors expected
				default:
					client.UpdateWidget(ctxAs("racer@redpanda.com"), &testv1.UpdateWidgetRequest{Widget: &testv1.Widget{Id: "w2"}}) //nolint:errcheck // race test: errors expected
				}
			}
		})
	}

	// Simultaneously swap policies back and forth.
	wg.Go(func() {
		for range iterations {
			interceptor.SwapPolicy(policyDenied)  //nolint:errcheck // race test: intentional
			interceptor.SwapPolicy(policyGranted) //nolint:errcheck // race test: intentional
		}
	})

	wg.Wait()
	// If we get here without a race detector complaint, we're good.
}

// --- Error details ---

func TestGRPC_DenialErrorDetails(t *testing.T) {
	client := startTestServer(t, realisticPolicy)

	tests := []struct {
		name       string
		call       func() error
		wantCode   codes.Code
		wantReason string
		wantMeta   map[string]string
	}{
		{
			name: "forbidden",
			call: func() error {
				_, err := client.GetWidget(ctxAs("intern@redpanda.com"), &testv1.GetWidgetRequest{Id: "w1"})
				return err
			},
			wantCode:   codes.PermissionDenied,
			wantReason: "REASON_PERMISSION_DENIED",
			wantMeta: map[string]string{
				"method":        "/authz.test.v1.TestService/GetWidget",
				"permission":    "test_scoped_perm",
				"principal":     "User:intern@redpanda.com",
				"resource_type": "widgets",
				"resource_id":   string(testDataplane) + "/widgets/w1",
			},
		},
		{
			name: "empty_resource_id",
			call: func() error {
				_, err := client.GetWidget(ctxAs("stephan@redpanda.com"), &testv1.GetWidgetRequest{Id: ""})
				return err
			},
			wantCode:   codes.InvalidArgument,
			wantReason: "REASON_INVALID_INPUT",
			wantMeta: map[string]string{
				"method":        "/authz.test.v1.TestService/GetWidget",
				"permission":    "test_scoped_perm",
				"principal":     "User:stephan@redpanda.com",
				"resource_type": "widgets",
			},
		},
		{
			name: "unknown_method",
			call: func() error {
				_, err := client.UnannotatedMethod(ctxAs("stephan@redpanda.com"), &testv1.SimpleRequest{})
				return err
			},
			wantCode:   codes.FailedPrecondition,
			wantReason: "REASON_INVALID_INPUT",
			wantMeta: map[string]string{
				"method":    "/authz.test.v1.TestService/UnannotatedMethod",
				"principal": "User:stephan@redpanda.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("expected status error, got %v", err)
			}
			if st.Code() != tt.wantCode {
				t.Fatalf("expected code %v, got %v", tt.wantCode, st.Code())
			}

			details := st.Details()
			if len(details) == 0 {
				t.Fatal("expected error details, got none")
			}

			info, ok := details[0].(*errdetails.ErrorInfo)
			if !ok {
				t.Fatalf("expected ErrorInfo, got %T", details[0])
			}
			if info.Reason != tt.wantReason {
				t.Errorf("reason: got %s, want %s", info.Reason, tt.wantReason)
			}
			if info.Domain != "redpanda.com" {
				t.Errorf("domain: got %s, want redpanda.com", info.Domain)
			}
			if len(info.Metadata) != len(tt.wantMeta) {
				t.Errorf("metadata length: got %d, want %d\n  got:  %v\n  want: %v", len(info.Metadata), len(tt.wantMeta), info.Metadata, tt.wantMeta)
			}
			for k, want := range tt.wantMeta {
				if got := info.Metadata[k]; got != want {
					t.Errorf("metadata[%q]: got %q, want %q", k, got, want)
				}
			}
		})
	}
}

// --- Benchmarks ---

func BenchmarkInterceptor_SimplePermission(b *testing.B) {
	interceptor := newTestInterceptor(b, realisticPolicy)
	h := interceptor.Unary()
	ctx := ctxAsIncoming("stephan@redpanda.com")
	info := srvInfo("/authz.test.v1.TestService/SimpleMethod")

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		h(ctx, nil, info, noopHandler) //nolint:errcheck // benchmark/race test: result not needed
	}
}

func BenchmarkInterceptor_ScopedPermission(b *testing.B) {
	interceptor := newTestInterceptor(b, realisticPolicy)
	h := interceptor.Unary()
	ctx := ctxAsIncoming("stephan@redpanda.com")
	info := srvInfo("/authz.test.v1.TestService/GetWidget")
	req := &testv1.GetWidgetRequest{Id: "widget-abc"}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		h(ctx, req, info, noopHandler) //nolint:errcheck // benchmark/race test: result not needed
	}
}

func BenchmarkInterceptor_NestedFieldPath(b *testing.B) {
	interceptor := newTestInterceptor(b, realisticPolicy)
	h := interceptor.Unary()
	ctx := ctxAsIncoming("stephan@redpanda.com")
	info := srvInfo("/authz.test.v1.TestService/UpdateWidget")
	req := &testv1.UpdateWidgetRequest{Widget: &testv1.Widget{Id: "widget-abc"}}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		h(ctx, req, info, noopHandler) //nolint:errcheck // benchmark/race test: result not needed
	}
}

func BenchmarkInterceptor_Denied(b *testing.B) {
	interceptor := newTestInterceptor(b, realisticPolicy)
	h := interceptor.Unary()
	ctx := ctxAsIncoming("random@attacker.com")
	info := srvInfo("/authz.test.v1.TestService/SimpleMethod")

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		h(ctx, nil, info, noopHandler) //nolint:errcheck // benchmark/race test: result not needed
	}
}

func BenchmarkInterceptor_Parallel(b *testing.B) {
	interceptor := newTestInterceptor(b, realisticPolicy)
	h := interceptor.Unary()
	info := srvInfo("/authz.test.v1.TestService/GetWidget")
	req := &testv1.GetWidgetRequest{Id: "widget-abc"}

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		ctx := ctxAsIncoming("stephan@redpanda.com")
		for pb.Next() {
			h(ctx, req, info, noopHandler) //nolint:errcheck // benchmark/race test: result not needed
		}
	})
}
