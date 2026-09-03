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
	"errors"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func legacyConfig() Config {
	cfg := testConfig()
	cfg.PublishLegacyEndpoints = true
	return cfg
}

func getEndpointsObject(t *testing.T, c client.Client) *corev1.Endpoints {
	t.Helper()
	var endpoints corev1.Endpoints
	err := c.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: "my-service"}, &endpoints)
	require.NoError(t, err)
	return &endpoints
}

func requireNoEndpointsObject(t *testing.T, c client.Client) {
	t.Helper()
	var endpoints corev1.Endpoints
	err := c.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: "my-service"}, &endpoints)
	require.True(t, apierrors.IsNotFound(err), "expected the Endpoints object to not exist, got err=%v", err)
}

// subsetAddresses flattens a subset's address IPs.
func subsetAddresses(addresses []corev1.EndpointAddress) []string {
	out := make([]string, 0, len(addresses))
	for _, address := range addresses {
		out = append(out, address.IP)
	}
	return out
}

// subsetPortNames flattens a subset's port names.
func subsetPortNames(subset corev1.EndpointSubset) []string {
	out := make([]string, 0, len(subset.Ports))
	for _, port := range subset.Ports {
		out = append(out, port.Name)
	}
	return out
}

func TestReconcilePublishesLegacyEndpoints(t *testing.T) {
	r, c := newReconciler(t, legacyConfig(), testObjects()...)
	reconcileService(t, r)

	endpoints := getEndpointsObject(t, c)

	require.Equal(t, r.cfg.ManagedBy, endpoints.Labels[discoveryv1.LabelManagedBy])
	require.Equal(t, "true", endpoints.Labels[discoveryv1.LabelSkipMirror],
		"published Endpoints must opt out of the EndpointSliceMirroring controller")
	owner := metav1.GetControllerOf(endpoints)
	require.NotNil(t, owner)
	require.Equal(t, types.UID("svc-uid"), owner.UID)

	// Addresses group into subsets by the exact ports they serve: pod-a
	// backs only http, pods b and c back both, pod-d backs only https.
	require.Len(t, endpoints.Subsets, 3)
	byPorts := map[string]corev1.EndpointSubset{}
	for _, subset := range endpoints.Subsets {
		key := ""
		for _, name := range subsetPortNames(subset) {
			key += name + ","
		}
		byPorts[key] = subset
	}

	httpOnly := byPorts["http,"]
	require.Equal(t, []string{"10.0.0.11"}, subsetAddresses(httpOnly.Addresses))
	require.Equal(t, int32(8080), httpOnly.Ports[0].Port)
	require.Equal(t, corev1.ProtocolTCP, httpOnly.Ports[0].Protocol)

	both := byPorts["http,https,"]
	require.Equal(t, []string{"10.0.0.12", "10.0.0.13"}, subsetAddresses(both.Addresses))

	httpsOnly := byPorts["https,"]
	require.Equal(t, []string{"10.0.0.14"}, subsetAddresses(httpsOnly.Addresses))
	require.Equal(t, int32(8443), httpsOnly.Ports[0].Port)

	// Address metadata carries over from the slices.
	require.Equal(t, "pod-a", httpOnly.Addresses[0].TargetRef.Name)
	require.Equal(t, "node-1", ptr.Deref(httpOnly.Addresses[0].NodeName, ""))
}

func TestReconcileLegacyEndpointsCopiesServiceLabels(t *testing.T) {
	// The native endpoints controller stamps the Service's labels (and the
	// headless marker) onto the Endpoints object; label-keyed consumers
	// depend on it.
	objs := testObjects()
	svc := objs[0].(*corev1.Service)
	svc.Labels = map[string]string{"app": "my-app", "team": "core"}
	svc.Spec.ClusterIP = corev1.ClusterIPNone

	r, c := newReconciler(t, legacyConfig(), objs...)
	reconcileService(t, r)

	endpoints := getEndpointsObject(t, c)
	require.Equal(t, "my-app", endpoints.Labels["app"])
	require.Equal(t, "core", endpoints.Labels["team"])
	_, headless := endpoints.Labels[corev1.IsHeadlessService]
	require.True(t, headless, "headless Services carry the headless marker")
	require.Equal(t, r.cfg.ManagedBy, endpoints.Labels[discoveryv1.LabelManagedBy])

	// Label changes on the Service propagate.
	require.NoError(t, c.Get(t.Context(), serviceRequest().NamespacedName, svc))
	svc.Labels["team"] = "edge"
	require.NoError(t, c.Update(t.Context(), svc))
	reconcileService(t, r)
	require.Equal(t, "edge", getEndpointsObject(t, c).Labels["team"])
}

