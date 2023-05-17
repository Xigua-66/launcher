package utils

import (
	"context"
	ecnsv1 "easystack.com/plan/api/v1"
	clusterapi "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ListMachineSets(ctx context.Context, client client.Client, plan *ecnsv1.Plan) (clusterapi.MachineSetList, error) {
	return clusterapi.MachineSetList{}, nil
}

// CreateMachineSet when create machineset, we need to finish the following things:
// 1. check openstack cluster ready
// 2. get or create openstacktemplate resource with plan MachineSetReconcile first element(eg AvailabilityZone,Subnets,FloatingIPPool,Volumes)
// 3. get or create cloud init secret
// 4.create a new machineset replicas==0,deletePolicy==Newest
func CreateMachineSet(ctx context.Context, client client.Client, plan *ecnsv1.Plan, set *ecnsv1.MachineSetReconcile) error {

	return nil
}

// TODO check openstack cluster ready,one openstack cluster for one plan
func checkOpenstackClusterReady(ctx context.Context, client client.Client, plan *ecnsv1.Plan) (bool, error) {
	return false, nil
}

// TODO get or create openstacktemplate resource,n openstacktemplate for one machineset
func getOrCreateOpenstackTemplate(ctx context.Context, client client.Client, plan *ecnsv1.Plan, index int) error {
	return nil
}

// TODO get or create cloud init secret,one cloud init secret for one machineset
func getOrCreateCloudInitSecret(ctx context.Context, client client.Client, plan *ecnsv1.Plan) error {
	return nil
}

// TODO create a new machineset replicas==0,deletePolicy==Newest,one machineset for one plan.spec.machinesetsReconcile
func createMachineset(ctx context.Context, client client.Client, plan *ecnsv1.Plan) error {
	return nil
}
