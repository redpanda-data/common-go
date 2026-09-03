# port-mapper

A library-style Kubernetes controller that publishes the EndpointSlices
backing "colocated" Services — Services whose ports are served by
overlapping-but-distinct subsets of a shared pod pool.

It replaces the native EndpointSlice controller for Services that
deliberately define no selector, so the two never conflict. Where the native
controller aligns Pods to Services with `spec.selector`, port-mapper aligns
them through a configurable label **or** annotation, and a pluggable
membership check decides — per pod, per port — which pods back which ports:

```
Service: my-service
  ├── port 8080 (http)  → pods A, B, C
  └── port 8443 (https) → pods B, C, D
```

renders as one EndpointSlice per Service port:

```yaml
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata:
  name: my-service-http
  labels:
    kubernetes.io/service-name: my-service
    endpointslice.kubernetes.io/managed-by: my-controller
addressType: IPv4
ports:
  - name: http
    protocol: TCP
    port: 8080
endpoints:
  - addresses: ["10.0.0.11"]   # pod A
  - addresses: ["10.0.0.12"]   # pod B
  - addresses: ["10.0.0.13"]   # pod C
```

## How it works

The controller never creates or mutates Services or Pods; it only observes
them and writes EndpointSlices.

1. A **Service opts in** by carrying the configured `ServiceKey` (a label or
   annotation) whose value names a *pod group*. Managed Services define
   `spec.ports` — the source of truth for what gets published — and **no
   selector**.
2. **Pods join a group** by carrying the configured `PodKey` (defaults to
   `ServiceKey`) with a matching value, in the same namespace.
3. For every Service port, each aligned pod that could receive traffic is
   passed to the **membership `Checker`**; the pods that pass are published
   as endpoints for that port.

Published slices carry `kubernetes.io/service-name` (so kube-proxy consumes
them), `endpointslice.kubernetes.io/managed-by` (so controllers coexist), and
an owner reference to the Service for garbage collection. Writes use
server-side apply.

## Usage

```go
mapper, err := portmapper.New(portmapper.Config{
    ManagedBy:  "my-controller",
    ServiceKey: portmapper.AnnotationKey("example.com/group"), // or portmapper.LabelKey(...)
    Membership: portmapper.All(
        portmapper.PodReady(),
        portmapper.PortNames(portmapper.AnnotationKey("example.com/ports")),
    ),
    ResyncPeriod: 10 * time.Second,
})
if err != nil {
    return err
}
if err := mapper.SetupWithManager(mgr); err != nil { // any controller-runtime manager
    return err
}
```

## Membership checks

Checkers receive the Service, the pod, and the (resolved) port. Built-ins:

| Checker | Includes a pod when... |
| --- | --- |
| `PodReady()` (default) | the pod's `Ready` condition is true (mirrors the native controller) |
| `PortNames(key)` | the pod's label/annotation lists the port's name (`"http,https"`) |
| `TCPDial(timeout)` | a TCP dial to `<address>:<targetPort>` succeeds |
| `HTTPGet(path, timeout)` | `GET http://<address>:<targetPort><path>` answers 2xx |
| `NotTerminating()` | the pod isn't being deleted (opts out of graceful drain publishing) |
| `PerPort(map, fallback)` | the checker registered for the port's name agrees |
| `All(...)` | every combined checker agrees |
| `Stable(checker, s, f)` | the wrapped checker's answer, damped: membership only flips after `s` consecutive passes / `f` consecutive failures (the first check of a pod is taken as-is, so restarts don't empty slices) |

Network checkers need pod-network reachability (run in-cluster); their
results are refreshed every `ResyncPeriod` even without API events. Checkers
receive the address under evaluation as `Port.Address` — on dual-stack
Services each family is probed separately against its own address — and
custom probes should target it too. Because checkers see the port, different
ports can use entirely different logic:

```go
Membership: portmapper.PerPort(map[string]portmapper.Checker{
    "http":  portmapper.All(portmapper.PodReady(), portmapper.HTTPGet("/healthz", time.Second)),
    "https": portmapper.TCPDial(time.Second),
}, nil) // nil fallback: unconfigured ports publish no endpoints
```

And custom logic is a one-liner:

```go
portmapper.CheckerFunc(func(ctx context.Context, svc *corev1.Service, pod *corev1.Pod, port portmapper.Port) bool {
    return myRegistry.IsServing(port.Address, port.Port)
})
```

### Probe errors

