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
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// reconciler reconciles the EndpointSlices of one aligned Service per
// request.
//
// +kubebuilder:rbac:groups="",resources=services;pods;nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=endpoints,verbs=delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch;create;update;patch;delete
type reconciler struct {
	client   client.Client
	scheme   *runtime.Scheme
	recorder record.EventRecorder
	cfg      Config

	// podIndex and serviceIndex name the cache field indexes registered for
	// annotation-based keys (empty when the key is a label, which the cache
	// selects natively).
	podIndex     string
	serviceIndex string

	// warned tracks which per-Service warnings have fired so they log/Event
	// once per transition instead of every resync.
	mu     sync.Mutex
	warned map[client.ObjectKey]uint8
}

const (
	warnedSelector uint8 = 1 << iota
	warnedTopology
	warnedDeferredCleanup
)

// warnOnce reports whether the warning tracked by flag should fire for key.
// It returns true only the first time condition is seen true; once the
// condition clears, the next occurrence warns again.
func (r *reconciler) warnOnce(key client.ObjectKey, flag uint8, condition bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	current := r.warned[key]
	if !condition {
		if current&flag != 0 {
			r.warned[key] = current &^ flag
		}
		return false
	}
	if current&flag != 0 {
		return false
	}
	if r.warned == nil {
		r.warned = map[client.ObjectKey]uint8{}
	}
	r.warned[key] = current | flag
	return true
}

// forgetService drops the per-Service warning state and metric series once a
// Service is deleted or no longer managed.
func (r *reconciler) forgetService(key client.ObjectKey) {
	r.mu.Lock()
	delete(r.warned, key)
	r.mu.Unlock()

	forgetServiceMetrics(key)
}

