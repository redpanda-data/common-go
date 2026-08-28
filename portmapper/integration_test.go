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

package portmapper_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crconfig "sigs.k8s.io/controller-runtime/pkg/config"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/redpanda-data/common-go/portmapper"
)

const (
	k3sImage = "rancher/k3s:v1.31.5-k3s1"

	itGroupKey      = "port-mapper.example.com/group"
	itPortsKey      = "port-mapper.example.com/ports"
	itManagedBy     = "port-mapper-integration"
	nativeManagedBy = "endpointslice-controller.k8s.io"
	itZone          = "test-zone-a"
)

// TestIntegration runs the controller against a real single-node k3s cluster
// (the same testcontainers harness common-go's kube tests use for
// full-cluster coverage), exercising real pod IPs and readiness, server-side
// apply against a real API server, garbage collection via owner references,
// and coexistence with the native EndpointSlice controller.
//
// It requires Docker and is skipped in -short mode.
func TestIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker; skipped in -short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	t.Cleanup(cancel)

	container, err := k3s.Run(ctx, k3sImage)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	kubeconfig, err := container.GetKubeConfig(ctx)
	require.NoError(t, err)
	restcfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	require.NoError(t, err)

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	c, err := client.New(restcfg, client.Options{Scheme: scheme})
	require.NoError(t, err)

	// Label the single k3s node with a zone so topology publishing is
	// exercised end to end.
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		var nodes corev1.NodeList
		if !assert.NoError(collect, c.List(ctx, &nodes)) || !assert.NotEmpty(collect, nodes.Items) {
			return
		}
		node := &nodes.Items[0]
		patch := client.MergeFrom(node.DeepCopy())
		node.Labels[corev1.LabelTopologyZone] = itZone
		assert.NoError(collect, c.Patch(ctx, node, patch))
	}, time.Minute, time.Second, "node never became labelable")

	logger := testr.NewWithOptions(t, testr.Options{Verbosity: 2})
	mgr, err := ctrl.NewManager(restcfg, ctrl.Options{
		Scheme:     scheme,
		Metrics:    metricsserver.Options{BindAddress: "0"},
		Logger:     logger,
		Controller: crconfig.Controller{Logger: logger},
		BaseContext: func() context.Context {
			return crlog.IntoContext(context.Background(), logger)
		},
	})
	require.NoError(t, err)

	mapper, err := portmapper.New(portmapper.Config{
		ManagedBy:  itManagedBy,
		ServiceKey: portmapper.AnnotationKey(itGroupKey),
		Membership: portmapper.All(
			portmapper.PodReady(),
			portmapper.PortNames(portmapper.AnnotationKey(itPortsKey)),
		),
		ResyncPeriod: 2 * time.Second,
	})
	require.NoError(t, err)
	require.NoError(t, mapper.SetupWithManager(mgr))

	managerCtx, stopManager := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		if err := mgr.Start(managerCtx); err != nil {
			t.Errorf("manager exited with error: %v", err)
		}
	}()
	t.Cleanup(func() {
		stopManager()
		<-stopped
	})

	t.Run("lifecycle", func(t *testing.T) {
		t.Parallel()
		testLifecycle(t, ctx, c)
	})
	t.Run("selector migration", func(t *testing.T) {
		t.Parallel()
		testSelectorMigration(t, ctx, c)
	})
}

