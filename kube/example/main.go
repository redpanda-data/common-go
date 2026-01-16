package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"log"
	"os"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/redpanda-data/common-go/kube"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	examplev1 "github.com/redpanda-data/common-go/kube/example/api/v1"
	"github.com/redpanda-data/common-go/kube/example/crds"
)

var logger logr.Logger

func init() {
	kube.SetContextLoggerFactory(func(ctx context.Context) logr.Logger {
		return ctrl.LoggerFrom(ctx)
	})
	zaplogger, err := zap.NewProduction(zap.AddStacktrace(zapcore.ErrorLevel))
	if err != nil {
		log.Fatal(err)
	}

	logger = zapr.NewLogger(zaplogger)
	ctrl.SetLogger(logger)
}

func main() {
	var hostname, service, namespace string
	flag.StringVar(&hostname, "hostname", "", "DNS host name for certificate")
	flag.StringVar(&service, "service", "", "Service where this is deployed")
	flag.StringVar(&namespace, "namespace", "", "Namespace where this is deployed")
	flag.Parse()

	var flagErr error
	if hostname == "" {
		flagErr = errors.Join(flagErr, errors.New("hostname must be specified"))
	}
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
		DNSName: hostname,
		SecretKey: types.NamespacedName{
			Namespace: metav1.NamespaceDefault,
			Name:      service + "-certificate",
		},
		Service: &apiextensionsv1.ServiceReference{
			Namespace: metav1.NamespaceDefault,
			Name:      service,
			Path:      ptr.To("/convert"),
		},
		Webhooks: []kube.WebhookInfo{{
			Type:     kube.CRDConversion,
			Name:     "examples.example.kube.redpanda.com",
			Versions: []string{"v1"},
		}},
	})

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(examplev1.AddToScheme(scheme))

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

	if err := ctrl.NewWebhookManagedBy(mgr).For(&examplev1.Example{}).Complete(); err != nil {
		logger.Error(err, "setting up webhook")
		os.Exit(1)
	}

	if err := ctrl.NewControllerManagedBy(mgr).For(&examplev1.Example{}).Complete(&reconciler{ctl: ctl}); err != nil {
		logger.Error(err, "setting up reconciler")
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