// Reconcile brings one Service's EndpointSlices in line with what its
// aligned pods and the configured membership checks say they should be. Each
// pass works through the same steps:
//
//  1. Fetch the Service and every EndpointSlice labeled for it, noting which
//     slices this controller owns. A Service that is gone needs nothing (its
//     slices are garbage collected through their owner references); one that
//     dropped its marker, or is of type ExternalName, gets anything we
//     previously published deleted.
//  2. Warn -- once per occurrence, as a log line and an Event -- about
//     misconfigurations: a selector still present, or the unsupported
//     topology-mode Auto annotation.
//  3. Gather the aligned pods, look up their nodes' zones, and render the
//     desired slices: one per Service port, address family, and resolved
//     target port, split into chunks of at most MaxEndpointsPerSlice, with
//     membership checks running concurrently.
//  4. Sync the cluster to match: server-side apply new and changed slices,
//     delete leftovers, repair tampered ones, and report name collisions
//     with other controllers' slices rather than overwriting them.
//  5. Once our slices are live, finish any selector migration by removing
//     what the native controllers left behind.
//
// The pass always requeues after ResyncPeriod so checks whose answers change
// outside the API (TCP probes, for example) get re-run, and so any drift the
// event filters miss eventually heals itself.
func (r *reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var svc corev1.Service
	if err := r.client.Get(ctx, req.NamespacedName, &svc); err != nil {
		if apierrors.IsNotFound(err) {
			// Published slices are owner-referenced to the Service and
			// garbage collected along with it.
			r.forgetService(req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, errors.WithStack(err)
	}

	// A Service with a deletion timestamp is still reconciled: finalizers
	// can hold it in Terminating for a while, and its endpoints must stay
	// accurate until it's actually gone (the native controller does the
	// same).

	all, err := r.serviceSlices(ctx, &svc)
	if err != nil {
		return ctrl.Result{}, err
	}
	owned := make([]discoveryv1.EndpointSlice, 0, len(all))
	for i := range all {
		if all[i].Labels[discoveryv1.LabelManagedBy] == r.cfg.ManagedBy {
			owned = append(owned, all[i])
		}
	}

	group, managed := r.cfg.ServiceKey.valueOf(&svc)
	if !managed {
		// The Service's marker was removed; garbage collect anything we
		// still own for it.
		r.forgetService(req.NamespacedName)
		return ctrl.Result{}, r.syncSlices(ctx, &svc, owned, nil)
	}

	if svc.Spec.Type == corev1.ServiceTypeExternalName {
		// ExternalName Services must not have endpoints; mirror the native
		// controller and ignore them, removing anything published before the
		// type changed.
		logger.Info("WARNING: managed service is of type ExternalName, which cannot have endpoints; ignoring",
			"service", req.NamespacedName)
		if r.recorder != nil {
			r.recorder.Event(&svc, corev1.EventTypeWarning, "ExternalNameUnsupported",
				"ExternalName Services cannot have EndpointSlices; this Service is ignored by port-mapper.")
		}
		r.forgetService(req.NamespacedName)
		return ctrl.Result{}, r.syncSlices(ctx, &svc, owned, nil)
	}

	if r.warnOnce(req.NamespacedName, warnedSelector, svc.Spec.Selector != nil) {
		logger.Info("WARNING: managed service defines a selector; the native EndpointSlice controller will publish competing slices for it",
			"service", req.NamespacedName)
	}

	if r.warnOnce(req.NamespacedName, warnedTopology, topologyHintsRequested(&svc)) {
		logger.Info("WARNING: managed service requests topology-mode Auto, which this controller does not support; publishing endpoints without topology hints",
			"service", req.NamespacedName)
		if r.recorder != nil {
			r.recorder.Event(&svc, corev1.EventTypeWarning, "TopologyAwareHintsDisabled",
				"This Service's EndpointSlices are managed by a port-mapper controller, which does not implement topology-mode Auto; endpoints are published without topology hints. Use spec.trafficDistribution instead.")
		}
	}

	pods, err := r.alignedPods(ctx, svc.Namespace, group)
	if err != nil {
		return ctrl.Result{}, err
	}

	desired, err := r.desiredSlices(ctx, &svc, pods, r.nodeZones(ctx, pods), previousMemberships(owned))
	if err != nil {
		// Don't sync a partial result: a failure while building the desired
		// slices must not turn into deleting slices that still carry real
		// endpoints.
		return ctrl.Result{}, err
	}

	if err := r.syncSlices(ctx, &svc, owned, desired); err != nil {
		return ctrl.Result{}, err
	}
	recordPublished(&svc, desired)

	// Only once our slices are live: finish any selector migration by
	// removing what the native controllers abandoned. The other order could
	// delete the only working endpoints and then fail before publishing.
	if svc.Spec.Selector == nil && !r.cfg.DisableNativeCleanup {
		if err := r.cleanupNativeEndpoints(ctx, &svc, all, anyEndpoints(desired)); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Requeue so membership decisions whose outcome can change without an
	// API event (e.g. TCP health checks) are re-evaluated.
	return ctrl.Result{RequeueAfter: r.cfg.ResyncPeriod}, nil
}

// serviceSlices lists every EndpointSlice labeled for svc, regardless of
// manager.
func (r *reconciler) serviceSlices(ctx context.Context, svc *corev1.Service) ([]discoveryv1.EndpointSlice, error) {
	var existing discoveryv1.EndpointSliceList
	if err := r.client.List(ctx, &existing,
		client.InNamespace(svc.Namespace),
		client.MatchingLabels{discoveryv1.LabelServiceName: svc.Name}); err != nil {
		return nil, errors.WithStack(err)
	}
	return existing.Items, nil
}

// anyEndpoints reports whether any rendered slice carries endpoints.
func anyEndpoints(slices map[string]*discoveryv1.EndpointSlice) bool {
	for _, slice := range slices {
		if len(slice.Endpoints) > 0 {
			return true
		}
	}
	return false
}

// cleanupNativeEndpoints finishes a selector migration. When a Service's
// selector is removed, the native controllers simply stop managing it: their
// old EndpointSlices stick around, and so does the legacy Endpoints object
// -- which the EndpointSliceMirroring controller keeps copying back into new
// slices for as long as it exists. This deletes both, and only acts at all
// when it sees those stale slices, so a Service that finished migrating long
// ago costs nothing.
//
// The order is deliberate: the Endpoints object goes first, and the stale
// slices only once it is confirmed gone. If the Endpoints delete fails, the
// stale slices are left in place -- they are what triggers this cleanup, so
// the next reconcile (or a freshly restarted controller) picks up exactly
// where this one left off, with no state to remember. Mirrored slices are
// garbage collected along with the Endpoints object that owns them.
//
// Nothing runs until this controller is publishing at least one endpoint:
// otherwise (say, a probe checker that can't reach the pod network) deleting
// the native slices would leave the Service with no endpoints at all.
func (r *reconciler) cleanupNativeEndpoints(ctx context.Context, svc *corev1.Service, slices []discoveryv1.EndpointSlice, publishing bool) error {
	logger := log.FromContext(ctx)

	var stale []*discoveryv1.EndpointSlice
	leftovers := false
	for i := range slices {
		switch slices[i].Labels[discoveryv1.LabelManagedBy] {
		case kubeManagedBy:
			leftovers = true
			stale = append(stale, &slices[i])
		case mirrorManagedBy:
			leftovers = true
		}
	}

	// A stalled takeover would otherwise be invisible; say so once.
	if r.warnOnce(client.ObjectKeyFromObject(svc), warnedDeferredCleanup, leftovers && !publishing) {
		logger.Info("deferring native endpoint cleanup until this controller publishes endpoints; check the membership configuration if this persists",
			"service", client.ObjectKeyFromObject(svc))
	}
	if !leftovers || !publishing {
		return nil
	}

	cleaned := false
	//nolint:staticcheck // the deprecated legacy object is exactly what's being cleaned up
	endpoints := &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Namespace: svc.Namespace, Name: svc.Name}}
	switch err := r.client.Delete(ctx, endpoints); {
	case err == nil:
		logger.Info("deleted legacy Endpoints object left behind by the removed selector",
			"endpoints", client.ObjectKeyFromObject(endpoints))
		nativeCleanups.WithLabelValues("endpoints").Inc()
		cleaned = true
	case !apierrors.IsNotFound(err):
		// Keep the stale slices: they're what triggers this cleanup again on
		// the next reconcile.
		return errors.WithStack(err)
	}

	var errs []error
	for _, slice := range stale {
		logger.Info("deleting stale native EndpointSlice left behind by the removed selector",
			"endpointslice", client.ObjectKeyFromObject(slice))
		switch err := r.client.Delete(ctx, slice); {
		case apierrors.IsNotFound(err):
			// Someone else already removed it; not our deletion to count.
		case err != nil:
			errs = append(errs, errors.WithStack(err))
		default:
			nativeCleanups.WithLabelValues("endpointslice").Inc()
			cleaned = true
		}
	}

	if cleaned && r.recorder != nil {
		r.recorder.Event(svc, corev1.EventTypeNormal, "NativeEndpointsCleanedUp",
			"Removed the stale EndpointSlices and/or legacy Endpoints object left behind by the Service's removed selector.")
	}

	return utilerrors.NewAggregate(errs)
}

// alignedPods lists the pods in namespace whose PodKey value matches group.
func (r *reconciler) alignedPods(ctx context.Context, namespace, group string) ([]corev1.Pod, error) {
	opts := []client.ListOption{client.InNamespace(namespace)}
	switch {
	case r.cfg.PodKey.Kind == KeyKindLabel:
		opts = append(opts, client.MatchingLabels{r.cfg.PodKey.Name: group})
	case r.podIndex != "":
		opts = append(opts, client.MatchingFields{r.podIndex: group})
	}

	var pods corev1.PodList
	if err := r.client.List(ctx, &pods, opts...); err != nil {
		return nil, errors.WithStack(err)
	}

	aligned := make([]corev1.Pod, 0, len(pods.Items))
	for _, pod := range pods.Items {
		if value, ok := r.cfg.PodKey.valueOf(&pod); ok && value == group {
			aligned = append(aligned, pod)
		}
	}

	return aligned, nil
}

// nodeLookupTimeout bounds each Node cache lookup: the first Get lazily
// starts a Node informer, and without Node RBAC its cache never syncs -- an
// unbounded Get would wedge the reconcile worker forever instead of
// degrading.
const nodeLookupTimeout = 5 * time.Second

// nodeZones resolves the topology zone of every node hosting one of pods.
// Lookup failures degrade to publishing no zone.
func (r *reconciler) nodeZones(ctx context.Context, pods []corev1.Pod) map[string]string {
	if r.cfg.DisableNodeLookups {
		return nil
	}

	zones := map[string]string{}
	for i := range pods {
		nodeName := pods[i].Spec.NodeName
		if nodeName == "" {
			continue
		}
		if _, done := zones[nodeName]; done {
			continue
		}

		lookupCtx, cancel := context.WithTimeout(ctx, nodeLookupTimeout)
		var node corev1.Node
		err := r.client.Get(lookupCtx, client.ObjectKey{Name: nodeName}, &node)
		cancel()
		if err != nil {
			// One failure usually means the Node informer can't sync at all
			// (e.g. missing RBAC); don't pay the timeout again for every
			// other node -- finish this pass without the remaining zones.
			log.FromContext(ctx).V(1).Info("node lookup failed; publishing endpoints without zones", "node", nodeName, "error", err.Error())
			break
		}
		zones[nodeName] = node.Labels[corev1.LabelTopologyZone]
	}

	return zones
}

// membershipKey identifies one published endpoint by port name, address
// family, resolved target port, and pod. Used to answer "was this pod
// already published here?" when a check abstains. The target port matters:
// an abstaining check must not carry a pod's membership over to a port
// number no check ever passed for.
type membershipKey struct {
	port   string
	family discoveryv1.AddressType
	target int32
	pod    types.UID
}

// previousMemberships indexes which pods the given slices currently publish.
// Slices with tampered ownership labels are invisible here until repaired,
// so an abstention in that same window can drop the tampered slice's
// members.
func previousMemberships(slices []discoveryv1.EndpointSlice) map[membershipKey]struct{} {
	previous := map[membershipKey]struct{}{}
	for i := range slices {
		slice := &slices[i]
		for _, port := range slice.Ports {
			key := membershipKey{
				port:   ptr.Deref(port.Name, ""),
				family: slice.AddressType,
				target: ptr.Deref(port.Port, 0),
			}
			for _, endpoint := range slice.Endpoints {
				if endpoint.TargetRef == nil || endpoint.TargetRef.UID == "" {
					continue
				}
				key.pod = endpoint.TargetRef.UID
				previous[key] = struct{}{}
			}
		}
	}
	return previous
}

// desiredSlices renders every EndpointSlice the Service should have: one per
// (port, address family, resolved target port), plus overflow chunks.
func (r *reconciler) desiredSlices(ctx context.Context, svc *corev1.Service, pods []corev1.Pod, zones map[string]string, previous map[membershipKey]struct{}) (map[string]*discoveryv1.EndpointSlice, error) {
	desired := map[string]*discoveryv1.EndpointSlice{}

	// One check budget for the whole render: membership probes can block up
	// to their timeout apiece, so fan-out is bounded per reconcile.
	sem := make(chan struct{}, r.cfg.MaxConcurrentChecks)

	var errs []error
	for _, sp := range svc.Spec.Ports {
		for _, family := range serviceFamilies(svc) {
			for _, group := range r.portGroups(ctx, svc, sp, family, pods, zones, previous, sem) {
				for chunkIdx, endpoints := range chunkEndpoints(group.endpoints, r.cfg.MaxEndpointsPerSlice) {
					name := sliceName(svc.Name, sp, family, group.port.Port, chunkIdx)
					if _, duplicate := desired[name]; duplicate {
						errs = append(errs, errors.Newf("EndpointSlice name collision on %q; rename the service's ports to disambiguate", name))
						continue
					}

					slice, err := r.renderSlice(svc, name, family, group.port, endpoints)
					if err != nil {
						errs = append(errs, err)
						continue
					}
					desired[name] = slice
				}
			}
		}
	}

	return desired, utilerrors.NewAggregate(errs)
}

// endpointGroup is the set of endpoints sharing a resolved target port for
// one Service port and address family; it renders as one or more (chunked)
// slices.
type endpointGroup struct {
	port      Port
	endpoints []discoveryv1.Endpoint
}

// portGroups runs membership for every routable candidate pod against one
// Service port and groups the members by their resolved target port. Numeric
// (or defaulted) targetPorts resolve the same for every pod, so they produce
// at most one group; named targetPorts resolve per pod and can produce
// several. With no members at all, one empty placeholder group is kept
// whenever a target number can still be worked out. Abstaining checks fall
// back to whatever was previously published. Checks run concurrently,
// bounded by sem.
func (r *reconciler) portGroups(ctx context.Context, svc *corev1.Service, sp corev1.ServicePort, family discoveryv1.AddressType, pods []corev1.Pod, zones map[string]string, previous map[membershipKey]struct{}, sem chan struct{}) []endpointGroup {
	protocol := sp.Protocol
	if protocol == "" {
		protocol = corev1.ProtocolTCP
	}

	// Gather candidates serially (cheap), then evaluate membership
	// concurrently: probes can block up to their timeout apiece.
	type candidate struct {
		pod  *corev1.Pod
		port Port
	}
	var candidates []candidate
	var fallback int32 // lowest target resolved by any candidate pod
	for i := range pods {
		pod := &pods[i]
		if !routable(pod) {
			continue
		}
		address, ok := podAddress(pod, family)
		if !ok {
			continue
		}
		targetPort, ok := resolveTargetPort(pod, sp, protocol)
		if !ok {
			continue
		}
		if fallback == 0 || targetPort < fallback {
			fallback = targetPort
		}
		candidates = append(candidates, candidate{
			pod:  pod,
			port: Port{Name: sp.Name, Port: targetPort, Protocol: protocol, AppProtocol: sp.AppProtocol, Address: address},
		})
	}

	decisions := make([]Decision, len(candidates))
	var wg sync.WaitGroup
	for i := range candidates {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			start := time.Now()
			decision := DecisionFor(ctx, r.cfg.Membership, svc, candidates[i].pod, candidates[i].port)
			checkDuration.Observe(time.Since(start).Seconds())
			membershipChecks.WithLabelValues(svc.Namespace, svc.Name, decisionLabel(decision)).Inc()
			decisions[i] = decision
		}()
	}
	wg.Wait()

	grouped := map[int32][]discoveryv1.Endpoint{}
	for i, c := range candidates {
		switch decisions[i] {
		case Exclude:
			continue
		case Abstain:
			_, published := previous[membershipKey{port: sp.Name, family: family, target: c.port.Port, pod: c.pod.UID}]
			log.FromContext(ctx).V(1).Info("membership check abstained; keeping previously published state",
				"pod", client.ObjectKeyFromObject(c.pod), "port", sp.Name, "previouslyPublished", published)
			if !published {
				continue
			}
		}

		grouped[c.port.Port] = append(grouped[c.port.Port], endpointForPod(svc, c.pod, c.port.Address, zones))
	}

	// With no members, keep a placeholder slice: at the statically resolved
	// target, or, for named targetPorts, at the lowest target any candidate
	// pod resolves. A named target no pod resolves is unknowable -- no slice.
	if len(grouped) == 0 {
		number, ok := staticTargetPort(sp)
		if !ok {
			number, ok = fallback, fallback != 0
		}
		if ok {
			grouped[number] = nil
		}
	}

	groups := make([]endpointGroup, 0, len(grouped))
	for number, endpoints := range grouped {
		slices.SortFunc(endpoints, func(a, b discoveryv1.Endpoint) int {
			// Tie-break equal addresses (e.g. hostNetwork pods sharing a
			// node IP) so ordering is deterministic across reconciles.
			if c := strings.Compare(a.Addresses[0], b.Addresses[0]); c != 0 {
				return c
			}
			return strings.Compare(string(a.TargetRef.UID), string(b.TargetRef.UID))
		})
		groups = append(groups, endpointGroup{
			port:      Port{Name: sp.Name, Port: number, Protocol: protocol, AppProtocol: sp.AppProtocol},
			endpoints: endpoints,
		})
	}
	slices.SortFunc(groups, func(a, b endpointGroup) int {
		return int(a.port.Port) - int(b.port.Port)
	})

	return groups
}

