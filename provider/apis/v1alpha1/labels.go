package v1alpha1

import (
	"github.com/sliman/k3s-lxd-provider/apis"
	v1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

const (
	InstanceSizeLabelKey   = apis.Group + "/instance-size"
	InstanceFamilyLabelKey = apis.Group + "/instance-family"
	InstanceMemoryLabelKey = apis.Group + "/instance-memory"
	InstanceCPULabelKey    = apis.Group + "/instance-cpu"

	NodeViewerLabelKey = "eks-node-viewer/instance-price"
	LXDNodeLabelKey    = "k3s-lxd.x-k8s.io/node"
	LXDNodeLabelValue  = "real"
)

const ProviderPrefix = "k3s-lxd://"

func init() {
	v1.RestrictedLabelDomains = v1.RestrictedLabelDomains.Insert(apis.Group)
	v1.WellKnownLabels = v1.WellKnownLabels.Insert(
		InstanceSizeLabelKey,
		InstanceFamilyLabelKey,
		InstanceCPULabelKey,
		InstanceMemoryLabelKey,
	)
}
