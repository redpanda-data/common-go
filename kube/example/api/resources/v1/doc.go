// Package v2 contains API Schema definitions for the redpanda v1 API group
// +groupName=resources.kube.redpanda.com
// +versionName=v1
// +k8s:conversion-gen=github.com/redpanda-data/common-go/kube/example/api/resources
package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "resources.kube.redpanda.com", Version: "v1"}

	// SchemeBuilder is the scheme builder with scheme init functions to run for this API package
	SchemeBuilder      runtime.SchemeBuilder
	localSchemeBuilder = &SchemeBuilder

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = localSchemeBuilder.AddToScheme
)

func init() {
	localSchemeBuilder.Register(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(GroupVersion, &Virtual{}, &VirtualList{})
		metav1.AddToGroupVersion(scheme, GroupVersion)
		return nil
	})
}
