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
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// [Config.PublishLegacyEndpoints] exists precisely to serve consumers of the
// deprecated core/v1 Endpoints API, so the deprecated types are aliased once
// here rather than acknowledged at every use.
type (
	legacyEndpoints     = corev1.Endpoints       //nolint:staticcheck // see above
	legacyEndpointsList = corev1.EndpointsList   //nolint:staticcheck // see above
	legacySubset        = corev1.EndpointSubset  //nolint:staticcheck // see above
	legacyAddress       = corev1.EndpointAddress //nolint:staticcheck // see above
	legacyPort          = corev1.EndpointPort
)

const (
	// legacyEndpointsMaxAddresses mirrors the native endpoints controller's
	// cap on the total addresses one Endpoints object carries.
	legacyEndpointsMaxAddresses = 1000

	// endpointsOverCapacityTruncated marks a truncated Endpoints object,
	// with the same value the native endpoints controller writes under
	// [corev1.EndpointsOverCapacity].
	endpointsOverCapacityTruncated = "truncated"
)

// legacyOwnership classifies who holds a Service's Endpoints object.
type legacyOwnership int

const (
	// ownershipOurs: labeled as this controller's, with no conflicting
	// controller owner.
	ownershipOurs legacyOwnership = iota
	// ownershipOursTampered: controller-owned by the Service but with the
	// manager label stripped -- the ownerReference, which this controller
	// always sets and tampering rarely touches, is the stronger signal. Only
	// the publish path acts on this (repairing the label); deletion paths
	// require the label, so a third-party object that merely owner-refs the
	// Service is never deleted by teardown or the startup sweep.
	ownershipOursTampered
	// ownershipAbandoned: no manager label and no controller owner -- the
	// shape the native endpoints controller leaves behind when a Service's
	// selector is removed. Adoptable.
	ownershipAbandoned
	// ownershipForeign: another manager's label, or a controller owner other
	// than the Service (including this controller's own leftover for a
	// deleted-and-recreated Service, until GC reaps it). Never touched.
	ownershipForeign
)

func classifyLegacyEndpoints(endpoints *legacyEndpoints, svc *corev1.Service, managedBy string) legacyOwnership {
	manager := endpoints.Labels[discoveryv1.LabelManagedBy]
	owner := metav1.GetControllerOf(endpoints)
	ownedBySvc := owner != nil && owner.UID == svc.UID

	switch {
	case manager == managedBy && (owner == nil || ownedBySvc):
		return ownershipOurs
	case manager == "" && ownedBySvc:
		return ownershipOursTampered
	case manager == "" && owner == nil:
		return ownershipAbandoned
	default:
		return ownershipForeign
	}
}

