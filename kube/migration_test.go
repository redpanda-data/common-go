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
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	"github.com/redpanda-data/common-go/kube"
	"github.com/redpanda-data/common-go/kube/kubetest"
)

var update = os.Getenv("KUBE_TEST_UPDATE_GOLDEN") == "true"

const (
	testPackage      = "example.com"
	testKind         = "MyKind"
	testNameSingular = "mykind"
	testNamePlural   = "mykinds"
	testCRDName      = testNamePlural + "." + testPackage

	deprecatedVersion = "v1"
	latestVersion     = "v2"
)

var (
	testGK = schema.GroupKind{
		Group: testPackage,
		Kind:  testKind,
	}
	deprecatedGVK = schema.GroupVersionKind{
		Group:   testPackage,
		Kind:    testKind,
		Version: deprecatedVersion,
	}
	latestGVK = schema.GroupVersionKind{
		Group:   testPackage,
		Kind:    testKind,
		Version: latestVersion,
	}
)

type myKindV1 struct {
	metav1.TypeMeta   `json:",inline"`            //nolint:revive // test code
	metav1.ObjectMeta `json:"metadata,omitempty"` //nolint:revive // test code
	Value             string                      `json:"value"`
}

func (m *myKindV1) DeepCopyObject() runtime.Object {
	return &myKindV1{
		TypeMeta:   m.TypeMeta,
		ObjectMeta: m.ObjectMeta,
		Value:      m.Value,
	}
}

func (m *myKindV1) ToV2() *myKindV2 {
	return &myKindV2{
		ObjectMeta: m.ObjectMeta,
		Value:      m.Value == "true",
	}
}

type myKindV2 struct {
	metav1.TypeMeta   `json:",inline"`            //nolint:revive // test code
	metav1.ObjectMeta `json:"metadata,omitempty"` //nolint:revive // test code
	Value             bool                        `json:"value"`
}

func (m *myKindV2) DeepCopyObject() runtime.Object {
	return &myKindV2{
		TypeMeta:   m.TypeMeta,
		ObjectMeta: m.ObjectMeta,
		Value:      m.Value,
	}
}

func (m *myKindV2) ToV1() *myKindV1 {
	return &myKindV1{
		ObjectMeta: m.ObjectMeta,
		Value:      fmt.Sprintf("%v", m.Value),
	}
}

// Conversions

func (*myKindV2) Hub() {}

func (m *myKindV1) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*myKindV2) //nolint:revive // test file, we know what this is
	dst.ObjectMeta = m.ToV2().ObjectMeta
	dst.Value = m.ToV2().Value
	return nil
}

func (m *myKindV1) ConvertFrom(srcRaw conversion.Hub) error {
	src := srcRaw.(*myKindV2) //nolint:revive // test file, we know what this is
	m.ObjectMeta = src.ToV1().ObjectMeta
	m.Value = src.ToV1().Value
	return nil
}

func setupCRD(t *testing.T, c *kube.Ctl) {
	t.Helper()

	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: testCRDName},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: testPackage,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     testKind,
				Plural:   testNamePlural,
				Singular: testNameSingular,
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:       deprecatedVersion,
				Deprecated: false,
				Served:     true,
				Storage:    true,
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type: "object",
						Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"value": {
								Type: "string",
							},
						},
					},
				},
			}},
		},
	}

	if err := c.Create(t.Context(), crd); err != nil {
		t.Fatalf("create crd: %v", err)
	}

	if err := wait.PollUntilContextTimeout(t.Context(), 500*time.Millisecond, 15*time.Second, true, func(ctx context.Context) (bool, error) {
		if err := c.Get(ctx, types.NamespacedName{Name: testCRDName}, crd); err != nil {
			return false, err
		}
		for _, cond := range crd.Status.Conditions {
			if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		t.Fatalf("crd not established: %v", err)
	}
}

func updateCRD(t *testing.T, c *kube.Ctl) {
	t.Helper()

	var crd apiextensionsv1.CustomResourceDefinition
	if err := c.Get(t.Context(), types.NamespacedName{Name: testCRDName}, &crd); err != nil {
		t.Fatalf("get crd: %v", err)
	}

	crd.Spec.Versions[0].Deprecated = true
	crd.Spec.Versions[0].Storage = false
	crd.Spec.Versions = append(crd.Spec.Versions, apiextensionsv1.CustomResourceDefinitionVersion{
		Name:    latestVersion,
		Served:  true,
		Storage: true,
		Schema: &apiextensionsv1.CustomResourceValidation{
			OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"value": {
						Type: "boolean",
					},
				},
			},
		},
	})

	if err := c.Update(t.Context(), &crd); err != nil {
		t.Fatalf("update crd: %v", err)
	}
}