// testLifecycle walks a Service that was born selectorless through the whole
// port-mapper lifecycle: per-port publishing with topology, membership
// changes, repair of tampered slices, pod drain, marker removal, and finally
// real owner-reference garbage collection.
func testLifecycle(t *testing.T, ctx context.Context, c client.Client) {
	const namespace = "pm-lifecycle"
	require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}))

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "my-service",
			Namespace:   namespace,
			Annotations: map[string]string{itGroupKey: "lifecycle-group"},
		},
		Spec: corev1.ServiceSpec{
			// Selectorless on purpose: port-mapper publishes the slices.
			TrafficDistribution: ptr.To(corev1.ServiceTrafficDistributionPreferClose),
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP},
				{Name: "https", Port: 8443, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	require.NoError(t, c.Create(ctx, svc))

	for name, ports := range map[string]string{
		"pod-a": "http",
		"pod-b": "http,https",
		"pod-c": "http,https",
		"pod-d": "https",
	} {
		annotations := map[string]string{itGroupKey: "lifecycle-group", itPortsKey: ports}
		require.NoError(t, c.Create(ctx, integrationPod(namespace, name, nil, annotations)))
	}
	ips := waitForPodIPs(t, ctx, c, namespace, "pod-a", "pod-b", "pod-c", "pod-d")

	expectAddresses(t, ctx, c, namespace, "my-service-http", ips, "pod-a", "pod-b", "pod-c")
	expectAddresses(t, ctx, c, namespace, "my-service-https", ips, "pod-b", "pod-c", "pod-d")

	// Published slices carry the full endpoint shape: labels, an owner
	// reference to the Service, real zones from the node, and same-zone
	// hints from trafficDistribution.
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		var slice discoveryv1.EndpointSlice
		if !assert.NoError(collect, c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "my-service-http"}, &slice)) {
			return
		}
		assert.Equal(collect, "my-service", slice.Labels[discoveryv1.LabelServiceName])
		assert.Equal(collect, itManagedBy, slice.Labels[discoveryv1.LabelManagedBy])
		if owner := metav1.GetControllerOf(&slice); assert.NotNil(collect, owner) {
			assert.Equal(collect, "Service", owner.Kind)
			assert.Equal(collect, "my-service", owner.Name)
		}
		if assert.Len(collect, slice.Ports, 1) {
			assert.Equal(collect, int32(8080), ptr.Deref(slice.Ports[0].Port, 0))
		}
		for _, endpoint := range slice.Endpoints {
			assert.True(collect, ptr.Deref(endpoint.Conditions.Ready, false))
			assert.Equal(collect, itZone, ptr.Deref(endpoint.Zone, ""))
			if assert.NotNil(collect, endpoint.Hints) {
				assert.Equal(collect, []discoveryv1.ForZone{{Name: itZone}}, endpoint.Hints.ForZones)
			}
		}
	}, time.Minute, 500*time.Millisecond, "slice endpoints never carried the full shape")

	// Membership follows annotation changes: pod-a starts backing https.
	var pod corev1.Pod
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "pod-a"}, &pod))
	patch := client.MergeFrom(pod.DeepCopy())
	pod.Annotations[itPortsKey] = "http,https"
	require.NoError(t, c.Patch(ctx, &pod, patch))
	expectAddresses(t, ctx, c, namespace, "my-service-https", ips, "pod-a", "pod-b", "pod-c", "pod-d")

	// Tampered slices are repaired by server-side apply.
	var tampered discoveryv1.EndpointSlice
	require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "my-service-https"}, &tampered))
	tampered.Endpoints = tampered.Endpoints[:1]
	require.NoError(t, c.Update(ctx, &tampered))
	expectAddresses(t, ctx, c, namespace, "my-service-https", ips, "pod-a", "pod-b", "pod-c", "pod-d")

	// Deleted pods drain out of every slice.
	require.NoError(t, c.Delete(ctx, integrationPod(namespace, "pod-d", nil, nil)))
	expectAddresses(t, ctx, c, namespace, "my-service-https", ips, "pod-a", "pod-b", "pod-c")

	// Removing the marker garbage collects everything we own.
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(svc), svc))
	patch = client.MergeFrom(svc.DeepCopy())
	delete(svc.Annotations, itGroupKey)
	require.NoError(t, c.Patch(ctx, svc, patch))
	expectNoSlice(t, ctx, c, namespace, "my-service-http")
	expectNoSlice(t, ctx, c, namespace, "my-service-https")

	// Re-marking brings the slices back; deleting the Service lets the real
	// garbage collector reap them through their owner references.
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(svc), svc))
	patch = client.MergeFrom(svc.DeepCopy())
	svc.Annotations = map[string]string{itGroupKey: "lifecycle-group"}
	require.NoError(t, c.Patch(ctx, svc, patch))
	expectAddresses(t, ctx, c, namespace, "my-service-http", ips, "pod-a", "pod-b", "pod-c")

	require.NoError(t, c.Delete(ctx, svc))
	expectNoSlice(t, ctx, c, namespace, "my-service-http")
	expectNoSlice(t, ctx, c, namespace, "my-service-https")
}