// syncLegacyEndpoints converges a managed Service's legacy Endpoints mirror
// to what the configuration demands: published (mirroring the desired
// slices) with [Config.PublishLegacyEndpoints] set, and absent otherwise --
// with the option off, or while the Service has a selector, an owned mirror
// is deleted. The returned bool reports whether the mirror is in this
// controller's hands (published or adopted, with the skip-mirror label
// applied): the signal cleanupNativeEndpoints needs before it may delete the
// mirroring controller's slices, since only a skip-mirror-labeled object
// stops them from being recreated.
func (r *reconciler) syncLegacyEndpoints(ctx context.Context, svc *corev1.Service, desired map[string]*discoveryv1.EndpointSlice) (bool, error) {
	if !r.cfg.PublishLegacyEndpoints {
		// The startup sweep is one-shot; converging here as well removes a
		// mirror recreated after it (say, by a not-yet-terminated replica
		// still running the old configuration during a rolling downgrade).
		// The sweep's informer is warm by now, so this is an in-memory
		// lookup that is almost always not-found.
		return false, r.deleteLegacyEndpoints(ctx, svc)
	}
	if svc.Spec.Selector != nil {
		// The native endpoints controller owns the Endpoints object of any
		// Service with a selector; competing for the same object would just
		// fight it write-for-write (the selector warning already covers this
		// state). Relinquish anything published before the selector
		// (re)appeared -- the native controller rebuilds the object from the
		// selector -- rather than leaving it mislabeled as ours. Publish
		// warnings reset here so a condition that clears during the selector
		// interlude warns again afterwards.
		r.clearLegacyPublishWarnings(client.ObjectKeyFromObject(svc))
		return false, r.deleteLegacyEndpoints(ctx, svc)
	}

	want, err := r.renderLegacyEndpoints(svc, desired)
	if err != nil {
		return false, err
	}

	found := true
	var current legacyEndpoints
	switch err := r.client.Get(ctx, client.ObjectKeyFromObject(want), &current); {
	case apierrors.IsNotFound(err):
		found = false
	case err != nil:
		return false, errors.WithStack(err)
	}

	adopting, proceed := r.gateLegacyPublish(ctx, svc, &current, want, found)
	if !proceed {
		return false, nil
	}

	if !found {
		// A cached not-found can be stale (informer catching up); Create
		// surfaces that race as AlreadyExists instead of force-applying over
		// a live object the ownership checks above never saw.
		log.FromContext(ctx).Info("creating legacy Endpoints", "endpoints", client.ObjectKeyFromObject(want), "subsets", len(want.Subsets))
		return true, errors.WithStack(r.client.Create(ctx, want))
	}

	if !legacyEndpointsChanged(&current, want) {
		return true, nil
	}
	if err := r.stripInheritedMetadata(ctx, &current, want, adopting); err != nil {
		return false, err
	}

	// Carrying the read object's resourceVersion into the apply makes it an
	// optimistic-concurrency guard, mirroring what Create and Delete already
	// do for their halves of the stale-cache race: if the object changed
	// hands between the cached read and this write, the apply conflicts
	// cleanly instead of force-overwriting an owner the classification never
	// saw. (stripInheritedMetadata refreshes current from its patch
	// response, so a strip doesn't self-conflict here.)
	want.ResourceVersion = current.ResourceVersion

	log.FromContext(ctx).Info("applying legacy Endpoints", "endpoints", client.ObjectKeyFromObject(want), "subsets", len(want.Subsets))
	if err := r.apply(ctx, want); err != nil {
		return false, err
	}
	if adopting {
		r.event(svc, corev1.EventTypeNormal, "LegacyEndpointsAdopted",
			"Adopted the legacy Endpoints object the native controller abandoned; it now mirrors the published EndpointSlices.")
	}
	return true, nil
}

// clearLegacyPublishWarnings re-arms the publish-state warnings for a
// Service whose mirror is deliberately not being published right now, so the
// warnOnce contract (once per occurrence) holds across selector interludes.
func (r *reconciler) clearLegacyPublishWarnings(key client.ObjectKey) {
	for _, flag := range []uint8{warnedEndpointsCollision, warnedForeignEndpoints, warnedDeferredAdoption} {
		r.warnOnce(key, flag, false)
	}
}

