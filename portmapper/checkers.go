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
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Checker decides whether a pod is published as an endpoint for a Service
// port. Checks run once per (pod, port, address) on every reconcile --
// concurrently, bounded by [Config.MaxConcurrentChecks], so implementations
// must be safe for concurrent use (all built-ins are). Keep them fast and
// give probes tight timeouts. Returning false excludes the pod from that
// port's slice without affecting other ports.
//
// For probe-style checks an error (refused, timeout, non-2xx) is the signal
// itself: swallow it and return false, like a kubelet readiness probe; the
// pod is re-evaluated within [Config.ResyncPeriod]. Log through
// log.FromContext(ctx) when it needs visibility -- built-ins log probe
// failures at V(1) and metadata mismatches at V(2). For failures that say
// nothing about the pod (e.g. the controller lost pod-network access),
// return [Abstain] via a [Decider] instead, so the previously published
// membership is kept.
type Checker interface {
	Check(ctx context.Context, svc *corev1.Service, pod *corev1.Pod, port Port) bool
}

// CheckerFunc adapts a plain function into a [Checker].
type CheckerFunc func(ctx context.Context, svc *corev1.Service, pod *corev1.Pod, port Port) bool

// Check implements [Checker].
func (f CheckerFunc) Check(ctx context.Context, svc *corev1.Service, pod *corev1.Pod, port Port) bool {
	return f(ctx, svc, pod, port)
}

// Decision is the tri-state outcome of a membership evaluation.
type Decision int

const (
	// Exclude drops the pod from the port's slice.
	Exclude Decision = iota
	// Include publishes the pod as an endpoint for the port.
	Include
	// Abstain makes no claim: the controller keeps whatever membership it
	// last published for the (pod, port, address family). Return it when a
	// check cannot determine health at all -- e.g. a probe failing with a
	// controller-side error -- so infrastructure failures near the
	// controller don't read as "every pod is unhealthy" and empty slices.
	Abstain
)

// String implements fmt.Stringer.
func (d Decision) String() string {
	switch d {
	case Exclude:
		return "Exclude"
	case Include:
		return "Include"
	case Abstain:
		return "Abstain"
	}
	return fmt.Sprintf("Decision(%d)", int(d))
}

// Decider is an optional, richer counterpart to [Checker]. Any Checker that
// also implements Decider -- configured directly or nested inside [All],
// [PerPort], or [Stable] -- has its tri-state Decide consulted instead of
// Check, enabling [Abstain].
type Decider interface {
	Decide(ctx context.Context, svc *corev1.Service, pod *corev1.Pod, port Port) Decision
}

// DeciderFunc adapts a plain function into a [Checker] that also implements
// [Decider].
type DeciderFunc func(ctx context.Context, svc *corev1.Service, pod *corev1.Pod, port Port) Decision

// Decide implements [Decider].
func (f DeciderFunc) Decide(ctx context.Context, svc *corev1.Service, pod *corev1.Pod, port Port) Decision {
	return f(ctx, svc, pod, port)
}

// Check implements [Checker]; anything but [Include] reads as false.
func (f DeciderFunc) Check(ctx context.Context, svc *corev1.Service, pod *corev1.Pod, port Port) bool {
	return f(ctx, svc, pod, port) == Include
}

// DecisionFor evaluates checker, consulting its [Decider] implementation when
// present and mapping a plain boolean onto [Include]/[Exclude] otherwise.
// Custom combinators should call this rather than Check so [Abstain] survives
// composition.
func DecisionFor(ctx context.Context, checker Checker, svc *corev1.Service, pod *corev1.Pod, port Port) Decision {
	if decider, ok := checker.(Decider); ok {
		return decider.Decide(ctx, svc, pod, port)
	}
	return decisionOf(checker.Check(ctx, svc, pod, port))
}

func decisionOf(included bool) Decision {
	if included {
		return Include
	}
	return Exclude
}