`Checker` is boolean by design: for a probe, a dial failure, timeout, or
non-2xx *is* the signal — swallow it and return `false`, exactly like a
kubelet readiness probe. Built-ins log every exclusion through the
reconcile's logger, tagged with the deciding checker, pod, and port: probe
failures (with the swallowed error) at **V(1)**, expected metadata mismatches
at **V(2)** — so verbosity 2 answers "why isn't this pod in the slice?".
Keep probe timeouts tight; checks run inside the reconcile loop.

The exception is a failure that says nothing about the pod — the *controller*
losing pod-network access would otherwise read as "every pod is unhealthy"
and empty every slice. For that, abstain.

### Abstaining

A `Checker` that also implements `Decider` (most easily via `DeciderFunc`)
returns `Include`, `Exclude`, or `Abstain`. On `Abstain` the controller keeps
whatever it last published for that (pod, port, address family) — resolved
against the live slices and logged at V(1). A sustained controller-side
outage freezes memberships instead of draining them:

```go
Membership: portmapper.DeciderFunc(func(ctx context.Context, svc *corev1.Service, pod *corev1.Pod, port portmapper.Port) portmapper.Decision {
    healthy, err := probe(ctx, pod, port)
    switch {
    case isControllerSide(err): // e.g. no route to the pod network
        return portmapper.Abstain // keep whatever is currently published
    case err != nil || !healthy: // refused/timeout/non-200: the pod's problem
        return portmapper.Exclude
    default:
        return portmapper.Include
    }
}),
```

Abstentions survive composition: `All` lets a definitive `Exclude` win but
otherwise propagates `Abstain`; `PerPort` passes through the routed decision;
`Stable` holds its remembered state through abstentions (streaks freeze),
propagating only before any state exists. Custom combinators should evaluate
nested checkers with `portmapper.DecisionFor`, not `Check`.

## Native-controller parity

- **Dual-stack**: one slice set per family in `spec.ipFamilies`
  (`my-service-http`, `my-service-http--ipv6`), using each pod's per-family
  IP from `status.podIPs`; membership is evaluated per family against the
  address actually being published.
- **Chunking**: endpoint sets larger than `MaxEndpointsPerSlice` (default
  100) spill into `--2`, `--3`, ... overflow slices.
- **Named `targetPort`s**: resolved per pod against container ports (matching
  name and protocol); pods resolving to different numbers — e.g. mid
  rolling-update — group into separate slices, each named for its resolved
  number (`my-service-https--p8443`) so names stay stable as groups come and
  go.
- **Graceful termination**: terminating members are published with
  `ready: false, serving: true, terminating: true` for drain (compose with
  `NotTerminating()` to drop them immediately);
  `spec.publishNotReadyAddresses` is honored.