// renderSlice renders the EndpointSlice for one endpoint group chunk.
func (r *reconciler) renderSlice(svc *corev1.Service, name string, family discoveryv1.AddressType, port Port, endpoints []discoveryv1.Endpoint) (*discoveryv1.EndpointSlice, error) {
	slice := &discoveryv1.EndpointSlice{
		// Server-side apply requires TypeMeta.
		TypeMeta: metav1.TypeMeta{
			APIVersion: discoveryv1.SchemeGroupVersion.String(),
			Kind:       "EndpointSlice",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: svc.Namespace,
			Labels: map[string]string{
				discoveryv1.LabelServiceName: svc.Name,
				discoveryv1.LabelManagedBy:   r.cfg.ManagedBy,
			},
		},
		AddressType: family,
		Ports: []discoveryv1.EndpointPort{{
			// Must mirror the Service port's name so kube-proxy can map
			// Service ports onto endpoint ports.
			Name:        ptr.To(port.Name),
			Port:        ptr.To(port.Port),
			Protocol:    ptr.To(port.Protocol),
			AppProtocol: port.AppProtocol,
		}},
		Endpoints: endpoints,
	}

	if err := controllerutil.SetControllerReference(svc, slice, r.scheme); err != nil {
		return nil, errors.WithStack(err)
	}

	return slice, nil
}

