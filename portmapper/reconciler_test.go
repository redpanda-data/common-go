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

package portmapper

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// capturingContext returns a context whose logger records every line emitted
// at or below the given verbosity.
func capturingContext(ctx context.Context, verbosity int) (context.Context, *[]string) {
	var lines []string
	logger := funcr.New(func(prefix, args string) {
		lines = append(lines, prefix+" "+args)
	}, funcr.Options{Verbosity: verbosity})
	return log.IntoContext(ctx, logger), &lines
}

const (
	testNamespace = "demo"
	testGroupKey  = "port-mapper.example.com/group"
	testPortsKey  = "port-mapper.example.com/ports"
)

func testConfig() Config {
	return Config{
		ManagedBy:    "my-controller",
		ServiceKey:   AnnotationKey(testGroupKey),
		Membership:   PortNames(AnnotationKey(testPortsKey)),
		ResyncPeriod: 10 * time.Second,
	}
}

func newReconciler(t *testing.T, cfg Config, objs ...client.Object) (*reconciler, client.Client) {
	t.Helper()

	mapper, err := New(cfg)
	require.NoError(t, err)

	c := fake.NewClientBuilder().
		WithScheme(clientgoscheme.Scheme).
		WithObjects(objs...).
		Build()

	return &reconciler{client: c, scheme: clientgoscheme.Scheme, cfg: mapper.cfg}, c
}

func testService(meta map[string]string, annotation bool) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service",
			Namespace: testNamespace,
			UID:       types.UID("svc-uid"),
		},
		Spec: corev1.ServiceSpec{
			// Intentionally selectorless: this controller publishes the
			// slices.
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt32(8080)},
				{Name: "https", Port: 8443, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt32(8443)},
			},
		},
	}
	if annotation {
		svc.Annotations = meta
	} else {
		svc.Labels = meta
	}
	return svc
}

func testPod(name, ip string, meta map[string]string, annotation bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			UID:       types.UID(name + "-uid"),
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: ip,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
	if ip != "" {
		pod.Status.PodIPs = []corev1.PodIP{{IP: ip}}
	}
	if annotation {
		pod.Annotations = meta
	} else {
		pod.Labels = meta
	}
	return pod
}

// testObjects reproduces the canonical example: my-service exposing 8080
// backed by pods a, b, c and 8443 backed by pods b, c, d.
func testObjects() []client.Object {
	return []client.Object{
		testService(map[string]string{testGroupKey: "my-group"}, true),
		testPod("pod-a", "10.0.0.11", map[string]string{testGroupKey: "my-group", testPortsKey: "http"}, true),
		testPod("pod-b", "10.0.0.12", map[string]string{testGroupKey: "my-group", testPortsKey: "http,https"}, true),
		testPod("pod-c", "10.0.0.13", map[string]string{testGroupKey: "my-group", testPortsKey: "http,https"}, true),
		testPod("pod-d", "10.0.0.14", map[string]string{testGroupKey: "my-group", testPortsKey: "https"}, true),
	}
}

func serviceRequest() ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: "my-service"}}
}

func reconcileService(t *testing.T, r *reconciler) ctrl.Result {
	t.Helper()
	result, err := r.Reconcile(t.Context(), serviceRequest())
	require.NoError(t, err)
	return result
}

func getSlice(t *testing.T, c client.Client, name string) *discoveryv1.EndpointSlice {
	t.Helper()
	var slice discoveryv1.EndpointSlice
	err := c.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: name}, &slice)
	require.NoError(t, err)
	return &slice
}

func requireNoSlice(t *testing.T, c client.Client, name string) {
	t.Helper()
	var slice discoveryv1.EndpointSlice
	err := c.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: name}, &slice)
	require.True(t, apierrors.IsNotFound(err), "expected %s to not exist", name)
}

func sliceAddresses(slice *discoveryv1.EndpointSlice) []string {
	var addresses []string
	for _, endpoint := range slice.Endpoints {
		addresses = append(addresses, endpoint.Addresses...)
	}
	return addresses
}

func TestReconcilePublishesSlicePerPort(t *testing.T) {
	r, c := newReconciler(t, testConfig(), testObjects()...)

	result := reconcileService(t, r)
	require.Equal(t, 10*time.Second, result.RequeueAfter)

	http := getSlice(t, c, "my-service-http")
	require.Equal(t, []string{"10.0.0.11", "10.0.0.12", "10.0.0.13"}, sliceAddresses(http))
	require.Equal(t, discoveryv1.AddressTypeIPv4, http.AddressType)
	require.Len(t, http.Ports, 1)
	require.Equal(t, "http", *http.Ports[0].Name)
	require.Equal(t, int32(8080), *http.Ports[0].Port)
	require.Equal(t, corev1.ProtocolTCP, *http.Ports[0].Protocol)
	require.Equal(t, "my-service", http.Labels[discoveryv1.LabelServiceName])
	require.Equal(t, "my-controller", http.Labels[discoveryv1.LabelManagedBy])

	owner := metav1.GetControllerOf(http)
	require.NotNil(t, owner)
	require.Equal(t, types.UID("svc-uid"), owner.UID)

	https := getSlice(t, c, "my-service-https")
	require.Equal(t, []string{"10.0.0.12", "10.0.0.13", "10.0.0.14"}, sliceAddresses(https))
	require.Equal(t, "https", *https.Ports[0].Name)
	require.Equal(t, int32(8443), *https.Ports[0].Port)

	// Endpoints carry pod details for consumers that resolve them.
	require.Equal(t, "pod-a", http.Endpoints[0].TargetRef.Name)
	require.Equal(t, "node-1", *http.Endpoints[0].NodeName)
	require.True(t, *http.Endpoints[0].Conditions.Ready)
	require.True(t, *http.Endpoints[0].Conditions.Serving)
	require.False(t, *http.Endpoints[0].Conditions.Terminating)

	// No trafficDistribution (e.g. a pre-1.30 cluster where the field
	// doesn't exist) means no hints -- never an error.
	require.Nil(t, http.Endpoints[0].Hints)
}

func TestReconcileTracksMembershipChanges(t *testing.T) {
	r, c := newReconciler(t, testConfig(), testObjects()...)
	reconcileService(t, r)

	// pod-a starts backing https too.
	var pod corev1.Pod
	require.NoError(t, c.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: "pod-a"}, &pod))
	pod.Annotations[testPortsKey] = "http,https"
	require.NoError(t, c.Update(t.Context(), &pod))
	reconcileService(t, r)
	require.Equal(t, []string{"10.0.0.11", "10.0.0.12", "10.0.0.13", "10.0.0.14"},
		sliceAddresses(getSlice(t, c, "my-service-https")))

	// pod-d goes away entirely.
	require.NoError(t, c.Delete(t.Context(), testPod("pod-d", "", nil, true)))
	reconcileService(t, r)
	require.Equal(t, []string{"10.0.0.11", "10.0.0.12", "10.0.0.13"},
		sliceAddresses(getSlice(t, c, "my-service-https")))
}

func TestReconcileLabelAlignment(t *testing.T) {
	cfg := testConfig()
	cfg.ServiceKey = LabelKey(testGroupKey)
	cfg.PodKey = LabelKey("group")
	cfg.Membership = All(PodReady(), PortNames(LabelKey("ports")))

	r, c := newReconciler(t, cfg,
		testService(map[string]string{testGroupKey: "my-group"}, false),
		testPod("pod-a", "10.0.0.11", map[string]string{"group": "my-group", "ports": "http"}, false),
		testPod("pod-b", "10.0.0.12", map[string]string{"group": "other-group", "ports": "http"}, false),
	)
	reconcileService(t, r)

	require.Equal(t, []string{"10.0.0.11"}, sliceAddresses(getSlice(t, c, "my-service-http")))
	require.Empty(t, sliceAddresses(getSlice(t, c, "my-service-https")))
}

func TestReconcileDefaultMembershipIsReadiness(t *testing.T) {
	cfg := testConfig()
	cfg.Membership = nil // default to PodReady

	unready := testPod("pod-b", "10.0.0.12", map[string]string{testGroupKey: "my-group"}, true)
	unready.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}

	r, c := newReconciler(t, cfg,
		testService(map[string]string{testGroupKey: "my-group"}, true),
		testPod("pod-a", "10.0.0.11", map[string]string{testGroupKey: "my-group"}, true),
		unready,
	)
	reconcileService(t, r)

	// Readiness applies uniformly to every port.
	require.Equal(t, []string{"10.0.0.11"}, sliceAddresses(getSlice(t, c, "my-service-http")))
	require.Equal(t, []string{"10.0.0.11"}, sliceAddresses(getSlice(t, c, "my-service-https")))
}