func dropCRDDeprecation(t *testing.T, c *kube.Ctl) {
	t.Helper()

	var crd apiextensionsv1.CustomResourceDefinition
	if err := c.Get(t.Context(), types.NamespacedName{Name: testCRDName}, &crd); err != nil {
		t.Fatalf("get crd: %v", err)
	}

	version := crd.Spec.Versions[1]
	crd.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{version}

	if err := c.Update(t.Context(), &crd); err != nil {
		t.Fatalf("update crd: %v", err)
	}
}

func createCR(t *testing.T, c *kube.Ctl, gvk schema.GroupVersionKind, nn types.NamespacedName, data map[string]any, shouldError bool) {
	t.Helper()

	// ensure namespace exists
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nn.Namespace}}
	if err := c.Create(t.Context(), ns); err != nil && !errors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	u.SetName(nn.Name)
	u.SetNamespace(nn.Namespace)
	maps.Copy(u.Object, data)

	err := c.Create(t.Context(), u)
	if err != nil && !shouldError {
		t.Fatalf("create custom resource: %v", err)
	}
	if err == nil && shouldError {
		t.Fatal("there should have beeen an error creating this CR, but there wasn't")
	}
}

func fetchCRDVersions(t *testing.T, c *kube.Ctl) []string {
	t.Helper()

	var got apiextensionsv1.CustomResourceDefinition
	if err := c.Get(t.Context(), types.NamespacedName{Name: testCRDName}, &got); err != nil {
		t.Fatalf("get crd: %v", err)
	}

	return got.Status.StoredVersions
}

func fetchCRs(t *testing.T, c *kube.Ctl, gvk schema.GroupVersionKind) []unstructured.Unstructured {
	t.Helper()

	var got unstructured.UnstructuredList
	got.SetGroupVersionKind(gvk)
	if err := c.List(t.Context(), "", &got); err != nil {
		t.Fatalf("get crs: %v", err)
	}

	return got.Items
}

