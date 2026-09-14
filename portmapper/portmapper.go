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

// Package portmapper implements a library-style controller that publishes the
// EndpointSlices backing "colocated" Services -- Services whose ports are
// served by overlapping-but-distinct subsets of a shared pod pool.
//
// It replaces the Kubernetes-native EndpointSlice controller for Services
// that intentionally define no selector (so the two never conflict). The
// controller never creates or mutates Services or Pods; it aligns the two
// through object metadata:
//
//   - A Service opts in by carrying a configured label or annotation
//     ([Config.ServiceKey]) whose value names a pod group.
//   - Pods carrying a matching key/value ([Config.PodKey]) in the same
//     namespace become endpoint candidates for that Service.
//   - One EndpointSlice is published per Service port, and a pluggable
//     [Checker] decides -- per pod, per port -- whether the pod backs that
//     port, so each port can be served by a different subset of pods.
//
// For example, a Service "my-service" exposing ports 8080 and 8443 backed by
// pods A-D might render as:
//
//	Service: my-service
//	  ├── port 8080 → my-service-http  (pods A, B, C)
//	  └── port 8443 → my-service-https (pods B, C, D)
//
// Slices are written with server-side apply and otherwise mirror the native
// controller's semantics: dual-stack, per-pod named-targetPort resolution,
// chunking at [Config.MaxEndpointsPerSlice], graceful termination for
// terminating pods that still pass membership, and topology (node names,
// zones, hostnames, and spec.trafficDistribution hints). The
// "service.kubernetes.io/topology-mode: Auto" annotation is
// explicitly unsupported: such Services are published without topology hints
// and receive a warning Event.
//
// For consumers that still read the deprecated core/v1 Endpoints API,
// [Config.PublishLegacyEndpoints] additionally maintains a legacy Endpoints
// object mirroring the published slices.
package portmapper

import (
	"context"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// DefaultManagedBy is the managed-by label value used when
	// [Config.ManagedBy] is unset.
	DefaultManagedBy = "port-mapper"

	// DefaultResyncPeriod is the membership re-evaluation interval used when
	// [Config.ResyncPeriod] is unset.
	DefaultResyncPeriod = 30 * time.Second

	// DefaultMaxEndpointsPerSlice caps endpoints per slice when
	// [Config.MaxEndpointsPerSlice] is unset, matching the native
	// controller's default.
	DefaultMaxEndpointsPerSlice = 100

	// DefaultMaxConcurrentChecks bounds parallel membership checks when
	// [Config.MaxConcurrentChecks] is unset.
	DefaultMaxConcurrentChecks = 16

	// apiMaxEndpointsPerSlice is the hard cap the Kubernetes API enforces on
	// a single EndpointSlice.
	apiMaxEndpointsPerSlice = 1000

	// kubeManagedBy is reserved by the native EndpointSlice controller.
	kubeManagedBy = "endpointslice-controller.k8s.io"

	// mirrorManagedBy is reserved by the native EndpointSliceMirroring
	// controller, which mirrors the legacy Endpoints objects of selectorless
	// Services into EndpointSlices.
	mirrorManagedBy = "endpointslicemirroring-controller.k8s.io"
)

// KeyKind discriminates which piece of object metadata a [Key] refers to.
type KeyKind string

const (
	// KeyKindLabel aligns objects via their labels.
	KeyKindLabel KeyKind = "label"
	// KeyKindAnnotation aligns objects via their annotations.
	KeyKindAnnotation KeyKind = "annotation"
)

// Key identifies a piece of object metadata -- a label or an annotation --
// used to align Pods with the Services they back.
type Key struct {
	Kind KeyKind
	Name string
}

// LabelKey returns a [Key] that reads the named label.
func LabelKey(name string) Key { return Key{Kind: KeyKindLabel, Name: name} }

// AnnotationKey returns a [Key] that reads the named annotation.
func AnnotationKey(name string) Key { return Key{Kind: KeyKindAnnotation, Name: name} }

// valueOf returns the key's value on obj; absent or empty values report
// false.
func (k Key) valueOf(obj metav1.Object) (string, bool) {
	var meta map[string]string
	switch k.Kind {
	case KeyKindLabel:
		meta = obj.GetLabels()
	case KeyKindAnnotation:
		meta = obj.GetAnnotations()
	}

	value, ok := meta[k.Name]
	return value, ok && value != ""
}

