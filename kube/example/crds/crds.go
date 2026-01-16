package crds

import (
	"embed"
	"io/fs"

	"github.com/cockroachdb/errors"
	"github.com/redpanda-data/common-go/kube"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

func Example() *apiextensionsv1.CustomResourceDefinition {
	return mustT(ByName("examples.example.kube.redpanda.com"))
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