func TestReconcileDualStack(t *testing.T) {
	objs := testObjects()
	svc := objs[0].(*corev1.Service)
	svc.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol}
	for i, obj := range objs[1:] {
		pod := obj.(*corev1.Pod)
		pod.Status.PodIPs = append(pod.Status.PodIPs, corev1.PodIP{IP: "fd00::1" + strconv.Itoa(i+1)})
	}

	r, c := newReconciler(t, testConfig(), objs...)
	reconcileService(t, r)

	require.Equal(t, []string{"10.0.0.11", "10.0.0.12", "10.0.0.13"},
		sliceAddresses(getSlice(t, c, "my-service-http")))

	httpV6 := getSlice(t, c, "my-service-http--ipv6")
	require.Equal(t, discoveryv1.AddressTypeIPv6, httpV6.AddressType)
	require.Equal(t, []string{"fd00::11", "fd00::12", "fd00::13"}, sliceAddresses(httpV6))
	require.Equal(t, int32(8080), *httpV6.Ports[0].Port)

	httpsV6 := getSlice(t, c, "my-service-https--ipv6")
	require.Equal(t, []string{"fd00::12", "fd00::13", "fd00::14"}, sliceAddresses(httpsV6))

	// Dropping the IPv6 family garbage collects the IPv6 slices.
	require.NoError(t, c.Get(t.Context(), serviceRequest().NamespacedName, svc))
	svc.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv4Protocol}
	require.NoError(t, c.Update(t.Context(), svc))
	reconcileService(t, r)
	requireNoSlice(t, c, "my-service-http--ipv6")
	requireNoSlice(t, c, "my-service-https--ipv6")
}

func TestReconcileChunksLargeSlices(t *testing.T) {
	cfg := testConfig()
	cfg.MaxEndpointsPerSlice = 2

	objs := []client.Object{testService(map[string]string{testGroupKey: "my-group"}, true)}
	for i, name := range []string{"pod-a", "pod-b", "pod-c", "pod-d", "pod-e"} {
		objs = append(objs, testPod(name, "10.0.0.1"+strconv.Itoa(i+1),
			map[string]string{testGroupKey: "my-group", testPortsKey: "http"}, true))
	}

	r, c := newReconciler(t, cfg, objs...)
	reconcileService(t, r)

	require.Equal(t, []string{"10.0.0.11", "10.0.0.12"}, sliceAddresses(getSlice(t, c, "my-service-http")))
	require.Equal(t, []string{"10.0.0.13", "10.0.0.14"}, sliceAddresses(getSlice(t, c, "my-service-http--2")))
	require.Equal(t, []string{"10.0.0.15"}, sliceAddresses(getSlice(t, c, "my-service-http--3")))
	// Nothing backs https, but a placeholder slice is still published.
	require.Empty(t, sliceAddresses(getSlice(t, c, "my-service-https")))

	// Shrinking the pod set retires overflow chunks.
	require.NoError(t, c.Delete(t.Context(), testPod("pod-d", "", nil, true)))
	require.NoError(t, c.Delete(t.Context(), testPod("pod-e", "", nil, true)))
	reconcileService(t, r)
	require.Equal(t, []string{"10.0.0.13"}, sliceAddresses(getSlice(t, c, "my-service-http--2")))
	requireNoSlice(t, c, "my-service-http--3")
}

func TestReconcileResolvesNamedTargetPorts(t *testing.T) {
	svc := testService(map[string]string{testGroupKey: "my-group"}, true)
	svc.Spec.Ports[1].TargetPort = intstr.FromString("tls")

	withTLSPort := func(pod *corev1.Pod, port int32) *corev1.Pod {
		pod.Spec.Containers = []corev1.Container{{
			Name:  "app",
			Ports: []corev1.ContainerPort{{Name: "tls", ContainerPort: port}},
		}}
		return pod
	}

	r, c := newReconciler(t, testConfig(),
		svc,
		testPod("pod-a", "10.0.0.11", map[string]string{testGroupKey: "my-group", testPortsKey: "http"}, true),
		withTLSPort(testPod("pod-b", "10.0.0.12", map[string]string{testGroupKey: "my-group", testPortsKey: "http,https"}, true), 8443),
		withTLSPort(testPod("pod-c", "10.0.0.13", map[string]string{testGroupKey: "my-group", testPortsKey: "https"}, true), 8443),
		// Resolves the same named port to a different number, e.g. mid
		// rolling-update.
		withTLSPort(testPod("pod-d", "10.0.0.14", map[string]string{testGroupKey: "my-group", testPortsKey: "https"}, true), 9443),
		// Doesn't expose the named port at all.
		testPod("pod-e", "10.0.0.15", map[string]string{testGroupKey: "my-group", testPortsKey: "https"}, true),
	)
	reconcileService(t, r)

	// Named targetPorts always carry their resolved number, so group names
	// never depend on which sibling groups exist.
	https := getSlice(t, c, "my-service-https--p8443")
	require.Equal(t, int32(8443), *https.Ports[0].Port)
	require.Equal(t, "https", *https.Ports[0].Name)
	require.Equal(t, []string{"10.0.0.12", "10.0.0.13"}, sliceAddresses(https))

	other := getSlice(t, c, "my-service-https--p9443")
	require.Equal(t, int32(9443), *other.Ports[0].Port)
	require.Equal(t, "https", *other.Ports[0].Name)
	require.Equal(t, []string{"10.0.0.14"}, sliceAddresses(other))

	// The numeric http port keeps its unsuffixed name.
	require.Equal(t, []string{"10.0.0.11", "10.0.0.12"}, sliceAddresses(getSlice(t, c, "my-service-http")))

	// With no members left, the named port keeps a placeholder at the lowest
	// target any candidate pod still resolves.
	for _, name := range []string{"pod-b", "pod-c", "pod-d"} {
		var pod corev1.Pod
		require.NoError(t, c.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: name}, &pod))
		pod.Annotations[testPortsKey] = "http"
		require.NoError(t, c.Update(t.Context(), &pod))
	}
	reconcileService(t, r)
	require.Empty(t, sliceAddresses(getSlice(t, c, "my-service-https--p8443")))
	requireNoSlice(t, c, "my-service-https--p9443")
}

func TestReconcileRepairsStrippedLabels(t *testing.T) {
	r, c := newReconciler(t, testConfig(), testObjects()...)
	reconcileService(t, r)

	// Strip the ownership labels: the slice falls out of the label-filtered
	// listing, but the controller owner reference still identifies it as
	// ours and it must be re-applied, not treated as a name collision.
	slice := getSlice(t, c, "my-service-http")
	delete(slice.Labels, discoveryv1.LabelManagedBy)
	delete(slice.Labels, discoveryv1.LabelServiceName)
	slice.Endpoints = slice.Endpoints[:1]
	require.NoError(t, c.Update(t.Context(), slice))

	reconcileService(t, r)
	repaired := getSlice(t, c, "my-service-http")
	require.Equal(t, "my-controller", repaired.Labels[discoveryv1.LabelManagedBy])
	require.Equal(t, "my-service", repaired.Labels[discoveryv1.LabelServiceName])
	require.Equal(t, []string{"10.0.0.11", "10.0.0.12", "10.0.0.13"}, sliceAddresses(repaired))
}

func TestManagedByPredicate(t *testing.T) {
	labeled := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{
		Labels: map[string]string{discoveryv1.LabelManagedBy: "mine"},
	}}
	stripped := &discoveryv1.EndpointSlice{}

	pred := managedByPredicate("mine")
	require.True(t, pred.Create(event.CreateEvent{Object: labeled}))
	require.False(t, pred.Create(event.CreateEvent{Object: stripped}))
	// Stripping the label must still trigger a reconcile so the slice can be
	// repaired.
	require.True(t, pred.Update(event.UpdateEvent{ObjectOld: labeled, ObjectNew: stripped}))
	require.False(t, pred.Update(event.UpdateEvent{ObjectOld: stripped, ObjectNew: stripped}))
}

func TestReconcileExternalName(t *testing.T) {
	svc := testService(map[string]string{testGroupKey: "my-group"}, true)
	svc.Spec.Type = corev1.ServiceTypeExternalName
	svc.Spec.ExternalName = "example.com"

	// A slice published before the type changed must be garbage collected.
	stale := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service-http",
			Namespace: testNamespace,
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "my-service",
				discoveryv1.LabelManagedBy:   "my-controller",
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
	}

	r, c := newReconciler(t, testConfig(), append(testObjects()[1:], svc, stale)...)
	recorder := record.NewFakeRecorder(16)
	r.recorder = recorder
	reconcileService(t, r)

	requireNoSlice(t, c, "my-service-http")
	requireNoSlice(t, c, "my-service-https")
	select {
	case event := <-recorder.Events:
		require.Contains(t, event, "ExternalNameUnsupported")
	default:
		t.Fatal("expected a warning event on the service")
	}
}

