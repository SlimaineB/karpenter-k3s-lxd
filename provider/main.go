package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"

	k3slxd "github.com/sliman/k3s-lxd-provider/cloudprovider"
	nodeclassctl "github.com/sliman/k3s-lxd-provider/controllers/nodeclass"
	lxdclient "github.com/sliman/k3s-lxd-provider/lxd"
	"sigs.k8s.io/karpenter/pkg/cloudprovider/overlay"
	"sigs.k8s.io/karpenter/pkg/controllers"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	"sigs.k8s.io/karpenter/pkg/operator"
)

const lxdBridge = "lxdbr0"
const lxdCIDR = "10.103.76.0/24"

func main() {
	ctx, op := operator.NewOperator()

	logger := log.FromContext(ctx)

	setupIPTables(logger)

	instanceTypes, err := k3slxd.ConstructInstanceTypes(ctx)
	if err != nil {
		logger.Error(err, "failed constructing instance types")
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

func setupIPTables(logger logr.Logger) {
	rules := []struct {
		desc string
		args []string
	}{
		{"allow forward from lxdbr0", []string{"-I", "FORWARD", "-i", lxdBridge, "-j", "ACCEPT"}},
		{"allow forward to lxdbr0", []string{"-I", "FORWARD", "-o", lxdBridge, "-j", "ACCEPT"}},
	}

	for _, r := range rules {
		checkArgs := append([]string{"-C"}, r.args[1:]...)
		if err := exec.Command("iptables", checkArgs...).Run(); err == nil {
			continue
		}
		if err := exec.Command("iptables", r.args...).Run(); err != nil {
			logger.Info(fmt.Sprintf("iptables %s: %v (non-fatal)", r.desc, err))
		} else {
			logger.Info(fmt.Sprintf("iptables %s: OK", r.desc))
		}
	}

	natRule := []string{"-t", "nat", "-C", "POSTROUTING", "-s", lxdCIDR, "!", "-o", lxdBridge, "-j", "MASQUERADE"}
	if err := exec.Command("iptables", natRule...).Run(); err != nil {
		addRule := []string{"-t", "nat", "-A", "POSTROUTING", "-s", lxdCIDR, "!", "-o", lxdBridge, "-j", "MASQUERADE"}
		if err := exec.Command("iptables", addRule...).Run(); err != nil {
			logger.Info(fmt.Sprintf("iptables NAT rule: %v (non-fatal)", err))
		} else {
			logger.Info("iptables NAT masquerade for lxdbr0: OK")
		}
	} else {
		logger.Info("iptables NAT masquerade for lxdbr0: already present")
	}

	if err := exec.Command("sh", "-c", "echo 1 > /proc/sys/net/ipv4/ip_forward 2>/dev/null || sysctl -w net.ipv4.ip_forward=1").Run(); err != nil {
		logger.Info(fmt.Sprintf("enable ip_forward: %v (non-fatal)", err))
	} else {
		logger.Info("ip_forward enabled: OK")
	}
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
