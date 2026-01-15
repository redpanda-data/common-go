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
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/redpanda-data/common-go/kube"
	"github.com/redpanda-data/common-go/kube/kubetest"
)

func TestCtl(t *testing.T) {
	log.SetLogger(testr.New(t))

	ctl := kubetest.NewEnv(t)
	ctx := t.Context()

	require.NoError(t, ctl.Apply(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hello-world",
		},
	}))

	ns, err := kube.Get[corev1.Namespace](ctx, ctl, kube.ObjectKey{Name: "hello-world"})
	require.NoError(t, err)

	// Cond is explicitly a *Namespace here!
	require.EqualError(t, kube.WaitFor(ctx, ctl, ns, func(_ *corev1.Namespace, _ error) (bool, error) {
		return false, errors.New("passed through")
	}), "passed through")

	var seen []string
	require.NoError(t, kube.ApplyAllAndWait(ctx, ctl, func(cm *corev1.ConfigMap, _ error) (bool, error) {
		seen = append(seen, cm.Name)
		return true, nil
	},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "hello-world", Name: "cm-0"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "hello-world", Name: "cm-1"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "hello-world", Name: "cm-2"}},
	))
	require.Equal(t, []string{"cm-0", "cm-1", "cm-2"}, seen)

	cms, err := kube.List[corev1.ConfigMapList](ctx, ctl, "hello-world")
	require.NoError(t, err)
	require.Len(t, cms.Items, 3)

	t.Run("Apply", func(t *testing.T) {
		t.Parallel()

		s := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "conflict-me",
				Namespace: ns.Name,
			},
			Data: map[string][]byte{},
		}

		require.NoError(t, ctl.Create(t.Context(), s))

		// Modify to change (increment) resource version.
		// DeepCopy and an explicit scope is used so `s`'s ResourceVersion is unchanged.
		{
			s := s.DeepCopy()
			s.Data = map[string][]byte{"key": []byte("value")}
			require.NoError(t, ctl.Apply(t.Context(), s))
		}

		// Update fails with an optimistic locking error as .ResourceVersion is not zero.
		s.Data = map[string][]byte{"key": []byte("valuevalue")}
		require.EqualError(t, ctl.Update(ctx, s), "Operation cannot be fulfilled on secrets \"conflict-me\": the object has been modified; please apply your changes to the latest version and try again")

		// Apply succeeds, it does not support optimistic locking.
		require.NoError(t, ctl.Apply(ctx, s))
	})

	t.Run("Delete", func(t *testing.T) {
		t.Parallel()

		s := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "delete-me",
				Namespace: ns.Name,
				// Set a finalizer to stall deletion.
				Finalizers: []string{"i-prevent.com/deletion"},
			},
		}

		require.NoError(t, ctl.Apply(ctx, s))
		require.NotZero(t, s.UID)

		require.NoError(t, ctl.Delete(ctx, s))

		// Refresh s and assert that it's now deleting.
		require.NoError(t, ctl.Get(ctx, kube.AsKey(s), s))
		require.NotNil(t, s.DeletionTimestamp)

		// Re-issue delete with DeletionTimestamp already set to showcase that
		// doesn't result in errors.
		require.NoError(t, ctl.Delete(ctx, s))

		// Clear finalizers to allow deletion to progress.
		s.Finalizers = nil
		require.NoError(t, ctl.ApplyAndWait(ctx, s, kube.IsDeleted))

		// No 404s if we attempt o delete an already deleted object.
		require.NoError(t, ctl.Delete(ctx, s))
	})
}