func TestReconcilePublishesTerminatingEndpoints(t *testing.T) {
	terminatingObjects := func() []client.Object {
		objs := testObjects()
		pod := objs[2].(*corev1.Pod) // pod-b
		pod.DeletionTimestamp = ptr.To(metav1.Now())
		pod.Finalizers = []string{"port-mapper.example.com/test"}
		return objs
	}

	t.Run("drains gracefully", func(t *testing.T) {
		r, c := newReconciler(t, testConfig(), terminatingObjects()...)
		reconcileService(t, r)

		https := getSlice(t, c, "my-service-https")
		require.Equal(t, []string{"10.0.0.12", "10.0.0.13", "10.0.0.14"}, sliceAddresses(https))

		terminating := https.Endpoints[0] // pod-b
		require.False(t, *terminating.Conditions.Ready)
		require.True(t, *terminating.Conditions.Serving)
		require.True(t, *terminating.Conditions.Terminating)

		remaining := https.Endpoints[1] // pod-c
		require.True(t, *remaining.Conditions.Ready)
		require.False(t, *remaining.Conditions.Terminating)
	})

	t.Run("publishNotReadyAddresses keeps ready", func(t *testing.T) {
		objs := terminatingObjects()
		objs[0].(*corev1.Service).Spec.PublishNotReadyAddresses = true

		r, c := newReconciler(t, testConfig(), objs...)
		reconcileService(t, r)

		terminating := getSlice(t, c, "my-service-https").Endpoints[0]
		require.True(t, *terminating.Conditions.Ready)
		require.True(t, *terminating.Conditions.Terminating)
	})

	t.Run("NotTerminating opts out", func(t *testing.T) {
		cfg := testConfig()
		cfg.Membership = All(NotTerminating(), PortNames(AnnotationKey(testPortsKey)))

		r, c := newReconciler(t, cfg, terminatingObjects()...)
		reconcileService(t, r)

		require.Equal(t, []string{"10.0.0.13", "10.0.0.14"},
			sliceAddresses(getSlice(t, c, "my-service-https")))
	})
}

// topologyObjects extends the canonical objects with a zone-labeled node, a
// per-pod DNS hostname on pod-a, and the given trafficDistribution.
func topologyObjects(distribution string) []client.Object {
	objs := testObjects()
	svc := objs[0].(*corev1.Service)
	if distribution != "" {
		svc.Spec.TrafficDistribution = ptr.To(distribution)
	}
	// pod-a participates in per-pod DNS for this service.
	pod := objs[1].(*corev1.Pod)
	pod.Spec.Hostname = "pod-a"
	pod.Spec.Subdomain = "my-service"
	return append(objs, &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-1",
			Labels: map[string]string{corev1.LabelTopologyZone: "us-east-1a"},
		},
	})
}

func TestReconcileTopology(t *testing.T) {
	t.Run("publishes zones, hostnames, and hints", func(t *testing.T) {
		r, c := newReconciler(t, testConfig(), topologyObjects(corev1.ServiceTrafficDistributionPreferClose)...)
		reconcileService(t, r)

		endpoint := getSlice(t, c, "my-service-http").Endpoints[0] // pod-a
		require.Equal(t, "us-east-1a", *endpoint.Zone)
		require.Equal(t, "pod-a", *endpoint.Hostname)
		require.NotNil(t, endpoint.Hints)
		require.Equal(t, []discoveryv1.ForZone{{Name: "us-east-1a"}}, endpoint.Hints.ForZones)

		// Only pod-a set a matching hostname/subdomain.
		require.Nil(t, getSlice(t, c, "my-service-https").Endpoints[0].Hostname)
	})

	t.Run("DisableNodeLookups skips zones", func(t *testing.T) {
		cfg := testConfig()
		cfg.DisableNodeLookups = true

		r, c := newReconciler(t, cfg, topologyObjects(corev1.ServiceTrafficDistributionPreferClose)...)
		reconcileService(t, r)

		endpoint := getSlice(t, c, "my-service-http").Endpoints[0]
		require.Nil(t, endpoint.Zone)
		require.Nil(t, endpoint.Hints)
		require.Equal(t, "node-1", *endpoint.NodeName)
	})
}

func TestReconcilePreferSameNode(t *testing.T) {
	t.Run("publishes node hints with zone fallback", func(t *testing.T) {
		r, c := newReconciler(t, testConfig(), topologyObjects(corev1.ServiceTrafficDistributionPreferSameNode)...)
		reconcileService(t, r)

		endpoint := getSlice(t, c, "my-service-http").Endpoints[0]
		require.NotNil(t, endpoint.Hints)
		require.Equal(t, []discoveryv1.ForNode{{Name: "node-1"}}, endpoint.Hints.ForNodes)
		require.Equal(t, []discoveryv1.ForZone{{Name: "us-east-1a"}}, endpoint.Hints.ForZones)
	})

	t.Run("without zones publishes node hints only", func(t *testing.T) {
		cfg := testConfig()
		cfg.DisableNodeLookups = true

		r, c := newReconciler(t, cfg, topologyObjects(corev1.ServiceTrafficDistributionPreferSameNode)...)
		reconcileService(t, r)

		endpoint := getSlice(t, c, "my-service-http").Endpoints[0]
		require.NotNil(t, endpoint.Hints)
		require.Equal(t, []discoveryv1.ForNode{{Name: "node-1"}}, endpoint.Hints.ForNodes)
		require.Empty(t, endpoint.Hints.ForZones)
	})

	t.Run("tolerates servers stripping forNodes", func(t *testing.T) {
		r, c := newReconciler(t, testConfig(), topologyObjects(corev1.ServiceTrafficDistributionPreferSameNode)...)
		reconcileService(t, r)

		// Simulate an API server with the PreferSameTrafficDistribution
		// feature gate off: forNodes never persists.
		slice := getSlice(t, c, "my-service-http")
		for i := range slice.Endpoints {
			slice.Endpoints[i].Hints.ForNodes = nil
		}
		require.NoError(t, c.Update(t.Context(), slice))
		version := getSlice(t, c, "my-service-http").ResourceVersion

		reconcileService(t, r)
		final := getSlice(t, c, "my-service-http")
		require.Equal(t, version, final.ResourceVersion, "stripped forNodes hints should not be re-applied")
		require.Empty(t, final.Endpoints[0].Hints.ForNodes)
	})

	t.Run("removes stale forNodes", func(t *testing.T) {
		r, c := newReconciler(t, testConfig(), topologyObjects(corev1.ServiceTrafficDistributionPreferSameNode)...)
		reconcileService(t, r)
		require.NotEmpty(t, getSlice(t, c, "my-service-http").Endpoints[0].Hints.ForNodes)

		var svc corev1.Service
		require.NoError(t, c.Get(t.Context(), serviceRequest().NamespacedName, &svc))
		svc.Spec.TrafficDistribution = ptr.To(corev1.ServiceTrafficDistributionPreferClose)
		require.NoError(t, c.Update(t.Context(), &svc))

		reconcileService(t, r)
		endpoint := getSlice(t, c, "my-service-http").Endpoints[0]
		require.Empty(t, endpoint.Hints.ForNodes)
		require.Equal(t, []discoveryv1.ForZone{{Name: "us-east-1a"}}, endpoint.Hints.ForZones)
	})
}

