package main

import (
	"os"

	"sigs.k8s.io/controller-runtime/pkg/log"

	k3slxd "github.com/sliman/k3s-lxd-provider/cloudprovider"
	nodeclassctl "github.com/sliman/k3s-lxd-provider/controllers/nodeclass"
	lxdclient "github.com/sliman/k3s-lxd-provider/lxd"
	"sigs.k8s.io/karpenter/pkg/cloudprovider/overlay"
	"sigs.k8s.io/karpenter/pkg/controllers"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/operator"
)

func main() {
	ctx, op := operator.NewOperator()

	instanceTypes, err := k3slxd.ConstructInstanceTypes(ctx)
	if err != nil {
		log.FromContext(ctx).Error(err, "failed constructing instance types")
	}

	lxdSocket := getEnvOrDefault("LXD_SOCKET", "/var/snap/lxd/common/lxd/unix.socket")
	k3sServerURL := getEnvOrDefault("K3S_SERVER_URL", "https://192.168.1.59:6443")
	k3sToken := getEnvOrDefault("K3S_TOKEN", "")

	lxd := lxdclient.NewClient(lxdSocket)

	overlayUndecoratedCloudProvider := k3slxd.NewCloudProvider(ctx, op.GetClient(), instanceTypes, lxd, k3sServerURL, k3sToken)
	cloudProvider := overlay.Decorate(overlayUndecoratedCloudProvider, op.GetClient(), op.InstanceTypeStore)
	clusterState := state.NewCluster(op.Clock, op.GetClient(), cloudProvider)

	op.
		WithControllers(ctx,
			nodeclassctl.NewController(op.GetClient()),
		).WithControllers(ctx, controllers.NewControllers(
			ctx,
			op.Manager,
			op.Clock,
			op.GetClient(),
			op.EventRecorder,
			cloudProvider,
			overlayUndecoratedCloudProvider,
			clusterState,
			op.InstanceTypeStore,
		)...).Start(ctx)
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