// gateLegacyPublish evaluates the tolerated-but-unserved states -- a
// collision with someone else's object, DisableNativeCleanup forbidding
// adoption, and the adoption blackout safeguard -- reporting whether
// publishing may proceed and whether it would adopt an abandoned object.
// Every warnOnce receives its live condition so warnings re-arm once the
// state clears; none of the states is an error, so the reconcile continues
// to the native cleanup and keeps its ResyncPeriod requeue.
func (r *reconciler) gateLegacyPublish(ctx context.Context, svc *corev1.Service, current, want *legacyEndpoints, found bool) (adopting, proceed bool) {
	key := client.ObjectKeyFromObject(svc)
	logger := log.FromContext(ctx)

	ownership := ownershipAbandoned
	if found {
		ownership = classifyLegacyEndpoints(current, svc, r.cfg.ManagedBy)
	}

	collision := found && ownership == ownershipForeign
	if r.warnOnce(key, warnedEndpointsCollision, collision) {
		message := fmt.Sprintf("Endpoints %q already belongs to another manager (managed-by %q); not publishing the legacy mirror",
			current.Name, current.Labels[discoveryv1.LabelManagedBy])
		logger.Info("WARNING: " + message)
		r.event(svc, corev1.EventTypeWarning, "EndpointsCollision", message)
	}

	adopting = found && ownership == ownershipAbandoned
	blocked := adopting && r.cfg.DisableNativeCleanup
	if r.warnOnce(key, warnedForeignEndpoints, blocked) {
		logger.Info("WARNING: not adopting the service's existing Endpoints object because DisableNativeCleanup is set; no legacy Endpoints will be published",
			"endpoints", client.ObjectKeyFromObject(current))
	}

	// Adopting an object whose addresses would be replaced with nothing
	// routable blacks out legacy consumers -- the same safeguard the native
	// cleanup applies to slices. An abandoned object that is itself empty
	// protects nothing, so it is adopted regardless.
	deferred := adopting && !blocked && !anyReadyLegacyAddresses(want) && legacyEndpointsHasAddresses(current)
	if r.warnOnce(key, warnedDeferredAdoption, deferred) {
		message := "deferring adoption of the service's abandoned Endpoints object until this controller publishes ready addresses for the service's primary family; its stale addresses remain live until then"
		logger.Info("WARNING: "+message, "endpoints", client.ObjectKeyFromObject(current))
		r.event(svc, corev1.EventTypeWarning, "LegacyEndpointsAdoptionDeferred", message)
	}

	return adopting, !collision && !blocked && !deferred
}

// anyReadyLegacyAddresses reports whether the rendered mirror carries at
// least one ready address.
func anyReadyLegacyAddresses(endpoints *legacyEndpoints) bool {
	for _, subset := range endpoints.Subsets {
		if len(subset.Addresses) > 0 {
			return true
		}
	}
	return false
}

// legacyEndpointsHasAddresses reports whether the object carries any address
// at all, ready or not.
func legacyEndpointsHasAddresses(endpoints *legacyEndpoints) bool {
	for _, subset := range endpoints.Subsets {
		if len(subset.Addresses) > 0 || len(subset.NotReadyAddresses) > 0 {
			return true
		}
	}
	return false
}

// deleteLegacyEndpoints removes the Service's published Endpoints object, if
// this controller owns one; anything else -- the native controller's object,
// a third party's, or our own label-stripped object (deleting on the
// ownerRef alone could destroy a third-party object that merely owner-refs
// the Service) -- is left alone. Cache staleness is harmless: the UID
// precondition makes a stale delete racing a replacement (the native
// controller rebuilding the object during a selector rollback) a no-op
// instead of a casualty, and a stale skip retries later -- via the Endpoints
// watch when the option is on; via the per-reconcile converge or the next
// startup sweep when it is off.
//
// Known gap, accepted: a mirror whose label is stripped in the same window
// its Service is unmanaged has no deleter left (repair only runs on the
// publish path) and needs manual cleanup.
func (r *reconciler) deleteLegacyEndpoints(ctx context.Context, svc *corev1.Service) error {
	var current legacyEndpoints
	switch err := r.client.Get(ctx, client.ObjectKeyFromObject(svc), &current); {
	case apierrors.IsNotFound(err):
		return nil
	case err != nil:
		return errors.WithStack(err)
	}
	if classifyLegacyEndpoints(&current, svc, r.cfg.ManagedBy) != ownershipOurs {
		return nil
	}

	log.FromContext(ctx).Info("deleting legacy Endpoints", "endpoints", client.ObjectKeyFromObject(&current))
	err := r.client.Delete(ctx, &current, client.Preconditions{UID: &current.UID})
	if err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
		return errors.WithStack(err)
	}
	return nil
}

