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
	"github.com/cockroachdb/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ListFor returns a new ObjectList appropriate for holding a list of the
// provided object's type.
func ListFor(scheme *runtime.Scheme, obj Object) (ObjectList, error) {
	gvk, err := GVKFor(scheme, obj)
	if err != nil {
		return nil, err
	}

	olist, err := scheme.New(schema.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind + "List",
	})
	if err != nil {
		return nil, errors.WithStack(err)
	}

	list, ok := olist.(ObjectList)
	if !ok {
		return nil, errors.Newf("type is not ObjectList: %T", obj)
	}

	return list, nil
}

// GVKFor returns the GroupVersionKind for the provided object as known by
// the provided scheme.
func GVKFor(scheme *runtime.Scheme, object Object) (schema.GroupVersionKind, error) {
	kinds, _, err := scheme.ObjectKinds(object)
	if err != nil {
		return schema.GroupVersionKind{}, errors.WithStack(err)
	}

	if len(kinds) == 0 {
		return schema.GroupVersionKind{}, errors.Newf("unable to determine object kind: %T", object)
	}

	return kinds[0], nil
}

func setGVK(scheme *runtime.Scheme, obj Object) error {
	gvk, err := GVKFor(scheme, obj)
	if err != nil {
		return err
	}

	obj.GetObjectKind().SetGroupVersionKind(gvk)

	return nil
}
