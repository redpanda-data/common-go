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

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	clientscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	"github.com/redpanda-data/common-go/kube"
)

func TestEncodeDecode(t *testing.T) {
	objs := []kube.Object{
		&corev1.Pod{Spec: corev1.PodSpec{DNSPolicy: corev1.DNSClusterFirst}},
		&corev1.Service{Spec: corev1.ServiceSpec{ClusterIP: "127.0.0.1"}},
		&appsv1.StatefulSet{Spec: appsv1.StatefulSetSpec{Replicas: ptr.To[int32](10)}},
		&appsv1.Deployment{Spec: appsv1.DeploymentSpec{Paused: true}},
	}

	encoded, err := kube.EncodeYAML(clientscheme.Scheme, objs...)
	require.NoError(t, err)

	decoded, err := kube.DecodeYAML(encoded, nil)
	require.NoError(t, err)

	require.IsType(t, &corev1.Pod{}, decoded[0])
	require.IsType(t, &corev1.Service{}, decoded[1])
	require.IsType(t, &appsv1.StatefulSet{}, decoded[2])
	require.IsType(t, &appsv1.Deployment{}, decoded[3])
	require.Equal(t, objs, decoded)
}