// sweepLegacyEndpoints deletes every Endpoints object labeled as managedBy's
// -- the mirrors a previous configuration with PublishLegacyEndpoints
// published. It runs once at startup when the option is off (the option only
// changes across a restart), covering Services that nothing would ever
// reconcile again (unmanaged, or deleted mid-teardown); mirrors of Services
// still reconciling are additionally converged away on every pass. Failures
// are retried briefly and then logged -- on every exit path, including
// shutdown -- and a failed sweep never stops the manager.
func sweepLegacyEndpoints(ctx context.Context, c client.Client, managedBy string) error {
	logger := log.FromContext(ctx)

	attempt := func(ctx context.Context) error {
		var list legacyEndpointsList
		if err := c.List(ctx, &list, client.MatchingLabels{discoveryv1.LabelManagedBy: managedBy}); err != nil {
			return errors.WithStack(err)
		}
		var errs []error
		for i := range list.Items {
			item := &list.Items[i]
			// The label alone isn't proof of ownership: every mirror this
			// controller publishes is controller-owned by a Service, so
			// anything else carrying the label (a third party's object with
			// its own controller reference) is not ours to delete. A stale
			// Service UID is fine -- sweeping mirrors whose Service is gone
			// is exactly the job.
			if owner := metav1.GetControllerOf(item); owner != nil && (owner.APIVersion != "v1" || owner.Kind != "Service") {
				continue
			}
			logger.Info("sweeping legacy Endpoints left behind by a previous PublishLegacyEndpoints configuration",
				"endpoints", client.ObjectKeyFromObject(item))
			if err := c.Delete(ctx, item, client.Preconditions{UID: &item.UID}); err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
				errs = append(errs, errors.WithStack(err))
			}
		}
		return utilerrors.NewAggregate(errs)
	}

	var lastErr error
	err := wait.ExponentialBackoffWithContext(ctx, wait.Backoff{Duration: time.Second, Factor: 2, Steps: 3}, func(ctx context.Context) (bool, error) {
		lastErr = attempt(ctx)
		return lastErr == nil, nil
	})
	if err != nil {
		if lastErr == nil {
			lastErr = err
		}
		logger.Error(lastErr, "failed to sweep leftover legacy Endpoints; they may need manual deletion",
			"selector", discoveryv1.LabelManagedBy+"="+managedBy)
	}
	return nil
}

// legacyEntry is one pod endpoint's contribution to a legacy Endpoints
// object: its rendered address, its readiness, and every port it serves.
// Entries are per pod, like the native controller's repacking: pods sharing
// an address stay separate entries, so their differing readiness or port
// sets land in the right subsets instead of merging.
type legacyEntry struct {
	address legacyAddress
	ready   bool
	ports   []legacyPort
}

// legacyAddressFor renders a slice endpoint as a legacy address.
func legacyAddressFor(endpoint *discoveryv1.Endpoint) legacyAddress {
	return legacyAddress{
		IP:        endpoint.Addresses[0],
		Hostname:  ptr.Deref(endpoint.Hostname, ""),
		NodeName:  endpoint.NodeName,
		TargetRef: endpoint.TargetRef,
	}
}

// legacyEntriesForFamily gathers one entry per pod endpoint of the given
// family from the desired slices, deduplicating ports across the slices that
// mention the pod. Slices are visited in name order so the outcome never
// depends on map iteration.
func legacyEntriesForFamily(desired map[string]*discoveryv1.EndpointSlice, family discoveryv1.AddressType) map[string]*legacyEntry {
	entries := map[string]*legacyEntry{}
	for _, name := range slices.Sorted(maps.Keys(desired)) {
		slice := desired[name]
		if slice.AddressType != family || len(slice.Ports) == 0 {
			continue
		}
		port := legacyPort{
			Name:        ptr.Deref(slice.Ports[0].Name, ""),
			Port:        ptr.Deref(slice.Ports[0].Port, 0),
			Protocol:    ptr.Deref(slice.Ports[0].Protocol, corev1.ProtocolTCP),
			AppProtocol: slice.Ports[0].AppProtocol,
		}
		for i := range slice.Endpoints {
			endpoint := &slice.Endpoints[i]
			ready := ptr.Deref(endpoint.Conditions.Ready, true)
			if !ready && ptr.Deref(endpoint.Conditions.Terminating, false) {
				// The native controller drops terminating pods from
				// Endpoints entirely (they stay only under
				// publishNotReadyAddresses, which renders them ready);
				// consumers read notReadyAddresses as pods that will
				// *become* ready, the opposite of draining.
				continue
			}
			key := endpoint.Addresses[0]
			if endpoint.TargetRef != nil {
				key += "/" + string(endpoint.TargetRef.UID)
			}
			e, ok := entries[key]
			if !ok {
				e = &legacyEntry{
					address: legacyAddressFor(endpoint),
					ready:   ready,
				}
				entries[key] = e
			}
			if !slices.ContainsFunc(e.ports, func(p legacyPort) bool { return legacyPortID(p) == legacyPortID(port) }) {
				e.ports = append(e.ports, port)
			}
		}
	}
	return entries
}