// indexerFunc extracts k's value for cache field indexes.
func (k Key) indexerFunc() client.IndexerFunc {
	return func(obj client.Object) []string {
		if value, ok := k.valueOf(obj); ok {
			return []string{value}
		}
		return nil
	}
}

func (k Key) validate() error {
	switch k.Kind {
	case KeyKindLabel, KeyKindAnnotation:
	default:
		return errors.Newf("invalid key kind %q", k.Kind)
	}
	if errs := validation.IsQualifiedName(k.Name); len(errs) > 0 {
		return errors.Newf("invalid key name %q: %s", k.Name, strings.Join(errs, ", "))
	}
	return nil
}

// Port describes a single Service port as it will be published on an
// EndpointSlice, derived from the Service's spec.ports and handed to
// [Checker] implementations.
type Port struct {
	// Name is the Service port's name; it also anchors the names of the
	// port's slices ("<service>-<name>[...]").
	Name string
	// Port is the resolved target port on the pod being checked. Named
	// targetPorts resolve per pod, so the value can differ between pods of
	// the same Service port.
	Port int32
	// Protocol is the port's protocol, defaulting to TCP.
	Protocol corev1.Protocol
	// AppProtocol mirrors the Service port's appProtocol, if any.
	AppProtocol *string
	// Address is the pod IP whose membership is being evaluated -- the
	// address that would be published. Dual-stack Services evaluate each
	// family separately, so probes should target Address rather than the
	// pod's primary IP (the built-in network checkers do). Empty when a
	// checker is invoked outside a reconcile.
	Address string
}

// Config configures a [Mapper].
type Config struct {
	// ManagedBy is written to (and filtered on) the
	// "endpointslice.kubernetes.io/managed-by" label of every published
	// slice. Defaults to [DefaultManagedBy]; the native controllers' values
	// are rejected.
	ManagedBy string

	// ServiceKey is the label or annotation marking a Service as managed by
	// this controller; its value names the pod group backing the Service.
	// Managed Services should not define a selector.
	ServiceKey Key

	// PodKey is the label or annotation assigning a Pod to a pod group. A
	// Pod is an endpoint candidate for a Service when its PodKey value
	// equals the Service's ServiceKey value in the same namespace. Defaults
	// to ServiceKey.
	PodKey Key

	// Membership decides, per Service port, whether an aligned pod is
	// published as an endpoint. Only plausibly routable pods (running, with
	// an IP of the slice's family) are checked. A checker implementing
	// [Decider] may return [Abstain] to keep a pod's previously published
	// membership. Defaults to [PodReady].
	Membership Checker

	// ResyncPeriod bounds how stale membership can be: every managed Service
	// re-reconciles at this interval, re-running checks whose outcome can
	// change without an API event (e.g. [TCPDial]). Defaults to
	// [DefaultResyncPeriod].
	ResyncPeriod time.Duration

	// MaxEndpointsPerSlice caps endpoints per published slice; overflow
	// spills into "--2", "--3", ... slices. Defaults to
	// [DefaultMaxEndpointsPerSlice]; the API rejects values above 1000.
	MaxEndpointsPerSlice int

	// MaxConcurrentChecks bounds how many membership checks run in parallel
	// within one reconcile. Network probes block up to their timeout apiece,
	// so serial evaluation would cost pods x ports x families x timeout in
	// the worst case. Defaults to [DefaultMaxConcurrentChecks]; 1 evaluates
	// serially. Concurrent reconciles each get their own budget.
	MaxConcurrentChecks int

	// MaxConcurrentReconciles caps how many Services reconcile in parallel
	// (each Service is still serialized with itself). 0 defers to the
	// manager's default (normally 1). Raise it alongside network checkers so
	// one slow Service doesn't starve the rest.
	MaxConcurrentReconciles int

	// DisableNodeLookups skips resolving pods' Nodes, so endpoints omit
	// zones (and zone-based hints). Set it to avoid the Node RBAC and the
	// cluster-wide Node informer the first cache lookup starts.
	DisableNodeLookups bool

	// DisableNativeCleanup stops the controller from finishing selector
	// migrations itself. By default, once a managed Service has no selector,
	// the controller deletes what the native controllers left behind: their
	// stale EndpointSlices, and the legacy Endpoints object -- which would
	// otherwise keep feeding the EndpointSliceMirroring controller new stale
	// slices. Cleanup only acts when those leftovers are actually present,
	// so a long-migrated Service costs nothing. Set this when something else
	// legitimately manages those objects.
	DisableNativeCleanup bool

	// PublishLegacyEndpoints additionally publishes a legacy core/v1
	// Endpoints object (named after the Service, as the API requires)
	// mirroring the published EndpointSlices, for consumers that still read
	// the deprecated Endpoints API. Like the native endpoints controller,
	// the object carries the Service's labels (plus the headless marker),
	// publishes only the Service's primary address family, and omits
	// terminating pods; it additionally carries this controller's managed-by
	// label and the "endpointslice.kubernetes.io/skip-mirror" label, so the
	// native EndpointSliceMirroring controller ignores it. Services that
	// still define a selector are skipped -- the native endpoints controller
	// owns their Endpoints object until the selector is removed. An
	// Endpoints object that belongs to someone else -- another manager's
	// label, or a foreign controller owner -- is never overwritten; the
	// collision is reported as an Event. With DisableNativeCleanup also set,
	// even an unowned object is never adopted. Requires additional RBAC:
	// create and patch on "endpoints", which the generated markers
	// deliberately exclude (get, list, watch, and delete are needed even
	// without this option, for migration cleanup and for sweeping up mirrors
	// a previous configuration published).
	//
	// Disabling the option later cleans up after itself: published objects
	// are removed alongside the slices when a Service stops being served,
	// and a one-time startup sweep deletes every leftover mirror carrying
	// the ManagedBy label, so no manual deletion is ever needed.
	PublishLegacyEndpoints bool
}

