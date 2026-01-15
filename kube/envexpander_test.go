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
	"strconv"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/redpanda-data/common-go/kube"
	"github.com/redpanda-data/common-go/kube/kubetest"
)

func TestEnvExpander(t *testing.T) {
	t.Parallel()

	log.SetLogger(testr.New(t))

	ctl := kubetest.NewEnv(t)
	c, err := client.New(ctl.RestConfig(), client.Options{})
	require.NoError(t, err)

	require.NoError(t, ctl.Create(t.Context(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "configs",
			Namespace: "default",
		},
		Data: map[string]string{
			"NODE_ENV": "PROD",
			"KEY":      "Value!",
		},
	}))

	require.NoError(t, ctl.Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secrets",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"ACCESS_KEY": []byte(`super!s3cret1`),
		},
	}))

	cases := []struct {
		In      string
		Out     string
		Err     error
		Env     []corev1.EnvVar
		EnvFrom []corev1.EnvFromSource
	}{
		{In: "${FOO}", Out: ""},
		{
			In:  "$BAR",
			Out: "Hello!",
			Env: []corev1.EnvVar{
				{
					Name:  "BAR",
					Value: "Hello!",
				},
			},
		},
		{
			In:  "$NODE_ENV uses ${ACCESS_KEY}",
			Out: "PROD uses super!s3cret1",
			Env: []corev1.EnvVar{
				{
					Name:  "BAR",
					Value: "Hello!",
				},
			},
			EnvFrom: []corev1.EnvFromSource{
				{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "configs"}}},
				{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "secrets"}}},
			},
		},
		{
			In:  "Precedence is LIFO: ${ACCESS_KEY}",
			Out: "Precedence is LIFO: Value!",
			Env: []corev1.EnvVar{},
			EnvFrom: []corev1.EnvFromSource{
				{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "secrets"}}},
				{Prefix: "ACCESS_", ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "configs"}}},
			},
		},
	}

	for i, tc := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()

			expander := kube.EnvExpander{
				Client:    c,
				Namespace: "default",
				Env:       tc.Env,
				EnvFrom:   tc.EnvFrom,
			}

			expanded, err := expander.Expand(t.Context(), tc.In)
			assert.NoError(t, err)

			assert.Equal(t, tc.Out, expanded)
		})
	}
}