func TestReconcileLegacyEndpointsAdoptsEmptyObject(t *testing.T) {
	// An abandoned object with no addresses protects nothing: adoption
	// proceeds even while this controller renders no ready addresses.
	empty := &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "my-service"}}
	r, c := newReconciler(t, legacyConfig(), testService(map[string]string{testGroupKey: "my-group"}, true), empty)
	reconcileService(t, r)

	endpoints := getEndpointsObject(t, c)
	require.Equal(t, r.cfg.ManagedBy, endpoints.Labels[discoveryv1.LabelManagedBy], "an empty abandoned object should be adopted immediately")
}

func TestReconcileLegacyEndpointsConvergesWithFlagOff(t *testing.T) {
	// A mirror that reappears after the startup sweep (a racing replica
	// still running the old configuration) is removed on the next reconcile.
	orphan := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "my-service",
			Labels:    map[string]string{discoveryv1.LabelManagedBy: "my-controller"},
		},
	}
	r, c := newReconciler(t, testConfig(), append(testObjects(), orphan)...)
	reconcileService(t, r)
	requireNoEndpointsObject(t, c)
}

func TestReconcileLegacyEndpointsErrorDoesNotBlockCleanup(t *testing.T) {
	// A failing mirror write must not stall the slice-side migration: the
	// native cleanup still runs, and the error is surfaced afterwards.
	staleNative := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service-native",
			Namespace: testNamespace,
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "my-service",
				discoveryv1.LabelManagedBy:   kubeManagedBy,
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
	}

	mapper, err := New(legacyConfig())
	require.NoError(t, err)
	c := fake.NewClientBuilder().
		WithScheme(clientgoscheme.Scheme).
		WithObjects(append(testObjects(), staleNative)...).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*corev1.Endpoints); ok {
					return apierrors.NewForbidden(corev1.Resource("endpoints"), obj.GetName(), errors.New("rbac denied"))
				}
				return cl.Create(ctx, obj, opts...)
			},
		}).
		Build()
	r := &reconciler{client: c, scheme: clientgoscheme.Scheme, cfg: mapper.cfg}

	_, err = r.Reconcile(t.Context(), serviceRequest())
	require.Error(t, err, "the mirror failure is still surfaced")
	requireNoSlice(t, c, "my-service-native")
}

func TestReconcileLegacyEndpointsDisabled(t *testing.T) {
	r, c := newReconciler(t, testConfig(), testObjects()...)
	reconcileService(t, r)
	requireNoEndpointsObject(t, c)
}

func TestReconcileLegacyEndpointsSkipsSelectorServices(t *testing.T) {
	objs := testObjects()
	svc := objs[0].(*corev1.Service)
	svc.Spec.Selector = map[string]string{"app": "mine"}

	r, c := newReconciler(t, legacyConfig(), objs...)
	reconcileService(t, r)

	// The native endpoints controller owns the Endpoints object while the
	// selector remains; slices are still published.
	requireNoEndpointsObject(t, c)
	getSlice(t, c, "my-service-http")
}

func TestReconcileLegacyEndpointsTerminatingPods(t *testing.T) {
	// The slices keep draining pods (ready=false, serving=true); the legacy
	// mirror drops them entirely, like the native endpoints controller --
	// consumers read notReadyAddresses as pods that will become ready.
	objs := testObjects()
	pod := objs[2].(*corev1.Pod) // pod-b
	pod.DeletionTimestamp = ptr.To(metav1.Now())
	pod.Finalizers = []string{"port-mapper.example.com/test"}

	r, c := newReconciler(t, legacyConfig(), objs...)
	reconcileService(t, r)

	endpoints := getEndpointsObject(t, c)
	for _, subset := range endpoints.Subsets {
		require.NotContains(t, subsetAddresses(subset.Addresses), "10.0.0.12", "a draining pod must not publish as ready")
		require.Empty(t, subset.NotReadyAddresses, "a draining pod must be omitted, not published as not-ready")
	}

	// publishNotReadyAddresses keeps draining pods, marked ready -- also
	// matching the native controller.
	var svc corev1.Service
	require.NoError(t, c.Get(t.Context(), serviceRequest().NamespacedName, &svc))
	svc.Spec.PublishNotReadyAddresses = true
	require.NoError(t, c.Update(t.Context(), &svc))
	reconcileService(t, r)

	published := false
	for _, subset := range getEndpointsObject(t, c).Subsets {
		published = published || slices.Contains(subsetAddresses(subset.Addresses), "10.0.0.12")
	}
	require.True(t, published, "publishNotReadyAddresses keeps the draining pod")
}