// Mapper manages the EndpointSlices of every Service aligned to it via
// [Config.ServiceKey]. Construct one with [New] and register it via
// [Mapper.SetupWithManager].
type Mapper struct {
	cfg Config
}

// New validates cfg, applies defaults, and returns a [Mapper] ready to be
// registered with a manager.
func New(cfg Config) (*Mapper, error) {
	if cfg.ManagedBy == "" {
		cfg.ManagedBy = DefaultManagedBy
	}
	if cfg.ManagedBy == kubeManagedBy || cfg.ManagedBy == mirrorManagedBy {
		return nil, errors.Newf("ManagedBy %q is reserved by the Kubernetes-native endpoint controllers", cfg.ManagedBy)
	}
	if errs := validation.IsValidLabelValue(cfg.ManagedBy); len(errs) > 0 {
		return nil, errors.Newf("invalid ManagedBy %q: %s", cfg.ManagedBy, strings.Join(errs, ", "))
	}
	if err := cfg.ServiceKey.validate(); err != nil {
		return nil, errors.Wrap(err, "ServiceKey")
	}
	if cfg.PodKey == (Key{}) {
		cfg.PodKey = cfg.ServiceKey
	}
	if err := cfg.PodKey.validate(); err != nil {
		return nil, errors.Wrap(err, "PodKey")
	}
	if cfg.Membership == nil {
		cfg.Membership = PodReady()
	}
	if cfg.ResyncPeriod <= 0 {
		cfg.ResyncPeriod = DefaultResyncPeriod
	}
	if cfg.MaxEndpointsPerSlice == 0 {
		cfg.MaxEndpointsPerSlice = DefaultMaxEndpointsPerSlice
	}
	if cfg.MaxEndpointsPerSlice < 1 || cfg.MaxEndpointsPerSlice > apiMaxEndpointsPerSlice {
		return nil, errors.Newf("MaxEndpointsPerSlice must be between 1 and %d, got %d", apiMaxEndpointsPerSlice, cfg.MaxEndpointsPerSlice)
	}
	if cfg.MaxConcurrentChecks == 0 {
		cfg.MaxConcurrentChecks = DefaultMaxConcurrentChecks
	}
	if cfg.MaxConcurrentChecks < 1 {
		return nil, errors.Newf("MaxConcurrentChecks must be positive, got %d", cfg.MaxConcurrentChecks)
	}
	if cfg.MaxConcurrentReconciles < 0 {
		return nil, errors.Newf("MaxConcurrentReconciles must not be negative, got %d", cfg.MaxConcurrentReconciles)
	}

	return &Mapper{cfg: cfg}, nil
}

