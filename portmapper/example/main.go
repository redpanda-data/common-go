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

// Command example runs a port-mapper controller against the current
// kubeconfig context, aligning Services and Pods via annotations. Pair it
// with manifests.yaml for a demo.
package main

import (
	"flag"
	"os"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/redpanda-data/common-go/portmapper"
)

func main() {
	var (
		kind        string
		groupKey    string
		portsKey    string
		managedBy   string
		metricsAddr string
		resync      time.Duration
	)
	flag.StringVar(&kind, "key-kind", string(portmapper.KeyKindAnnotation), "how services and pods are aligned: label or annotation")
	flag.StringVar(&groupKey, "group-key", "port-mapper.example.com/group", "label/annotation aligning pods with the services they back")
	flag.StringVar(&portsKey, "ports-key", "port-mapper.example.com/ports", "pod annotation listing the port names the pod backs")
	flag.StringVar(&managedBy, "managed-by", "port-mapper-example", "endpointslice.kubernetes.io/managed-by value for published slices")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "address to serve metrics on (\"0\" disables)")
	flag.DurationVar(&resync, "resync", 10*time.Second, "membership re-evaluation interval")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	logger := ctrl.Log.WithName("example")

	// The manager's default scheme already covers the core and discovery
	// types this controller needs.
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Metrics: metricsserver.Options{BindAddress: metricsAddr},
	})
	if err != nil {
		logger.Error(err, "creating manager")
		os.Exit(1)
	}

	mapper, err := portmapper.New(portmapper.Config{
		ManagedBy:  managedBy,
		ServiceKey: portmapper.Key{Kind: portmapper.KeyKind(kind), Name: groupKey},
		// A pod backs a port when it is Ready AND lists the port's name in
		// its ports annotation. Ports with different semantics can route to
		// different checks instead (run in-cluster for network checkers):
		//
		//	portmapper.PerPort(map[string]portmapper.Checker{
		//	    "http":  portmapper.HTTPGet("/healthz", time.Second),
		//	    "https": portmapper.TCPDial(time.Second),
		//	}, nil)
		Membership: portmapper.All(
			portmapper.PodReady(),
			portmapper.PortNames(portmapper.AnnotationKey(portsKey)),
		),
		ResyncPeriod: resync,
	})
	if err != nil {
		logger.Error(err, "configuring port-mapper")
		os.Exit(1)
	}

	if err := mapper.SetupWithManager(mgr); err != nil {
		logger.Error(err, "registering port-mapper")
		os.Exit(1)
	}

	logger.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "running manager")
		os.Exit(1)
	}
}
