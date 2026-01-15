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

package kube

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
)

// MigrateManifest runs conversion routines associated with the conversion.Hub of the given GroupKind and registered
// in the provided runtime.Schema, returning the equivalent manifest, but with migrated resources.
func MigrateManifest(scheme *runtime.Scheme, r io.Reader, gk schema.GroupKind) ([]byte, error) { //nolint:gocognit,cyclop // complexity is fine
	var hub schema.GroupVersionKind
	convertibles := []schema.GroupVersionKind{}
	hasHub := false

	for gvk := range scheme.AllKnownTypes() {
		if gvk.GroupKind() != gk {
			continue
		}
		obj, err := scheme.New(gvk)
		if err != nil {
			return nil, err
		}
		if _, ok := obj.(conversion.Hub); ok {
			hub = gvk
			hasHub = true
			continue
		}

		if _, ok := obj.(conversion.Convertible); ok {
			convertibles = append(convertibles, gvk)
		}
	}

	if !hasHub {
		return nil, fmt.Errorf("groupkind %q does not have a conversion hub specified", gk.String())
	}

	var buffer bytes.Buffer
	reader := yaml.NewYAMLReader(bufio.NewReader(r))
	codec := serializer.NewCodecFactory(scheme)
	deserializer := codec.UniversalDeserializer()
	serializer := json.NewYAMLSerializer(json.DefaultMetaFactory, scheme, scheme)
	i := 0
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		if i > 0 {
			if _, err := fmt.Fprintln(&buffer, "---"); err != nil {
				return nil, err
			}
		}
		i++

		obj, _, err := deserializer.Decode(doc, nil, nil)
		if err != nil {
			return nil, err
		}
		gvk := obj.GetObjectKind().GroupVersionKind()
		if !slices.Contains(convertibles, gvk) {
			if _, err := buffer.Write(doc); err != nil {
				return nil, err
			}
			continue
		}

		encoder := codec.EncoderForVersion(serializer, hub.GroupVersion())
		hubObject, err := scheme.New(hub)
		if err != nil {
			return nil, err
		}
		converter := obj.(conversion.Convertible)     //nolint:revive // this was already checked above
		convertedObject := hubObject.(conversion.Hub) //nolint:revive // this was already checked above
		if err := converter.ConvertTo(convertedObject); err != nil {
			return nil, err
		}

		if err := encoder.Encode(convertedObject, &buffer); err != nil {
			return nil, err
		}
	}
	return buffer.Bytes(), nil
}

// Migrate migrates all objects of the given GroupKind to the CRD's storage version,
// afterwards updating the CRD's stored versions so that deprecated versions can
// subsequently be removed.
func (c *Ctl) Migrate(ctx context.Context, gk schema.GroupKind) error {
	mapping, err := c.RESTMapper().RESTMapping(gk)
	if err != nil {
		return err
	}

	gr := mapping.Resource.GroupResource()

	var crd apiextensionsv1.CustomResourceDefinition
	if err := c.Get(ctx, types.NamespacedName{Name: gr.String()}, &crd); err != nil {
		return fmt.Errorf("unable to fetch crd %s - %w", gr, err)
	}

	version := storageVersion(crd)
	if version == "" {
		return fmt.Errorf("unable to determine storage version for %s", gr)
	}

	if err := migrateResources(ctx, c.client, schema.GroupVersionKind{
		Group:   gk.Group,
		Kind:    gk.Kind,
		Version: version,
	}); err != nil {
		return err
	}

	patch := `{"status":{"storedVersions":["` + version + `"]}}`
	if err := c.client.Status().Patch(ctx, &crd, client.RawPatch(types.StrategicMergePatchType, []byte(patch))); err != nil {
		return fmt.Errorf("unable to drop storage version definition %s - %w", gr, err)
	}

	return nil
}

func migrateResources(ctx context.Context, c client.Client, gvk schema.GroupVersionKind) error {
	var list unstructured.UnstructuredList
	list.SetGroupVersionKind(gvk)

	if err := c.List(ctx, &list); err != nil {
		return err
	}

	var err error
	for _, item := range list.Items {
		if patchErr := c.Patch(ctx, &item, client.RawPatch(types.MergePatchType, []byte("{}"))); patchErr != nil {
			return errors.Join(err, fmt.Errorf("unable to patch resource %q: %v", client.ObjectKeyFromObject(&item), patchErr))
		}
	}

	return err
}

func storageVersion(crd apiextensionsv1.CustomResourceDefinition) string {
	var version string

	for _, v := range crd.Spec.Versions {
		if v.Storage {
			version = v.Name
			break
		}
	}

	return version
}