// checkLog annotates the reconcile's logger (a no-op logger outside a
// reconcile) with the deciding checker, pod, and port.
func checkLog(ctx context.Context, checker string, pod *corev1.Pod, port Port) logr.Logger {
	return log.FromContext(ctx).WithValues(
		"checker", checker,
		"pod", client.ObjectKeyFromObject(pod),
		"port", port.Name,
		"targetPort", port.Port,
	)
}

// PodReady returns a [Checker] that includes pods whose Ready condition is
// true, mirroring the native controller.
func PodReady() Checker {
	return CheckerFunc(func(ctx context.Context, _ *corev1.Service, pod *corev1.Pod, port Port) bool {
		for _, cond := range pod.Status.Conditions {
			if cond.Type != corev1.PodReady {
				continue
			}
			if cond.Status != corev1.ConditionTrue {
				checkLog(ctx, "PodReady", pod, port).V(2).Info("excluding pod: not ready", "status", cond.Status)
				return false
			}
			return true
		}
		checkLog(ctx, "PodReady", pod, port).V(2).Info("excluding pod: no Ready condition reported")
		return false
	})
}

// NotTerminating returns a [Checker] that excludes pods being deleted. By
// default terminating members stay published for graceful drain; compose with
// NotTerminating to drop them immediately.
func NotTerminating() Checker {
	return CheckerFunc(func(ctx context.Context, _ *corev1.Service, pod *corev1.Pod, port Port) bool {
		if !pod.DeletionTimestamp.IsZero() {
			checkLog(ctx, "NotTerminating", pod, port).V(2).Info("excluding pod: terminating")
			return false
		}
		return true
	})
}

// PortNames returns a [Checker] that reads a label or annotation on the pod
// listing, comma-separated, the Service port names it backs (e.g.
// "http,https"). Pods without the key back no ports; unnamed Service ports
// can't be listed and are never matched.
func PortNames(key Key) Checker {
	return CheckerFunc(func(ctx context.Context, _ *corev1.Service, pod *corev1.Pod, port Port) bool {
		value, ok := key.valueOf(pod)
		if !ok {
			checkLog(ctx, "PortNames", pod, port).V(2).Info("excluding pod: ports key absent", "key", key.Name)
			return false
		}
		for name := range strings.SplitSeq(value, ",") {
			// Empty segments (e.g. a trailing comma) must not match unnamed
			// ports.
			if name = strings.TrimSpace(name); name != "" && name == port.Name {
				return true
			}
		}
		checkLog(ctx, "PortNames", pod, port).V(2).Info("excluding pod: port not listed", "key", key.Name, "value", value)
		return false
	})
}

// probeHost is the address a network probe should target: the address under
// evaluation ([Port.Address], per family on dual-stack Services), falling
// back to the pod's primary IP for direct invocations.
func probeHost(pod *corev1.Pod, port Port) string {
	if port.Address != "" {
		return port.Address
	}
	return pod.Status.PodIP
}

// TCPDial returns a [Checker] that includes a pod when a TCP dial to the
// address under evaluation succeeds within timeout. Requires pod-network
// reachability (i.e. run in-cluster); non-TCP ports are never included.
func TCPDial(timeout time.Duration) Checker {
	return CheckerFunc(func(ctx context.Context, _ *corev1.Service, pod *corev1.Pod, port Port) bool {
		if port.Protocol != corev1.ProtocolTCP {
			checkLog(ctx, "TCPDial", pod, port).V(2).Info("excluding pod: non-TCP port", "protocol", port.Protocol)
			return false
		}

		address := net.JoinHostPort(probeHost(pod, port), strconv.Itoa(int(port.Port)))
		dialer := net.Dialer{Timeout: timeout}
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			checkLog(ctx, "TCPDial", pod, port).V(1).Info("excluding pod: dial failed", "address", address, "error", err)
			return false
		}
		_ = conn.Close()

		return true
	})
}

