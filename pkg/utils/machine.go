package utils

import (
	"context"
	ecnsv1 "easystack.com/plan/api/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterapi "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// MachineToInfrastructureMapFunc returns a handler.ToRequestsFunc that watches for
// Machine events and returns reconciliation requests for an infrastructure provider object.
func MachineToInfrastructureMapFunc(ctx context.Context, c client.Client) handler.MapFunc {
	return func(o client.Object) []reconcile.Request {
		machine, ok := o.(*clusterapi.Machine)
		if !ok {
			return nil
		}
		// Return early if the InfrastructureRef is nil.
		if machine.ObjectMeta.Labels[ecnsv1.MachineSetClusterLabelName] == "" {
			return nil
		}
		clusterName := machine.ObjectMeta.Labels[ecnsv1.MachineSetClusterLabelName]
		var cluster clusterapi.Cluster
		err := c.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: machine.Namespace}, &cluster)
		if err != nil {
			return nil
		}
		planName := cluster.ObjectMeta.Labels[LabelEasyStackPlan]
		if planName == "" {
			return nil
		}
		return []reconcile.Request{
			{
				NamespacedName: client.ObjectKey{
					Namespace: cluster.Namespace,
					Name:      planName,
				},
			},
		}
	}
}
