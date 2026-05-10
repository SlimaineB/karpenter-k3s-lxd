// +groupName=karpenter.k3s.sh
package v1alpha1

import (
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/sliman/k3s-lxd-provider/apis"
)

func init() {
	gv := schema.GroupVersion{Group: apis.Group, Version: "v1alpha1"}
	v1.AddToGroupVersion(scheme.Scheme, gv)
	scheme.Scheme.AddKnownTypes(gv,
		&LXDNodeClass{},
		&LXDNodeClassList{},
	)
}