// testSelectorMigration walks the migration path from a classic
// selector-based Service to a port-mapper-managed one:
//
//  1. The Service has a selector; the native EndpointSlice controller
//     publishes every Ready pod -- including pod-x, which port-mapper's
//     membership check would exclude from https.
//  2. The Service opts into port-mapper while keeping its selector: both
//     controllers publish side by side (kube-proxy routes the union).
//  3. The selector is dropped and the native controller's leftover slices
//     are removed, after which pod-x is no longer an https endpoint.
func testSelectorMigration(t *testing.T, ctx context.Context, c client.Client) {
	const namespace = "pm-migration"
	require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}))

	// pod-x is Ready as far as Kubernetes is concerned -- the native
	// controller will happily publish it on every port -- but it only backs
	// http per port-mapper's membership check.
	selector := map[string]string{"app": "migrate"}
	for name, ports := range map[string]string{
		"pod-a": "http,https",
		"pod-b": "http,https",
		"pod-x": "http",
	} {
		annotations := map[string]string{itGroupKey: "migrate-group", itPortsKey: ports}
		require.NoError(t, c.Create(ctx, integrationPod(namespace, name, selector, annotations)))
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "my-service", Namespace: namespace},
		Spec: corev1.ServiceSpec{
			Selector: selector,
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP},
				{Name: "https", Port: 8443, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	require.NoError(t, c.Create(ctx, svc))

	ips := waitForPodIPs(t, ctx, c, namespace, "pod-a", "pod-b", "pod-x")

	listNative := func() ([]discoveryv1.EndpointSlice, error) {
		var list discoveryv1.EndpointSliceList
		err := c.List(ctx, &list, client.InNamespace(namespace), client.MatchingLabels{
			discoveryv1.LabelServiceName: "my-service",
			discoveryv1.LabelManagedBy:   nativeManagedBy,
		})
		return list.Items, err
	}

	// Phase 1: the native controller publishes every Ready pod, pod-x
	// included.
	waitForNativeAddresses(t, listNative, ips)

	// Phase 2: opt into port-mapper with the selector still in place. Both
	// controllers publish side by side; ours applies per-port membership.
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(svc), svc))
	patch := client.MergeFrom(svc.DeepCopy())
	svc.Annotations = map[string]string{itGroupKey: "migrate-group"}
	require.NoError(t, c.Patch(ctx, svc, patch))

	expectAddresses(t, ctx, c, namespace, "my-service-http", ips, "pod-a", "pod-b", "pod-x")
	expectAddresses(t, ctx, c, namespace, "my-service-https", ips, "pod-a", "pod-b")

	native, err := listNative()
	require.NoError(t, err)
	require.NotEmpty(t, native, "native slices should coexist while the selector remains")

	// Phase 3: drop the selector. port-mapper finishes the migration on its
	// own: it deletes the stale EndpointSlices the native controller
	// abandons (it ignores selectorless Services rather than cleaning up
	// after them) and the legacy Endpoints object -- which would otherwise
	// keep feeding the EndpointSliceMirroring controller, resurrecting
	// retired endpoints like pod-x no matter how often the stale slices are
	// deleted. The mirrored slices are garbage collected along with the
	// Endpoints object that owns them.
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(svc), svc))
	patch = client.MergeFrom(svc.DeepCopy())
	svc.Spec.Selector = nil
	require.NoError(t, c.Patch(ctx, svc, patch))

	// The legacy Endpoints object disappears without any manual step...
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		var endpoints corev1.Endpoints
		err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "my-service"}, &endpoints)
		assert.True(collect, apierrors.IsNotFound(err), "legacy Endpoints object should be cleaned up, got err=%v", err)
	}, time.Minute, 500*time.Millisecond, "legacy Endpoints object never cleaned up")

	// ...and the takeover is surfaced as an Event on the Service.
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		var events corev1.EventList
		if !assert.NoError(collect, c.List(ctx, &events, client.InNamespace(namespace))) {
			return
		}
		seen := false
		for _, event := range events.Items {
			if event.Reason == "NativeEndpointsCleanedUp" && event.InvolvedObject.Name == "my-service" {
				seen = true
			}
		}
		assert.True(collect, seen, "expected a NativeEndpointsCleanedUp event")
	}, time.Minute, 500*time.Millisecond, "cleanup event never recorded")

	// Phase 4: only port-mapper's slices remain, and pod-x is no longer an
	// https endpoint anywhere on the Service.
	waitForTakeover(t, ctx, c, namespace, ips)
}