func TestReconcileTopologyModeAutoUnsupported(t *testing.T) {
	for name, annotate := range map[string]func(*corev1.Service){
		"topology-mode":              func(svc *corev1.Service) { svc.Annotations[corev1.AnnotationTopologyMode] = "Auto" },
		"deprecated legacy spelling": func(svc *corev1.Service) { svc.Annotations[corev1.DeprecatedAnnotationTopologyAwareHints] = "auto" },
	} {
		t.Run(name, func(t *testing.T) {
			objs := topologyObjects(corev1.ServiceTrafficDistributionPreferClose)
			annotate(objs[0].(*corev1.Service))

			r, c := newReconciler(t, testConfig(), objs...)
			recorder := record.NewFakeRecorder(16)
			r.recorder = recorder
			reconcileService(t, r)

			// The annotation overrides trafficDistribution: no hints at all,
			// though zones are still published.
			endpoint := getSlice(t, c, "my-service-http").Endpoints[0]
			require.Nil(t, endpoint.Hints)
			require.Equal(t, "us-east-1a", *endpoint.Zone)

			select {
			case event := <-recorder.Events:
				require.Contains(t, event, "TopologyAwareHintsDisabled")
			default:
				t.Fatal("expected a warning event on the service")
			}

			// The warning fires on the transition, not on every resync.
			reconcileService(t, r)
			require.Empty(t, recorder.Events)
		})
	}

	t.Run("disabled value publishes hints normally", func(t *testing.T) {
		objs := topologyObjects(corev1.ServiceTrafficDistributionPreferClose)
		objs[0].(*corev1.Service).Annotations[corev1.AnnotationTopologyMode] = "Disabled"

		r, c := newReconciler(t, testConfig(), objs...)
		recorder := record.NewFakeRecorder(16)
		r.recorder = recorder
		reconcileService(t, r)

		require.NotNil(t, getSlice(t, c, "my-service-http").Endpoints[0].Hints)
		require.Empty(t, recorder.Events)
	})
}

func TestEndpointEqualToleratesStrippedForNodes(t *testing.T) {
	desired := discoveryv1.Endpoint{
		Addresses: []string{"10.0.0.11"},
		Hints: &discoveryv1.EndpointHints{
			ForZones: []discoveryv1.ForZone{{Name: "us-east-1a"}},
			ForNodes: []discoveryv1.ForNode{{Name: "node-1"}},
		},
	}
	nodesOnly := discoveryv1.Endpoint{
		Addresses: []string{"10.0.0.11"},
		Hints:     &discoveryv1.EndpointHints{ForNodes: []discoveryv1.ForNode{{Name: "node-1"}}},
	}

	clone := func(endpoint discoveryv1.Endpoint, mutate func(*discoveryv1.Endpoint)) *discoveryv1.Endpoint {
		copied := endpoint.DeepCopy()
		if mutate != nil {
			mutate(copied)
		}
		return copied
	}

	// Identical endpoints are equal.
	require.True(t, endpointEqual(clone(desired, nil), &desired))
	// A server that stripped forNodes (feature gate off) is tolerated...
	require.True(t, endpointEqual(clone(desired, func(e *discoveryv1.Endpoint) { e.Hints.ForNodes = nil }), &desired))
	// ...including when stripping leaves hints empty or removed entirely.
	require.True(t, endpointEqual(clone(nodesOnly, func(e *discoveryv1.Endpoint) { e.Hints = &discoveryv1.EndpointHints{} }), &nodesOnly))
	require.True(t, endpointEqual(clone(nodesOnly, func(e *discoveryv1.Endpoint) { e.Hints = nil }), &nodesOnly))
	// Any other drift alongside stripped forNodes still registers.
	require.False(t, endpointEqual(clone(desired, func(e *discoveryv1.Endpoint) {
		e.Hints.ForNodes = nil
		e.Hints.ForZones[0].Name = "us-east-1b"
	}), &desired))
	// Stale forNodes the desired state no longer wants get repaired.
	require.False(t, endpointEqual(&desired, clone(desired, func(e *discoveryv1.Endpoint) { e.Hints.ForNodes = nil })))
}

func TestReconcileCleansUpUnmarkedService(t *testing.T) {
	r, c := newReconciler(t, testConfig(), testObjects()...)
	reconcileService(t, r)

	var svc corev1.Service
	require.NoError(t, c.Get(t.Context(), serviceRequest().NamespacedName, &svc))
	svc.Annotations = nil
	require.NoError(t, c.Update(t.Context(), &svc))

	result := reconcileService(t, r)
	require.Zero(t, result.RequeueAfter)

	var slices discoveryv1.EndpointSliceList
	require.NoError(t, c.List(t.Context(), &slices, client.InNamespace(testNamespace)))
	require.Empty(t, slices.Items)
}

func TestReconcileDeletesStraySlices(t *testing.T) {
	stray := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service-stale",
			Namespace: testNamespace,
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "my-service",
				discoveryv1.LabelManagedBy:   "my-controller",
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
	}
	// Slices owned by unrelated third-party controllers are left alone.
	// (Native-controller leftovers are deliberately removed instead; see
	// TestReconcileCleansUpNativeArtifacts.)
	unmanaged := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service-abc123",
			Namespace: testNamespace,
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "my-service",
				discoveryv1.LabelManagedBy:   "some-other-controller.example.com",
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
	}

	r, c := newReconciler(t, testConfig(), append(testObjects(), stray, unmanaged)...)
	reconcileService(t, r)

	requireNoSlice(t, c, "my-service-stale")
	getSlice(t, c, "my-service-abc123")
}

func TestReconcileRepairsTamperedSlice(t *testing.T) {
	r, c := newReconciler(t, testConfig(), testObjects()...)
	reconcileService(t, r)

	slice := getSlice(t, c, "my-service-http")
	slice.Endpoints = slice.Endpoints[:1]
	require.NoError(t, c.Update(t.Context(), slice))

	reconcileService(t, r)
	require.Equal(t, []string{"10.0.0.11", "10.0.0.12", "10.0.0.13"},
		sliceAddresses(getSlice(t, c, "my-service-http")))
}

func TestSliceNamesCannotCollideWithPortNames(t *testing.T) {
	// The naming scheme's collision-freedom rests on the API rejecting
	// adjacent hyphens in port names; make sure that invariant holds.
	require.NotEmpty(t, validation.IsValidPortName("http--2"))
	require.NotEmpty(t, validation.IsValidPortName("http--ipv6"))

	// A port literally named "http-2" next to a chunked "http" used to
	// collide with chunk 2; the "--" suffix grammar keeps them distinct.
	cfg := testConfig()
	cfg.MaxEndpointsPerSlice = 1

	svc := testService(map[string]string{testGroupKey: "my-group"}, true)
	svc.Spec.Ports = append(svc.Spec.Ports[:1], corev1.ServicePort{
		Name: "http-2", Port: 9000, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt32(9000),
	})

	r, c := newReconciler(t, cfg,
		svc,
		testPod("pod-a", "10.0.0.11", map[string]string{testGroupKey: "my-group", testPortsKey: "http"}, true),
		testPod("pod-b", "10.0.0.12", map[string]string{testGroupKey: "my-group", testPortsKey: "http"}, true),
	)
	reconcileService(t, r)

	require.Equal(t, []string{"10.0.0.11"}, sliceAddresses(getSlice(t, c, "my-service-http")))
	require.Equal(t, []string{"10.0.0.12"}, sliceAddresses(getSlice(t, c, "my-service-http--2")))
	require.Equal(t, int32(9000), *getSlice(t, c, "my-service-http-2").Ports[0].Port)
}

func TestReconcileCrossServiceNameCollision(t *testing.T) {
	// Service "my" with port "service-http" renders the same slice name as
	// service "my-service" with port "http": inherent to concatenated names,
	// so it must surface as an error rather than two controllers fighting.
	other := testService(map[string]string{testGroupKey: "other-group"}, true)
	other.Name = "my"
	other.UID = types.UID("other-uid")
	other.Spec.Ports = []corev1.ServicePort{
		{Name: "service-http", Port: 8080, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt32(8080)},
	}

	r, c := newReconciler(t, testConfig(), append(testObjects(), other)...)
	recorder := record.NewFakeRecorder(16)
	r.recorder = recorder

	reconcileService(t, r) // my-service claims my-service-http

	_, err := r.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: "my"},
	})
	require.ErrorContains(t, err, "already belongs to service \"my-service\"")

	// The established slice is untouched and an event flags the loser.
	require.Equal(t, "my-service", getSlice(t, c, "my-service-http").Labels[discoveryv1.LabelServiceName])
	select {
	case event := <-recorder.Events:
		require.Contains(t, event, "EndpointSliceNameCollision")
	default:
		t.Fatal("expected a collision event on the service")
	}
}

