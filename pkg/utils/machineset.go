package utils

import (
	"context"
	ecnsv1 "easystack.com/plan/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clusterapi "sigs.k8s.io/cluster-api/api/v1beta1"
)

func ListMachineSets(ctx context.Context, client client.Client, plan *ecnsv1.Plan) (clusterapi.MachineSetList,error) {
	return clusterapi.MachineSetList{},nil
}

func CreateMachineSet(ctx context.Context, client client.Client, plan *ecnsv1.Plan,set *ecnsv1.MachineSetReconcile, index int) (error) {
	return nil
}
func CheckopenstackclusterReady()  {
	
}
