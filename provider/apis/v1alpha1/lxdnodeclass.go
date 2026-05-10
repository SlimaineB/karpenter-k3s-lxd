package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type LXDNodeClassSpec struct {
	Image string `json:"image,omitempty"`

	DefaultCPU string `json:"defaultCPU,omitempty"`

	DefaultMemory string `json:"defaultMemory,omitempty"`

	NodeRegistrationDelay metav1.Duration `json:"nodeRegistrationDelay,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=lxdnodeclasses,scope=Cluster,categories=karpenter,shortName={lxdnc,lxdncs}
// +kubebuilder:subresource:status
type LXDNodeClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec LXDNodeClassSpec `json:"spec,omitempty"`
	// +kubebuilder:default:={conditions: {{type: "Ready", status: "True", reason:"Ready", lastTransitionTime: "2024-01-01T01:01:01Z", message: ""}}}
	Status LXDNodeClassStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type LXDNodeClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LXDNodeClass `json:"items"`
}
