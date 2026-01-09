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
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/redpanda-data/common-go/kube"
)

const controlPlaneVersion = "1.30.x"

// NewEnv starts a local kubernetes control plane via [envtest.Environment] and
// returns a [kube.Ctl] to access it. The provided [testing.T] will be used to
// shutdown the control plane at the end of the test.
func NewEnv(t *testing.T, opts ...kube.Option) *kube.Ctl {
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