func TestReconcileCleansUpNativeArtifacts(t *testing.T) {
	nativeSlice := func() *discoveryv1.EndpointSlice {
		return &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-service-abc12",
				Namespace: testNamespace,
				Labels: map[string]string{
					discoveryv1.LabelServiceName: "my-service",
					discoveryv1.LabelManagedBy:   "endpointslice-controller.k8s.io",
				},
			},
			AddressType: discoveryv1.AddressTypeIPv4,
		}
	}
	mirrorSlice := func() *discoveryv1.EndpointSlice {
		return &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-service-def34",
				Namespace: testNamespace,
				Labels: map[string]string{
					discoveryv1.LabelServiceName: "my-service",
					discoveryv1.LabelManagedBy:   "endpointslicemirroring-controller.k8s.io",
				},
			},
			AddressType: discoveryv1.AddressTypeIPv4,
		}
	}
	legacyEndpoints := func() *corev1.Endpoints {
		return &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Name: "my-service", Namespace: testNamespace}}
	}
	getEndpoints := func(t *testing.T, c client.Client) error {
		t.Helper()
		var endpoints corev1.Endpoints
		return c.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: "my-service"}, &endpoints)
	}

	t.Run("selectorless service takes over", func(t *testing.T) {
		r, c := newReconciler(t, testConfig(), append(testObjects(), nativeSlice(), mirrorSlice(), legacyEndpoints())...)
		recorder := record.NewFakeRecorder(16)
		r.recorder = recorder
		reconcileService(t, r)

		// Stale native slices and the legacy Endpoints object are removed...
		requireNoSlice(t, c, "my-service-abc12")
		require.True(t, apierrors.IsNotFound(getEndpoints(t, c)))
		// ...while mirrored slices are left to garbage collection via their
		// owner reference to the deleted Endpoints object.
		getSlice(t, c, "my-service-def34")
		// Our own slices publish as usual.
		require.Equal(t, []string{"10.0.0.11", "10.0.0.12", "10.0.0.13"},
			sliceAddresses(getSlice(t, c, "my-service-http")))

		select {
		case event := <-recorder.Events:
			require.Contains(t, event, "NativeEndpointsCleanedUp")
		default:
			t.Fatal("expected a cleanup event on the service")
		}
	})

	t.Run("no stale slices means no endpoints deletion", func(t *testing.T) {
		// Without native or mirrored slices as evidence, a lone Endpoints
		// object is left alone (nothing is resurrecting endpoints from it
		// that kube-proxy would consume, and it may be hand-managed).
		r, c := newReconciler(t, testConfig(), append(testObjects(), legacyEndpoints())...)
		reconcileService(t, r)
		require.NoError(t, getEndpoints(t, c))
	})

	t.Run("disabled leaves everything", func(t *testing.T) {
		cfg := testConfig()
		cfg.DisableNativeCleanup = true

		r, c := newReconciler(t, cfg, append(testObjects(), nativeSlice(), legacyEndpoints())...)
		reconcileService(t, r)
		getSlice(t, c, "my-service-abc12")
		require.NoError(t, getEndpoints(t, c))
	})

	t.Run("services with selectors are not touched", func(t *testing.T) {
		objs := testObjects()
		objs[0].(*corev1.Service).Spec.Selector = map[string]string{"app": "legacy"}

		r, c := newReconciler(t, testConfig(), append(objs, nativeSlice(), legacyEndpoints())...)
		reconcileService(t, r)
		getSlice(t, c, "my-service-abc12")
		require.NoError(t, getEndpoints(t, c))
	})

	t.Run("defers takeover until endpoints are published", func(t *testing.T) {
		// With a membership check excluding everything (e.g. a probe that
		// can't reach the pod network), deleting the native artifacts would
		// black the Service out; takeover must wait.
		healthy := false
		cfg := testConfig()
		cfg.Membership = CheckerFunc(func(context.Context, *corev1.Service, *corev1.Pod, Port) bool { return healthy })

		r, c := newReconciler(t, cfg, append(testObjects(), nativeSlice(), legacyEndpoints())...)
		reconcileService(t, r)
		getSlice(t, c, "my-service-abc12")
		require.NoError(t, getEndpoints(t, c))

		healthy = true
		reconcileService(t, r)
		requireNoSlice(t, c, "my-service-abc12")
		require.True(t, apierrors.IsNotFound(getEndpoints(t, c)))
	})

	t.Run("unmanaged services keep their native artifacts", func(t *testing.T) {
		objs := append(testObjects(), nativeSlice(), legacyEndpoints())
		objs[0].(*corev1.Service).Annotations = nil

		r, c := newReconciler(t, testConfig(), objs...)
		reconcileService(t, r)
		getSlice(t, c, "my-service-abc12")
		require.NoError(t, getEndpoints(t, c))
	})

	t.Run("failed endpoints deletion is retried, even across restarts", func(t *testing.T) {
		mapper, err := New(testConfig())
		require.NoError(t, err)

		// Fail the first Endpoints delete. The Endpoints object goes first,
		// so the stale native slices must survive the failed pass as the
		// evidence that re-derives the obligation -- even for a brand-new
		// reconciler with no memory of the first attempt.
		failures := 1
		c := fake.NewClientBuilder().
			WithScheme(clientgoscheme.Scheme).
			WithObjects(append(testObjects(), nativeSlice(), legacyEndpoints())...).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if _, ok := obj.(*corev1.Endpoints); ok && failures > 0 {
						failures--
						return apierrors.NewInternalError(context.DeadlineExceeded)
					}
					return cl.Delete(ctx, obj, opts...)
				},
			}).
			Build()
		r := &reconciler{client: c, scheme: clientgoscheme.Scheme, cfg: mapper.cfg}

		_, err = r.Reconcile(t.Context(), serviceRequest())
		require.Error(t, err)
		getSlice(t, c, "my-service-abc12")
		require.NoError(t, getEndpoints(t, c), "evidence and endpoints must both survive the failed pass")

		// Simulate a controller restart: fresh reconciler, no state.
		restarted := &reconciler{client: c, scheme: clientgoscheme.Scheme, cfg: mapper.cfg}
		reconcileService(t, restarted)
		require.True(t, apierrors.IsNotFound(getEndpoints(t, c)))
		requireNoSlice(t, c, "my-service-abc12")
	})
}

func TestReconcileForeignManagerSameService(t *testing.T) {
	// A same-named slice owner-referenced to the SAME Service but claimed by
	// another manager (a second Mapper, or the native controller) must be
	// surfaced as a collision, not silently hijacked via the owner reference.
	foreign := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service-http",
			Namespace: testNamespace,
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "my-service",
				discoveryv1.LabelManagedBy:   "some-other-mapper",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "v1",
				Kind:       "Service",
				Name:       "my-service",
				UID:        types.UID("svc-uid"),
				Controller: ptr.To(true),
			}},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
	}

	r, c := newReconciler(t, testConfig(), append(testObjects(), foreign)...)
	_, err := r.Reconcile(t.Context(), serviceRequest())
	require.ErrorContains(t, err, "already belongs")
	require.Equal(t, "some-other-mapper", getSlice(t, c, "my-service-http").Labels[discoveryv1.LabelManagedBy])
}

func TestReconcileSyncsDuringServiceDeletion(t *testing.T) {
	// Finalizers can hold a Service Terminating for a while; endpoints must
	// stay accurate until it is actually gone.
	objs := testObjects()
	svc := objs[0].(*corev1.Service)
	svc.DeletionTimestamp = ptr.To(metav1.Now())
	svc.Finalizers = []string{"port-mapper.example.com/test"}

	r, c := newReconciler(t, testConfig(), objs...)
	reconcileService(t, r)
	require.Equal(t, []string{"10.0.0.12", "10.0.0.13", "10.0.0.14"},
		sliceAddresses(getSlice(t, c, "my-service-https")))

	require.NoError(t, c.Delete(t.Context(), testPod("pod-d", "", nil, true)))
	reconcileService(t, r)
	require.Equal(t, []string{"10.0.0.12", "10.0.0.13"},
		sliceAddresses(getSlice(t, c, "my-service-https")))
}

func TestReconcileResolvesSidecarTargetPorts(t *testing.T) {
	svc := testService(map[string]string{testGroupKey: "my-group"}, true)
	svc.Spec.Ports = svc.Spec.Ports[1:]
	svc.Spec.Ports[0].TargetPort = intstr.FromString("tls")

	// The named port is exposed by a restartable init (sidecar) container.
	sidecar := testPod("pod-a", "10.0.0.11", map[string]string{testGroupKey: "my-group", testPortsKey: "https"}, true)
	sidecar.Spec.InitContainers = []corev1.Container{{
		Name:          "proxy",
		RestartPolicy: ptr.To(corev1.ContainerRestartPolicyAlways),
		Ports:         []corev1.ContainerPort{{Name: "tls", ContainerPort: 8443}},
	}}
	// Plain (non-restartable) init containers don't count.
	plainInit := testPod("pod-b", "10.0.0.12", map[string]string{testGroupKey: "my-group", testPortsKey: "https"}, true)
	plainInit.Spec.InitContainers = []corev1.Container{{
		Name:  "setup",
		Ports: []corev1.ContainerPort{{Name: "tls", ContainerPort: 8443}},
	}}

	r, c := newReconciler(t, testConfig(), svc, sidecar, plainInit)
	reconcileService(t, r)

	require.Equal(t, []string{"10.0.0.11"}, sliceAddresses(getSlice(t, c, "my-service-https--p8443")))
}