// syncSlices makes the cluster match desired: owned slices that are no
// longer wanted are deleted, changed ones are re-applied, and missing ones
// are created. A nil desired deletes everything this controller owns for
// svc.
func (r *reconciler) syncSlices(ctx context.Context, svc *corev1.Service, existing []discoveryv1.EndpointSlice, desired map[string]*discoveryv1.EndpointSlice) error {
	logger := log.FromContext(ctx)

	var errs []error
	seen := make(map[string]bool, len(existing))
	for i := range existing {
		current := &existing[i]

		want, ok := desired[current.Name]
		// AddressType is immutable: a mismatch deletes the slice and
		// recreates it on a subsequent reconcile.
		if !ok || current.AddressType != want.AddressType {
			logger.Info("deleting EndpointSlice", "endpointslice", client.ObjectKeyFromObject(current))
			if err := r.client.Delete(ctx, current); err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, errors.WithStack(err))
			}
			continue
		}
		seen[current.Name] = true

		if !sliceChanged(current, want) {
			continue
		}

		logger.Info("applying EndpointSlice", "endpointslice", client.ObjectKeyFromObject(want), "endpoints", len(want.Endpoints))
		if err := r.applySlice(ctx, want); err != nil {
			errs = append(errs, err)
		}
	}

	for name, want := range desired {
		if seen[name] {
			continue
		}

		// A slice by this name that our label-filtered listing didn't return
		// is either another controller's or ours with tampered labels. Only
		// adopt it (repairing the labels via the apply below) when its owner
		// reference points at this Service AND no other manager claims it --
		// the owner reference alone isn't enough, because the native
		// controller and other Mappers reference the same Service. Anything
		// else is a name collision to report, not something to overwrite.
		var found discoveryv1.EndpointSlice
		err := r.client.Get(ctx, client.ObjectKey{Namespace: svc.Namespace, Name: name}, &found)
		switch {
		case err != nil && !apierrors.IsNotFound(err):
			errs = append(errs, errors.WithStack(err))
			continue
		case err == nil:
			owner := metav1.GetControllerOf(&found)
			manager := found.Labels[discoveryv1.LabelManagedBy]
			if owner == nil || owner.UID != svc.UID || (manager != "" && manager != r.cfg.ManagedBy) {
				collision := errors.Newf(
					"EndpointSlice %q already belongs to service %q (managed by %q); rename the service or its ports to disambiguate",
					name, found.Labels[discoveryv1.LabelServiceName], manager)
				if r.recorder != nil {
					r.recorder.Event(svc, corev1.EventTypeWarning, "EndpointSliceNameCollision", collision.Error())
				}
				nameCollisions.WithLabelValues(svc.Namespace, svc.Name).Inc()
				errs = append(errs, collision)
				continue
			}
		}

		logger.Info("applying EndpointSlice", "endpointslice", client.ObjectKeyFromObject(want), "endpoints", len(want.Endpoints))
		if err := r.applySlice(ctx, want); err != nil {
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}

// applySlice server-side applies a fully rendered slice, claiming any fields
// other managers may have written.
func (r *reconciler) applySlice(ctx context.Context, slice *discoveryv1.EndpointSlice) error {
	return errors.WithStack(r.client.Patch(ctx, slice, client.Apply,
		client.FieldOwner(r.cfg.ManagedBy), client.ForceOwnership))
}

// mapPodToServices enqueues every Service in the pod's namespace aligned to
// the pod's group.
func (r *reconciler) mapPodToServices(ctx context.Context, obj client.Object) []reconcile.Request {
	group, ok := r.cfg.PodKey.valueOf(obj)
	if !ok {
		return nil
	}

	opts := []client.ListOption{client.InNamespace(obj.GetNamespace())}
	switch {
	case r.cfg.ServiceKey.Kind == KeyKindLabel:
		opts = append(opts, client.MatchingLabels{r.cfg.ServiceKey.Name: group})
	case r.serviceIndex != "":
		opts = append(opts, client.MatchingFields{r.serviceIndex: group})
	}

	var services corev1.ServiceList
	if err := r.client.List(ctx, &services, opts...); err != nil {
		log.FromContext(ctx).Error(err, "listing services for pod", "pod", client.ObjectKeyFromObject(obj))
		return nil
	}

	var requests []reconcile.Request
	for i := range services.Items {
		if value, ok := r.cfg.ServiceKey.valueOf(&services.Items[i]); ok && value == group {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&services.Items[i])})
		}
	}

	return requests
}