func TestReconcileLegacyEndpointsPrimaryFamilyOnly(t *testing.T) {
	objs := testObjects()
	svc := objs[0].(*corev1.Service)
	svc.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol}
	for i, obj := range objs[1:] {
		pod := obj.(*corev1.Pod)
		pod.Status.PodIPs = append(pod.Status.PodIPs, corev1.PodIP{IP: "fd00::1" + strconv.Itoa(i+1)})
	}

	r, c := newReconciler(t, legacyConfig(), objs...)
	reconcileService(t, r)

	endpoints := getEndpointsObject(t, c)
	for _, subset := range endpoints.Subsets {
		for _, address := range append(subset.Addresses, subset.NotReadyAddresses...) {
			require.NotContains(t, address.IP, ":", "legacy Endpoints publish only the primary address family")
		}
	}
}

func TestReconcileLegacyEndpointsTeardown(t *testing.T) {
	t.Run("unmanaged service deletes ours", func(t *testing.T) {
		objs := testObjects()
		r, c := newReconciler(t, legacyConfig(), objs...)
		reconcileService(t, r)
		getEndpointsObject(t, c)

		var svc corev1.Service
		require.NoError(t, c.Get(t.Context(), serviceRequest().NamespacedName, &svc))
		svc.Annotations = nil
		require.NoError(t, c.Update(t.Context(), &svc))

		reconcileService(t, r)
		requireNoEndpointsObject(t, c)
	})

	t.Run("unmanaged service keeps foreign Endpoints", func(t *testing.T) {
		foreign := &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "my-service"}}
		r, c := newReconciler(t, legacyConfig(), testService(nil, true), foreign)
		reconcileService(t, r)
		require.Empty(t, getEndpointsObject(t, c).Labels[discoveryv1.LabelManagedBy],
			"an Endpoints object this controller doesn't manage must survive teardown")
	})

	t.Run("ExternalName service deletes ours", func(t *testing.T) {
		objs := testObjects()
		r, c := newReconciler(t, legacyConfig(), objs...)
		reconcileService(t, r)
		getEndpointsObject(t, c)

		var svc corev1.Service
		require.NoError(t, c.Get(t.Context(), serviceRequest().NamespacedName, &svc))
		svc.Spec.Type = corev1.ServiceTypeExternalName
		require.NoError(t, c.Update(t.Context(), &svc))

		reconcileService(t, r)
		requireNoEndpointsObject(t, c)
	})
}

func TestReconcileLegacyEndpointsAdoptsDuringMigration(t *testing.T) {
	// A selector migration leaves the native controller's Endpoints object
	// behind. With PublishLegacyEndpoints the controller adopts it in place
	// instead of the delete/recreate churn, and the native cleanup must
	// leave the adopted object alone while still removing stale slices.
	nativeSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service-native",
			Namespace: testNamespace,
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "my-service",
				discoveryv1.LabelManagedBy:   kubeManagedBy,
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
	}
	// The mirroring controller's slice is normally garbage collected with
	// the Endpoints object; adoption keeps that object alive, so the
	// controller must delete the mirror directly.
	mirrorSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service-mirror",
			Namespace: testNamespace,
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "my-service",
				discoveryv1.LabelManagedBy:   mirrorManagedBy,
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
	}
	nativeEndpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "my-service"},
		Subsets:    []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.9.9.9"}}}},
	}

	r, c := newReconciler(t, legacyConfig(), append(testObjects(), nativeSlice, mirrorSlice, nativeEndpoints)...)
	reconcileService(t, r)

	requireNoSlice(t, c, "my-service-native")
	requireNoSlice(t, c, "my-service-mirror")

	endpoints := getEndpointsObject(t, c)
	require.Equal(t, r.cfg.ManagedBy, endpoints.Labels[discoveryv1.LabelManagedBy], "the abandoned object should be adopted")
	require.Equal(t, "true", endpoints.Labels[discoveryv1.LabelSkipMirror])
	for _, subset := range endpoints.Subsets {
		require.NotContains(t, subsetAddresses(subset.Addresses), "10.9.9.9", "stale native addresses must be replaced")
	}
	require.NotEmpty(t, endpoints.Subsets)
}

func TestReconcileLegacyEndpointsDefersAdoptionUntilPublishing(t *testing.T) {
	// No pod backs any port, so this controller publishes zero endpoints;
	// taking over the native controller's abandoned Endpoints object now
	// would black out its consumers.
	nativeEndpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "my-service"},
		Subsets:    []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.9.9.9"}}}},
	}
	objs := []client.Object{
		testService(map[string]string{testGroupKey: "my-group"}, true),
		testPod("pod-a", "10.0.0.11", map[string]string{testGroupKey: "my-group", testPortsKey: "none"}, true),
		nativeEndpoints,
	}

	r, c := newReconciler(t, legacyConfig(), objs...)
	reconcileService(t, r)

	endpoints := getEndpointsObject(t, c)
	require.Empty(t, endpoints.Labels[discoveryv1.LabelManagedBy], "the native object must not be adopted before publishing")
	require.Equal(t, "10.9.9.9", endpoints.Subsets[0].Addresses[0].IP)
}

