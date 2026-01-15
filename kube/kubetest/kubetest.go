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

package kubetest

import (
	"context"
	"crypto/tls"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/redpanda-data/common-go/kube"
)

const controlPlaneVersion = "1.30.x"

// NewEnv starts a local kubernetes control plane via [envtest.Environment] and
// returns a [kube.Ctl] to access it. The provided [testing.T] will be used to
// shutdown the control plane at the end of the test.
func NewEnv(t *testing.T, opts ...kube.Option) *kube.Ctl {
	t.Helper()

	// TODO: Would be nice to instead just import setup-envtest but the package
	// isn't exactly friendly to be used as a library. Alternatively, we could
	// use nix to provide the etcd and kubeapi-server binaries as that's all
	// setup-envtest does.
	if _, err := exec.LookPath("setup-envtest"); err != nil {
		t.Fatal("setup-envtest not found in $PATH. Did you forget to install it?")
	}

	stdout, err := exec.CommandContext(t.Context(), "setup-envtest", "use", controlPlaneVersion, "-p", "path").CombinedOutput()
	require.NoError(t, err)

	env := envtest.Environment{
		BinaryAssetsDirectory:    string(stdout),
		ControlPlaneStartTimeout: 30 * time.Second,
		ControlPlaneStopTimeout:  30 * time.Second,
	}

	cfg, err := env.Start()
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, env.Stop())
	})

	ctl, err := kube.FromRESTConfig(cfg, opts...)
	require.NoError(t, err)

	return ctl
}

// KubetestManagerOptions contains options for running
// components of a kubetest-initialized manager.
type KubetestManagerOptions struct {
	rotatorConfig   *kube.CertRotatorConfig
	registrationFns []func(ctrl.Manager) error
	cancelContext   context.Context
	stopped         chan struct{}
}

// KubeTestManagerOption is a function for specifying optional
// parameters to a kubetest-initialized manager
type KubeTestManagerOption func(*KubetestManagerOptions)

// WithRotator adds in a certificate rotator meant for injecting
// certificate information into webhooks.
func WithRotator(config *kube.CertRotatorConfig) KubeTestManagerOption {
	return func(o *KubetestManagerOptions) {
		o.rotatorConfig = config
	}
}

// WithRegisterFn allows for registering arbitrary runnables on the manager
// before it starts.
func WithRegisterFn(fn func(ctrl.Manager) error) KubeTestManagerOption {
	return func(o *KubetestManagerOptions) {
		o.registrationFns = append(o.registrationFns, fn)
	}
}

// WithCancelation allows use to specify some controls about when to stop
// the manager (and a channel to ensure it is stopped) to simulate things
// like controller crashes.
func WithCancelation(ctx context.Context, stopped chan struct{}) KubeTestManagerOption {
	return func(o *KubetestManagerOptions) {
		o.cancelContext = ctx
		o.stopped = stopped
	}
}

// RunManager runs a manager wired up to the given kube.Ctl instance.
func RunManager(t *testing.T, ctl *kube.Ctl, opts ...KubeTestManagerOption) {
	t.Helper()

	hooks := &envtest.WebhookInstallOptions{}
	if err := hooks.PrepWithoutInstalling(); err != nil {
		t.Fatalf("failed to prep webhooks: %v", err)
	}
	url := fmt.Sprintf("https://%s:%d/convert", hooks.LocalServingHost, hooks.LocalServingPort)

	options := &KubetestManagerOptions{}
	for _, opt := range opts {
		opt(options)
	}

	webhookServerOpts := webhook.Options{
		Host: hooks.LocalServingHost,
		Port: hooks.LocalServingPort,
	}

	var rotator *kube.CertRotator
	if options.rotatorConfig != nil {
		options.rotatorConfig.DNSName = hooks.LocalServingHost
		options.rotatorConfig.URL = ptr.To(url)

		rotator = kube.NewCertRotator(*options.rotatorConfig)
		webhookServerOpts.TLSOpts = append(webhookServerOpts.TLSOpts, func(c *tls.Config) {
			c.GetCertificate = rotator.GetCertificate
		})
	}

	cancellable := context.Background()
	stopped := make(chan struct{})
	if options.cancelContext != nil {
		cancellable = options.cancelContext
		stopped = options.stopped
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-t.Context().Done():
		case <-cancellable.Done():
		}
		cancel()
	}()

	logger := testr.NewWithOptions(t, testr.Options{Verbosity: 3})
	mgr, err := ctrl.NewManager(ctl.RestConfig(), ctrl.Options{
		Scheme:        ctl.Scheme(),
		WebhookServer: webhook.NewServer(webhookServerOpts),
		BaseContext: func() context.Context {
			return log.IntoContext(ctx, logger)
		},
		Controller: config.Controller{
			Logger: logger,
		},
		Logger: logger,
	})

	for _, fn := range options.registrationFns {
		if err := fn(mgr); err != nil {
			t.Fatalf("failed to call registration function: %v", err)
		}
	}

	if err != nil {
		t.Fatalf("failed to initialize manager: %v", err)
	}

	if rotator != nil {
		if err := kube.AddRotator(mgr, rotator); err != nil {
			t.Fatalf("failed to add rotator: %v", err)
		}
	}

	go func() {
		if err := mgr.Start(ctx); err != nil {
			t.Errorf("error running manager: %v", err)
		}
		close(stopped)
	}()
}
