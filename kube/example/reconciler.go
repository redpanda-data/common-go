package main

import (
	"context"

	"github.com/redpanda-data/common-go/kube"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type reconciler struct {
	ctl *kube.Ctl
}

func (r *reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("reconciler")
	logger.Info("reconciling", "namespace", req.Namespace, "name", req.Name)
	return ctrl.Result{}, nil
}