func TestReconcileLegacyEndpointsDeferredAdoptionKeepsMirrorSlices(t *testing.T) {
	// While adoption is deferred (primary family renders no addresses), the
	// abandoned Endpoints object has no skip-mirror label, so deleting the
	// mirroring controller's slices would just make it recreate them; they
	// must be left alone until the adoption actually lands.
	objs := testObjects()
	svc := objs[0].(*corev1.Service)
	svc.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv6Protocol, corev1.IPv4Protocol}

	mirrorSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-service-mirror",
			Namespace: testNamespace,
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "my-service",
				discoveryv1.LabelManagedBy:   mirrorManagedBy,
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
	}
	nativeEndpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "my-service"},
		Subsets:    []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.9.9.9"}}}},
	}

	r, c := newReconciler(t, legacyConfig(), append(objs, mirrorSlice, nativeEndpoints)...)
	reconcileService(t, r)

	// The IPv4 slices publish, but the primary (IPv6) family is empty, so
	// adoption deferred: the native object and the mirror slice both remain.
	require.NotEmpty(t, getSlice(t, c, "my-service-http").Endpoints)
	getSlice(t, c, "my-service-mirror")
	require.Empty(t, getEndpointsObject(t, c).Labels[discoveryv1.LabelManagedBy])
}

func TestReconcileLegacyEndpointsCollisions(t *testing.T) {
	// A collision is a tolerated-but-unserved state, not an error: the
	// reconcile proceeds (including the native cleanup) with its normal
	// requeue, and the foreign object is never touched.
	t.Run("another manager's label", func(t *testing.T) {
		foreign := &corev1.Endpoints{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNamespace,
				Name:      "my-service",
				Labels:    map[string]string{discoveryv1.LabelManagedBy: "someone-else"},
			},
			Subsets: []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.9.9.9"}}}},
		}
		staleNative := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-service-native",
				Namespace: testNamespace,
				Labels: map[string]string{
					discoveryv1.LabelServiceName: "my-service",
					discoveryv1.LabelManagedBy:   kubeManagedBy,
				},
			},
			AddressType: discoveryv1.AddressTypeIPv4,
		}
		r, c := newReconciler(t, legacyConfig(), append(testObjects(), foreign, staleNative)...)
		result := reconcileService(t, r)

		endpoints := getEndpointsObject(t, c)
		require.Equal(t, "someone-else", endpoints.Labels[discoveryv1.LabelManagedBy], "a foreign manager's object must not be overwritten")
		require.Equal(t, "10.9.9.9", endpoints.Subsets[0].Addresses[0].IP)
		require.Equal(t, r.cfg.ResyncPeriod, result.RequeueAfter, "a collision must not replace the resync requeue with error backoff")
		requireNoSlice(t, c, "my-service-native")
	})

	t.Run("foreign controller owner", func(t *testing.T) {
		foreign := &corev1.Endpoints{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNamespace,
				Name:      "my-service",
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "example.com/v1",
					Kind:       "Other",
					Name:       "other",
					UID:        types.UID("other-uid"),
					Controller: ptr.To(true),
				}},
			},
		}
		r, c := newReconciler(t, legacyConfig(), append(testObjects(), foreign)...)
		reconcileService(t, r)

		owner := metav1.GetControllerOf(getEndpointsObject(t, c))
		require.Equal(t, types.UID("other-uid"), owner.UID, "a foreign controller's object must not be adopted")
	})

	t.Run("recreated Service's old mirror", func(t *testing.T) {
		// A deleted-and-recreated Service (same name, new UID) leaves the
		// old mirror behind until GC reaps it: ours by label, foreign by
		// owner UID. It must be neither overwritten nor deleted, without
		// erroring the reconcile.
		stale := &corev1.Endpoints{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: testNamespace,
				Name:      "my-service",
				Labels:    map[string]string{discoveryv1.LabelManagedBy: "my-controller"},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "v1",
					Kind:       "Service",
					Name:       "my-service",
					UID:        types.UID("old-svc-uid"),
					Controller: ptr.To(true),
				}},
			},
			Subsets: []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.9.9.9"}}}},
		}
		r, c := newReconciler(t, legacyConfig(), append(testObjects(), stale)...)
		reconcileService(t, r)

		endpoints := getEndpointsObject(t, c)
		require.Equal(t, "10.9.9.9", endpoints.Subsets[0].Addresses[0].IP, "the old Service's mirror is GC's to reap, not ours to overwrite")
	})
}