func TestMetrics(t *testing.T) {
	r, c := newReconciler(t, testConfig(), testObjects()...)
	reconcileService(t, r)

	published := func(port string) float64 {
		return testutil.ToFloat64(endpointsPublished.WithLabelValues(testNamespace, "my-service", port, "IPv4"))
	}
	require.Equal(t, 3.0, published("http"))
	require.Equal(t, 3.0, published("https"))
	require.Equal(t, 2.0, testutil.ToFloat64(slicesManaged.WithLabelValues(testNamespace, "my-service")))
	require.Positive(t, testutil.ToFloat64(membershipChecks.WithLabelValues(testNamespace, "my-service", "include")))
	require.Positive(t, testutil.ToFloat64(membershipChecks.WithLabelValues(testNamespace, "my-service", "exclude")))

	// Unmanaging the Service clears its series.
	var svc corev1.Service
	require.NoError(t, c.Get(t.Context(), serviceRequest().NamespacedName, &svc))
	svc.Annotations = nil
	require.NoError(t, c.Update(t.Context(), &svc))
	reconcileService(t, r)

	require.Zero(t, published("http"))
	require.Zero(t, testutil.ToFloat64(slicesManaged.WithLabelValues(testNamespace, "my-service")))
}

func TestReconcileChecksConcurrently(t *testing.T) {
	// Track how many checks overlap rather than asserting on wall time,
	// which flakes on loaded machines.
	var calls, inFlight, maxInFlight atomic.Int32
	cfg := testConfig()
	cfg.Membership = CheckerFunc(func(context.Context, *corev1.Service, *corev1.Pod, Port) bool {
		calls.Add(1)
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			observed := maxInFlight.Load()
			if current <= observed || maxInFlight.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond) // give checks a chance to overlap
		return true
	})

	r, c := newReconciler(t, cfg, testObjects()...)
	reconcileService(t, r)

	require.Equal(t, int32(8), calls.Load(), "4 pods x 2 ports")
	require.Greater(t, maxInFlight.Load(), int32(1), "checks should fan out concurrently")
	require.Equal(t, []string{"10.0.0.11", "10.0.0.12", "10.0.0.13", "10.0.0.14"},
		sliceAddresses(getSlice(t, c, "my-service-http")))
}

func TestReconcileUsesFieldIndexes(t *testing.T) {
	// Annotation-key alignment served by cache field indexes, the same shape
	// SetupWithManager registers on a real manager.
	mapper, err := New(testConfig())
	require.NoError(t, err)

	const podIndex, serviceIndex = "test/pod-key", "test/service-key"
	c := fake.NewClientBuilder().
		WithScheme(clientgoscheme.Scheme).
		WithIndex(&corev1.Pod{}, podIndex, mapper.cfg.PodKey.indexerFunc()).
		WithIndex(&corev1.Service{}, serviceIndex, mapper.cfg.ServiceKey.indexerFunc()).
		WithObjects(testObjects()...).
		Build()
	r := &reconciler{client: c, scheme: clientgoscheme.Scheme, cfg: mapper.cfg, podIndex: podIndex, serviceIndex: serviceIndex}

	reconcileService(t, r)
	require.Equal(t, []string{"10.0.0.11", "10.0.0.12", "10.0.0.13"},
		sliceAddresses(getSlice(t, c, "my-service-http")))

	requests := r.mapPodToServices(t.Context(),
		testPod("pod-a", "10.0.0.11", map[string]string{testGroupKey: "my-group"}, true))
	require.Equal(t, []ctrl.Request{serviceRequest()}, requests)
}

func TestReconcileMissingService(t *testing.T) {
	r, _ := newReconciler(t, testConfig())
	result := reconcileService(t, r)
	require.Zero(t, result)
}

func TestReconcileSkipsUnroutablePods(t *testing.T) {
	noIP := testPod("pod-b", "", map[string]string{testGroupKey: "my-group", testPortsKey: "http"}, true)
	// IPv6-only pod on a single-stack IPv4 service has no publishable address.
	ipv6 := testPod("pod-c", "fd00::1", map[string]string{testGroupKey: "my-group", testPortsKey: "http"}, true)
	failed := testPod("pod-d", "10.0.0.14", map[string]string{testGroupKey: "my-group", testPortsKey: "http"}, true)
	failed.Status.Phase = corev1.PodFailed

	r, c := newReconciler(t, testConfig(),
		testService(map[string]string{testGroupKey: "my-group"}, true),
		testPod("pod-a", "10.0.0.11", map[string]string{testGroupKey: "my-group", testPortsKey: "http"}, true),
		noIP, ipv6, failed,
	)
	reconcileService(t, r)

	require.Equal(t, []string{"10.0.0.11"}, sliceAddresses(getSlice(t, c, "my-service-http")))
}

func TestMapPodToServices(t *testing.T) {
	other := testService(map[string]string{testGroupKey: "my-group"}, true)
	other.Name = "my-other-service"
	other.UID = types.UID("other-uid")
	unrelated := testService(map[string]string{testGroupKey: "unrelated"}, true)
	unrelated.Name = "unrelated-service"
	unrelated.UID = types.UID("unrelated-uid")

	r, _ := newReconciler(t, testConfig(), append(testObjects(), other, unrelated)...)

	requests := r.mapPodToServices(t.Context(), testPod("pod-a", "10.0.0.11", map[string]string{testGroupKey: "my-group"}, true))
	require.ElementsMatch(t, []ctrl.Request{
		{NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: "my-service"}},
		{NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: "my-other-service"}},
	}, requests)

	require.Empty(t, r.mapPodToServices(t.Context(), testPod("pod-x", "10.0.0.15", nil, true)))
}

func TestNewValidation(t *testing.T) {
	base := testConfig()

	for name, mutate := range map[string]func(*Config){
		"reserved managed-by":     func(c *Config) { c.ManagedBy = "endpointslice-controller.k8s.io" },
		"invalid managed-by":      func(c *Config) { c.ManagedBy = "not a valid label value!" },
		"missing service key":     func(c *Config) { c.ServiceKey = Key{} },
		"invalid key kind":        func(c *Config) { c.ServiceKey = Key{Kind: KeyKind("bogus"), Name: "x"} },
		"invalid pod key name":    func(c *Config) { c.PodKey = AnnotationKey("!!") },
		"negative max endpoints":  func(c *Config) { c.MaxEndpointsPerSlice = -1 },
		"excessive max endpoints": func(c *Config) { c.MaxEndpointsPerSlice = 1001 },
		"negative max checks":     func(c *Config) { c.MaxConcurrentChecks = -1 },
		"negative max reconciles": func(c *Config) { c.MaxConcurrentReconciles = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			_, err := New(cfg)
			require.Error(t, err)
		})
	}

	mapper, err := New(Config{ServiceKey: LabelKey("group")})
	require.NoError(t, err)
	require.Equal(t, DefaultManagedBy, mapper.cfg.ManagedBy)
	require.Equal(t, LabelKey("group"), mapper.cfg.PodKey)
	require.Equal(t, DefaultResyncPeriod, mapper.cfg.ResyncPeriod)
	require.Equal(t, DefaultMaxEndpointsPerSlice, mapper.cfg.MaxEndpointsPerSlice)
	require.Equal(t, DefaultMaxConcurrentChecks, mapper.cfg.MaxConcurrentChecks)
	require.NotNil(t, mapper.cfg.Membership)
}

func TestControllerNames(t *testing.T) {
	name := func(managedBy string) string {
		mapper, err := New(Config{ServiceKey: LabelKey("group"), ManagedBy: managedBy})
		require.NoError(t, err)
		return mapper.controllerName()
	}

	// Clean values pass through untouched...
	require.Equal(t, "port-mapper-my-controller", name("my-controller"))
	// ...while values that sanitize lossily must not collide.
	require.NotEqual(t, name("team.mapper"), name("team_mapper"))
}