// mapSliceToService enqueues the Service named by a published slice's
// service-name label so tampered or deleted slices get repaired.
func mapSliceToService(_ context.Context, obj client.Object) []reconcile.Request {
	name := obj.GetLabels()[discoveryv1.LabelServiceName]
	if name == "" {
		return nil
	}

	return []reconcile.Request{{
		NamespacedName: client.ObjectKey{Namespace: obj.GetNamespace(), Name: name},
	}}
}

// serviceFamilies maps spec.ipFamilies onto slice address types, defaulting
// to IPv4 when unset.
func serviceFamilies(svc *corev1.Service) []discoveryv1.AddressType {
	families := make([]discoveryv1.AddressType, 0, len(svc.Spec.IPFamilies))
	for _, family := range svc.Spec.IPFamilies {
		switch family {
		case corev1.IPv4Protocol:
			families = append(families, discoveryv1.AddressTypeIPv4)
		case corev1.IPv6Protocol:
			families = append(families, discoveryv1.AddressTypeIPv6)
		}
	}
	if len(families) == 0 {
		return []discoveryv1.AddressType{discoveryv1.AddressTypeIPv4}
	}
	return families
}

// podAddress picks the pod IP matching the slice's address family,
// canonicalized (e.g. an IPv4-mapped "::ffff:10.0.0.1" publishes as
// "10.0.0.1").
func podAddress(pod *corev1.Pod, family discoveryv1.AddressType) (string, bool) {
	ips := pod.Status.PodIPs
	if len(ips) == 0 && pod.Status.PodIP != "" {
		ips = []corev1.PodIP{{IP: pod.Status.PodIP}}
	}

	for _, ip := range ips {
		parsed := net.ParseIP(ip.IP)
		if parsed == nil {
			continue
		}
		if (parsed.To4() != nil) == (family == discoveryv1.AddressTypeIPv4) {
			return parsed.String(), true
		}
	}

	return "", false
}

