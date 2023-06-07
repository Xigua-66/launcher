package utils

import (
	"context"
	ecnsv1 "easystack.com/plan/api/v1"
	"easystack.com/plan/pkg/cloudinit"
	"encoding/base64"
	errNew "errors"
	"fmt"
	clusteropenstack "github.com/easystack/cluster-api-provider-openstack/api/v1alpha6"
	"github.com/easystack/cluster-api-provider-openstack/pkg/scope"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	clusterapi "sigs.k8s.io/cluster-api/api/v1beta1"
	bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

const (
	Clusterapibootstrapapi  = "bootstrap.cluster.x-k8s.io/v1beta1"
	Clusterapibootstrapkind = "KubeadmConfigTemplate"
	Clusteropenstackapi     = "infrastructure.cluster.x-k8s.io/v1alpha6"
	Clusteropenstackkind    = "OpenStackMachineTemplate"
	retryIntervalInstanceStatus = 1 * time.Second
	timeoutInstanceCreate       = 3 * time.Second
)


func ListMachineSets(ctx context.Context, client client.Client, plan *ecnsv1.Plan) (clusterapi.MachineSetList, error) {
	return clusterapi.MachineSetList{}, nil
}

// CreateMachineSet when create machineset, we need to finish the following things:
// 1. check openstack cluster ready
// 2. get or create openstacktemplate resource with plan MachineSetReconcile first element(eg AvailabilityZone,Subnets,FloatingIPPool,Volumes)
// 3. get or create cloud init secret
// 4.create a new machineset replicas==0,deletePolicy==Newest
func CreateMachineSet(ctx context.Context, scope *scope.Scope, client client.Client, plan *ecnsv1.Plan, set *ecnsv1.MachineSetReconcile, masterGroupID string, nodeGroupID string) error {
	clusterReady, err := checkOpenstackClusterReady(ctx, client, plan)
	if err != nil {
		return err
	}
	if !clusterReady {
		return errNew.New("openstack cluster is not ready")
	}
	err = getOrCreateOpenstackTemplate(ctx, scope, client, plan, set, 0, masterGroupID, nodeGroupID)
	if err != nil {
		return err
	}
	err = getOrCreateCloudInitSecret(ctx, scope, client, plan, set)
	if err != nil {
		return err
	}
	err = createMachineset(ctx, scope, client, plan, set, 0)

	if err != nil {
		return err
	}
	return nil
}

// AddReplicas for machineset add replicas
func AddReplicas(ctx context.Context, scope *scope.Scope, cli client.Client, target *ecnsv1.MachineSetReconcile, actual clusterapi.MachineSet, plan *ecnsv1.Plan, index int, mastergroup string, nodegroup string) error {
	err := getOrCreateOpenstackTemplate(ctx, scope, cli, plan, target, index, mastergroup, nodegroup)
	if err != nil {
		return err
	}
	err = getOrCreateCloudInitSecret(ctx, scope, cli, plan, target)
	if err != nil {
		return err
	}

	if index > len(target.Infra) {
		scope.Logger.Error(fmt.Errorf("index out of range infra"), "check plan machinesetreconcile infra")
		return errNew.New("index out of range infra")
	}
	infra := target.Infra[index]
	// merge patch machineSet config
	actual.Spec.Template.Spec.FailureDomain = &infra.AvailabilityZone
	actual.Spec.Template.Spec.InfrastructureRef.APIVersion = Clusteropenstackapi
	actual.Spec.Template.Spec.InfrastructureRef.Kind = Clusteropenstackkind
	actual.Spec.Template.Spec.InfrastructureRef.Name = fmt.Sprintf("%s%s%d", plan.Spec.ClusterName, target.Role, index)
	err = cli.Update(ctx, &actual)
	if err != nil {
		return err
	}
	//TODO check new machine has created and InfrastructureRef !=nil,or give a reason to user
	err = PollImmediate(retryIntervalInstanceStatus, timeoutInstanceCreate, func() (bool, error) {
		var m *clusterapi.MachineSet
		err := cli.Get(ctx,types.NamespacedName{
			Namespace: actual.Namespace,
			Name:      actual.Name,
		},m)
		if err != nil {
			return false, err
		}

		switch m.Status.FullyLabeledReplicas {
		case  *actual.Spec.Replicas:
			return true, nil
		default:
			return false, nil
		}
	})
	if err != nil {
		return fmt.Errorf("check replicas is ready error:%v in get replicas %d",err,actual.Spec.Replicas)
	}
	return nil
}



// TODO check openstack cluster ready,one openstack cluster for one plan
func checkOpenstackClusterReady(ctx context.Context, client client.Client, plan *ecnsv1.Plan) (bool, error) {
	var cluster clusteropenstack.OpenStackCluster
	err := client.Get(ctx, types.NamespacedName{
		Namespace: plan.Namespace,
		Name:      plan.Spec.ClusterName,
	}, &cluster)
	if err != nil {
		return false, err
	}
	if cluster.Status.Ready {
		return true, nil
	}
	return false, nil
}

func CheckOpenstackClusterReady(ctx context.Context, client client.Client, plan *ecnsv1.Plan) (bool, error) {
	return checkOpenstackClusterReady(ctx, client, plan)
}

// TODO get or create openstacktemplate resource,n openstacktemplate for one machineset
func getOrCreateOpenstackTemplate(ctx context.Context, scope *scope.Scope, client client.Client, plan *ecnsv1.Plan, set *ecnsv1.MachineSetReconcile, index int, masterGroup string, nodeGroup string) error {
	if index > len(set.Infra) {
		scope.Logger.Error(fmt.Errorf("index out of range infra"), "check plan machinesetreconcile infra")
		return errNew.New("index out of range infra")
	}
	infra := set.Infra[index]
	// get openstacktemplate by name ,if not exist,create it
	var openstackTemplate clusteropenstack.OpenStackMachineTemplate
	// get openstacktemplate by filiter from cache
	err := client.Get(ctx, types.NamespacedName{
		Namespace: plan.Namespace,
		Name:      fmt.Sprintf("%s%s%d", plan.Spec.ClusterName, set.Role, index),
	}, &openstackTemplate)
	if err != nil {
		if errors.IsNotFound(err) {
			// create openstacktemplate
			openstackTemplate.Name = fmt.Sprintf("%s%s%d", plan.Spec.ClusterName, set.Role, index)
			openstackTemplate.Namespace = plan.Namespace
			openstackTemplate.Spec.Template.Spec.Flavor = set.Flavor
			openstackTemplate.Spec.Template.Spec.Image = set.Image
			openstackTemplate.Spec.Template.Spec.SSHKeyName = plan.Spec.SshKey
			openstackTemplate.Spec.Template.Spec.CloudName = plan.Spec.ClusterName
			openstackTemplate.Spec.Template.Spec.IdentityRef.Kind = "Secret"
			openstackTemplate.Spec.Template.Spec.IdentityRef.Name = plan.Spec.ClusterName
			for _, volume := range infra.Volumes {
				if volume.Index == 1 {
					openstackTemplate.Spec.Template.Spec.RootVolume.VolumeType = volume.VolumeType
					openstackTemplate.Spec.Template.Spec.RootVolume.Size = volume.VolumeSize
				} else {
					openstackTemplate.Spec.Template.Spec.CustomeVolumes = append(openstackTemplate.Spec.Template.Spec.CustomeVolumes, &clusteropenstack.RootVolume{
						Size:       volume.VolumeSize,
						VolumeType: volume.VolumeType,
					})
				}
			}
			if plan.Spec.NetMode == "existed" {
				if infra.Subnets != nil {
					if infra.Subnets.SubnetUUID == "" {
						err = errors.NewBadRequest("subnet uuid is empty")
						scope.Logger.Error(err, "please check your plan machineSetReconcile infra subnets uuid")
						return err
					} else {
						openstackTemplate.Spec.Template.Spec.Ports = append(openstackTemplate.Spec.Template.Spec.Ports, clusteropenstack.PortOpts{
							Network: &clusteropenstack.NetworkFilter{
								ID: infra.Subnets.SubnetNetwork,
							},
							FixedIPs: []clusteropenstack.FixedIP{
								{
									Subnet: &clusteropenstack.SubnetFilter{
										ID: infra.Subnets.SubnetUUID,
									},
									IPAddress: infra.Subnets.FixIP,
								},
							},
						})
					}

				} else {
					err = errors.NewBadRequest("subnet  is empty")
					scope.Logger.Error(err, "please check your plan machineSetReconcile infra subnets")
					return err

				}
			} else {
				// dont config subnet
			}

			if set.Role == "mas" {
				openstackTemplate.Spec.Template.Spec.ServerGroupID = masterGroup
			} else {
				openstackTemplate.Spec.Template.Spec.ServerGroupID = nodeGroup
			}
			//TODO create openstacktemplate resource
			err = client.Create(ctx, &openstackTemplate)
			if err != nil {
				return err
			}
			return nil
		}
		return err

	}
	return nil
}

// TODO get or create cloud init secret,one cloud init secret for one machineset
func getOrCreateCloudInitSecret(ctx context.Context, scope *scope.Scope, client client.Client, plan *ecnsv1.Plan, set *ecnsv1.MachineSetReconcile) error {
	// get cloud init secret by name ,if not exist,create it
	var cloudInitSecret corev1.Secret
	var base64 base64.Encoding
	err := client.Get(ctx, types.NamespacedName{
		Namespace: plan.Namespace,
		Name:      fmt.Sprintf("%s_cloudinit", plan.Spec.ClusterName),
	}, &cloudInitSecret)
	if err != nil {
		if errors.IsNotFound(err) {
			// create cloud init secret
			cloudInitSecret.Name = fmt.Sprintf("%s_%s_cloudinit", plan.Spec.ClusterName, set.Name)
			cloudInitSecret.Namespace = plan.Namespace
			cloudInitSecret.Data = make(map[string][]byte)
			// TODO add cloud init
			// 1. add ssh key
			// 2. add set cloud init
			//  get sshkey by name
			var sshKey corev1.Secret
			err = client.Get(ctx, types.NamespacedName{
				Namespace: plan.Namespace,
				Name:      plan.Name + "-default-ssh",
			}, &sshKey)

			var eksInput cloudinit.EKSInput
			eksInput.WriteFiles = append(eksInput.WriteFiles, bootstrapv1.File{
				Path:        "/root/.ssh/authorized_keys",
				Owner:       "root:root",
				Permissions: "0600",
				Encoding:    bootstrapv1.Base64,
				Append:      true,
				Content:     base64.EncodeToString(sshKey.Data["public_key"]),
			})
			cloudInitData, err := cloudinit.NewEKS(&eksInput)
			if err != nil {
				return err
			}

			if set.CloudInit != "" {
				//base64 old cloud init
				configOld, err := base64.DecodeString(string(cloudInitData))
				if err != nil {
					scope.Logger.Error(err, "sshkey cloud init base64 decode error")
					return err
				}
				//base64 set cloud init
				configAppend, err := base64.DecodeString(set.CloudInit)
				if err != nil {
					scope.Logger.Error(err, "set cloud init base64 decode error")
					return err
				}
				confignew := fmt.Sprintf("%s\n%s ", configOld, configAppend)
				//Encode new cloud init
				cloudInitData = []byte(base64.EncodeToString([]byte(confignew)))
			}
			cloudInitSecret.Data["format"] = []byte(base64.EncodeToString([]byte("cloud-config")))
			cloudInitSecret.Data["value"] = cloudInitData
			//TODO create cloud init secret resource
			err = client.Create(ctx, &cloudInitSecret)
			if err != nil {
				return err
			}
			return nil

		}
		return err
	}
	return nil
}

// TODO create a new machineset replicas==0,deletePolicy==Newest,one machineset for one plan.spec.machinesetsReconcile
func createMachineset(ctx context.Context, scope *scope.Scope, client client.Client, plan *ecnsv1.Plan, set *ecnsv1.MachineSetReconcile, index int) error {
	infra := set.Infra[index]
	var machineSet clusterapi.MachineSet
	machineSet.Name = fmt.Sprintf("%s%s", plan.Spec.ClusterName, set.Role)
	machineSet.Namespace = plan.Namespace
	machineSet.Spec.ClusterName = plan.Spec.ClusterName
	var re int32 = 0
	machineSet.Spec.Replicas = &re
	machineSet.Spec.DeletePolicy = "Newest"
	machineSet.Spec.Selector.MatchLabels = make(map[string]string)
	machineSet.Spec.Selector.MatchLabels["cluster.x-k8s.io/cluster-name"] = plan.Spec.ClusterName
	if plan.Spec.UseFloatIP == true {
		machineSet.Spec.Template.ObjectMeta.Annotations = make(map[string]string)
		machineSet.Spec.Template.ObjectMeta.Annotations["machinedeployment.clusters.x-k8s.io/fip"] = "enable"
	}
	machineSet.Spec.Template.Labels = make(map[string]string)
	machineSet.Spec.Template.Labels["cluster.x-k8s.io/cluster-name"] = plan.Spec.ClusterName
	machineSet.Spec.Template.Spec.Bootstrap.ConfigRef.APIVersion = Clusterapibootstrapapi
	machineSet.Spec.Template.Spec.Bootstrap.ConfigRef.Kind = Clusterapibootstrapkind
	machineSet.Spec.Template.Spec.Bootstrap.ConfigRef.Name = plan.Spec.ClusterName
	cloud_secret_name := fmt.Sprintf("%s_%s_cloudinit", plan.Spec.ClusterName, set.Name)
	machineSet.Spec.Template.Spec.Bootstrap.DataSecretName = &cloud_secret_name
	machineSet.Spec.Template.Spec.ClusterName = plan.Spec.ClusterName
	machineSet.Spec.Template.Spec.FailureDomain = &infra.AvailabilityZone
	machineSet.Spec.Template.Spec.InfrastructureRef.APIVersion = Clusteropenstackapi
	machineSet.Spec.Template.Spec.InfrastructureRef.Kind = Clusteropenstackkind
	machineSet.Spec.Template.Spec.InfrastructureRef.Name = fmt.Sprintf("%s%s%d", plan.Spec.ClusterName, set.Role, index)
	machineSet.Spec.Template.Spec.Version = &plan.Spec.K8sVersion
	err := client.Create(ctx, &machineSet)
	if err != nil {
		return err
	}
	return nil
}
