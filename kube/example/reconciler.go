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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/redpanda-data/common-go/kube"
	clusterv1 "github.com/redpanda-data/common-go/kube/example/api/cluster/v1"
	"github.com/redpanda-data/common-go/kube/example/api/resources"
)

type reconciler struct {
	ctl     *kube.Ctl
	storage *resources.VirtualStorage
}

func (r *reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("reconciler")
	logger.Info("reconciling", "namespace", req.Namespace, "name", req.Name)

	_, err := kube.Get[clusterv1.Cluster](ctx, r.ctl, req.NamespacedName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			r.storage.MarkUnlinked(req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	r.storage.MarkLinked(req.NamespacedName)

	return ctrl.Result{}, nil
}