// SetupWithManager registers the Mapper's controller with mgr, watching
// Services carrying [Config.ServiceKey], Pods carrying [Config.PodKey], and
// the EndpointSlices the controller itself publishes -- plus, with
// [Config.PublishLegacyEndpoints], the legacy Endpoints objects it manages
// (without it, a one-shot startup sweep for leftover mirrors is registered
// instead).
func (m *Mapper) SetupWithManager(mgr ctrl.Manager) error {
	name := m.controllerName()
	//nolint:staticcheck // migrating to GetEventRecorder means adopting the
	// events.k8s.io API, which changes the recorder interface and the emitted
	// Event objects; deferred until consumers are ready for that.
	recorder := mgr.GetEventRecorderFor(name)
	r := &reconciler{
		client:   mgr.GetClient(),
		scheme:   mgr.GetScheme(),
		recorder: recorder,
		cfg:      m.cfg,
	}

	// Label keys are selectable in the cache as-is; annotation keys get a
	// field index so alignment lookups stay O(aligned) instead of scanning
	// whole namespaces. Index names include the controller name so Mappers
	// never collide.
	if m.cfg.PodKey.Kind == KeyKindAnnotation {
		r.podIndex = name + "/pod-key"
		if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, r.podIndex, m.cfg.PodKey.indexerFunc()); err != nil {
			return errors.WithStack(err)
		}
	}
	if m.cfg.ServiceKey.Kind == KeyKindAnnotation {
		r.serviceIndex = name + "/service-key"
		if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Service{}, r.serviceIndex, m.cfg.ServiceKey.indexerFunc()); err != nil {
			return errors.WithStack(err)
		}
	}

	bldr := ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(controller.Options{MaxConcurrentReconciles: m.cfg.MaxConcurrentReconciles}).
		For(&corev1.Service{}, builder.WithPredicates(keyPredicate(m.cfg.ServiceKey))).
		Watches(&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(r.mapPodToServices),
			builder.WithPredicates(keyPredicate(m.cfg.PodKey))).
		Watches(&discoveryv1.EndpointSlice{},
			handler.EnqueueRequestsFromMapFunc(mapSliceToService),
			builder.WithPredicates(managedByPredicate(m.cfg.ManagedBy)))

	if m.cfg.PublishLegacyEndpoints {
		// Watch the published Endpoints objects so tampering and deletion
		// get repaired, mirroring the slice watch above. The Endpoints API
		// guarantees the object shares the Service's name, so the identity
		// handler enqueues the right Service.
		bldr = bldr.Watches(&legacyEndpoints{},
			&handler.EnqueueRequestForObject{},
			builder.WithPredicates(managedByPredicate(m.cfg.ManagedBy)))
	} else {
		// A previous configuration may have published legacy Endpoints
		// mirrors; sweep them once at startup -- the option only changes
		// across a restart, so that's exactly when leftovers can appear.
		// The sweep runs on the leader after the cache syncs, and covers
		// Services nothing would ever reconcile again (unmanaged, or
		// deleted mid-teardown).
		c := mgr.GetClient()
		if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
			return sweepLegacyEndpoints(ctx, c, m.cfg.ManagedBy)
		})); err != nil {
			return errors.WithStack(err)
		}
	}

	return bldr.Complete(r)
}

// controllerName derives a unique, sanitized controller name from ManagedBy.
func (m *Mapper) controllerName() string {
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, m.cfg.ManagedBy)

	// Sanitization is lossy ("team.mapper" and "team_mapper" collapse to the
	// same string); a hash of the raw value keeps distinct ManagedBy values
	// from colliding on controller name.
	if sanitized != m.cfg.ManagedBy {
		sum := fnv.New32a()
		_, _ = sum.Write([]byte(m.cfg.ManagedBy))
		sanitized += "-" + strconv.FormatUint(uint64(sum.Sum32()), 36)
	}

	return "port-mapper-" + sanitized
}

// eitherSidePredicate passes events for objects matched by has; updates pass
// if either side matches, so removals still trigger cleanup and repair.
func eitherSidePredicate(has func(client.Object) bool) predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return has(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return has(e.ObjectOld) || has(e.ObjectNew) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return has(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return has(e.Object) },
	}
}

// keyPredicate passes events for objects carrying k.
func keyPredicate(k Key) predicate.Predicate {
	return eitherSidePredicate(func(obj client.Object) bool {
		_, ok := k.valueOf(obj)
		return ok
	})
}

// managedByPredicate passes events for slices this controller publishes.
func managedByPredicate(managedBy string) predicate.Predicate {
	return eitherSidePredicate(func(obj client.Object) bool {
		return obj.GetLabels()[discoveryv1.LabelManagedBy] == managedBy
	})
}
