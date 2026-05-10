package k3slxd

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/awslabs/operatorpkg/status"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/sliman/k3s-lxd-provider/apis/v1alpha1"
	lxdclient "github.com/sliman/k3s-lxd-provider/lxd"
	v1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"
)

func NewCloudProvider(ctx context.Context, kubeClient client.Client, instanceTypes []*cloudprovider.InstanceType, lxd *lxdclient.Client, k3sServerURL, k3sToken string) *CloudProvider {
	return &CloudProvider{
		kubeClient:    kubeClient,
		instanceTypes: instanceTypes,
		lxd:           lxd,
		k3sServerURL:  k3sServerURL,
		k3sToken:      k3sToken,
	}
}

type CloudProvider struct {
	kubeClient    client.Client
	instanceTypes []*cloudprovider.InstanceType
	lxd           *lxdclient.Client
	k3sServerURL  string
	k3sToken      string
}

func (c *CloudProvider) Create(ctx context.Context, nodeClaim *v1.NodeClaim) (*v1.NodeClaim, error) {
	nodeClass, err := c.resolveNodeClassFromNodeClaim(ctx, nodeClaim)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, cloudprovider.NewInsufficientCapacityError(fmt.Errorf("resolving node class from nodeclaim, %w", err))
		}
		return nil, fmt.Errorf("resolving node class from nodeclaim, %w", err)
	}
	if status := nodeClass.StatusConditions().Get(status.ConditionReady); status.IsFalse() {
		return nil, cloudprovider.NewNodeClassNotReadyError(fmt.Errorf(status.Message))
	}

	cpu := nodeClass.Spec.DefaultCPU
	if cpu == "" {
		cpu = "2"
	}
	memory := nodeClass.Spec.DefaultMemory
	if memory == "" {
		memory = "4Gi"
	}
	image := nodeClass.Spec.Image
	if image == "" {
		image = "ubuntu:24.04"
	}

	instanceType, err := c.selectInstanceType(nodeClaim)
	if err != nil {
		return nil, cloudprovider.NewInsufficientCapacityError(fmt.Errorf("selecting instance type, %w", err))
	}
	if instanceType != nil {
		if cpuStr, ok := instanceType.Capacity[corev1.ResourceCPU]; ok {
			cpu = cpuStr.String()
		}
		if memStr, ok := instanceType.Capacity[corev1.ResourceMemory]; ok {
			memory = memStr.String()
		}
	}

	memory = toLXDMemory(memory)

	containerName := fmt.Sprintf("k3s-lxd-%s-%d", nodeClaim.Name, rand.Uint32())
	providerID := v1alpha1.ProviderPrefix + containerName

	log.FromContext(ctx).Info("creating LXD container", "name", containerName, "cpu", cpu, "memory", memory, "image", image)

	if err := c.lxd.Launch(ctx, containerName, cpu, memory, image); err != nil {
		return nil, fmt.Errorf("launching LXD container: %w", err)
	}

	registrationDelay := nodeClass.Spec.NodeRegistrationDelay.Duration
	if registrationDelay == 0 {
		registrationDelay = 5 * time.Second
	}

	go func() {
		time.Sleep(registrationDelay)

		if err := retry.OnError(retry.DefaultBackoff, func(err error) bool { return true }, func() error {
			return c.bootstrapContainer(context.Background(), containerName, providerID)
		}); err != nil {
			log.FromContext(ctx).Error(err, "failed bootstrapping container", "container", containerName)
		}
	}()

	nc, err := c.toNodeClaim(containerName, providerID, instanceType)
	if err != nil {
		return nil, err
	}
	for _, req := range nodeClaim.Spec.Requirements {
		if req.Operator == corev1.NodeSelectorOpIn && len(req.Values) == 1 {
			nc.Labels[req.Key] = req.Values[0]
		}
	}
	if nc.Labels[corev1.LabelInstanceTypeStable] == "" && instanceType != nil {
		nc.Labels[corev1.LabelInstanceTypeStable] = instanceType.Name
	}
	return nc, nil
}

func (c *CloudProvider) bootstrapContainer(ctx context.Context, containerName, providerID string) error {
	steps := []struct {
		name string
		cmd  string
	}{
		{"fix /dev/kmsg", "ln -sf /dev/null /dev/kmsg"},
		{"create k3s config dir", "mkdir -p /etc/rancher/k3s /etc/systemd/system/k3s-agent.service.d"},
		{"write config.yaml", fmt.Sprintf(`cat > /etc/rancher/k3s/config.yaml << 'K3SEOF'
kubelet-arg:
  - "provider-id=%s"
snapshotter: native
K3SEOF`, providerID)},
		{"install k3s-agent", fmt.Sprintf(
			"curl -sfL https://get.k3s.io | K3S_URL=%s K3S_TOKEN=%s sh -",
			c.k3sServerURL, c.k3sToken)},
		{"write systemd override", `cat > /etc/systemd/system/k3s-agent.service.d/override.conf << 'OVERRIDE'
[Service]
ExecStartPre=
ExecStartPre=/bin/true
OVERRIDE`},
		{"restart k3s-agent", "systemctl daemon-reload && systemctl restart k3s-agent"},
	}

	for _, step := range steps {
		if err := c.lxd.Exec(ctx, containerName, step.cmd); err != nil {
			return fmt.Errorf("step %q: %w", step.name, err)
		}
	}
	return nil
}