func TestReconcileAbstain(t *testing.T) {
	// A scripted decider keyed by "<pod>/<port>"; unlisted pairs exclude.
	decisions := map[string]Decision{}
	set := func(entries map[string]Decision) {
		clear(decisions)
		maps.Copy(decisions, entries)
	}

	cfg := testConfig()
	cfg.Membership = DeciderFunc(func(_ context.Context, _ *corev1.Service, pod *corev1.Pod, port Port) Decision {
		return decisions[pod.Name+"/"+port.Name]
	})

	r, c := newReconciler(t, cfg, testObjects()...)

	// Establish the canonical memberships.
	set(map[string]Decision{
		"pod-a/http": Include, "pod-b/http": Include, "pod-c/http": Include,
		"pod-b/https": Include, "pod-c/https": Include, "pod-d/https": Include,
	})
	reconcileService(t, r)
	require.Equal(t, []string{"10.0.0.11", "10.0.0.12", "10.0.0.13"}, sliceAddresses(getSlice(t, c, "my-service-http")))
	require.Equal(t, []string{"10.0.0.12", "10.0.0.13", "10.0.0.14"}, sliceAddresses(getSlice(t, c, "my-service-https")))

	// A blanket abstention -- e.g. the controller lost pod-network access --
	// keeps exactly the previously published memberships: published pods stay
	// in, never-published pods (pod-d/http, pod-a/https) stay out.
	blanket := map[string]Decision{}
	for _, pod := range []string{"pod-a", "pod-b", "pod-c", "pod-d"} {
		blanket[pod+"/http"] = Abstain
		blanket[pod+"/https"] = Abstain
	}
	set(blanket)
	reconcileService(t, r)
	require.Equal(t, []string{"10.0.0.11", "10.0.0.12", "10.0.0.13"}, sliceAddresses(getSlice(t, c, "my-service-http")))
	require.Equal(t, []string{"10.0.0.12", "10.0.0.13", "10.0.0.14"}, sliceAddresses(getSlice(t, c, "my-service-https")))

	// Definitive decisions still win while others abstain.
	set(map[string]Decision{
		"pod-a/http": Abstain, "pod-b/http": Abstain, "pod-c/http": Abstain,
		"pod-a/https": Include,                         // joins
		"pod-b/https": Exclude,                         // drops
		"pod-c/https": Abstain, "pod-d/https": Abstain, // hold
	})
	reconcileService(t, r)
	require.Equal(t, []string{"10.0.0.11", "10.0.0.12", "10.0.0.13"}, sliceAddresses(getSlice(t, c, "my-service-http")))
	require.Equal(t, []string{"10.0.0.11", "10.0.0.13", "10.0.0.14"}, sliceAddresses(getSlice(t, c, "my-service-https")))
}

func TestDecisions(t *testing.T) {
	ctx := context.Background()
	pod := testPod("pod-a", "10.0.0.11", nil, true)
	httpPort := Port{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP}

	yes := CheckerFunc(func(context.Context, *corev1.Service, *corev1.Pod, Port) bool { return true })
	no := CheckerFunc(func(context.Context, *corev1.Service, *corev1.Pod, Port) bool { return false })
	abstain := DeciderFunc(func(context.Context, *corev1.Service, *corev1.Pod, Port) Decision { return Abstain })

	// Plain checkers map onto Include/Exclude; deciders pass through.
	require.Equal(t, Include, DecisionFor(ctx, yes, nil, pod, httpPort))
	require.Equal(t, Exclude, DecisionFor(ctx, no, nil, pod, httpPort))
	require.Equal(t, Abstain, DecisionFor(ctx, abstain, nil, pod, httpPort))

	// The boolean collapse treats anything but Include as false.
	require.False(t, abstain.Check(ctx, nil, pod, httpPort))
	require.True(t, DeciderFunc(func(context.Context, *corev1.Service, *corev1.Pod, Port) Decision { return Include }).Check(ctx, nil, pod, httpPort))

	// All: Exclude dominates, then Abstain, then Include.
	require.Equal(t, Abstain, DecisionFor(ctx, All(yes, abstain), nil, pod, httpPort))
	require.Equal(t, Exclude, DecisionFor(ctx, All(abstain, no), nil, pod, httpPort))
	require.Equal(t, Include, DecisionFor(ctx, All(yes, yes), nil, pod, httpPort))

	// PerPort preserves abstentions from routed checkers and fallbacks.
	perPort := PerPort(map[string]Checker{"http": abstain}, no)
	require.Equal(t, Abstain, DecisionFor(ctx, perPort, nil, pod, httpPort))
	require.Equal(t, Exclude, DecisionFor(ctx, perPort, nil, pod, Port{Name: "https"}))

	require.Equal(t, "Include", Include.String())
	require.Equal(t, "Exclude", Exclude.String())
	require.Equal(t, "Abstain", Abstain.String())
}

func TestStable(t *testing.T) {
	ctx := context.Background()
	port := Port{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP}

	// scripted inner checker: reports whatever `healthy` currently says.
	healthy := true
	inner := CheckerFunc(func(context.Context, *corev1.Service, *corev1.Pod, Port) bool { return healthy })

	newPod := func(name string) *corev1.Pod { return testPod(name, "10.0.0.11", nil, true) }

	t.Run("seeds from the first observation", func(t *testing.T) {
		stable := Stable(inner, 3, 3)

		healthy = true
		require.True(t, stable.Check(ctx, nil, newPod("pod-up"), port))
		healthy = false
		require.False(t, stable.Check(ctx, nil, newPod("pod-down"), port))
		// ...and each key's state is independent.
		healthy = true
		require.True(t, stable.Check(ctx, nil, newPod("pod-up"), port))
	})

	t.Run("requires consecutive failures to evict", func(t *testing.T) {
		stable := Stable(inner, 1, 3)
		pod := newPod("pod-a")

		healthy = true
		require.True(t, stable.Check(ctx, nil, pod, port))

		healthy = false
		require.True(t, stable.Check(ctx, nil, pod, port), "streak 1 of 3")
		require.True(t, stable.Check(ctx, nil, pod, port), "streak 2 of 3")
		require.False(t, stable.Check(ctx, nil, pod, port), "streak 3 evicts")
	})

	t.Run("requires consecutive successes to admit", func(t *testing.T) {
		stable := Stable(inner, 2, 1)
		pod := newPod("pod-a")

		healthy = false
		require.False(t, stable.Check(ctx, nil, pod, port))

		healthy = true
		require.False(t, stable.Check(ctx, nil, pod, port), "streak 1 of 2")
		require.True(t, stable.Check(ctx, nil, pod, port), "streak 2 admits")
	})

	t.Run("a contrary streak resets on agreement", func(t *testing.T) {
		stable := Stable(inner, 1, 2)
		pod := newPod("pod-a")

		healthy = true
		require.True(t, stable.Check(ctx, nil, pod, port))
		for range 3 {
			// Flapping between healthy and unhealthy never reaches the
			// failure threshold.
			healthy = false
			require.True(t, stable.Check(ctx, nil, pod, port))
			healthy = true
			require.True(t, stable.Check(ctx, nil, pod, port))
		}
	})

	t.Run("thresholds below one are clamped", func(t *testing.T) {
		stable := Stable(inner, 0, 0)
		pod := newPod("pod-a")

		healthy = true
		require.True(t, stable.Check(ctx, nil, pod, port))
		healthy = false
		require.False(t, stable.Check(ctx, nil, pod, port))
	})

	t.Run("prunes idle state", func(t *testing.T) {
		stable := Stable(inner, 1, 1).(*stableChecker)
		clock := time.Now()
		stable.now = func() time.Time { return clock }

		healthy = true
		stable.Check(ctx, nil, newPod("pod-a"), port)
		require.Len(t, stable.states, 1)

		clock = clock.Add(stableStateIdleExpiry + stableSweepInterval + time.Second)
		stable.Check(ctx, nil, newPod("pod-b"), port)
		require.Len(t, stable.states, 1, "pod-a's idle state should have been swept")
	})

	t.Run("distinguishes ports on the same pod", func(t *testing.T) {
		stable := Stable(inner, 1, 2).(*stableChecker)
		pod := newPod("pod-a")

		healthy = true
		require.True(t, stable.Check(ctx, nil, pod, port))
		healthy = false
		require.False(t, stable.Check(ctx, nil, pod, Port{Name: "https", Port: 8443}))
		require.Len(t, stable.states, 2)
	})

	t.Run("distinguishes addresses on the same port", func(t *testing.T) {
		// Dual-stack Services evaluate each family's address separately;
		// their streaks must not share (and thereby halve) the thresholds.
		stable := Stable(inner, 1, 2).(*stableChecker)
		pod := newPod("pod-a")

		healthy = true
		v4 := Port{Name: "http", Port: 8080, Address: "10.0.0.11"}
		require.True(t, stable.Check(ctx, nil, pod, v4))

		healthy = false
		v6 := Port{Name: "http", Port: 8080, Address: "fd00::11"}
		require.False(t, stable.Check(ctx, nil, pod, v6), "fresh key seeds from observation")
		require.True(t, stable.Check(ctx, nil, pod, v4), "v4 streak unaffected by v6: 1 of 2")
		require.Len(t, stable.states, 2)
	})

	t.Run("holds state through abstentions", func(t *testing.T) {
		decision := Include
		abstaining := DeciderFunc(func(context.Context, *corev1.Service, *corev1.Pod, Port) Decision { return decision })
		stable := Stable(abstaining, 1, 2)
		pod := newPod("pod-a")

		require.Equal(t, Include, DecisionFor(ctx, stable, nil, pod, port))

		// Abstentions freeze an in-flight eviction streak rather than
		// resetting it.
		decision = Exclude
		require.Equal(t, Include, DecisionFor(ctx, stable, nil, pod, port), "streak 1 of 2")
		decision = Abstain
		require.Equal(t, Include, DecisionFor(ctx, stable, nil, pod, port), "no observation: hold")
		decision = Exclude
		require.Equal(t, Exclude, DecisionFor(ctx, stable, nil, pod, port), "streak 2 evicts")

		decision = Abstain
		require.Equal(t, Exclude, DecisionFor(ctx, stable, nil, pod, port), "hold the excluded state")
	})

	t.Run("propagates abstention before state is established", func(t *testing.T) {
		abstaining := DeciderFunc(func(context.Context, *corev1.Service, *corev1.Pod, Port) Decision { return Abstain })
		stable := Stable(abstaining, 1, 1)

		require.Equal(t, Abstain, DecisionFor(ctx, stable, nil, newPod("pod-new"), port))
		// The boolean view collapses that to exclusion.
		require.False(t, stable.Check(ctx, nil, newPod("pod-new"), port))
	})
}