func TestReconcileLegacyEndpointsRepairsStrippedLabel(t *testing.T) {
	// The managed-by label being stripped is the tamper the ownerReference
	// disambiguates: the object is still ours and must be repaired, even
	// with DisableNativeCleanup set (which only protects objects that are
	// not ours).
	cfg := legacyConfig()
	cfg.DisableNativeCleanup = true
	r, c := newReconciler(t, cfg, testObjects()...)
	reconcileService(t, r)

	endpoints := getEndpointsObject(t, c)
	delete(endpoints.Labels, discoveryv1.LabelManagedBy)
	require.NoError(t, c.Update(t.Context(), endpoints))

	reconcileService(t, r)
	require.Equal(t, r.cfg.ManagedBy, getEndpointsObject(t, c).Labels[discoveryv1.LabelManagedBy])
}

func TestReconcileLegacyEndpointsStripsInheritedOverCapacity(t *testing.T) {
	// An over-capacity annotation inherited from the native controller is
	// owned by a foreign field manager, which SSA alone can never remove;
	// the sync must strip it explicitly so drift detection converges.
	native := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   testNamespace,
			Name:        "my-service",
			Annotations: map[string]string{corev1.EndpointsOverCapacity: endpointsOverCapacityTruncated},
		},
		Subsets: []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.9.9.9"}}}},
	}
	r, c := newReconciler(t, legacyConfig(), append(testObjects(), native)...)
	reconcileService(t, r)

	endpoints := getEndpointsObject(t, c)
	require.Equal(t, r.cfg.ManagedBy, endpoints.Labels[discoveryv1.LabelManagedBy])
	require.Empty(t, endpoints.Annotations[corev1.EndpointsOverCapacity], "the inherited truncation marker must be stripped")
}

func TestReconcileLegacyEndpointsWriteQuiet(t *testing.T) {
	// Once converged, further reconciles must not re-apply -- including for
	// a Service with zero addresses, where nil-vs-empty subsets asymmetry
	// would otherwise report drift forever.
	for name, objs := range map[string][]client.Object{
		"with endpoints": testObjects(),
		"zero endpoints": {
			testService(map[string]string{testGroupKey: "my-group"}, true),
		},
	} {
		t.Run(name, func(t *testing.T) {
			mapper, err := New(legacyConfig())
			require.NoError(t, err)

			var endpointsWrites atomic.Int64
			c := fake.NewClientBuilder().
				WithScheme(clientgoscheme.Scheme).
				WithObjects(objs...).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						if _, ok := obj.(*corev1.Endpoints); ok {
							endpointsWrites.Add(1)
						}
						return cl.Create(ctx, obj, opts...)
					},
					Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						if _, ok := obj.(*corev1.Endpoints); ok {
							endpointsWrites.Add(1)
						}
						return cl.Patch(ctx, obj, patch, opts...)
					},
				}).
				Build()
			r := &reconciler{client: c, scheme: clientgoscheme.Scheme, cfg: mapper.cfg}

			reconcileService(t, r)
			first := endpointsWrites.Load()
			require.GreaterOrEqual(t, first, int64(1), "the first reconcile publishes the mirror")

			reconcileService(t, r)
			reconcileService(t, r)
			require.Equal(t, first, endpointsWrites.Load(), "converged reconciles must not re-write the mirror")
		})
	}
}

func TestSweepLegacyEndpoints(t *testing.T) {
	// The startup sweep (option off) removes every mirror labeled as this
	// controller's, wherever it lives -- including objects for Services that
	// nothing would ever reconcile again -- and touches nothing else.
	orphan := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "unmanaged-service",
			Labels: map[string]string{
				discoveryv1.LabelManagedBy:  "my-controller",
				discoveryv1.LabelSkipMirror: "true",
			},
		},
	}
	foreign := &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "other-service"}}
	otherManager := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "third-service",
			Labels:    map[string]string{discoveryv1.LabelManagedBy: "someone-else"},
		},
	}
	// Carries our label but is controller-owned by something that isn't a
	// Service: never published by this controller, so never swept.
	crOwned := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "cr-owned-service",
			Labels:    map[string]string{discoveryv1.LabelManagedBy: "my-controller"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "example.com/v1",
				Kind:       "Other",
				Name:       "other",
				UID:        types.UID("other-uid"),
				Controller: ptr.To(true),
			}},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(clientgoscheme.Scheme).
		WithObjects(orphan, foreign, otherManager, crOwned).
		Build()
	require.NoError(t, sweepLegacyEndpoints(t.Context(), c, "my-controller"))

	var endpoints corev1.Endpoints
	err := c.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: "unmanaged-service"}, &endpoints)
	require.True(t, apierrors.IsNotFound(err), "the labeled orphan should be swept")
	require.NoError(t, c.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: "other-service"}, &endpoints))
	require.NoError(t, c.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: "third-service"}, &endpoints))
	require.NoError(t, c.Get(t.Context(), types.NamespacedName{Namespace: testNamespace, Name: "cr-owned-service"}, &endpoints))
}