// staticTargetPort resolves targetPorts that don't depend on a pod: numeric
// ones, or none at all (defaulting to the port itself). Named targetPorts
// report false.
func staticTargetPort(sp corev1.ServicePort) (int32, bool) {
	switch sp.TargetPort.Type {
	case intstr.String:
		if sp.TargetPort.StrVal != "" {
			return 0, false
		}
	case intstr.Int:
		if sp.TargetPort.IntVal != 0 {
			return sp.TargetPort.IntVal, true
		}
	}
	return sp.Port, true
}

// resolveTargetPort resolves a Service port's targetPort for a specific pod;
// named targetPorts are looked up among the pod's container ports --
// including restartable init (sidecar) containers -- matching name and
// protocol like the native controller.
func resolveTargetPort(pod *corev1.Pod, sp corev1.ServicePort, protocol corev1.Protocol) (int32, bool) {
	if number, ok := staticTargetPort(sp); ok {
		return number, true
	}

	scan := func(ports []corev1.ContainerPort) (int32, bool) {
		for _, containerPort := range ports {
			containerProtocol := containerPort.Protocol
			if containerProtocol == "" {
				containerProtocol = corev1.ProtocolTCP
			}
			if containerPort.Name == sp.TargetPort.StrVal && containerProtocol == protocol {
				return containerPort.ContainerPort, true
			}
		}
		return 0, false
	}

	for _, container := range pod.Spec.Containers {
		if number, ok := scan(container.Ports); ok {
			return number, true
		}
	}
	for _, container := range pod.Spec.InitContainers {
		if container.RestartPolicy == nil || *container.RestartPolicy != corev1.ContainerRestartPolicyAlways {
			continue
		}
		if number, ok := scan(container.Ports); ok {
			return number, true
		}
	}
	return 0, false
}

