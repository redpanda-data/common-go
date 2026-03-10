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
	"net"
	"net/http"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	testv1 "github.com/redpanda-data/common-go/authz/testdata/gen"
	"github.com/redpanda-data/common-go/authz/testdata/gen/testv1connect"
)

type connectTestHandler struct {
	testv1connect.UnimplementedTestServiceHandler
}

func (*connectTestHandler) SimpleMethod(_ context.Context, _ *connect.Request[testv1.SimpleRequest]) (*connect.Response[testv1.SimpleResponse], error) {
	return connect.NewResponse(&testv1.SimpleResponse{}), nil
}

func (*connectTestHandler) GetWidget(_ context.Context, _ *connect.Request[testv1.GetWidgetRequest]) (*connect.Response[testv1.GetWidgetResponse], error) {
	return connect.NewResponse(&testv1.GetWidgetResponse{}), nil
}

func (*connectTestHandler) CreateWidget(_ context.Context, _ *connect.Request[testv1.CreateWidgetRequest]) (*connect.Response[testv1.CreateWidgetResponse], error) {
	return connect.NewResponse(&testv1.CreateWidgetResponse{}), nil
}

func (*connectTestHandler) UpdateWidget(_ context.Context, _ *connect.Request[testv1.UpdateWidgetRequest]) (*connect.Response[testv1.UpdateWidgetResponse], error) {
	return connect.NewResponse(&testv1.UpdateWidgetResponse{}), nil
}

func (*connectTestHandler) ListWidgets(_ context.Context, _ *connect.Request[testv1.ListWidgetsRequest]) (*connect.Response[testv1.ListWidgetsResponse], error) {
	return connect.NewResponse(&testv1.ListWidgetsResponse{
		Widgets: []*testv1.Widget{
			{Id: "widget-1", Name: "one"},
			{Id: "widget-2", Name: "two"},
			{Id: "widget-3", Name: "three"},
		},
	}), nil
}

func (*connectTestHandler) UnannotatedMethod(_ context.Context, _ *connect.Request[testv1.SimpleRequest]) (*connect.Response[testv1.SimpleResponse], error) {
	return connect.NewResponse(&testv1.SimpleResponse{}), nil
}

// connectTestExtractor reads the principal from Connect request headers.
func connectTestExtractor(_ context.Context, h http.Header) (PrincipalID, bool) {
	v := h.Get(testPrincipalMDKey)
	if v == "" {
		return "", false
	}
	return UserPrincipal(v), true
}

func startConnectTestServer(t testing.TB, policy Policy) testv1connect.TestServiceClient {
	t.Helper()
	client, _ := startConnectTestServerWithInterceptor(t, policy)
	return client
}

func startConnectTestServerWithInterceptor(t testing.TB, policy Policy) (testv1connect.TestServiceClient, *Interceptor) {
	t.Helper()
	l, _ := zap.NewDevelopment()

	// PrincipalExtractor here is a dummy — the Connect interceptor uses
	// ConnectPrincipalExtractor from the config override.
	interceptor, err := NewInterceptor(InterceptorConfig{
		Logger:           l,
		ResourceName:     testDataplane,
		ExtractPrincipal: func(context.Context) (PrincipalID, bool) { return "", false },
		Policy:           policy,
	})
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	path, handler := testv1connect.NewTestServiceHandler(
		&connectTestHandler{},
		connect.WithInterceptors(interceptor.ConnectInterceptor(ConnectInterceptorConfig{
			ExtractPrincipal: connectTestExtractor,
		})),
	)
	mux.Handle(path, handler)

	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	client := testv1connect.NewTestServiceClient(
		http.DefaultClient,
		"http://"+ln.Addr().String(),
	)
	return client, interceptor
}

// connectReqAs creates a Connect request with the test principal header.
func connectReqAs[T any](email string, msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set(testPrincipalMDKey, email)
	return req
}

// --- Fail-closed and bypass ---