func TestReconcileLegacyEndpointsTeardownKeepsUnlabeled(t *testing.T) {
	// Deletion requires the managed-by label: an unlabeled object that
	// merely owner-refs the Service could be a third party's, so teardown
	// leaves it alone (the flag-on publish path repairs our own tampered
	// label before teardown would ever matter).
	unlabeled := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "my-service",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "v1",
				Kind:       "Service",
				Name:       "my-service",
				UID:        types.UID("svc-uid"),
				Controller: ptr.To(true),
			}},
		},
	}
	r, c := newReconciler(t, legacyConfig(), testService(nil, true), unlabeled)
	reconcileService(t, r)
	getEndpointsObject(t, c)
}

func TestReconcileLegacyEndpointsTeardownWithFlagOff(t *testing.T) {
	// Teardown of previously published mirrors works even after the option
	// was disabled: unmanaging the Service removes the leftover.
	orphan := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      "my-service",
			Labels:    map[string]string{discoveryv1.LabelManagedBy: "my-controller"},
		},
	}
	r, c := newReconciler(t, testConfig(), testService(nil, true), orphan)
	reconcileService(t, r)
	requireNoEndpointsObject(t, c)
}

func TestReconcileLegacyEndpointsRepairsTampering(t *testing.T) {
	r, c := newReconciler(t, legacyConfig(), testObjects()...)
	reconcileService(t, r)

	endpoints := getEndpointsObject(t, c)
	endpoints.Subsets = endpoints.Subsets[:1]
	endpoints.Labels[discoveryv1.LabelSkipMirror] = "false"
	require.NoError(t, c.Update(t.Context(), endpoints))

	reconcileService(t, r)

	repaired := getEndpointsObject(t, c)
	require.Len(t, repaired.Subsets, 3)
	require.Equal(t, "true", repaired.Labels[discoveryv1.LabelSkipMirror])
}

func TestReconcileLegacyEndpointsSharedIP(t *testing.T) {
	// Distinct pods can share an address (hostNetwork pods on one node). The
	// address renders without duplicated ports, and a draining pod behind it
	// is simply omitted -- the surviving pod keeps the address routable.
	objs := []client.Object{
		testService(map[string]string{testGroupKey: "my-group"}, true),
		testPod("pod-old", "10.0.0.50", map[string]string{testGroupKey: "my-group", testPortsKey: "http"}, true),
		testPod("pod-new", "10.0.0.50", map[string]string{testGroupKey: "my-group", testPortsKey: "http"}, true),
	}
	old := objs[1].(*corev1.Pod)
	old.DeletionTimestamp = ptr.To(metav1.Now())
	old.Finalizers = []string{"port-mapper.example.com/test"}

	r, c := newReconciler(t, legacyConfig(), objs...)
	reconcileService(t, r)

	endpoints := getEndpointsObject(t, c)
	require.Len(t, endpoints.Subsets, 1)
	subset := endpoints.Subsets[0]
	require.Equal(t, []string{"http"}, subsetPortNames(subset), "shared addresses must not duplicate ports")
	require.Equal(t, []string{"10.0.0.50"}, subsetAddresses(subset.Addresses), "the ready pod keeps the address routable")
	require.Equal(t, "pod-new", subset.Addresses[0].TargetRef.Name)
	require.Empty(t, subset.NotReadyAddresses, "the draining pod is omitted")
}

func TestReconcileLegacyEndpointsSharedIPSplitReadiness(t *testing.T) {
	// A draining pod's port must never be advertised as ready just because
	// another pod shares the address on a different port: the draining pod
	// is omitted, so only the ready pod's port subset survives.
	objs := []client.Object{
		testService(map[string]string{testGroupKey: "my-group"}, true),
		testPod("pod-x", "10.0.0.50", map[string]string{testGroupKey: "my-group", testPortsKey: "http"}, true),
		testPod("pod-y", "10.0.0.50", map[string]string{testGroupKey: "my-group", testPortsKey: "https"}, true),
	}
	draining := objs[2].(*corev1.Pod) // pod-y
	draining.DeletionTimestamp = ptr.To(metav1.Now())
	draining.Finalizers = []string{"port-mapper.example.com/test"}

	r, c := newReconciler(t, legacyConfig(), objs...)
	reconcileService(t, r)

	endpoints := getEndpointsObject(t, c)
	require.Len(t, endpoints.Subsets, 1)
	subset := endpoints.Subsets[0]
	require.Equal(t, []string{"http"}, subsetPortNames(subset))
	require.Equal(t, []string{"10.0.0.50"}, subsetAddresses(subset.Addresses))
	require.Empty(t, subset.NotReadyAddresses)
}