// routable reports whether a pod can plausibly receive traffic at all; finer
// decisions belong to the configured [Checker]. Terminating pods stay
// routable so they can be published for graceful drain, while
// Succeeded/Failed pods have released their IPs.
func routable(pod *corev1.Pod) bool {
	return pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed
}

func endpointForPod(svc *corev1.Service, pod *corev1.Pod, address string, zones map[string]string) discoveryv1.Endpoint {
	terminating := !pod.DeletionTimestamp.IsZero()

	endpoint := discoveryv1.Endpoint{
		Addresses: []string{address},
		Conditions: discoveryv1.EndpointConditions{
			// Published endpoints passed membership by definition, so
			// they're always serving; readiness additionally requires the
			// pod not be draining.
			Ready:       ptr.To(svc.Spec.PublishNotReadyAddresses || !terminating),
			Serving:     ptr.To(true),
			Terminating: ptr.To(terminating),
		},
		TargetRef: &corev1.ObjectReference{
			Kind:      "Pod",
			Namespace: pod.Namespace,
			Name:      pod.Name,
			UID:       pod.UID,
		},
	}

	if pod.Spec.NodeName != "" {
		endpoint.NodeName = ptr.To(pod.Spec.NodeName)
		if zone := zones[pod.Spec.NodeName]; zone != "" {
			endpoint.Zone = ptr.To(zone)
		}
	}

	// Hostname feeds per-pod DNS and only applies when the pod's subdomain
	// targets this Service, mirroring the native controller.
	if pod.Spec.Hostname != "" && pod.Spec.Subdomain == svc.Name {
		endpoint.Hostname = ptr.To(pod.Spec.Hostname)
	}

	endpoint.Hints = hintsForEndpoint(svc, &endpoint)

	return endpoint
}

// hintsForEndpoint renders topology hints from spec.trafficDistribution: the
// endpoint's own zone for PreferClose/PreferSameZone, its own node (with the
// zone as fallback tier) for PreferSameNode.
func hintsForEndpoint(svc *corev1.Service, endpoint *discoveryv1.Endpoint) *discoveryv1.EndpointHints {
	// topology-mode Auto asks for proportional hints this controller doesn't
	// implement; matching native precedence (the annotation overrides
	// trafficDistribution), publish no hints at all. Reconcile surfaces the
	// gap as a warning Event.
	if svc.Spec.TrafficDistribution == nil || topologyHintsRequested(svc) {
		return nil
	}

	switch *svc.Spec.TrafficDistribution {
	case corev1.ServiceTrafficDistributionPreferClose, corev1.ServiceTrafficDistributionPreferSameZone:
		if endpoint.Zone != nil {
			return &discoveryv1.EndpointHints{ForZones: []discoveryv1.ForZone{{Name: *endpoint.Zone}}}
		}
	case corev1.ServiceTrafficDistributionPreferSameNode:
		hints := &discoveryv1.EndpointHints{}
		if endpoint.Zone != nil {
			hints.ForZones = []discoveryv1.ForZone{{Name: *endpoint.Zone}}
		}
		if endpoint.NodeName != nil {
			hints.ForNodes = []discoveryv1.ForNode{{Name: *endpoint.NodeName}}
		}
		if len(hints.ForZones) > 0 || len(hints.ForNodes) > 0 {
			return hints
		}
	}

	return nil
}