func TestConnect_UnannotatedMethodDenied(t *testing.T) {
	client := startConnectTestServer(t, realisticPolicy)
	_, err := client.UnannotatedMethod(context.Background(), connectReqAs("stephan@redpanda.com", &testv1.SimpleRequest{}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func TestConnect_NoIdentityReturnsInternal(t *testing.T) {
	client := startConnectTestServer(t, realisticPolicy)
	_, err := client.SimpleMethod(context.Background(), connect.NewRequest(&testv1.SimpleRequest{}))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("expected Internal, got %v", err)
	}
}

func TestConnect_UnboundUserDenied(t *testing.T) {
	client := startConnectTestServer(t, realisticPolicy)
	_, err := client.SimpleMethod(context.Background(), connectReqAs("random@attacker.com", &testv1.SimpleRequest{}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

// --- Role-based access at dataplane level ---

func TestConnect_AdminGrantedSimple(t *testing.T) {
	client := startConnectTestServer(t, realisticPolicy)
	_, err := client.SimpleMethod(context.Background(), connectReqAs("stephan@redpanda.com", &testv1.SimpleRequest{}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestConnect_ReaderGrantedSimple(t *testing.T) {
	client := startConnectTestServer(t, realisticPolicy)
	_, err := client.SimpleMethod(context.Background(), connectReqAs("intern@redpanda.com", &testv1.SimpleRequest{}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestConnect_ReaderDeniedGetWidget(t *testing.T) {
	client := startConnectTestServer(t, realisticPolicy)
	_, err := client.GetWidget(context.Background(), connectReqAs("intern@redpanda.com", &testv1.GetWidgetRequest{Id: "w1"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func TestConnect_ReaderDeniedCreateWidget(t *testing.T) {
	client := startConnectTestServer(t, realisticPolicy)
	_, err := client.CreateWidget(context.Background(), connectReqAs("intern@redpanda.com", &testv1.CreateWidgetRequest{Name: "new"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

// --- Collection authorization (List RPCs) ---

func TestConnect_ListWidgets_DataplaneScopeReturnsAll(t *testing.T) {
	client := startConnectTestServer(t, realisticPolicy)
	resp, err := client.ListWidgets(context.Background(), connectReqAs("stephan@redpanda.com", &testv1.ListWidgetsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Msg.Widgets) != 3 {
		t.Fatalf("expected 3 widgets, got %d", len(resp.Msg.Widgets))
	}
}

func TestConnect_ListWidgets_PerResourceReturnsSubset(t *testing.T) {
	policy := Policy{
		Roles: []Role{{ID: "Lister", Permissions: []PermissionName{"test_list_perm"}}},
		Bindings: []RoleBinding{
			{Role: "Lister", Principal: UserPrincipal("alice@redpanda.com"), Scope: testDataplane + "/widgets/widget-1"},
			{Role: "Lister", Principal: UserPrincipal("alice@redpanda.com"), Scope: testDataplane + "/widgets/widget-3"},
		},
	}
	client := startConnectTestServer(t, policy)
	resp, err := client.ListWidgets(context.Background(), connectReqAs("alice@redpanda.com", &testv1.ListWidgetsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Msg.Widgets) != 2 {
		t.Fatalf("expected 2 widgets, got %d", len(resp.Msg.Widgets))
	}
	ids := map[string]bool{}
	for _, w := range resp.Msg.Widgets {
		ids[w.Id] = true
	}
	if !ids["widget-1"] || !ids["widget-3"] {
		t.Fatalf("expected widget-1 and widget-3, got %v", ids)
	}
}

func TestConnect_ListWidgets_NoPermissionReturnsEmpty(t *testing.T) {
	policy := Policy{
		Roles: []Role{{ID: "Lister", Permissions: []PermissionName{"test_list_perm"}}},
	}
	client := startConnectTestServer(t, policy)
	resp, err := client.ListWidgets(context.Background(), connectReqAs("bob@redpanda.com", &testv1.ListWidgetsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Msg.Widgets) != 0 {
		t.Fatalf("expected 0 widgets, got %d", len(resp.Msg.Widgets))
	}
}

func TestConnect_ListWidgets_WildcardReturnsAll(t *testing.T) {
	policy := Policy{
		Roles: []Role{{ID: "Lister", Permissions: []PermissionName{"test_list_perm"}}},
		Bindings: []RoleBinding{
			{Role: "Lister", Principal: UserPrincipal("carol@redpanda.com"), Scope: testDataplane + "/widgets/*"},
		},
	}
	client := startConnectTestServer(t, policy)
	resp, err := client.ListWidgets(context.Background(), connectReqAs("carol@redpanda.com", &testv1.ListWidgetsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Msg.Widgets) != 3 {
		t.Fatalf("expected 3 widgets, got %d", len(resp.Msg.Widgets))
	}
}

func TestConnect_ListWidgets_NoIdentityDenied(t *testing.T) {
	client := startConnectTestServer(t, realisticPolicy)
	_, err := client.ListWidgets(context.Background(), connect.NewRequest(&testv1.ListWidgetsRequest{}))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("expected Internal, got %v", err)
	}
}

// --- Sub-resource scoping: dataplane binding cascades to children ---

func TestConnect_DataplaneBindingGrantsGetWidget(t *testing.T) {
	client := startConnectTestServer(t, realisticPolicy)
	_, err := client.GetWidget(context.Background(), connectReqAs("stephan@redpanda.com", &testv1.GetWidgetRequest{Id: "any-widget"}))
	if err != nil {
		t.Fatalf("dataplane admin should access any widget: %v", err)
	}
}

func TestConnect_DataplaneBindingGrantsUpdateWidget(t *testing.T) {
	client := startConnectTestServer(t, realisticPolicy)
	_, err := client.UpdateWidget(context.Background(), connectReqAs("tyler@redpanda.com", &testv1.UpdateWidgetRequest{
		Widget: &testv1.Widget{Id: "widget-abc", Name: "updated"},
	}))
	if err != nil {
		t.Fatalf("dataplane writer should update any widget: %v", err)
	}
}

func TestConnect_DataplaneBindingGrantsCreateWidget(t *testing.T) {
	client := startConnectTestServer(t, realisticPolicy)
	_, err := client.CreateWidget(context.Background(), connectReqAs("tyler@redpanda.com", &testv1.CreateWidgetRequest{Name: "new-widget"}))
	if err != nil {
		t.Fatalf("dataplane writer should create: %v", err)
	}
}

// --- Sub-resource scoping: per-resource binding ---

func TestConnect_PerResourceBindingGrantsSpecificWidget(t *testing.T) {
	policy := Policy{
		Roles: []Role{{ID: "WidgetEditor", Permissions: []PermissionName{"test_scoped_perm"}}},
		Bindings: []RoleBinding{{
			Role:      "WidgetEditor",
			Principal: UserPrincipal("alice@redpanda.com"),
			Scope:     testDataplane + "/widgets/widget-123",
		}},
	}
	client := startConnectTestServer(t, policy)
	_, err := client.GetWidget(context.Background(), connectReqAs("alice@redpanda.com", &testv1.GetWidgetRequest{Id: "widget-123"}))
	if err != nil {
		t.Fatalf("should grant widget-123: %v", err)
	}
}

func TestConnect_PerResourceBindingDeniesOtherWidget(t *testing.T) {
	policy := Policy{
		Roles: []Role{{ID: "WidgetEditor", Permissions: []PermissionName{"test_scoped_perm"}}},
		Bindings: []RoleBinding{{
			Role:      "WidgetEditor",
			Principal: UserPrincipal("alice@redpanda.com"),
			Scope:     testDataplane + "/widgets/widget-123",
		}},
	}
	client := startConnectTestServer(t, policy)
	_, err := client.GetWidget(context.Background(), connectReqAs("alice@redpanda.com", &testv1.GetWidgetRequest{Id: "widget-456"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected PermissionDenied for wrong widget, got %v", err)
	}
}

func TestConnect_PerResourceBindingNestedFieldPath(t *testing.T) {
	policy := Policy{
		Roles: []Role{{ID: "WidgetEditor", Permissions: []PermissionName{"test_scoped_perm"}}},
		Bindings: []RoleBinding{{
			Role:      "WidgetEditor",
			Principal: UserPrincipal("alice@redpanda.com"),
			Scope:     testDataplane + "/widgets/widget-999",
		}},
	}
	client := startConnectTestServer(t, policy)

	_, err := client.UpdateWidget(context.Background(), connectReqAs("alice@redpanda.com", &testv1.UpdateWidgetRequest{
		Widget: &testv1.Widget{Id: "widget-999", Name: "x"},
	}))
	if err != nil {
		t.Fatalf("should grant update on widget-999: %v", err)
	}

	_, err = client.UpdateWidget(context.Background(), connectReqAs("alice@redpanda.com", &testv1.UpdateWidgetRequest{
		Widget: &testv1.Widget{Id: "widget-000", Name: "x"},
	}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected PermissionDenied for wrong widget, got %v", err)
	}
}

// --- Wildcard bindings ---

func TestConnect_WildcardBindingGrantsAllWidgets(t *testing.T) {
	policy := Policy{
		Roles: []Role{{ID: "WidgetEditor", Permissions: []PermissionName{"test_scoped_perm"}}},
		Bindings: []RoleBinding{{
			Role:      "WidgetEditor",
			Principal: UserPrincipal("bob@redpanda.com"),
			Scope:     testDataplane + "/widgets/*",
		}},
	}
	client := startConnectTestServer(t, policy)

	_, err := client.GetWidget(context.Background(), connectReqAs("bob@redpanda.com", &testv1.GetWidgetRequest{Id: "any-widget"}))
	if err != nil {
		t.Fatalf("wildcard binding should grant any widget: %v", err)
	}

	_, err = client.GetWidget(context.Background(), connectReqAs("bob@redpanda.com", &testv1.GetWidgetRequest{Id: "another-widget"}))
	if err != nil {
		t.Fatalf("wildcard binding should grant another widget: %v", err)
	}
}

// --- Empty resource ID when id_getter_cel is set ---

func TestConnect_EmptyResourceIDDenied(t *testing.T) {
	client := startConnectTestServer(t, realisticPolicy)
	_, err := client.GetWidget(context.Background(), connectReqAs("stephan@redpanda.com", &testv1.GetWidgetRequest{Id: ""}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument for empty resource ID, got %v", err)
	}
}

// --- Policy hot-reload ---

func TestConnect_SwapPolicy(t *testing.T) {
	client, interceptor := startConnectTestServerWithInterceptor(t, Policy{
		Roles: []Role{{ID: "r", Permissions: []PermissionName{"test_simple_perm"}}},
	})

	_, err := client.SimpleMethod(context.Background(), connectReqAs("bob@redpanda.com", &testv1.SimpleRequest{}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("expected PermissionDenied before swap, got %v", err)
	}

	if err := interceptor.SwapPolicy(Policy{
		Roles:    []Role{{ID: "r", Permissions: []PermissionName{"test_simple_perm"}}},
		Bindings: []RoleBinding{{Role: "r", Principal: UserPrincipal("bob@redpanda.com"), Scope: testDataplane}},
	}); err != nil {
		t.Fatal(err)
	}

	_, err = client.SimpleMethod(context.Background(), connectReqAs("bob@redpanda.com", &testv1.SimpleRequest{}))
	if err != nil {
		t.Fatalf("bob should be granted after swap: %v", err)
	}
}

// --- Race: concurrent requests + policy swaps ---

func TestConnect_ConcurrentRequestsAndSwaps(t *testing.T) {
	policyGranted := Policy{
		Roles:    []Role{{ID: "r", Permissions: []PermissionName{"test_simple_perm", "test_scoped_perm", "test_create_perm"}}},
		Bindings: []RoleBinding{{Role: "r", Principal: UserPrincipal("racer@redpanda.com"), Scope: testDataplane}},
	}
	policyDenied := Policy{
		Roles: []Role{{ID: "r", Permissions: []PermissionName{"test_simple_perm", "test_scoped_perm", "test_create_perm"}}},
	}

	client, interceptor := startConnectTestServerWithInterceptor(t, policyGranted)

	const goroutines = 20
	const iterations = 100

	var wg sync.WaitGroup

	for i := range goroutines {
		wg.Go(func() {
			for range iterations {
				switch i % 3 {
				case 0:
					client.SimpleMethod(context.Background(), connectReqAs("racer@redpanda.com", &testv1.SimpleRequest{}))
				case 1:
					client.GetWidget(context.Background(), connectReqAs("racer@redpanda.com", &testv1.GetWidgetRequest{Id: "w1"}))
				default:
					client.UpdateWidget(context.Background(), connectReqAs("racer@redpanda.com", &testv1.UpdateWidgetRequest{Widget: &testv1.Widget{Id: "w2"}}))
				}
			}
		})
	}

	wg.Go(func() {
		for range iterations {
			interceptor.SwapPolicy(policyDenied)  //nolint:errcheck // race test: intentional
			interceptor.SwapPolicy(policyGranted) //nolint:errcheck // race test: intentional
		}
	})

	wg.Wait()
}

// --- Benchmarks ---

func BenchmarkConnectInterceptor_SimplePermission(b *testing.B) {
	client := startConnectTestServer(b, realisticPolicy)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		client.SimpleMethod(context.Background(), connectReqAs("stephan@redpanda.com", &testv1.SimpleRequest{})) //nolint:errcheck // benchmark: result not needed
	}
}

func BenchmarkConnectInterceptor_ScopedPermission(b *testing.B) {
	client := startConnectTestServer(b, realisticPolicy)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		client.GetWidget(context.Background(), connectReqAs("stephan@redpanda.com", &testv1.GetWidgetRequest{Id: "widget-abc"})) //nolint:errcheck // benchmark: result not needed
	}
}

func BenchmarkConnectInterceptor_Denied(b *testing.B) {
	client := startConnectTestServer(b, realisticPolicy)

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		client.SimpleMethod(context.Background(), connectReqAs("random@attacker.com", &testv1.SimpleRequest{})) //nolint:errcheck // benchmark: result not needed
	}
}