func TestReconcileLegacyEndpointsPrimaryFamilyBlackoutGuard(t *testing.T) {
	// The primary family (IPv6) has no addresses even though the IPv4 slices
	// do; a foreign Endpoints object must not be adopted and wiped empty.
	objs := testObjects()
	svc := objs[0].(*corev1.Service)
	svc.Spec.IPFamilies = []corev1.IPFamily{corev1.IPv6Protocol, corev1.IPv4Protocol}
	foreign := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "my-service"},
		Subsets:    []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.9.9.9"}}}},
	}

	r, c := newReconciler(t, legacyConfig(), append(objs, foreign)...)
	reconcileService(t, r)

	// The IPv4 slices publish endpoints...
	require.NotEmpty(t, getSlice(t, c, "my-service-http").Endpoints)

	// ...but the Endpoints mirror would be empty, so the foreign object is
	// left alone.
	endpoints := getEndpointsObject(t, c)
	require.Empty(t, endpoints.Labels[discoveryv1.LabelManagedBy])
	require.Equal(t, "10.9.9.9", endpoints.Subsets[0].Addresses[0].IP)
}

func TestReconcileLegacyEndpointsRespectsDisableNativeCleanup(t *testing.T) {
	// DisableNativeCleanup declares the legacy objects legitimately managed
	// by something else: never adopt them, even while publishing.
	cfg := legacyConfig()
	cfg.DisableNativeCleanup = true
	foreign := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "my-service"},
		Subsets:    []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.9.9.9"}}}},
	}

	r, c := newReconciler(t, cfg, append(testObjects(), foreign)...)
	reconcileService(t, r)

	require.NotEmpty(t, getSlice(t, c, "my-service-http").Endpoints)
	endpoints := getEndpointsObject(t, c)
	require.Empty(t, endpoints.Labels[discoveryv1.LabelManagedBy], "a foreign Endpoints object must survive DisableNativeCleanup")
	require.Equal(t, "10.9.9.9", endpoints.Subsets[0].Addresses[0].IP)
}

func TestReconcileLegacyEndpointsRelinquishedOnSelector(t *testing.T) {
	// A selector (re)appearing hands the Endpoints object back to the native
	// endpoints controller: ours is deleted rather than left mislabeled.
	r, c := newReconciler(t, legacyConfig(), testObjects()...)
	reconcileService(t, r)
	getEndpointsObject(t, c)

	var svc corev1.Service
	require.NoError(t, c.Get(t.Context(), serviceRequest().NamespacedName, &svc))
	svc.Spec.Selector = map[string]string{"app": "mine"}
	require.NoError(t, c.Update(t.Context(), &svc))

	reconcileService(t, r)
	requireNoEndpointsObject(t, c)
}

func TestReconcileLegacyEndpointsDefersAdoptionWithoutReadyAddresses(t *testing.T) {
	// A render carrying only draining (not-ready) addresses must not adopt:
	// replacing the abandoned object's addresses with nothing routable is
	// the blackout the guard exists to prevent.
	objs := testObjects()
	for _, obj := range objs[1:] {
		pod := obj.(*corev1.Pod)
		pod.DeletionTimestamp = ptr.To(metav1.Now())
		pod.Finalizers = []string{"port-mapper.example.com/test"}
	}
	nativeEndpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "my-service"},
		Subsets:    []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.9.9.9"}}}},
	}

	r, c := newReconciler(t, legacyConfig(), append(objs, nativeEndpoints)...)
	reconcileService(t, r)

	endpoints := getEndpointsObject(t, c)
	require.Empty(t, endpoints.Labels[discoveryv1.LabelManagedBy], "adoption must wait for a ready address")
	require.Equal(t, "10.9.9.9", endpoints.Subsets[0].Addresses[0].IP)
}