func (c *CloudProvider) resolveNodeClassFromNodeClaim(ctx context.Context, nodeClaim *v1.NodeClaim) (*v1alpha1.LXDNodeClass, error) {
	nodeClass := &v1alpha1.LXDNodeClass{}
	if err := c.kubeClient.Get(ctx, types.NamespacedName{Name: nodeClaim.Spec.NodeClassRef.Name}, nodeClass); err != nil {
		return nil, err
	}
	return nodeClass, nil
}

func (c *CloudProvider) selectInstanceType(nodeClaim *v1.NodeClaim) (*cloudprovider.InstanceType, error) {
	requirements := scheduling.NewNodeSelectorRequirementsWithMinValues(nodeClaim.Spec.Requirements...)

	var best *cloudprovider.InstanceType
	var bestPrice float64

	for _, it := range c.instanceTypes {
		available := it.Offerings.Available().Compatible(requirements)
		if len(available) == 0 {
			continue
		}
		price := lo.Min(lo.Map(available, func(o *cloudprovider.Offering, _ int) float64 { return o.Price }))
		if best == nil || price < bestPrice {
			best = it
			bestPrice = price
		}
	}
	return best, nil
}

func (c *CloudProvider) Delete(ctx context.Context, nodeClaim *v1.NodeClaim) error {
	containerName := strings.TrimPrefix(nodeClaim.Status.ProviderID, v1alpha1.ProviderPrefix)
	if containerName == "" {
		containerName = nodeClaim.Name
	}

	if err := c.lxd.Stop(ctx, containerName); err != nil {
		log.FromContext(ctx).Error(err, "failed stopping LXD container", "container", containerName)
	}
	if err := c.lxd.Delete(ctx, containerName); err != nil {
		return fmt.Errorf("deleting LXD container: %w", err)
	}
	if err := c.kubeClient.Delete(ctx, nodeClaim); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("deleting nodeclaim: %w", err)
	}
	return cloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("instance terminated"))
}

func (c *CloudProvider) Get(ctx context.Context, providerID string) (*v1.NodeClaim, error) {
	nodeName := strings.TrimPrefix(providerID, v1alpha1.ProviderPrefix)
	node := &corev1.Node{}
	if err := c.kubeClient.Get(ctx, types.NamespacedName{Name: nodeName}, node); err != nil {
		if errors.IsNotFound(err) {
			return nil, cloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("node not found"))
		}
		return nil, fmt.Errorf("getting node: %w", err)
	}
	if node.DeletionTimestamp != nil {
		return nil, cloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("node deleted"))
	}
	return c.toNodeClaimFromNode(node)
}

func (c *CloudProvider) List(ctx context.Context) ([]*v1.NodeClaim, error) {
	nodeList := &corev1.NodeList{}
	if err := c.kubeClient.List(ctx, nodeList); err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}
	var nodeClaims []*v1.NodeClaim
	for i, node := range nodeList.Items {
		if !strings.HasPrefix(node.Spec.ProviderID, v1alpha1.ProviderPrefix) {
			continue
		}
		nc, err := c.toNodeClaimFromNode(&nodeList.Items[i])
		if err != nil {
			return nil, fmt.Errorf("converting node to nodeclaim: %w", err)
		}
		nodeClaims = append(nodeClaims, nc)
	}
	return nodeClaims, nil
}

func (c *CloudProvider) GetInstanceTypes(ctx context.Context, _ *v1.NodePool) ([]*cloudprovider.InstanceType, error) {
	return c.instanceTypes, nil
}

func (c *CloudProvider) IsDrifted(ctx context.Context, _ *v1.NodeClaim) (cloudprovider.DriftReason, error) {
	return "", nil
}

func (c *CloudProvider) Name() string {
	return "k3s-lxd"
}

func (c *CloudProvider) GetSupportedNodeClasses() []status.Object {
	return []status.Object{&v1alpha1.LXDNodeClass{}}
}

func (c *CloudProvider) RepairPolicies() []cloudprovider.RepairPolicy {
	return []cloudprovider.RepairPolicy{
		{
			ConditionType:      corev1.NodeReady,
			ConditionStatus:    corev1.ConditionFalse,
			TolerationDuration: 10 * time.Minute,
		},
		{
			ConditionType:      corev1.NodeReady,
			ConditionStatus:    corev1.ConditionUnknown,
			TolerationDuration: 10 * time.Minute,
		},
	}
}

func (c *CloudProvider) toNodeClaim(containerName, providerID string, instanceType *cloudprovider.InstanceType) (*v1.NodeClaim, error) {
	nc := &v1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: containerName,
			Labels: map[string]string{
				v1alpha1.LXDNodeLabelKey: v1alpha1.LXDNodeLabelValue,
			},
		},
		Spec: v1.NodeClaimSpec{},
		Status: v1.NodeClaimStatus{
			NodeName:   containerName,
			ProviderID: providerID,
		},
	}
	if instanceType != nil {
		nc.Status.Capacity = instanceType.Capacity
		nc.Status.Allocatable = lo.Assign(instanceType.Capacity, lo.Assign(instanceType.Allocatable()))
	}
	return nc, nil
}

func (c *CloudProvider) toNodeClaimFromNode(node *corev1.Node) (*v1.NodeClaim, error) {
	return &v1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        node.Name,
			Labels:      node.Labels,
			Annotations: node.Annotations,
		},
		Status: v1.NodeClaimStatus{
			NodeName:    node.Name,
			ProviderID:  node.Spec.ProviderID,
			Capacity:    node.Status.Capacity,
			Allocatable: node.Status.Allocatable,
		},
	}, nil
}
