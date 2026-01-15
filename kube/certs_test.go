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

package kube_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/redpanda-data/common-go/kube"
	"github.com/redpanda-data/common-go/kube/kubetest"
)

func ensureInjected(t *testing.T, ctl *kube.Ctl, crdName string) {
	t.Helper()

	if err := wait.PollUntilContextTimeout(t.Context(), 500*time.Millisecond, 15*time.Second, true, func(ctx context.Context) (bool, error) {
		var crd apiextensionsv1.CustomResourceDefinition
		if err := ctl.Get(ctx, types.NamespacedName{Name: crdName}, &crd); err != nil {
			return false, err
		}
		conversion := crd.Spec.Conversion
		hasFields := conversion != nil && conversion.Strategy == apiextensionsv1.WebhookConverter && conversion.Webhook != nil && conversion.Webhook.ClientConfig != nil
		if !hasFields {
			return false, nil
		}
		clientConfig := conversion.Webhook.ClientConfig
		hasConfigValues := clientConfig.URL != nil
		if !hasConfigValues {
			return false, nil
		}

		block, _ := pem.Decode(clientConfig.CABundle)
		if block == nil {
			return false, nil
		}

		_, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return false, err
		}

		return true, nil
	}); err != nil {
		t.Fatalf("crd not injected: %v", err)
	}
}

func TestCertRotator(t *testing.T) { //nolint:cyclop // complexity is fine, this is a test
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apiextensions to scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core to scheme: %v", err)
	}

	scheme.AddKnownTypeWithName(deprecatedGVK, &myKindV1{})
	ctl := kubetest.NewEnv(t, kube.Options{
		Options: client.Options{
			Scheme: scheme,
		},
	})

	setupCRD(t, ctl)

	kubetest.RunManager(t, ctl, kubetest.WithRotator(&kube.CertRotatorConfig{
		SecretKey: types.NamespacedName{
			Namespace: "default",
			Name:      "certificate",
		},
		Webhooks: []kube.WebhookInfo{{
			Type:     kube.CRDConversion,
			Name:     testCRDName,
			Versions: []string{deprecatedVersion},
		}},
		ControllerName: t.Name(),
	}))

	ensureInjected(t, ctl, testCRDName)
}
