package nodeclass

import (
	"context"

	"github.com/awslabs/operatorpkg/status"
	"k8s.io/apimachinery/pkg/api/equality"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/sliman/k3s-lxd-provider/apis/v1alpha1"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
)

type Controller struct {
	kubeClient client.Client
}

func NewController(kubeClient client.Client) *Controller {
	return &Controller{
		kubeClient: kubeClient,
	}
}

func (c *Controller) Name() string {
	return "lxdnodeclass"
}

func (c *Controller) Reconcile(ctx context.Context, nodeClass *v1alpha1.LXDNodeClass) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, c.Name())
	stored := nodeClass.DeepCopy()

	if nodeClass.Spec.Image == "" {
		nodeClass.StatusConditions().SetFalse(status.ConditionReady, "ImageNotSet", "Image is not set in the spec")
	} else {
		nodeClass.StatusConditions().SetTrue(status.ConditionReady)
	}

	if !equality.Semantic.DeepEqual(stored, nodeClass) {
		if err := c.kubeClient.Status().Update(ctx, nodeClass); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (c *Controller) Register(ctx context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named(c.Name()).
		For(&v1alpha1.LXDNodeClass{}, builder.WithPredicates()).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}