- **Topology**: endpoints carry `nodeName`, the node's zone, and `hostname`
  (when the pod's `subdomain` targets the Service).
  `trafficDistribution: PreferClose`/`PreferSameZone` publishes same-zone
  hints; `PreferSameNode` publishes same-node hints with the zone as
  fallback. API servers that strip `forNodes` (feature gate off) are
  tolerated rather than fought.
- **Placeholders**: a port with no members still publishes an empty slice
  (for a named `targetPort`, at the lowest target any candidate pod resolves;
  a name no pod resolves is unknowable and publishes nothing).
- **ExternalName Services are ignored** (with a warning Event), like the
  native controller — they must not have endpoints.
- **Server-side apply** with `ManagedBy` as field owner: manual edits are
  repaired — including stripped ownership labels, which are re-claimed via
  the slice's owner reference — while foreign fields are left alone.

## Migrating a selector-based Service

An existing Service cuts over live; both controllers coexist during the
transition (kube-proxy routes the union):

1. Annotate the pods, then mark the Service with the `ServiceKey` while its
   selector is still in place. port-mapper's slices appear alongside the
   native ones, per-port membership already applied.
2. Remove the selector. **port-mapper finishes the migration itself** — the
   native controllers abandon their artifacts rather than cleaning up
   (verified empirically in the integration test), so once a managed Service
   is selectorless the controller, after its own slices are applied, deletes:

   - stale slices labeled `managed-by: endpointslice-controller.k8s.io`;
   - the legacy `Endpoints` object — otherwise the EndpointSliceMirroring
     controller mirrors it back into stale slices forever. Its mirrors are
     garbage collected with it.

   The takeover starts only once port-mapper is publishing at least one
   endpoint (so a misconfigured checker can't black the Service out), emits a
   `NativeEndpointsCleanedUp` Event, and never touches slices belonging to
   other controllers. It acts only when the stale leftovers are actually
   present, so an already-migrated Service costs no API writes, and it
   deletes the `Endpoints` object before the stale slices — if anything fails
   partway, even a controller restart, the remaining leftovers simply trigger
   the cleanup again on the next pass.

If something else legitimately manages those legacy objects, set
`Config.DisableNativeCleanup` and clean up manually:

```sh
kubectl delete endpoints my-service
kubectl delete endpointslice \
    -l kubernetes.io/service-name=my-service,endpointslice.kubernetes.io/managed-by=endpointslice-controller.k8s.io
```

## Legacy Endpoints objects

Consumers that still read the deprecated `core/v1` Endpoints API can opt in
via `Config.PublishLegacyEndpoints`: the controller additionally publishes an
`Endpoints` object (named after the Service, as that API requires) mirroring
its slices — addresses packed into subsets by the exact set of ports they
serve, terminating pods omitted, only the primary address family, and the
Service's labels stamped on (all matching the native endpoints controller; a
label removed from the Service lingers on the mirror until the next content
change), truncated at 1000 addresses (ready addresses first) with the
`endpoints.kubernetes.io/over-capacity: truncated` annotation. The object
also carries the `ManagedBy` label plus `endpointslice.kubernetes.io/
skip-mirror: "true"` so the EndpointSliceMirroring controller ignores it, is
repaired on tampering and deletion like the slices, and is removed (or
garbage collected) with them.

Services that still define a selector are skipped — the native endpoints
controller owns their `Endpoints` object, and one published before a selector
(re)appeared is handed back (deleted, for the native controller to rebuild).
During a selector migration the abandoned object is instead adopted in place
(surfaced as a `LegacyEndpointsAdopted` Event) once this controller publishes
ready addresses for the primary family, so consumers never observe it
missing; because adoption keeps the object alive, the migration cleanup then
deletes the EndpointSliceMirroring controller's stale mirror slices directly
rather than waiting on owner-reference GC. Until adoption lands, the
abandoned object's stale addresses stay live — a
`LegacyEndpointsAdoptionDeferred` Event names the state, and if it persists
(say, a dual-stack Service whose primary `ipFamilies` entry never gets pod
addresses) the fix is correcting the family/pod configuration. An adopted
object also keeps the labels the native controller copied from the Service.
An `Endpoints` object that belongs to someone else — another manager's
label, or a foreign controller owner — is never overwritten or deleted: the
collision is reported as an `EndpointsCollision` Event, and with
`Config.DisableNativeCleanup` set even an unowned object is left alone.

Requires additional RBAC: `create` and `patch` on `endpoints` (see below),
and caches Endpoints cluster-wide — note that scoping that cache by label
via `cache.Options` would break the adoption/ownership checks, which must
see foreign objects.

Disabling the option later cleans up after itself: published objects are
removed alongside the slices when a Service stops being served, and a
one-time startup sweep deletes every leftover mirror (selected by the
`ManagedBy` label — covering even Services nothing reconciles anymore), so
no manual deletion is needed in either direction.

## Demo

```sh
kind create cluster
kubectl apply -f example/manifests.yaml   # selectorless Service + pods a-d
go run ./example                          # runs against your kubeconfig

kubectl -n port-mapper-demo get endpointslices
# NAME               ADDRESSTYPE   PORTS   ENDPOINTS
# my-service-http    IPv4          8080    10.244.0.5,10.244.0.6,10.244.0.7
# my-service-https   IPv4          8443    10.244.0.6,10.244.0.7,10.244.0.8
```

Flip a pod's membership and watch the slices follow:

```sh
kubectl -n port-mapper-demo annotate pod pod-a --overwrite \
    port-mapper.example.com/ports=http,https
```

## Testing

Unit tests use controller-runtime's fake client: `go test -short ./...`.
`TestIntegration` (requires Docker) runs the controller against a real
single-node k3s cluster via testcontainers — real pod IPs and readiness,
server-side apply and repair, owner-reference garbage collection, zones and
hints, and the full selector migration:

```sh
go test -run TestIntegration -v
```

## RBAC

- `get;list;watch` on `services`, `pods`, and `nodes` (nodes only for zone
  lookups; `Config.DisableNodeLookups` drops the requirement and the Node
  informer)
- `get;list;watch;create;update;patch;delete` on `endpointslices`
  (`discovery.k8s.io`)
- `get;list;watch;delete;` on `endpoints` (migration cleanup, teardown, and
  the startup sweep of mirrors a previous `PublishLegacyEndpoints`
  configuration published — the first Endpoints read lazily starts an
  informer, and without list/watch it never syncs and reconciles hang)
- `create;patch` on `endpoints` additionally with
  `Config.PublishLegacyEndpoints`
- `create;patch` on `events`

The manager's cache watches these cluster-wide by default; scope it with
`cache.Options` when embedding.

## Metrics

Registered into controller-runtime's registry, so they appear on the
manager's metrics endpoint alongside the standard reconcile metrics:

| Metric | Meaning |
| --- | --- |
| `portmapper_endpoints_published` (gauge) | endpoints currently published, per Service port and address family — alert when a port you expect traffic on hits zero |
| `portmapper_endpointslices_managed` (gauge) | slices this controller publishes, per Service |
| `portmapper_membership_checks_total` (counter) | check outcomes per Service, labeled `decision=include\|exclude\|abstain` — a spike in `abstain` means checks can't reach pods |
| `portmapper_membership_check_duration_seconds` (histogram) | how long individual checks take |
| `portmapper_native_cleanup_deletions_total` (counter) | native leftovers removed during selector migrations, labeled `resource=endpoints\|endpointslice` — with `PublishLegacyEndpoints` the Endpoints object is adopted rather than deleted, so the `endpoints` series stays flat and the `LegacyEndpointsAdopted` Event is the migration signal instead |
| `portmapper_name_collisions_total` (counter) | slice name collisions with slices owned by someone else |

The gauges reflect the last *successfully synced* state — a Service stuck in
a reconcile error keeps its previous values (watch the standard
`controller_runtime_reconcile_errors_total` for that). A Service's series are
removed when it is deleted or stops being managed.

## Compatibility

Requires the `discovery.k8s.io/v1` EndpointSlice API (Kubernetes 1.21+).
Everything newer degrades gracefully instead of erroring on older clusters:

- `spec.trafficDistribution` doesn't exist before 1.30 (and only accepts
  `PreferClose` before 1.33), so on older clusters the field is simply never
  set and no topology hints are published.
