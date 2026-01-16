// Package v1 contains API Schema definitions for the redpanda v1 API group
// +kubebuilder:object:generate=true
// +kubebuilder:ac:generate=true
// +groupName=cluster.kube.redpanda.com
// +versionName=v1
// +k8s:openapi-gen=true
package v1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "cluster.kube.redpanda.com", Version: "v1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
