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
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Label names shared by the per-Service metrics.
const (
	labelNamespace = "namespace"
	labelService   = "service"
)

// Registered into controller-runtime's registry, so they show up on the
// manager's metrics endpoint next to the standard reconcile metrics.
var (
	endpointsPublished = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "portmapper_endpoints_published",
		Help: "Endpoints currently published, per Service port and address family.",
	}, []string{labelNamespace, labelService, "port", "address_type"})

	slicesManaged = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "portmapper_endpointslices_managed",
		Help: "EndpointSlices this controller publishes, per Service.",
	}, []string{labelNamespace, labelService})

	membershipChecks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "portmapper_membership_checks_total",
		Help: "Membership check outcomes, per Service.",
	}, []string{labelNamespace, labelService, "decision"})

	checkDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "portmapper_membership_check_duration_seconds",
		Help:    "How long individual membership checks take.",
		Buckets: []float64{0.001, 0.005, 0.025, 0.1, 0.25, 0.5, 1, 2.5, 5},
	})

	nativeCleanups = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "portmapper_native_cleanup_deletions_total",
		Help: "Native controller leftovers deleted during selector migrations.",
	}, []string{"resource"})

	nameCollisions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "portmapper_name_collisions_total",
		Help: "EndpointSlice name collisions with slices owned by someone else.",
	}, []string{labelNamespace, labelService})
)

func init() {
	metrics.Registry.MustRegister(
		endpointsPublished,
		slicesManaged,
		membershipChecks,
		checkDuration,
		nativeCleanups,
		nameCollisions,
	)
}

func decisionLabel(d Decision) string {
	switch d {
	case Include:
		return "include"
	case Exclude:
		return "exclude"
	case Abstain:
		return "abstain"
	}
	return "unknown"
}

// recordPublished refreshes the per-Service gauges from the slices that were
// just synced.
func recordPublished(svc *corev1.Service, desired map[string]*discoveryv1.EndpointSlice) {
	endpointsPublished.DeletePartialMatch(prometheus.Labels{labelNamespace: svc.Namespace, labelService: svc.Name})

	type portKey struct {
		port   string
		family string
	}
	counts := map[portKey]int{}
	for _, slice := range desired {
		if len(slice.Ports) == 0 {
			continue
		}
		port := ptr.Deref(slice.Ports[0].Name, "")
		if port == "" {
			port = strconv.Itoa(int(ptr.Deref(slice.Ports[0].Port, 0)))
		}
		counts[portKey{port: port, family: string(slice.AddressType)}] += len(slice.Endpoints)
	}
	for key, count := range counts {
		endpointsPublished.WithLabelValues(svc.Namespace, svc.Name, key.port, key.family).Set(float64(count))
	}

	slicesManaged.WithLabelValues(svc.Namespace, svc.Name).Set(float64(len(desired)))
}

// forgetServiceMetrics drops a Service's metric series once it is deleted or
// no longer managed.
func forgetServiceMetrics(key client.ObjectKey) {
	labels := prometheus.Labels{labelNamespace: key.Namespace, labelService: key.Name}
	endpointsPublished.DeletePartialMatch(labels)
	slicesManaged.DeletePartialMatch(labels)
	membershipChecks.DeletePartialMatch(labels)
	nameCollisions.DeletePartialMatch(labels)
}