// legacySubsetsFor packs entries into subsets -- one per distinct set of
// served ports, like the native controller's repacking -- capping the total
// addresses at legacyEndpointsMaxAddresses and reporting whether anything
// was truncated. Under the cap, ready addresses across every subset take
// priority over any not-ready address. Zero subsets renders as nil so the
// result compares equal to the API server's stored form.
func legacySubsetsFor(entries map[string]*legacyEntry) (subsets []legacySubset, truncated bool) {
	groups, keys := groupLegacyEntries(entries)

	budget := legacyEndpointsMaxAddresses
	packed := make([]legacySubset, len(keys))
	for i, key := range keys {
		packed[i] = legacySubset{Ports: groups[key][0].ports}
	}
	for _, ready := range []bool{true, false} {
		for i, key := range keys {
			if !packLegacyAddresses(&packed[i], groups[key], ready, &budget) {
				truncated = true
			}
		}
	}

	for _, subset := range packed {
		if len(subset.Addresses) > 0 || len(subset.NotReadyAddresses) > 0 {
			subsets = append(subsets, subset)
		}
	}
	return subsets, truncated
}

// groupLegacyEntries groups entries by the exact set of ports they serve,
// returning the groups and their sorted keys, each group sorted for
// deterministic output.
func groupLegacyEntries(entries map[string]*legacyEntry) (map[string][]*legacyEntry, []string) {
	groups := map[string][]*legacyEntry{}
	for _, e := range entries {
		slices.SortFunc(e.ports, compareLegacyPorts)
		key := legacyPortsKey(e.ports)
		groups[key] = append(groups[key], e)
	}

	for _, group := range groups {
		slices.SortFunc(group, func(a, b *legacyEntry) int {
			if c := strings.Compare(a.address.IP, b.address.IP); c != 0 {
				return c
			}
			if a.ready != b.ready {
				if a.ready {
					return -1
				}
				return 1
			}
			return strings.Compare(legacyTargetUID(a), legacyTargetUID(b))
		})
	}
	return groups, slices.Sorted(maps.Keys(groups))
}

// legacyTargetUID is the entry's pod UID, for deterministic tie-breaking of
// pods sharing an address.
func legacyTargetUID(e *legacyEntry) string {
	if e.address.TargetRef == nil {
		return ""
	}
	return string(e.address.TargetRef.UID)
}

// packLegacyAddresses appends the group's entries of the given readiness to
// subset while the shared budget lasts, reporting false once it runs out.
func packLegacyAddresses(subset *legacySubset, group []*legacyEntry, ready bool, budget *int) bool {
	fit := true
	for _, e := range group {
		if e.ready != ready {
			continue
		}
		if *budget == 0 {
			fit = false
			continue
		}
		*budget--
		if ready {
			subset.Addresses = append(subset.Addresses, e.address)
		} else {
			subset.NotReadyAddresses = append(subset.NotReadyAddresses, e.address)
		}
	}
	return fit
}

