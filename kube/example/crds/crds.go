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

package crds

import (
	"embed"
	"io/fs"

	"github.com/cockroachdb/errors"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/redpanda-data/common-go/kube"
)

var (
	//go:embed *.yaml
	crdFS embed.FS

	crds   []*apiextensionsv1.CustomResourceDefinition
	byName map[string]*apiextensionsv1.CustomResourceDefinition
)

func init() {
	scheme := runtime.NewScheme()
	must(apiextensionsv1.AddToScheme(scheme))

	byName = map[string]*apiextensionsv1.CustomResourceDefinition{}

	must(fs.WalkDir(crdFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		data, err := fs.ReadFile(crdFS, path)
		if err != nil {
			return err
		}

		objs, err := kube.DecodeYAML(data, scheme)
		if err != nil {
			return err
		}

		for _, obj := range objs {
			crd := obj.(*apiextensionsv1.CustomResourceDefinition)

			crds = append(crds, crd)
			byName[crd.Name] = crd
		}

		return nil
	}))
}

func ByName(name string) (*apiextensionsv1.CustomResourceDefinition, error) {
	crd, ok := byName[name]
	if !ok {
		return nil, errors.Newf("no such CRD %q", name)
	}
	return crd, nil
}

func All() []*apiextensionsv1.CustomResourceDefinition {
	ret := make([]*apiextensionsv1.CustomResourceDefinition, len(crds))

	for i, crd := range crds {
		ret[i] = crd.DeepCopy()
	}

	return ret
}

func Cluster() *apiextensionsv1.CustomResourceDefinition {
	return mustT(ByName("clusters.cluster.kube.redpanda.com"))
}

func mustT[T any](r T, err error) T {
	must(err)
	return r
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
