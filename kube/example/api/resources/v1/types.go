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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Virtual is a virtual CRD
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +k8s:openapi-gen=true
// +genclient
type Virtual struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VirtualSpec   `json:"spec,omitempty"`
	Status VirtualStatus `json:"status,omitempty"`
}

// VirtualSpec defines the desired state of a virtual.
// +k8s:openapi-gen=true
type VirtualSpec struct {
	ClusterRef ClusterRef `json:"clusterRef"`
}

// ClusterRef is a reference to the cluster where this resource
// should be created.
// +k8s:openapi-gen=true
type ClusterRef struct {
	Name string `json:"name"`
}

// VirtualStatus is the status for a virtual.
// +k8s:openapi-gen=true
type VirtualStatus struct {
	Linked bool `json:"linked,omitempty"`
}

// +kubebuilder:object:root=true

// VirtualList is a list of Virtual objects.
// +k8s:openapi-gen=true
type VirtualList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Virtual `json:"items"`
}