// HTTPGet returns a [Checker] that includes a pod when GET
// "http://<address>:<targetPort><path>" against the address under evaluation
// answers 2xx within timeout. Requires pod-network reachability (i.e. run
// in-cluster); non-TCP ports are never included.
func HTTPGet(path string, timeout time.Duration) Checker {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	httpClient := &http.Client{
		Timeout: timeout,
		// Don't chase redirects; anything but a 2xx is unhealthy.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return CheckerFunc(func(ctx context.Context, _ *corev1.Service, pod *corev1.Pod, port Port) bool {
		if port.Protocol != corev1.ProtocolTCP {
			checkLog(ctx, "HTTPGet", pod, port).V(2).Info("excluding pod: non-TCP port", "protocol", port.Protocol)
			return false
		}

		url := fmt.Sprintf("http://%s%s", net.JoinHostPort(probeHost(pod, port), strconv.Itoa(int(port.Port))), path)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			checkLog(ctx, "HTTPGet", pod, port).V(1).Info("excluding pod: building probe request failed", "url", url, "error", err)
			return false
		}

		response, err := httpClient.Do(request)
		if err != nil {
			checkLog(ctx, "HTTPGet", pod, port).V(1).Info("excluding pod: probe failed", "url", url, "error", err)
			return false
		}
		defer response.Body.Close()

		if response.StatusCode < 200 || response.StatusCode >= 300 {
			checkLog(ctx, "HTTPGet", pod, port).V(1).Info("excluding pod: non-2xx probe response", "url", url, "status", response.StatusCode)
			return false
		}

		return true
	})
}

// All combines checkers: a pod is included only when every checker agrees.
// Evaluation short-circuits on the first [Exclude]; with none excluding, an
// [Abstain] from any checker abstains overall.
func All(checkers ...Checker) Checker {
	return allChecker(checkers)
}

type allChecker []Checker

// Decide implements [Decider].
func (a allChecker) Decide(ctx context.Context, svc *corev1.Service, pod *corev1.Pod, port Port) Decision {
	decision := Include
	for _, checker := range a {
		switch DecisionFor(ctx, checker, svc, pod, port) {
		case Exclude:
			return Exclude
		case Abstain:
			decision = Abstain
		}
	}
	return decision
}

// Check implements [Checker].
func (a allChecker) Check(ctx context.Context, svc *corev1.Service, pod *corev1.Pod, port Port) bool {
	return a.Decide(ctx, svc, pod, port) == Include
}

// PerPort routes each membership decision to the checker registered for the
// Service port's name, letting ports with different semantics use different
// checks:
//
//	portmapper.PerPort(map[string]portmapper.Checker{
//	    "http":  portmapper.HTTPGet("/healthz", time.Second), // REST API
//	    "https": portmapper.TCPDial(time.Second),             // raw TCP
//	}, nil)
//
// Ports without an entry fall through to fallback; a nil fallback excludes
// them so an unconfigured port never silently routes traffic. [Abstain]
// propagates from routed checkers.
func PerPort(checkers map[string]Checker, fallback Checker) Checker {
	return &perPortChecker{checkers: checkers, fallback: fallback}
}

type perPortChecker struct {
	checkers map[string]Checker
	fallback Checker
}

// Decide implements [Decider].
func (p *perPortChecker) Decide(ctx context.Context, svc *corev1.Service, pod *corev1.Pod, port Port) Decision {
	checker, ok := p.checkers[port.Name]
	if !ok {
		checker = p.fallback
	}
	if checker == nil {
		checkLog(ctx, "PerPort", pod, port).V(1).Info("excluding pod: no checker configured for port and no fallback set")
		return Exclude
	}
	return DecisionFor(ctx, checker, svc, pod, port)
}

// Check implements [Checker].
func (p *perPortChecker) Check(ctx context.Context, svc *corev1.Service, pod *corev1.Pod, port Port) bool {
	return p.Decide(ctx, svc, pod, port) == Include
}