// waitForNativeAddresses waits until the native controller's slices carry
// every pod, including pod-x, the one that fails port-mapper's check.
func waitForNativeAddresses(t *testing.T, listNative func() ([]discoveryv1.EndpointSlice, error), ips map[string]string) {
	t.Helper()

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		native, err := listNative()
		if !assert.NoError(collect, err) {
			return
		}
		published := map[string]bool{}
		for _, slice := range native {
			for _, endpoint := range slice.Endpoints {
				for _, address := range endpoint.Addresses {
					published[address] = true
				}
			}
		}
		assert.True(collect, published[ips["pod-a"]], "native slices should carry pod-a")
		assert.True(collect, published[ips["pod-b"]], "native slices should carry pod-b")
		assert.True(collect, published[ips["pod-x"]], "native slices should carry the pod that fails port-mapper's check")
	}, 90*time.Second, 500*time.Millisecond, "native controller never published the selected pods")
}

// waitForTakeover waits until only port-mapper's slices remain for
// my-service and pod-x has dropped out of https everywhere.
func waitForTakeover(t *testing.T, ctx context.Context, c client.Client, namespace string, ips map[string]string) {
	t.Helper()

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		var list discoveryv1.EndpointSliceList
		if !assert.NoError(collect, c.List(ctx, &list, client.InNamespace(namespace),
			client.MatchingLabels{discoveryv1.LabelServiceName: "my-service"})) {
			return
		}

		managers := map[string]bool{}
		var httpsAddresses []string
		for _, slice := range list.Items {
			managers[slice.Labels[discoveryv1.LabelManagedBy]] = true
			for _, port := range slice.Ports {
				if ptr.Deref(port.Name, "") != "https" {
					continue
				}
				for _, endpoint := range slice.Endpoints {
					httpsAddresses = append(httpsAddresses, endpoint.Addresses...)
				}
			}
		}
		slices.Sort(httpsAddresses)

		assert.Equal(collect, map[string]bool{itManagedBy: true}, managers, "only port-mapper slices should remain")
		assert.Equal(collect, sortedIPs(ips, "pod-a", "pod-b"), httpsAddresses)
		assert.NotContains(collect, httpsAddresses, ips["pod-x"], "pod-x should have dropped out of https")
	}, time.Minute, 500*time.Millisecond, "migration never converged on port-mapper-only slices")
}

func integrationPod(namespace, name string, labels, annotations map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			TerminationGracePeriodSeconds: ptr.To(int64(1)),
			Containers: []corev1.Container{{
				Name: "app",
				// The image k3s already uses for sandboxes, so it's either
				// preloaded or a tiny pull.
				Image: "rancher/mirrored-pause:3.6",
			}},
		},
	}
}

// waitForPodIPs waits for every named pod to be Running and Ready with an IP
// and returns the name → IP mapping.
func waitForPodIPs(t *testing.T, ctx context.Context, c client.Client, namespace string, names ...string) map[string]string {
	t.Helper()

	ips := map[string]string{}
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		for _, name := range names {
			var pod corev1.Pod
			if !assert.NoError(collect, c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &pod)) {
				return
			}
			ready := false
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					ready = true
				}
			}
			if !assert.True(collect, ready, "pod %s is not ready", name) {
				return
			}
			if !assert.NotEmpty(collect, pod.Status.PodIP, "pod %s has no IP", name) {
				return
			}
			ips[name] = pod.Status.PodIP
		}
	}, 2*time.Minute, time.Second, "pods never became ready")

	return ips
}

func sortedIPs(ips map[string]string, names ...string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, ips[name])
	}
	slices.Sort(out)
	return out
}

// expectAddresses waits for the named slice to publish exactly the given
// pods' addresses.
func expectAddresses(t *testing.T, ctx context.Context, c client.Client, namespace, sliceName string, ips map[string]string, pods ...string) {
	t.Helper()

	want := sortedIPs(ips, pods...)
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		var slice discoveryv1.EndpointSlice
		if !assert.NoError(collect, c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: sliceName}, &slice)) {
			return
		}
		got := make([]string, 0, len(slice.Endpoints))
		for _, endpoint := range slice.Endpoints {
			got = append(got, endpoint.Addresses...)
		}
		slices.Sort(got)
		assert.Equal(collect, want, got)
	}, 90*time.Second, 500*time.Millisecond, "slice %s never converged", sliceName)
}

//nolint:unparam // namespace stays a parameter to mirror expectAddresses
func expectNoSlice(t *testing.T, ctx context.Context, c client.Client, namespace, sliceName string) {
	t.Helper()

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		var slice discoveryv1.EndpointSlice
		err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: sliceName}, &slice)
		assert.True(collect, apierrors.IsNotFound(err), "expected %s to be gone, got err=%v", sliceName, err)
	}, time.Minute, 500*time.Millisecond)
}
