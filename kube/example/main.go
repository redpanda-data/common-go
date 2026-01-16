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

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/redpanda-data/common-go/kube"
	clusterv1 "github.com/redpanda-data/common-go/kube/example/api/cluster/v1"
	"github.com/redpanda-data/common-go/kube/example/api/resources"
	resourcesv1 "github.com/redpanda-data/common-go/kube/example/api/resources/v1"
	"github.com/redpanda-data/common-go/kube/example/crds"
)

var logger logr.Logger

func init() {
	kube.SetContextLoggerFactory(func(ctx context.Context) logr.Logger {
		return ctrl.LoggerFrom(ctx)
	})
	config := zap.NewProductionConfig()
	config.Level = zap.NewAtomicLevelAt(zapcore.Level(-4))
	zaplogger, err := config.Build(zap.AddStacktrace(zapcore.ErrorLevel))
	if err != nil {
		log.Fatal(err)
	}
	logger = zapr.NewLogger(zaplogger)
	ctrl.SetLogger(logger)
}

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions/status,verbs=update;patch
// +kubebuilder:rbac:groups=apiregistration.k8s.io,resources=apiservices,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=apiregistration.k8s.io,resources=apiservices/status,verbs=update;patch
// +kubebuilder:rbac:groups=cluster.kube.redpanda.com,resources=clusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cluster.kube.redpanda.com,resources=clusters/status,verbs=update;patch
// +kubebuilder:rbac:groups=cluster.kube.redpanda.com,resources=clusters/finalizers,verbs=update

func main() {
	var service, namespace string
	flag.StringVar(&service, "service", "", "Service where this is deployed")
	flag.StringVar(&namespace, "namespace", "", "Namespace where this is deployed")
	flag.Parse()

	var flagErr error
	if service == "" {
		flagErr = errors.Join(flagErr, errors.New("service must be specified"))
	}
	if namespace == "" {
		flagErr = errors.Join(flagErr, errors.New("namespace must be specified"))
	}

	if flagErr != nil {
		logger.Error(flagErr, "invalid flag usage")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	rotator := kube.NewCertRotator(kube.CertRotatorConfig{
		DNSName: fmt.Sprintf("%s.%s.svc", service, namespace),
		SecretKey: types.NamespacedName{
			Namespace: namespace,
			Name:      service + "-certificate",
		},
		Service: &apiextensionsv1.ServiceReference{
			Namespace: namespace,
			Name:      service,
			Path:      ptr.To("/convert"),
		},
		Webhooks: []kube.WebhookInfo{{
			Type:     kube.CRDConversion,
			Name:     "clusters.cluster.kube.redpanda.com",
			Versions: []string{"v1"},
		}, {
			Type: kube.APIService,
			Name: "v1.resources.kube.redpanda.com",
		}},
	})

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(resources.AddToScheme(scheme))
	utilruntime.Must(resourcesv1.AddToScheme(scheme))

	config := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme: scheme,
		WebhookServer: webhook.NewServer(webhook.Options{
			TLSOpts: []func(*tls.Config){func(c *tls.Config) {
				c.GetCertificate = rotator.GetCertificate
			}},
		}),
	})
	if err != nil {
		logger.Error(err, "initializing manager")
		os.Exit(1)
	}

	ctl, err := kube.FromRESTConfig(config, kube.Options{
		Options: client.Options{
			Scheme: scheme,
		},
		FieldManager: "example",
	})
	if err != nil {
		logger.Error(err, "initializing ctl")
		os.Exit(1)
	}

	if err := kube.AddRotator(mgr, rotator); err != nil {
		logger.Error(err, "setting up cert rotator")
		os.Exit(1)
	}

	storage := resources.NewVirtualStorage(ctl)

	if err := ctrl.NewWebhookManagedBy(mgr).For(&clusterv1.Cluster{}).Complete(); err != nil {
		logger.Error(err, "setting up webhook")
		os.Exit(1)
	}

	if err := ctrl.NewControllerManagedBy(mgr).For(&clusterv1.Cluster{}).Complete(&reconciler{ctl: ctl, storage: storage}); err != nil {
		logger.Error(err, "setting up reconciler")
		os.Exit(1)
	}

	if err := kube.NewAPIServerManagedBy(mgr).WithRotator(rotator).WithStorage("virtuals", storage).Complete(resourcesv1.GroupVersion, resourcesv1.GetOpenAPIDefinitions, "Virtual", "1.0"); err != nil {
		logger.Error(err, "setting up api server")
		os.Exit(1)
	}

	for _, crd := range crds.All() {
		if err := ctl.Apply(ctx, crd); err != nil {
			logger.Error(err, "applying crd")
			os.Exit(1)
		}
	}

	if err := mgr.Start(ctx); err != nil {
		logger.Error(err, "running manager")
		os.Exit(1)
	}
	logger.Info("shutting down")
}
