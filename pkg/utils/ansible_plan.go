package utils

import (
	"context"
	ecnsv1 "easystack.com/plan/api/v1"
	"easystack.com/plan/pkg/scope"
	"fmt"
	clusteropenstack "github.com/easystack/cluster-api-provider-openstack/api/v1alpha6"
	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

const (
	retryWaitInstanceStatus = 10 * time.Second
	timeoutInstanceReady    = 120 * time.Second
)

func WaitAnsiblePlan(ctx context.Context, scope *scope.Scope, cli client.Client, plan *ecnsv1.Plan) error {
	//TODO check new machine has created and InfrastructureRef !=nil,or give a reason to user
	err := PollImmediate(retryWaitInstanceStatus, timeoutInstanceReady, func() (bool, error) {
		var openstackMachines clusteropenstack.OpenStackMachineList
		labels := map[string]string{ecnsv1.MachineSetClusterLabelName: plan.Spec.ClusterName}
		err := cli.List(ctx, &openstackMachines, client.InNamespace(plan.Namespace), client.MatchingLabels(labels))
		if err != nil {
			return false, err
		}
		for _, oMachine := range openstackMachines.Items {
			if !oMachine.Status.Ready {
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("wait for OpenstackMachine ready timeout,cluster:%s", plan.Spec.ClusterName))
	}
	scope.Logger.Info("machine all has ready,continue task")
	return nil
}

func CreateAnsiblePlan(ctx context.Context, scope *scope.Scope, cli client.Client, plan *ecnsv1.Plan) ecnsv1.AnsiblePlan {
	var ansiblePlan ecnsv1.AnsiblePlan
	ansiblePlan.Name = plan.Name
	ansiblePlan.Namespace = plan.Namespace
	ansiblePlan.Spec.Type = ecnsv1.ExecTypeInstall
	ansiblePlan.Spec.ClusterName = plan.Spec.ClusterName
	ansiblePlan.Spec.AutoRun = plan.Spec.AnsiblePlanAuto
	ansiblePlan.Spec.SupportPython3 = plan.Spec.SupportPython3
	ansiblePlan.Spec.Done = false
	secretName := fmt.Sprintf("%s%s", plan.Name, SSHSecretSuffix)
	ansiblePlan.Spec.SSHSecret = secretName
	ansiblePlan.Spec.Version = plan.Spec.K8sVersion
	var nodePools []*ecnsv1.AnsibleNode
	for _, machine := range plan.Status.OpenstackMachineList {
		nodePools = append(nodePools, &ecnsv1.AnsibleNode{
			Name:                     machine.Name,
			AnsibleHost:              machine.Status.Addresses[0].Address,
			AnsibleIP:                machine.Status.Addresses[0].Address,
			MemoryReserve:            -4,
			AnsibleSSHPrivateKeyFile: "",
		})
	}
	ansiblePlan.Spec.Install = &ecnsv1.AnsibleInstall{}
	ansiblePlan.Spec.Install.NodePools = nodePools
	ansiblePlan.Spec.Install.OtherGroup = make(map[string][]string, 5)
	for _, machineSet := range plan.Status.InfraMachine {
		switch machineSet.Role {
		case ecnsv1.MasterSetRole:
			for name, _ := range machineSet.IPs {
				ansiblePlan.Spec.Install.KubeMaster = append(ansiblePlan.Spec.Install.KubeMaster, name)
			}
		case ecnsv1.WorkSetRole:
			for name, _ := range machineSet.IPs {
				ansiblePlan.Spec.Install.KubeNode = append(ansiblePlan.Spec.Install.KubeNode, name)
			}
		case ecnsv1.PrometheusSetRole:
			for name, _ := range machineSet.IPs {
				ansiblePlan.Spec.Install.KubePrometheus = append(ansiblePlan.Spec.Install.KubePrometheus, name)
			}
		case ecnsv1.LogSetRole:
			for name, _ := range machineSet.IPs {
				ansiblePlan.Spec.Install.KubeLog = append(ansiblePlan.Spec.Install.KubeLog, name)
			}
		case ecnsv1.IngressSetRole:
			for name, _ := range machineSet.IPs {
				ansiblePlan.Spec.Install.KubeIngress = append(ansiblePlan.Spec.Install.KubeIngress, name)
			}
		case ecnsv1.Etcd:
			for name, _ := range machineSet.IPs {
				ansiblePlan.Spec.Install.Etcd = append(ansiblePlan.Spec.Install.Etcd, name)
			}
		default:
			for name, _ := range machineSet.IPs {
				ansiblePlan.Spec.Install.OtherGroup[machineSet.Role] = append(ansiblePlan.Spec.Install.OtherGroup[machineSet.Role], name)
			}
		}
	}
	ansiblePlan.Spec.Install.OtherAnsibleOpts = make(map[string]string, 20)
	// add test/vars param
	if plan.Spec.PodCidr != "" {
		ansiblePlan.Spec.Install.OtherAnsibleOpts["kube_pods_subnet"] = plan.Spec.PodCidr
	}
	if plan.Spec.SvcCidr != "" {
		ansiblePlan.Spec.Install.OtherAnsibleOpts["kube_service_addresses"] = plan.Spec.SvcCidr
	}

	// TODO need support more cni
	if plan.Spec.CniType != "" {
		// TODO ansiblePlan.Spec.Install.OtherAnsibleOpts["kube_network_plugin"] = plan.Spec.CniType
		ansiblePlan.Spec.Install.OtherAnsibleOpts["kube_network_plugin"] = "flannel"
	}
	if plan.Spec.CniWorkMode != "" {
		// TODO ansiblePlan.Spec.Install.OtherAnsibleOpts["flannel_backend_type"] = plan.Spec.CniWorkMode
		if plan.Spec.NetMode != ecnsv1.NetWorkExist {
			ansiblePlan.Spec.Install.OtherAnsibleOpts["flannel_backend_type"] = "host-gw"
		} else {
			ansiblePlan.Spec.Install.OtherAnsibleOpts["flannel_backend_type"] = "vxlan"
		}
	}
	if plan.Spec.Monitor.PvcType != "" {
		ansiblePlan.Spec.Install.OtherAnsibleOpts["local_storageclass"] = plan.Spec.Monitor.PvcType
	}
	if plan.Spec.Monitor.PvcCap != "" {
		ansiblePlan.Spec.Install.OtherAnsibleOpts["prometheus_pv_size"] = plan.Spec.Monitor.PvcCap
	}
	if plan.Spec.Monitor.AutoClearDays != "" {
		ansiblePlan.Spec.Install.OtherAnsibleOpts["prometheus_retention_time"] = plan.Spec.Monitor.AutoClearDays
	}
	for key, value := range plan.Spec.OtherAnsibleOpts {
		ansiblePlan.Spec.Install.OtherAnsibleOpts[key] = value
	}
	return ansiblePlan

}