func TestRenderLegacyEndpointsNamedPortDivergence(t *testing.T) {
	// Two pods sharing an address whose named targetPort resolves to
	// different numbers must land in separate subsets (each with one 'web'
	// port), never one subset repeating the port name -- the shape the
	// native controller's repacking produces.
	r := &reconciler{cfg: Config{ManagedBy: "my-controller", PublishLegacyEndpoints: true}, scheme: clientgoscheme.Scheme}
	svc := testService(map[string]string{testGroupKey: "my-group"}, true)

	slice := func(name string, port int32, podName string) *discoveryv1.EndpointSlice {
		return &discoveryv1.EndpointSlice{
			ObjectMeta:  metav1.ObjectMeta{Namespace: testNamespace, Name: name},
			AddressType: discoveryv1.AddressTypeIPv4,
			Ports:       []discoveryv1.EndpointPort{{Name: ptr.To("web"), Port: ptr.To(port), Protocol: ptr.To(corev1.ProtocolTCP)}},
			Endpoints: []discoveryv1.Endpoint{{
				Addresses:  []string{"10.0.0.50"},
				Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(true)},
				TargetRef:  &corev1.ObjectReference{Kind: "Pod", Namespace: testNamespace, Name: podName, UID: types.UID(podName + "-uid")},
			}},
		}
	}

	endpoints, err := r.renderLegacyEndpoints(svc, map[string]*discoveryv1.EndpointSlice{
		"my-service-web--p8080": slice("my-service-web--p8080", 8080, "pod-a"),
		"my-service-web--p9090": slice("my-service-web--p9090", 9090, "pod-b"),
	})
	require.NoError(t, err)

	require.Len(t, endpoints.Subsets, 2, "diverging named-port resolutions must not merge into one subset")
	for _, subset := range endpoints.Subsets {
		require.Len(t, subset.Ports, 1)
		require.Equal(t, "web", subset.Ports[0].Name)
		require.Equal(t, []string{"10.0.0.50"}, subsetAddresses(subset.Addresses))
	}
}

func TestRenderLegacyEndpointsTruncatesReadyFirst(t *testing.T) {
	// Ready addresses across every subset take priority under the cap: a
	// first-sorting group full of draining pods must not starve a later
	// group's ready ones.
	r := &reconciler{cfg: Config{ManagedBy: "my-controller", PublishLegacyEndpoints: true}, scheme: clientgoscheme.Scheme}
	svc := testService(map[string]string{testGroupKey: "my-group"}, true)

	notReady := &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Namespace: testNamespace, Name: "my-service-aaa"},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports:       []discoveryv1.EndpointPort{{Name: ptr.To("aaa"), Port: ptr.To(int32(8080)), Protocol: ptr.To(corev1.ProtocolTCP)}},
	}
	for i := range legacyEndpointsMaxAddresses {
		notReady.Endpoints = append(notReady.Endpoints, discoveryv1.Endpoint{
			Addresses:  []string{"10.0." + strconv.Itoa(i/250) + "." + strconv.Itoa(i%250)},
			Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(false)},
		})
	}
	ready := &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Namespace: testNamespace, Name: "my-service-bbb"},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports:       []discoveryv1.EndpointPort{{Name: ptr.To("bbb"), Port: ptr.To(int32(8443)), Protocol: ptr.To(corev1.ProtocolTCP)}},
	}
	for i := range 5 {
		ready.Endpoints = append(ready.Endpoints, discoveryv1.Endpoint{
			Addresses:  []string{"10.9.0." + strconv.Itoa(i)},
			Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(true)},
		})
	}

	endpoints, err := r.renderLegacyEndpoints(svc, map[string]*discoveryv1.EndpointSlice{
		"my-service-aaa": notReady,
		"my-service-bbb": ready,
	})
	require.NoError(t, err)

	total, readyTotal := 0, 0
	for _, subset := range endpoints.Subsets {
		total += len(subset.Addresses) + len(subset.NotReadyAddresses)
		readyTotal += len(subset.Addresses)
	}
	require.Equal(t, legacyEndpointsMaxAddresses, total)
	require.Equal(t, 5, readyTotal, "every ready address survives truncation")
	require.Equal(t, endpointsOverCapacityTruncated, endpoints.Annotations[corev1.EndpointsOverCapacity])
}

func TestRenderLegacyEndpointsTruncates(t *testing.T) {
	r := &reconciler{cfg: Config{ManagedBy: "my-controller", PublishLegacyEndpoints: true}, scheme: clientgoscheme.Scheme}
	svc := testService(map[string]string{testGroupKey: "my-group"}, true)

	slice := &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Namespace: testNamespace, Name: "my-service-http"},
		AddressType: discoveryv1.AddressTypeIPv4,
		Ports:       []discoveryv1.EndpointPort{{Name: ptr.To("http"), Port: ptr.To(int32(8080)), Protocol: ptr.To(corev1.ProtocolTCP)}},
	}
	for i := range legacyEndpointsMaxAddresses + 5 {
		slice.Endpoints = append(slice.Endpoints, discoveryv1.Endpoint{
			Addresses:  []string{"10.0." + strconv.Itoa(i/250) + "." + strconv.Itoa(i%250)},
			Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(true)},
		})
	}

	endpoints, err := r.renderLegacyEndpoints(svc, map[string]*discoveryv1.EndpointSlice{"my-service-http": slice})
	require.NoError(t, err)

	total := 0
	for _, subset := range endpoints.Subsets {
		total += len(subset.Addresses) + len(subset.NotReadyAddresses)
	}
	require.Equal(t, legacyEndpointsMaxAddresses, total)
	require.Equal(t, endpointsOverCapacityTruncated, endpoints.Annotations[corev1.EndpointsOverCapacity])
}