// renderLegacyEndpoints projects the desired slices onto one legacy
// Endpoints object: only the Service's primary address family (matching the
// native endpoints controller), with addresses grouped into subsets by the
// exact set of ports they serve, and the total capped like the native
// controller caps it.
func (r *reconciler) renderLegacyEndpoints(svc *corev1.Service, desired map[string]*discoveryv1.EndpointSlice) (*legacyEndpoints, error) {
	subsets, truncated := legacySubsetsFor(legacyEntriesForFamily(desired, serviceFamilies(svc)[0]))

	// The native controller stamps the Service's labels (and the headless
	// marker) onto the Endpoints object; label-keyed consumers (kubectl
	// selectors, Prometheus endpoints discovery) depend on that. A label
	// removed from the Service lingers on the mirror until the next content
	// change re-applies (server-side apply prunes owned fields then).
	labels := make(map[string]string, len(svc.Labels)+3)
	maps.Copy(labels, svc.Labels)
	if svc.Spec.ClusterIP == corev1.ClusterIPNone {
		labels[corev1.IsHeadlessService] = ""
	}
	labels[discoveryv1.LabelManagedBy] = r.cfg.ManagedBy
	// Without skip-mirror the EndpointSliceMirroring controller would mirror
	// this object into duplicate EndpointSlices.
	labels[discoveryv1.LabelSkipMirror] = "true"

	endpoints := &legacyEndpoints{
		// Server-side apply requires TypeMeta.
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "Endpoints",
		},
		ObjectMeta: metav1.ObjectMeta{
			// The Endpoints API requires the object share the Service's
			// name; that's how consumers find it.
			Name:      svc.Name,
			Namespace: svc.Namespace,
			Labels:    labels,
		},
		Subsets: subsets,
	}
	if truncated {
		endpoints.Annotations = map[string]string{corev1.EndpointsOverCapacity: endpointsOverCapacityTruncated}
	}

	if err := controllerutil.SetControllerReference(svc, endpoints, r.scheme); err != nil {
		return nil, errors.WithStack(err)
	}

	return endpoints, nil
}

// stripInheritedMetadata removes annotations another manager wrote that this
// controller will never send: server-side apply cannot remove a field it
// doesn't own by omitting it, so without this the drift detection never
// converges (over-capacity) or a stale native timestamp lingers forever
// (last-change-trigger-time, misdescribing an adopted object's freshness).
// The patch is optimistically locked and refreshes current from the
// response, so the subsequent apply neither races a replacement nor
// conflicts with the strip itself.
func (r *reconciler) stripInheritedMetadata(ctx context.Context, current, want *legacyEndpoints, adopting bool) error {
	base := current.DeepCopy()
	if want.Annotations[corev1.EndpointsOverCapacity] == "" {
		delete(current.Annotations, corev1.EndpointsOverCapacity)
	}
	if adopting {
		delete(current.Annotations, corev1.EndpointsLastChangeTriggerTime)
	}
	if maps.Equal(base.Annotations, current.Annotations) {
		return nil
	}

	patch := client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})
	if err := r.client.Patch(ctx, current, patch); err != nil && !apierrors.IsNotFound(err) {
		return errors.WithStack(err)
	}
	return nil
}

// legacyEndpointsChanged reports whether the published Endpoints object
// drifted from the rendered one in any field this controller manages.
func legacyEndpointsChanged(current, desired *legacyEndpoints) bool {
	if managedMetadataDrifted(current, desired) {
		return true
	}
	if current.Annotations[corev1.EndpointsOverCapacity] != desired.Annotations[corev1.EndpointsOverCapacity] {
		return true
	}
	return !apiequality.Semantic.DeepEqual(current.Subsets, desired.Subsets)
}

// legacyPortID is the single definition of a legacy port's identity, used
// for deduplication, ordering, and subset grouping alike.
func legacyPortID(port legacyPort) string {
	return fmt.Sprintf("%s/%d/%s/%s", port.Name, port.Port, port.Protocol, ptr.Deref(port.AppProtocol, ""))
}

// compareLegacyPorts orders subset ports deterministically by their
// identity.
func compareLegacyPorts(a, b legacyPort) int {
	return strings.Compare(legacyPortID(a), legacyPortID(b))
}

// legacyPortsKey renders a port set's identity for subset grouping.
func legacyPortsKey(ports []legacyPort) string {
	parts := make([]string, len(ports))
	for i, port := range ports {
		parts[i] = legacyPortID(port)
	}
	return strings.Join(parts, ",")
}