const (
	// stableStateIdleExpiry is how long a [Stable] state survives without
	// being checked before it is dropped. It must comfortably exceed any
	// [Config.ResyncPeriod], or the damping silently resets between checks.
	stableStateIdleExpiry = 24 * time.Hour
	// stableSweepInterval bounds how often a [Stable] checker scans for
	// expired state.
	stableSweepInterval = time.Minute
)

// Stable damps a flapping checker the way kubelet probes do: once a pod's
// membership is established, it only flips to included after
// successThreshold consecutive passes, and only flips to excluded after
// failureThreshold consecutive failures (minimum 1 each; shorter runs of
// disagreement are ignored and logged at V(2)). Checks arrive once per
// reconcile per published address, so failureThreshold also caps how many
// endpoints a brief controller-side probe outage can drain.
//
// The very first check of a (service, pod, port, address) is taken as-is --
// starting everything as excluded would empty every slice each time the
// controller restarts. A wrapped checker that abstains (see [Abstain])
// leaves the remembered state untouched: streaks neither grow nor reset;
// with no remembered state yet, the abstention passes through.
//
// The returned checker is safe for concurrent use, drops state that hasn't
// been checked in a day, and can be shared across Mappers.
func Stable(checker Checker, successThreshold, failureThreshold int) Checker {
	return &stableChecker{
		inner:            checker,
		successThreshold: max(successThreshold, 1),
		failureThreshold: max(failureThreshold, 1),
		states:           map[stableKey]*stableState{},
		now:              time.Now,
	}
}

// stableKey includes the evaluated address so that dual-stack Services --
// which evaluate each family separately -- damp each family independently.
type stableKey struct {
	service types.UID
	pod     types.UID
	port    string
	target  int32
	address string
}

type stableState struct {
	included bool
	streak   int
	lastSeen time.Time
}

type stableChecker struct {
	inner            Checker
	successThreshold int
	failureThreshold int

	mu        sync.Mutex
	states    map[stableKey]*stableState
	lastSweep time.Time
	now       func() time.Time
}

// Decide implements [Decider].
func (s *stableChecker) Decide(ctx context.Context, svc *corev1.Service, pod *corev1.Pod, port Port) Decision {
	observed := DecisionFor(ctx, s.inner, svc, pod, port)

	key := stableKey{pod: pod.UID, port: port.Name, target: port.Port, address: port.Address}
	if svc != nil {
		key.service = svc.UID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.sweep(now)

	state, ok := s.states[key]
	if observed == Abstain {
		if !ok {
			return Abstain
		}
		// No observation: hold the remembered membership, freeze any streak.
		state.lastSeen = now
		return decisionOf(state.included)
	}

	included := observed == Include
	if !ok {
		state = &stableState{included: included}
		s.states[key] = state
	}
	state.lastSeen = now

	if included == state.included {
		state.streak = 0
		return decisionOf(state.included)
	}

	state.streak++
	threshold := s.failureThreshold
	if included {
		threshold = s.successThreshold
	}
	if state.streak >= threshold {
		state.included = included
		state.streak = 0
	} else {
		checkLog(ctx, "Stable", pod, port).V(2).Info("damping membership change",
			"observed", included, "streak", state.streak, "threshold", threshold)
	}

	return decisionOf(state.included)
}

// Check implements [Checker].
func (s *stableChecker) Check(ctx context.Context, svc *corev1.Service, pod *corev1.Pod, port Port) bool {
	return s.Decide(ctx, svc, pod, port) == Include
}

func (s *stableChecker) sweep(now time.Time) {
	if now.Sub(s.lastSweep) < stableSweepInterval {
		return
	}
	s.lastSweep = now
	for key, state := range s.states {
		if now.Sub(state.lastSeen) > stableStateIdleExpiry {
			delete(s.states, key)
		}
	}
}