func TestMigrate(t *testing.T) { //nolint:cyclop // complexity is fine
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apiextensions to scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core to scheme: %v", err)
	}

	scheme.AddKnownTypeWithName(deprecatedGVK, &myKindV1{})
	scheme.AddKnownTypeWithName(latestGVK, &myKindV2{})

	ctl := kubetest.NewEnv(t, kube.Options{
		Options: client.Options{
			Scheme: scheme,
		},
	})

	setupCRD(t, ctl)

	createCR(t, ctl, deprecatedGVK, types.NamespacedName{
		Namespace: "ns1",
		Name:      "a",
	}, map[string]any{
		"value": "true",
	}, false)

	for _, cr := range fetchCRs(t, ctl, deprecatedGVK) {
		if cr.Object["value"] != "true" {
			t.Fatalf("object %q value does not match: actual %v(%T) - expected true(string)", cr.GetName(), cr.Object["value"], cr.Object["value"])
		}
	}

	updateCRD(t, ctl)

	kubetest.RunManager(t, ctl, kubetest.WithRotator(&kube.CertRotatorConfig{
		SecretKey: types.NamespacedName{
			Namespace: "default",
			Name:      "certificate",
		},
		Webhooks: []kube.WebhookInfo{{
			Type:     kube.CRDConversion,
			Name:     testCRDName,
			Versions: []string{deprecatedVersion, latestVersion},
		}},
		ControllerName: t.Name(),
	}), kubetest.WithRegisterFn(func(m ctrl.Manager) error {
		return ctrl.NewWebhookManagedBy(m).For(&myKindV2{}).Complete()
	}))

	ensureInjected(t, ctl, testCRDName)

	createCR(t, ctl, latestGVK, types.NamespacedName{
		Namespace: "ns2",
		Name:      "b",
	}, map[string]any{
		"value": true,
	}, false)

	versions := fetchCRDVersions(t, ctl)
	if !slices.Contains(versions, deprecatedVersion) {
		t.Fatalf("versions %q not found in CRD stored versions", deprecatedVersion)
	}
	if !slices.Contains(versions, latestVersion) {
		t.Fatalf("versions %q not found in CRD stored versions", latestVersion)
	}

	if err := ctl.Migrate(t.Context(), testGK); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}

	versions = fetchCRDVersions(t, ctl)
	if slices.Contains(versions, deprecatedVersion) {
		t.Fatalf("versions %q found in CRD stored versions", deprecatedVersion)
	}
	if !slices.Contains(versions, latestVersion) {
		t.Fatalf("versions %q not found in CRD stored versions", latestVersion)
	}

	// v2 --> v1
	for _, cr := range fetchCRs(t, ctl, deprecatedGVK) {
		if cr.Object["value"] != "true" {
			t.Fatalf("object %q value does not match: actual %v(%T) - expected true(string)", cr.GetName(), cr.Object["value"], cr.Object["value"])
		}
	}
	// v1 --> v2
	for _, cr := range fetchCRs(t, ctl, latestGVK) {
		if cr.Object["value"] != true { //nolint:revive // we want the explicit boolean check
			t.Fatalf("object %q value does not match: actual %v(%T) - expected true(bool)", cr.GetName(), cr.Object["value"], cr.Object["value"])
		}
	}

	dropCRDDeprecation(t, ctl)
	createCR(t, ctl, latestGVK, types.NamespacedName{
		Namespace: "ns3",
		Name:      "c",
	}, map[string]any{
		"value": true,
	}, false)

	// check to make sure we error due to now invalid GVK

	createCR(t, ctl, deprecatedGVK, types.NamespacedName{
		Namespace: "ns1",
		Name:      "d",
	}, map[string]any{
		"value": "true",
	}, true)
}

func TestMigrateManifest(t *testing.T) { //nolint:gocognit // test is fine
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add apiextensions to scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core to scheme: %v", err)
	}

	scheme.AddKnownTypeWithName(deprecatedGVK, &myKindV1{})
	scheme.AddKnownTypeWithName(latestGVK, &myKindV2{})

	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("error reading fixture directory: %v", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		if strings.HasSuffix(e.Name(), ".yaml") {
			t.Run(strings.TrimSuffix(e.Name(), ".yaml"), func(t *testing.T) {
				t.Parallel()

				fixture := path.Join("testdata", e.Name())
				golden := path.Join("testdata", e.Name()+".golden")

				fixtureFile, err := os.Open(fixture)
				if err != nil {
					t.Fatalf("error reading fixture file %q: %v", fixture, err)
				}
				defer fixtureFile.Close()

				actual, err := kube.MigrateManifest(scheme, fixtureFile, testGK)
				if err != nil {
					t.Fatalf("error migrating file %q: %v", fixture, err)
				}

				if update {
					if err := os.WriteFile(golden, actual, 0o644); err != nil {
						t.Fatalf("error updating fixture golden file %q: %v", golden, err)
					}
				}

				expected, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("error reading fixture golden file %q: %v", golden, err)
				}

				if !bytes.Equal(actual, expected) {
					t.Fatalf("fixture file differs from expected:\n\nACTUAL:\n\n%s\n\nEXPECTED:\n\n%s", string(actual), string(expected))
				}
			})
		}
	}
}
