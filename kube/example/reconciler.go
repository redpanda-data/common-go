package main

import (
	"context"

	"github.com/redpanda-data/common-go/kube"
	clusterv1 "github.com/redpanda-data/common-go/kube/example/api/cluster/v1"
	"github.com/redpanda-data/common-go/kube/example/api/resources"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
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