// topologyHintsRequested reports whether the Service asks for
// controller-computed topology hints, matching the native controller's
// annotation handling (the deprecated annotation wins when both are set).
func topologyHintsRequested(svc *corev1.Service) bool {
	value, ok := svc.Annotations[corev1.DeprecatedAnnotationTopologyAwareHints]
	if !ok {
		value, ok = svc.Annotations[corev1.AnnotationTopologyMode]
		if !ok {
			return false
		}
	}
	return value == "Auto" || value == "auto"
}

// chunkEndpoints splits endpoints into chunks of at most size. An empty set
// still yields one (empty) chunk so the Service keeps a placeholder slice.
func chunkEndpoints(endpoints []discoveryv1.Endpoint, size int) [][]discoveryv1.Endpoint {
	if len(endpoints) == 0 {
		return [][]discoveryv1.Endpoint{nil}
	}
	return slices.Collect(slices.Chunk(endpoints, size))
}

// sliceName deterministically names one rendered slice:
//
//	<service>-<port name or number>[--ipv6][--p<target port>][--<chunk>]
//
// Every generated suffix starts with "--", which the API forbids inside port
// names (no adjacent hyphens), so a generated name can never collide with
// another port's plain <service>-<port> name from the same Service -- and
// the three suffix shapes can't be mistaken for each other. Named
// targetPorts always carry their resolved number, so a group keeps its name
// no matter which sibling groups come and go. Collisions between different
// Services (Service "my" with port "service-http" vs Service "my-service"
// with port "http") are still possible; syncSlices reports those instead of
// fighting over the slice.
func sliceName(svcName string, sp corev1.ServicePort, family discoveryv1.AddressType, targetPort int32, chunkIdx int) string {
	id := sp.Name
	if id == "" {
		id = strconv.Itoa(int(sp.Port))
	}

	name := svcName + "-" + id
	if family == discoveryv1.AddressTypeIPv6 {
		name += "--ipv6"
	}
	if _, static := staticTargetPort(sp); !static {
		name += "--p" + strconv.Itoa(int(targetPort))
	}
	if chunkIdx > 0 {
		name += "--" + strconv.Itoa(chunkIdx+1)
	}

	return name
}

func sliceChanged(current, desired *discoveryv1.EndpointSlice) bool {
	for key, value := range desired.Labels {
		if current.Labels[key] != value {
			return true
		}
	}
	if owner := metav1.GetControllerOf(current); owner == nil || owner.UID != desired.OwnerReferences[0].UID {
		return true
	}
	if !apiequality.Semantic.DeepEqual(current.Ports, desired.Ports) {
		return true
	}
	return !endpointsEqual(current.Endpoints, desired.Endpoints)
}

// endpointsEqual compares endpoints element-wise, treating nil and empty
// lists as equivalent.
func endpointsEqual(current, desired []discoveryv1.Endpoint) bool {
	if len(current) != len(desired) {
		return false
	}
	for i := range desired {
		if !endpointEqual(&current[i], &desired[i]) {
			return false
		}
	}
	return true
}

// endpointEqual tolerates a published endpoint whose forNodes hints the API
// server stripped (the PreferSameTrafficDistribution feature gate is off) so
// the controller doesn't loop re-applying a field that will never persist;
// stale forNodes the desired state no longer wants still register as a
// change.
func endpointEqual(current, desired *discoveryv1.Endpoint) bool {
	if apiequality.Semantic.DeepEqual(current, desired) {
		return true
	}
	if desired.Hints == nil || len(desired.Hints.ForNodes) == 0 {
		return false
	}

	stripped := *desired
	hints := *desired.Hints
	hints.ForNodes = nil
	stripped.Hints = &hints
	if apiequality.Semantic.DeepEqual(current, &stripped) {
		return true
	}
	// A server may also normalize the then-empty hints struct away entirely.
	if len(hints.ForZones) == 0 {
		stripped.Hints = nil
		return apiequality.Semantic.DeepEqual(current, &stripped)
	}
	return false
}