- Same-node (`forNodes`) hints only render when a server accepted
  `PreferSameNode` in the first place; servers that strip the field (feature
  gate off) are tolerated rather than fought.
- Named `targetPort`s on sidecar containers resolve only where sidecars
  exist (1.28+); on older clusters there's nothing to match.

The `k8s.io/*` versions compiled in come from your `go.mod`; the usual
client-go version-skew guidance applies.

## Scaling notes

- **Membership checks fan out concurrently** within each reconcile, bounded
  by `Config.MaxConcurrentChecks` (default 16), so a Service full of
  unreachable pods costs roughly `checks ÷ bound × timeout` instead of one
  full timeout per check. Checkers must therefore be safe for concurrent
  use — every built-in is.
- **Raise reconcile concurrency when using network checkers.**
  controller-runtime defaults each controller to a single worker, so one
  slow Service delays the rest. The reconciler is safe to run concurrently
  (the workqueue serializes per-Service); set
  `Config.MaxConcurrentReconciles` (0 defers to the manager's default).
  Budgets multiply: N workers × M checks in flight.
- **Annotation keys are field-indexed automatically.** Label keys are served
  by cache selectors natively; for annotation keys `SetupWithManager`
  registers cache field indexes, so alignment lookups are O(aligned objects)
  either way. Event-side cost is gated by predicates regardless — only pods
  and Services carrying the keys ever trigger work.

## Caveats

- **Membership is inclusion, not readiness.** Pods failing their check are
  omitted entirely, where the native controller would publish them with
  `ready: false` (terminating members excepted, as above).
- **Deterministic names instead of `generateName`.** Within one Service,
  names can't collide: generated suffixes start with `--`, which port names
  aren't allowed to contain. Two *different* Services can still produce the
  same name (`my` + port `service-http` vs `my-service` + port `http`); when
  that happens the controller reports it — a reconcile error plus an
  `EndpointSliceNameCollision` Event — instead of overwriting the other
  slice. Chunk boundaries also shift as endpoints come and go, so one pod
  change can rewrite several slices of a large Service (the native controller
  packs slices incrementally to avoid this).
- **No `topology-mode: Auto`.** The CPU-proportional hint allocation is not
  implemented. A managed Service requesting it is published with **no**
  topology hints — matching native precedence, where the annotation
  overrides `trafficDistribution` — plus a `TopologyAwareHintsDisabled`
  warning Event. Use `spec.trafficDistribution` instead.
