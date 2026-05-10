package apis

import (
	_ "embed"

	"github.com/awslabs/operatorpkg/object"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

const (
	Group = "karpenter.k3s.sh"
)

//go:embed crds/karpenter.k3s.sh_lxdnodeclasses.yaml
var LXDNodeClassCRD []byte

var CRDs = []*v1.CustomResourceDefinition{
	object.Unmarshal[v1.CustomResourceDefinition](LXDNodeClassCRD),
}