func TestCheckers(t *testing.T) {
	ctx := context.Background()
	pod := testPod("pod-a", "10.0.0.11", map[string]string{testPortsKey: "http, https"}, true)
	httpPort := Port{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP}

	yes := CheckerFunc(func(context.Context, *corev1.Service, *corev1.Pod, Port) bool { return true })
	no := CheckerFunc(func(context.Context, *corev1.Service, *corev1.Pod, Port) bool { return false })

	t.Run("port names", func(t *testing.T) {
		checker := PortNames(AnnotationKey(testPortsKey))
		require.True(t, checker.Check(ctx, nil, pod, httpPort))
		// Whitespace around entries is tolerated.
		require.True(t, checker.Check(ctx, nil, pod, Port{Name: "https"}))
		require.False(t, checker.Check(ctx, nil, pod, Port{Name: "admin"}))
		require.False(t, checker.Check(ctx, nil, testPod("pod-b", "10.0.0.12", nil, true), httpPort))

		// Empty segments (trailing commas) never match unnamed ports.
		trailing := testPod("pod-t", "10.0.0.13", map[string]string{testPortsKey: "http,"}, true)
		require.False(t, checker.Check(ctx, nil, trailing, Port{Name: ""}))
	})

	t.Run("pod ready", func(t *testing.T) {
		require.True(t, PodReady().Check(ctx, nil, pod, httpPort))

		unready := pod.DeepCopy()
		unready.Status.Conditions[0].Status = corev1.ConditionFalse
		require.False(t, PodReady().Check(ctx, nil, unready, httpPort))

		unknown := pod.DeepCopy()
		unknown.Status.Conditions = nil
		require.False(t, PodReady().Check(ctx, nil, unknown, httpPort))
	})

	t.Run("not terminating", func(t *testing.T) {
		require.True(t, NotTerminating().Check(ctx, nil, pod, httpPort))

		terminating := pod.DeepCopy()
		terminating.DeletionTimestamp = ptr.To(metav1.Now())
		require.False(t, NotTerminating().Check(ctx, nil, terminating, httpPort))
	})

	t.Run("per port", func(t *testing.T) {
		checker := PerPort(map[string]Checker{"http": yes, "https": no}, nil)
		require.True(t, checker.Check(ctx, nil, pod, httpPort))
		require.False(t, checker.Check(ctx, nil, pod, Port{Name: "https"}))
		// Unconfigured ports hit the fallback, defaulting to exclusion.
		require.False(t, checker.Check(ctx, nil, pod, Port{Name: "admin"}))
		require.True(t, PerPort(map[string]Checker{}, yes).Check(ctx, nil, pod, Port{Name: "admin"}))
	})

	t.Run("all", func(t *testing.T) {
		require.True(t, All(yes, yes).Check(ctx, nil, pod, httpPort))
		require.False(t, All(yes, no).Check(ctx, nil, pod, httpPort))
		require.True(t, All().Check(ctx, nil, pod, httpPort))
	})

	t.Run("network checkers", func(t *testing.T) {
		logCtx, lines := capturingContext(ctx, 1)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		parsed, err := url.Parse(server.URL)
		require.NoError(t, err)
		number, err := strconv.Atoi(parsed.Port())
		require.NoError(t, err)

		local := testPod("pod-local", parsed.Hostname(), nil, true)
		port := Port{Name: "http", Port: int32(number), Protocol: corev1.ProtocolTCP}
		udp := Port{Name: "dns", Port: int32(number), Protocol: corev1.ProtocolUDP}

		require.True(t, HTTPGet("healthz", time.Second).Check(logCtx, nil, local, port))
		require.False(t, HTTPGet("/broken", time.Second).Check(logCtx, nil, local, port))
		require.False(t, HTTPGet("/healthz", time.Second).Check(logCtx, nil, local, udp))
		require.True(t, TCPDial(time.Second).Check(logCtx, nil, local, port))
		require.False(t, TCPDial(time.Second).Check(logCtx, nil, local, udp))

		// Probes target the address under evaluation (Port.Address) when
		// set, falling back to the pod's primary IP.
		noIP := testPod("pod-via", "", nil, true)
		viaAddress := Port{Name: "http", Port: int32(number), Protocol: corev1.ProtocolTCP, Address: parsed.Hostname()}
		require.True(t, TCPDial(time.Second).Check(logCtx, nil, noIP, viaAddress))
		require.True(t, HTTPGet("/healthz", time.Second).Check(logCtx, nil, noIP, viaAddress))

		server.Close()
		require.False(t, TCPDial(time.Second).Check(logCtx, nil, local, port))
		require.False(t, HTTPGet("/healthz", time.Second).Check(logCtx, nil, local, port))

		// Probe failures surface the swallowed error at V(1).
		joined := strings.Join(*lines, "\n")
		require.Contains(t, joined, "non-2xx probe response")
		require.Contains(t, joined, "excluding pod: dial failed")
		require.Contains(t, joined, "excluding pod: probe failed")
		// Non-TCP refusals only log at V(2).
		require.NotContains(t, joined, "non-TCP port")
	})

	t.Run("logs exclusion decisions", func(t *testing.T) {
		logCtx, lines := capturingContext(ctx, 2)

		unready := pod.DeepCopy()
		unready.Status.Conditions[0].Status = corev1.ConditionFalse
		require.False(t, PodReady().Check(logCtx, nil, unready, httpPort))

		terminating := pod.DeepCopy()
		terminating.DeletionTimestamp = ptr.To(metav1.Now())
		require.False(t, NotTerminating().Check(logCtx, nil, terminating, httpPort))

		require.False(t, PortNames(AnnotationKey(testPortsKey)).Check(logCtx, nil, pod, Port{Name: "admin"}))
		require.False(t, PortNames(AnnotationKey("absent")).Check(logCtx, nil, pod, httpPort))
		require.False(t, PerPort(nil, nil).Check(logCtx, nil, pod, httpPort))

		joined := strings.Join(*lines, "\n")
		require.Contains(t, joined, "excluding pod: not ready")
		require.Contains(t, joined, "excluding pod: terminating")
		require.Contains(t, joined, "excluding pod: port not listed")
		require.Contains(t, joined, "excluding pod: ports key absent")
		require.Contains(t, joined, "no checker configured for port")
		// Decisions carry the deciding checker and subject pod.
		require.Contains(t, joined, `"checker"="PodReady"`)
		require.Contains(t, joined, "pod-a")

		// Passing checks log nothing.
		before := len(*lines)
		require.True(t, PodReady().Check(logCtx, nil, pod, httpPort))
		require.True(t, PortNames(AnnotationKey(testPortsKey)).Check(logCtx, nil, pod, httpPort))
		require.Len(t, *lines, before)
	})
}
