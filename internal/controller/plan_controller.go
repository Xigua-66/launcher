/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"text/template"
	"time"

	ecnsv1 "easystack.com/plan/api/v1"
	"easystack.com/plan/pkg/cloud/service/loadbalancer"
	"easystack.com/plan/pkg/cloud/service/networking"
	"easystack.com/plan/pkg/cloud/service/provider"
	"easystack.com/plan/pkg/cloudinit"
	"easystack.com/plan/pkg/scope"
	"easystack.com/plan/pkg/utils"
	"encoding/base64"
	clusteropenstackapis "github.com/easystack/cluster-api-provider-openstack/api/v1alpha6"
	clusteropenstackerrors "github.com/easystack/cluster-api-provider-openstack/pkg/utils/errors"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/servergroups"
	"github.com/gophercloud/gophercloud/pagination"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	clusterapi "sigs.k8s.io/cluster-api/api/v1beta1"
	bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
	clusterkubeadm "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
	clusterutils "sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	waitForClusterInfrastructureReadyDuration = 30 * time.Second
	waitForInstanceBecomeActiveToReconcile    = 60 * time.Second
)
const ProjectAdminEtcSuffix = "admin-etc"
const Authtmpl = `clouds:
  {{.ClusterName}}:
    identity_api_version: 3
    auth:
      auth_url: {{.AuthUrl}}
      application_credential_id: {{.AppCredID}}
      application_credential_secret: {{.AppCredSecret}}
    region_name: {{.Region}}
`

type AuthConfig struct {
	// ClusterName is the name of cluster
	ClusterName string
	// AuthUrl is the auth url of keystone
	AuthUrl string
	// AppCredID is the application credential id
	AppCredID string
	// AppCredSecret is the application credential secret
	AppCredSecret string
	// Region is the region of keystone
	Region string `json:"region"`
}

// PlanReconciler reconciles a Plan object
type PlanReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	EventRecorder record.EventRecorder
}

type MachineSetBind struct {
	ApiSet  *clusterapi.MachineSet      `json:"api_set"`
	PlanSet *ecnsv1.MachineSetReconcile `json:"plan_set"`
}

type PlanMachineSetBind struct {
	Plan *ecnsv1.Plan     `json:"plan"`
	Bind []MachineSetBind `json:"bind"`
}

//+kubebuilder:rbac:groups=ecns.easystack.com,resources=plans,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ecns.easystack.com,resources=plans/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ecns.easystack.com,resources=plans/finalizers,verbs=update
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;clusters/status,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=openstackclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=openstackclusters/status,verbs=get
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinesets;machinesets/status,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;machines/status,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=openstackmachinetemplates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets;,verbs=get;create;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Plan object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.4/pkg/reconcile
func (r *PlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	var (
		deletion = false
		log      = log.FromContext(ctx)
	)

	// Fetch the OpenStackMachine instance.

	plan := &ecnsv1.Plan{}
	err := r.Client.Get(ctx, req.NamespacedName, plan)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	log = log.WithValues("plan", plan.Name)

	if plan.Spec.Paused == true {
		cluster, err1 := clusterutils.GetClusterByName(ctx, r.Client, plan.Spec.ClusterName, plan.Namespace)
		if err1 == nil {
			log = log.WithValues("cluster", cluster.Name)
			if cluster == nil {
				log.Info("Cluster Controller has not yet set OwnerRef")
				return reconcile.Result{}, nil
			}
			// set cluster.Spec.Paused = true
			// first get the clusterv1.Cluster, then set cluster.Spec.Paused = true
			// then update the cluster
			// Fetch the Cluster.
			if cluster.Spec.Paused == true {
				log.Info("Cluster is already paused")
				return ctrl.Result{}, nil
			} else {
				cluster.Spec.Paused = true
				if err1 = r.Client.Update(ctx, cluster); err1 != nil {
					return ctrl.Result{}, err1
				}

			}
		}
		return ctrl.Result{}, nil
	} else {
		cluster, err1 := clusterutils.GetClusterByName(ctx, r.Client, plan.Spec.ClusterName, plan.Namespace)
		if err1 == nil {
			if cluster.Spec.Paused == true {
				cluster.Spec.Paused = false
				if err1 = r.Client.Update(ctx, cluster); err != nil {
					return ctrl.Result{}, err1
				}
			}
		}
	}
	patchHelper, err := patch.NewHelper(plan, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	osProviderClient, clientOpts, projectID, userID, err := provider.NewClientFromPlan(ctx, plan)
	if err != nil {
		return reconcile.Result{}, err
	}
	scope := &scope.Scope{
		ProviderClient:     osProviderClient,
		ProviderClientOpts: clientOpts,
		ProjectID:          projectID,
		UserID:             userID,
		Logger:             log,
	}

	if plan.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !StringInArray(ecnsv1.MachineFinalizer, plan.ObjectMeta.Finalizers) {
			plan.ObjectMeta.Finalizers = append(plan.ObjectMeta.Finalizers, ecnsv1.MachineFinalizer)
			if err := r.Update(context.Background(), plan); err != nil {
				return reconcile.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if StringInArray(ecnsv1.MachineFinalizer, plan.ObjectMeta.Finalizers) {
			// our finalizer is present, so lets handle any external dependency

			scope.Logger.Info("delete plan CR", "Namespace", plan.ObjectMeta.Namespace, "Name", plan.Name)
			err = r.deletePlanResource(ctx, scope, plan)
			if err != nil {
				r.EventRecorder.Eventf(plan, corev1.EventTypeWarning, PlanDeleteEvent, "Delete plan failed: %s", err.Error())
				return ctrl.Result{RequeueAfter: waitForInstanceBecomeActiveToReconcile}, err
			}
			r.EventRecorder.Eventf(plan, corev1.EventTypeNormal, PlanDeleteEvent, "Delete plan success")

			err = r.Client.Get(ctx, req.NamespacedName, plan)
			if err != nil {
				if apierrors.IsNotFound(err) {
					return ctrl.Result{}, nil
				}
			}

			// remove our finalizer from the list and update it.
			var found bool
			plan.ObjectMeta.Finalizers, found = RemoveString(ecnsv1.MachineFinalizer, plan.ObjectMeta.Finalizers)
			if found {
				if err := r.Update(context.Background(), plan); err != nil {
					return reconcile.Result{}, err
				}
			}
		}
		return reconcile.Result{}, err
	}

	defer func() {
		if !deletion {
			if err := patchHelper.Patch(ctx, plan); err != nil {
				if reterr == nil {
					reterr = errors.Wrapf(err, "error patching plan status %s/%s", plan.Namespace, plan.Name)
				}
			}
		}

	}()

	// Handle non-deleted clusters
	return r.reconcileNormal(ctx, scope, patchHelper, plan)
}

func (r *PlanReconciler) reconcileNormal(ctx context.Context, scope *scope.Scope, patchHelper *patch.Helper, plan *ecnsv1.Plan) (_ ctrl.Result, reterr error) {
	// get gopher cloud client
	// get or create app credential
	r.EventRecorder.Eventf(plan, corev1.EventTypeNormal, PlanStartEvent, "Start plan")
	scope.Logger.Info("Reconciling plan openstack resource")
	err := syncAppCre(ctx, scope, r.Client, plan)
	if err != nil {
		return ctrl.Result{}, err
	}
	// get or create sshkeys secret
	err = syncSSH(ctx, r.Client, plan)
	if err != nil {
		return ctrl.Result{}, err
	}

	//TODO  get or create cluster.cluster.x-k8s.io
	err = syncCreateCluster(ctx, r.Client, plan)
	if err != nil {
		return ctrl.Result{}, err
	}

	var masterM *ecnsv1.MachineSetReconcile

	for _, set := range plan.Spec.MachineSets {
		if set.Role == ecnsv1.MasterSetRole {
			masterM = set
		}
	}
	//TODO  get or create openstackcluster.infrastructure.cluster.x-k8s.io
	err = syncCreateOpenstackCluster(ctx, r.Client, plan, masterM)
	if err != nil {
		return ctrl.Result{}, err
	}

	//TODO  get or create KubeadmConfig ,no use
	err = syncCreateKubeadmConfig(ctx, r.Client, plan)
	if err != nil {
		return ctrl.Result{}, err
	}

	//TODO  get or create server groups,master one,work one
	mastergroupID, nodegroupID, err := syncServerGroups(ctx, scope, plan)
	if err != nil {
		return ctrl.Result{}, err
	}
	// create all machineset Once
	for _, set := range plan.Spec.MachineSets {
		// check machineSet is existed
		machineSetName := fmt.Sprintf("%s%s", plan.Spec.ClusterName, set.Role)
		machineSetNamespace := plan.Namespace
		machineSet := &clusterapi.MachineSet{}
		err = r.Client.Get(ctx, types.NamespacedName{Name: machineSetName, Namespace: machineSetNamespace}, machineSet)
		if err != nil {
			if apierrors.IsNotFound(err) {
				// create machineset
				scope.Logger.Info("Create machineSet", "Namespace", machineSetNamespace, "Name", machineSetName)
				err = utils.CreateMachineSet(ctx, scope, r.Client, plan, set, mastergroupID, nodegroupID)
				if err != nil {
					if err.Error() == "openstack cluster is not ready" {
						scope.Logger.Info("Wait openstack cluster ready", "Namespace", machineSetNamespace, "Name", machineSetName)
						return ctrl.Result{RequeueAfter: waitForClusterInfrastructureReadyDuration}, nil
					}
					return ctrl.Result{}, err
				}
				continue
			}
			return ctrl.Result{}, err
		}
		// skip create machineSet
		scope.Logger.Info("Skip create machineSet", "Role", set.Role, "Namespace", machineSetNamespace, "Name", machineSetName)
	}

	plan.Status.InfraMachine = make(map[string]ecnsv1.InfraMachine)
	// get or create HA port if needed
	if !plan.Spec.LBEnable {
		for _, set := range plan.Spec.MachineSets {
			if SetNeedKeepAlived(set.Role, plan.Spec.NeedKeepAlive) {
				err = syncHAPort(ctx, scope, r.Client, plan, set.Role)
				if err != nil {
					return ctrl.Result{}, err
				}
			}
		}
	} else {
		plan.Status.PlanLoadBalancer = nil
		for _, set := range plan.Spec.MachineSets {
			if SetNeedLoadBalancer(set.Role, plan.Spec.NeedLoadBalancer) {
				err = syncCreateLoadBalancer(ctx, scope, r.Client, plan, set.Role)
				if err != nil {
					return ctrl.Result{}, err
				}
			}
		}

	}
	// Reconcile every machineset replicas
	err = r.syncMachine(ctx, scope, r.Client, plan, mastergroupID, nodegroupID)
	if err != nil {
		return ctrl.Result{}, err
	}
	plan.Status.ServerGroupID = &ecnsv1.Servergroups{}
	plan.Status.ServerGroupID.MasterServerGroupID = mastergroupID
	plan.Status.ServerGroupID.WorkerServerGroupID = nodegroupID

	err = utils.WaitAnsiblePlan(ctx, scope, r.Client, plan)
	if err != nil {
		plan.Status.VMDone = false
		return ctrl.Result{}, err
	}
	plan.Status.VMDone = true
	// Update status.InfraMachine and OpenstackMachineList
	err = updatePlanStatus(ctx, scope, r.Client, plan)
	if err != nil {
		scope.Logger.Error(err, "update plan status error")
		return ctrl.Result{}, err
	}

	// update master role ports allowed-address-pairs
	if !plan.Spec.LBEnable {
		for index, set := range plan.Status.InfraMachine {
			for _, portID := range plan.Status.InfraMachine[index].PortIDs {
				var Pairs []string
				service, err := networking.NewService(scope)
				if err != nil {
					return ctrl.Result{}, err
				}
				if SetNeedKeepAlived(set.Role, plan.Spec.NeedKeepAlive) {
					Pairs = append(Pairs, plan.Status.InfraMachine[index].HAPrivateIP)
				}
				Pairs = append(Pairs, plan.Spec.PodCidr)

				err = service.UpdatePortAllowedAddressPairs(portID, Pairs)
				if err != nil {
					scope.Logger.Error(err, "Update vip port failed and add pod cidr", "ClusterName", plan.Spec.ClusterName, "Port", portID, "Pairs", Pairs)
					return ctrl.Result{}, err
				}
				scope.Logger.Info("Update vip port and add pod cidr", "ClusterName", plan.Spec.ClusterName, "Port", portID, "Pairs", Pairs)
			}
		}
	} else {
		// add lb member
		for _, set := range plan.Status.InfraMachine {
			if SetNeedLoadBalancer(set.Role, plan.Spec.NeedLoadBalancer) {
				err = syncMember(ctx, scope, r.Client, plan, &set)
				if err != nil {
					return ctrl.Result{}, err
				}
			}
		}
	}

	// TODO check all machineset replicas is ready to num,create ansible plan
	// 1.create ansiblePLan cr
	// 2.if ansiblePlan cr existed,which is different in plan to update ansiblePlan cr type and Done field.
	ansiblePlanName := fmt.Sprintf("%s", plan.Name)
	var ansiblePlan ecnsv1.AnsiblePlan
	err = r.Client.Get(ctx, types.NamespacedName{Name: ansiblePlanName, Namespace: plan.Namespace}, &ansiblePlan)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// create ansiblePlan
			scope.Logger.Info("Create ansiblePlan", "Namespace", plan.Namespace, "Name", ansiblePlanName)
			ansible := utils.CreateAnsiblePlan(ctx, scope, r.Client, plan)
			err = r.Client.Create(ctx, &ansible)
			if err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{}, err
		}
	}

	// Compare plan with ansiblePlan to update ansiblePlan
	err = syncAnsiblePlan(ctx, scope, r.Client, plan, &ansiblePlan)
	if err != nil {
		if err == TaskDoingError {
			scope.Logger.Info(TaskDoingError.Error())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func syncMember(ctx context.Context, scope *scope.Scope, cli client.Client, plan *ecnsv1.Plan, setStatus *ecnsv1.InfraMachine) error {
	if setStatus.Role == ecnsv1.MasterSetRole {
		return nil
	}
	loadBalancerService, err := loadbalancer.NewService(scope)
	if err != nil {
		return err
	}
	var openstackCluster clusteropenstackapis.OpenStackCluster
	err = cli.Get(ctx, types.NamespacedName{Name: plan.Spec.ClusterName, Namespace: plan.Namespace}, &openstackCluster)
	if err != nil {
		return err
	}
	lbName := fmt.Sprintf("%s-%s", plan.Spec.ClusterName, setStatus.Role)
	for openstackMachineName, ip := range setStatus.IPs {
		// get openstack machine
		var openstackMachine clusteropenstackapis.OpenStackMachine
		err = cli.Get(ctx, types.NamespacedName{Name: openstackMachineName, Namespace: plan.Namespace}, &openstackMachine)
		if err != nil {
			return err
		}
		// get openstack machine
		machine, err := utils.GetOwnerMachine(ctx, cli, openstackMachine.ObjectMeta)
		if err != nil {
			return err
		}
		var port []int = []int{80, 443}
		err = loadBalancerService.ReconcileLoadBalancerMember(&openstackCluster, machine, &openstackMachine, lbName, ip, port)
		if err != nil {
			return err
		}
	}
	return nil
}

func syncCreateLoadBalancer(ctx context.Context, scope *scope.Scope, cli client.Client, plan *ecnsv1.Plan, setRole string) error {
	// master lb not need create loadBalancer by plan operator
	if setRole == ecnsv1.MasterSetRole {
		return nil
	}
	loadBalancerService, err := loadbalancer.NewService(scope)
	if err != nil {
		return err
	}
	var infra clusteropenstackapis.OpenStackCluster
	//Separate ingress ports
	var port []int = []int{80, 443}
	// get openstack cluster network
	err = cli.Get(ctx, types.NamespacedName{Name: plan.Spec.ClusterName, Namespace: plan.Namespace}, &infra)
	if err != nil {
		return err
	}
	lbName := fmt.Sprintf("%s-%s", plan.Spec.ClusterName, setRole)
	err = loadBalancerService.ReconcileLoadBalancer(&infra, plan, lbName, port)
	if err != nil {
		return err
	}

	return nil
}

func syncHAPort(ctx context.Context, scope *scope.Scope, cli client.Client, plan *ecnsv1.Plan, setRole string) error {
	service, err := networking.NewService(scope)
	if err != nil {
		return err
	}
	// get openstack cluster network
	var openstackCluster clusteropenstackapis.OpenStackCluster
	err = cli.Get(ctx, types.NamespacedName{Name: plan.Spec.ClusterName, Namespace: plan.Namespace}, &openstackCluster)
	if err != nil {
		return err
	}
	net := openstackCluster.Status.Network
	portName := fmt.Sprintf("%s-%s-%s", plan.Spec.ClusterName, setRole, "keepalived_vip_eth0")
	var sg []clusteropenstackapis.SecurityGroupParam
	sg = append(sg, clusteropenstackapis.SecurityGroupParam{
		Name: "default",
	})
	securityGroups, err := service.GetSecurityGroups(sg)
	if err != nil {
		return fmt.Errorf("error getting security groups: %v", err)
	}
	keepPortTag := []string{"keepAlive", plan.Spec.ClusterName}
	var adminStateUp bool = false
	port, err := service.GetOrCreatePort(plan, plan.Spec.ClusterName, portName, *net, &securityGroups, keepPortTag, &adminStateUp)
	if err != nil {
		return err
	}
	fip, err := service.GetFloatingIPByPortID(port.ID)
	if err != nil {
		return err
	}
	var InfraStatus ecnsv1.InfraMachine
	InfraStatus.Role = setRole
	InfraStatus.HAPortID = port.ID
	InfraStatus.HAPrivateIP = port.FixedIPs[0].IPAddress
	if fip != nil {
		// port has fip
		InfraStatus.HAPublicIP = fip.FloatingIP
	} else {
		// port has no fip
		// create fip
		if plan.Spec.UseFloatIP {
			var fipAddress string
			f, err := service.GetOrCreateFloatingIP(&openstackCluster, &openstackCluster, port.ID, fipAddress)
			if err != nil {
				return err
			}
			// set status
			InfraStatus.HAPublicIP = f.FloatingIP
			err = service.AssociateFloatingIP(&openstackCluster, f, port.ID)
			if err != nil {
				return err
			}
		}
	}
	// update cluster status if setRole is master
	if setRole == ecnsv1.MasterSetRole {
		var cluster clusterapi.Cluster
		err = cli.Get(ctx, types.NamespacedName{Name: plan.Spec.ClusterName, Namespace: plan.Namespace}, &cluster)
		if err != nil {
			return err
		}
		origin := cluster.DeepCopy()
		if InfraStatus.HAPublicIP != "" {
			cluster.Spec.ControlPlaneEndpoint.Host = InfraStatus.HAPublicIP
		} else {
			cluster.Spec.ControlPlaneEndpoint.Host = InfraStatus.HAPrivateIP
		}
		cluster.Spec.ControlPlaneEndpoint.Port = 6443
		err = utils.PatchCluster(ctx, cli, origin, &cluster)
		if err != nil {
			scope.Logger.Info("Update cluster failed", "ClusterName", plan.Spec.ClusterName, "Endpoint", cluster.Spec.ControlPlaneEndpoint.Host)
			return err
		}
		scope.Logger.Info("Update cluster status", "ClusterName", plan.Spec.ClusterName, "Endpoint", cluster.Spec.ControlPlaneEndpoint.Host)
	}
	plan.Status.InfraMachine[setRole] = InfraStatus
	return nil
}

// SetNeedLoadBalancer get map existed key
func SetNeedLoadBalancer(role string, alive []string) bool {
	for _, a := range alive {
		if a == role {
			return true
		}
	}
	return false

}

func SetNeedKeepAlived(role string, alive []string) bool {
	for _, a := range alive {
		if a == role {
			return true
		}
	}
	return false

}

// TODO sync ansiblePlan
func syncAnsiblePlan(ctx context.Context, scope *scope.Scope, cli client.Client, plan *ecnsv1.Plan, ansibleOld *ecnsv1.AnsiblePlan) error {
	ansibleNew := utils.CreateAnsiblePlan(ctx, scope, cli, plan)
	ansibleOld.ObjectMeta.DeepCopyInto(&ansibleNew.ObjectMeta)
	ansibleOld.TypeMeta = ansibleNew.TypeMeta
	// 0. check if ansiblePlan can be updated
	if !ansibleOld.Status.Done {
		return TaskDoingError
	}
	// 1. check if upgrade is needed
	upgrade, err := utils.IsUpgradeNeeded(ansibleOld, &ansibleNew)
	if err != nil {
		return err
	}
	if upgrade {
		//update ansiblePlan
		ansibleNew.Spec.Type = ecnsv1.ExecTypeUpgrade
	}
	// del with remove or expansion
	DiffReporter := &utils.DiffReporter{}
	option := cmpopts.SortSlices(func(i, j *ecnsv1.AnsibleNode) bool { return i.Name < j.Name })
	ignore := cmpopts.IgnoreFields(ecnsv1.AnsibleNode{}, "MemoryReserve", "AnsibleSSHPrivateKeyFile")
	cmp.Diff(ansibleOld.Spec.Install.NodePools, ansibleNew.Spec.Install.NodePools, option, ignore, cmp.Reporter(DiffReporter))
	if DiffReporter.NodesUpdate || (DiffReporter.UpScale && DiffReporter.DownScale) {
		return errors.New("Nodes base's information has changed or need scale and remove node once,please check the instance status")
	} else if DiffReporter.UpScale {
		// set ansiblePlan type is expansion
		ansibleNew.Spec.Type = ecnsv1.ExecTypeExpansion
		// reset this scale field
		ansibleNew.Spec.Install.KubeNode = nil
		for _, node := range DiffReporter.AdditionalNodes {
			ansibleNew.Spec.Install.KubeNode = append(ansibleNew.Spec.Install.KubeNode, node.Name)
		}
		//if scale ingress up,OtherGroup need add new ingress node to update new ingress vip

		var flushIngressVirtualVip bool
		IngressLabel, scaleIngress := ansibleNew.Spec.Install.OtherAnsibleOpts["ingress_label"]
		if scaleIngress && !plan.Spec.LBEnable {
			// get ingress_virtual_vip
			for _, group := range plan.Status.InfraMachine {
				if group.Role == IngressLabel {
					ansibleNew.Spec.Install.OtherAnsibleOpts["ingress_virtual_vip"] = group.HAPrivateIP
				}
			}
			if flushIngressVirtualVip {
				return errors.New("ingress_label is set but ingress_virtual_vip no set ,please check ingress HA status")
			}
		}

	} else if DiffReporter.DownScale {
		// set ansiblePlan type is remove
		ansibleNew.Spec.Type = ecnsv1.ExecTypeRemove
		// reset this scale field
		ansibleNew.Spec.Install.KubeNode = nil
		for _, node := range DiffReporter.AdditionalNodes {
			ansibleNew.Spec.Install.KubeNode = append(ansibleNew.Spec.Install.KubeNode, node.Name)
		}
		// add remove force_delete_nodes,because machine instance has been deleted
		ansibleNew.Spec.Install.OtherAnsibleOpts["delete_nodes_confirmation"] = "yes"
		ansibleNew.Spec.Install.OtherAnsibleOpts["force_delete_nodes"] = "yes"
	}

	if upgrade || DiffReporter.UpScale || DiffReporter.DownScale {
		err = utils.PatchAnsiblePlan(ctx, cli, ansibleOld, &ansibleNew)
		if err != nil {
			return err
		}
	}

	return nil

}

// TODO update plan status
func updatePlanStatus(ctx context.Context, scope *scope.Scope, cli client.Client, plan *ecnsv1.Plan) error {
	// get all machineset
	machineSetList := &clusterapi.MachineSetList{}
	labels := map[string]string{ecnsv1.MachineSetClusterLabelName: plan.Spec.ClusterName}
	err := cli.List(ctx, machineSetList, client.InNamespace(plan.Namespace), client.MatchingLabels(labels))
	if err != nil {
		return err
	}
	plan.Status.OpenstackMachineList = nil
	for _, m := range machineSetList.Items {
		labelsOpenstackMachine := map[string]string{clusterapi.MachineSetNameLabel: m.Name}
		openstackMachineList := &clusteropenstackapis.OpenStackMachineList{}
		err = cli.List(ctx, openstackMachineList, client.InNamespace(plan.Namespace), client.MatchingLabels(labelsOpenstackMachine))
		if err != nil {
			return err
		}
		plan.Status.OpenstackMachineList = append(plan.Status.OpenstackMachineList, openstackMachineList.Items...)
		ips := make(map[string]string)
		var Ports []string
		_, role, _ := strings.Cut(m.Name, plan.Spec.ClusterName)
		for _, om := range openstackMachineList.Items {
			ips[om.Name] = om.Status.Addresses[0].Address
			// get port information from openstack machine
			service, err := networking.NewService(scope)
			if err != nil {
				return err
			}
			port, err := service.GetPortFromInstanceIP(*om.Spec.InstanceID, om.Status.Addresses[0].Address)
			if err != nil {
				return err
			}
			if len(port) != 0 {
				Ports = append(Ports, port[0].ID)
			}
		}
		// save pre status include HA information
		originPlan := plan.DeepCopy()
		plan.Status.InfraMachine[role] = ecnsv1.InfraMachine{
			Role:        role,
			PortIDs:     Ports,
			IPs:         ips,
			HAPortID:    originPlan.Status.InfraMachine[role].HAPortID,
			HAPrivateIP: originPlan.Status.InfraMachine[role].HAPrivateIP,
			HAPublicIP:  originPlan.Status.InfraMachine[role].HAPublicIP,
		}
	}

	var oc clusteropenstackapis.OpenStackCluster
	err = cli.Get(ctx, types.NamespacedName{Name: plan.Spec.ClusterName, Namespace: plan.Namespace}, &oc)
	if err != nil {
		return err
	}
	if oc.Status.Bastion == nil {
		return errors.New("bastion information is nil,please check bastion vm status and port information")
	}
	plan.Status.Bastion = oc.Status.Bastion
	return nil
}

// TODO sync app cre
func syncAppCre(ctx context.Context, scope *scope.Scope, cli client.Client, plan *ecnsv1.Plan) error {
	// TODO get openstack application credential secret by name  If not exist,then create openstack application credential and its secret.
	// create openstack application credential
	var (
		secretName = fmt.Sprintf("%s-%s", plan.Spec.ClusterName, ProjectAdminEtcSuffix)
		secret     = &corev1.Secret{}
	)

	IdentityClient, err := openstack.NewIdentityV3(scope.ProviderClient, gophercloud.EndpointOpts{
		Region: scope.ProviderClientOpts.RegionName,
	})
	if err != nil {
		return err
	}

	err = cli.Get(ctx, types.NamespacedName{Name: secretName, Namespace: plan.Namespace}, secret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			creId, appsecret, err := utils.CreateAppCre(ctx, scope, IdentityClient, secretName)
			if err != nil {
				return err
			}
			var auth AuthConfig = AuthConfig{
				ClusterName:   plan.Spec.ClusterName,
				AuthUrl:       plan.Spec.UserInfo.AuthUrl,
				AppCredID:     creId,
				AppCredSecret: appsecret,
				Region:        plan.Spec.UserInfo.Region,
			}
			var (
				secretData = make(map[string][]byte)
				creLabels  = make(map[string]string)
			)
			creLabels["creId"] = creId
			// Create a template object and parse the template string
			t, err := template.New("auth").Parse(Authtmpl)
			if err != nil {
				return err
			}
			var buf bytes.Buffer
			// Execute the template and write the output to the file
			err = t.Execute(&buf, auth)
			if err != nil {
				return err
			}
			// base64 encode the buffer contents and return as a string
			secretData["clouds.yaml"] = buf.Bytes()
			secretData["cacert"] = []byte(fmt.Sprintf("\n"))
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: plan.Namespace,
					Labels:    creLabels,
				},
				Data: secretData,
			}
			err = cli.Create(ctx, secret)
			if err != nil {
				return err
			}
			return nil

		} else {
			return err
		}
	}

	return nil
}

// TODO sync create cluster
func syncCreateCluster(ctx context.Context, client client.Client, plan *ecnsv1.Plan) error {
	// TODO get cluster by name  If not exist,then create cluster
	cluster := clusterapi.Cluster{}
	err := client.Get(ctx, types.NamespacedName{Name: plan.Spec.ClusterName, Namespace: plan.Namespace}, &cluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// TODO create cluster resource
			cluster.Labels = make(map[string]string, 1)
			cluster.Labels[utils.LabelEasyStackPlan] = plan.Name
			cluster.Name = plan.Spec.ClusterName
			cluster.Namespace = plan.Namespace
			cluster.Spec.ClusterNetwork = &clusterapi.ClusterNetwork{}
			cluster.Spec.ClusterNetwork.Pods = &clusterapi.NetworkRanges{}
			cluster.Spec.ClusterNetwork.Pods.CIDRBlocks = []string{plan.Spec.PodCidr}
			cluster.Spec.ClusterNetwork.ServiceDomain = "cluster.local"
			// if LBEnable is true,dont set the ControlPlaneEndpoint
			// else set the ControlPlaneEndpoint to keepalived VIP
			if !plan.Spec.LBEnable {
				cluster.Spec.ControlPlaneEndpoint = clusterapi.APIEndpoint{
					Host: "0.0.0.0",
					Port: 6443,
				}
			}
			cluster.Spec.InfrastructureRef = &corev1.ObjectReference{}
			cluster.Spec.InfrastructureRef.APIVersion = "infrastructure.cluster.x-k8s.io/v1alpha6"
			cluster.Spec.InfrastructureRef.Kind = "OpenStackCluster"
			cluster.Spec.InfrastructureRef.Name = plan.Spec.ClusterName
			err := client.Create(ctx, &cluster)
			if err != nil {
				return err
			}
		}
		return err
	}

	return nil
}

// Todo sync create openstackcluster
func syncCreateOpenstackCluster(ctx context.Context, client client.Client, plan *ecnsv1.Plan, MSet *ecnsv1.MachineSetReconcile) error {
	//TODO get openstackcluster by name  If not exist,then create openstackcluster
	openstackCluster := clusteropenstackapis.OpenStackCluster{}
	err := client.Get(ctx, types.NamespacedName{Name: plan.Spec.ClusterName, Namespace: plan.Namespace}, &openstackCluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// prepare bastion cloud-init userdata
			// 1. add asnible  ssh key
			var sshKey corev1.Secret
			err = client.Get(ctx, types.NamespacedName{
				Namespace: plan.Namespace,
				Name:      fmt.Sprintf("%s%s", plan.Name, utils.SSHSecretSuffix),
			}, &sshKey)
			if err != nil {
				return err
			}

			var eksInput cloudinit.EKSInput
			sshBase64 := base64.StdEncoding.EncodeToString(sshKey.Data["public_key"])
			eksInput.WriteFiles = append(eksInput.WriteFiles, bootstrapv1.File{
				Path:        "/root/.ssh/authorized_keys",
				Owner:       "root:root",
				Permissions: "0644",
				Encoding:    bootstrapv1.Base64,
				Append:      true,
				Content:     sshBase64,
			})

			eksInput.PreKubeadmCommands = append(eksInput.PreKubeadmCommands, "sed -i '/^#.*AllowTcpForwarding/s/^#//' /etc/ssh/sshd_config")
			eksInput.PreKubeadmCommands = append(eksInput.PreKubeadmCommands, "sed -i '/^AllowTcpForwarding/s/no/yes/' /etc/ssh/sshd_config")

			eksInput.PostKubeadmCommands = append(eksInput.PostKubeadmCommands, "service sshd restart")

			bastionUserData, err := cloudinit.NewEKS(&eksInput)
			if err != nil {
				return err
			}

			openstackCluster.Name = plan.Spec.ClusterName
			openstackCluster.Namespace = plan.Namespace
			if plan.Spec.LBEnable {
				openstackCluster.Spec.APIServerLoadBalancer.Enabled = true
				if isFusionArchitecture(plan.Spec.MachineSets) {
					openstackCluster.Spec.APIServerLoadBalancer.AdditionalPorts = []int{
						80,
						443,
					}
				}
			} else {
				openstackCluster.Spec.APIServerLoadBalancer.Enabled = false
				openstackCluster.Spec.ControlPlaneEndpoint.Host = "0.0.0.0"
				openstackCluster.Spec.ControlPlaneEndpoint.Port = 6443
			}
			openstackCluster.Spec.CloudName = plan.Spec.ClusterName
			openstackCluster.Spec.DNSNameservers = plan.Spec.DNSNameservers
			if plan.Spec.UseFloatIP == true {
				openstackCluster.Spec.DisableAPIServerFloatingIP = false
				openstackCluster.Spec.ExternalNetworkID = plan.Spec.ExternalNetworkId
			} else {
				openstackCluster.Spec.DisableAPIServerFloatingIP = true
				openstackCluster.Spec.ExternalNetworkID = ""
			}
			openstackCluster.Spec.ManagedSecurityGroups = true
			if plan.Spec.NetMode == ecnsv1.NetWorkNew {
				openstackCluster.Spec.NodeCIDR = plan.Spec.NodeCIDR
			} else {
				openstackCluster.Spec.NodeCIDR = ""
				//TODO openstackCluster.Spec.Network.ID should get master role plan.spec.Mach(master set only one infra)
				openstackCluster.Spec.Network.ID = MSet.Infra[0].Subnets.SubnetNetwork
				openstackCluster.Spec.Subnet.ID = MSet.Infra[0].Subnets.SubnetUUID
			}
			openstackCluster.Spec.IdentityRef = &clusteropenstackapis.OpenStackIdentityReference{}
			openstackCluster.Spec.IdentityRef.Kind = "Secret"
			secretName := fmt.Sprintf("%s-%s", plan.Spec.ClusterName, ProjectAdminEtcSuffix)
			openstackCluster.Spec.IdentityRef.Name = secretName
			openstackCluster.Spec.Bastion = &clusteropenstackapis.Bastion{}
			openstackCluster.Spec.Bastion.Enabled = true
			openstackCluster.Spec.Bastion.UserData = string(bastionUserData)
			openstackCluster.Spec.Bastion.AvailabilityZone = MSet.Infra[0].AvailabilityZone
			openstackCluster.Spec.Bastion.Instance = clusteropenstackapis.OpenStackMachineSpec{}
			openstackCluster.Spec.Bastion.Instance.Flavor = MSet.Infra[0].Flavor
			openstackCluster.Spec.Bastion.Instance.Image = MSet.Infra[0].Image
			openstackCluster.Spec.Bastion.Instance.SSHKeyName = plan.Spec.SshKey
			openstackCluster.Spec.Bastion.Instance.CloudName = plan.Spec.ClusterName
			openstackCluster.Spec.Bastion.Instance.IdentityRef = &clusteropenstackapis.OpenStackIdentityReference{}
			openstackCluster.Spec.Bastion.Instance.IdentityRef.Kind = "Secret"
			openstackCluster.Spec.Bastion.Instance.IdentityRef.Name = secretName
			openstackCluster.Spec.Bastion.Instance.RootVolume = &clusteropenstackapis.RootVolume{}
			for index, volume := range MSet.Infra[0].Volumes {
				// bastion only set rootVolume because image use masterSet image
				if volume.Index == 1 {
					openstackCluster.Spec.Bastion.Instance.RootVolume.Size = MSet.Infra[0].Volumes[index].VolumeSize
					openstackCluster.Spec.Bastion.Instance.RootVolume.VolumeType = MSet.Infra[0].Volumes[index].VolumeType
				}
			}
			if plan.Spec.NetMode == ecnsv1.NetWorkExist {
				openstackCluster.Spec.Bastion.Instance.Networks = []clusteropenstackapis.NetworkParam{
					{
						Filter: clusteropenstackapis.NetworkFilter{
							ID: MSet.Infra[0].Subnets.SubnetNetwork,
						},
					},
				}
				openstackCluster.Spec.Bastion.Instance.Ports = []clusteropenstackapis.PortOpts{}
				openstackCluster.Spec.Bastion.Instance.Ports = append(openstackCluster.Spec.Bastion.Instance.Ports, clusteropenstackapis.PortOpts{
					Network: &clusteropenstackapis.NetworkFilter{
						ID: MSet.Infra[0].Subnets.SubnetNetwork,
					},
					FixedIPs: []clusteropenstackapis.FixedIP{
						{
							Subnet: &clusteropenstackapis.SubnetFilter{
								ID: MSet.Infra[0].Subnets.SubnetUUID,
							},
						},
					},
				})
			}
			openstackCluster.Spec.AllowAllInClusterTraffic = false
			err = client.Create(ctx, &openstackCluster)
			if err != nil {
				return err
			}
			return nil
		}
		return err
	}
	return nil

}

func isFusionArchitecture(sets []*ecnsv1.MachineSetReconcile) bool {
	for _, set := range sets {
		if set.Role == ecnsv1.IngressSetRole && set.Number != 0 {
			return false
		}
	}
	return true
}

// TODO sync create kubeadmconfig
func syncCreateKubeadmConfig(ctx context.Context, client client.Client, plan *ecnsv1.Plan) error {
	//TODO get kubeadmconfig by name  If not exist,then create kubeadmconfig
	kubeadmconfigte := &clusterkubeadm.KubeadmConfigTemplate{}
	err := client.Get(ctx, types.NamespacedName{Name: plan.Spec.ClusterName, Namespace: plan.Namespace}, kubeadmconfigte)
	if err != nil {
		if apierrors.IsNotFound(err) {
			//TODO create kubeadmconfig resource
			kubeadmconfigte.Name = plan.Spec.ClusterName
			kubeadmconfigte.Namespace = plan.Namespace
			kubeadmconfigte.Spec.Template.Spec.Format = "cloud-config"
			kubeadmconfigte.Spec.Template.Spec.Files = []clusterkubeadm.File{
				{
					Path:        "/etc/kubernetes/cloud-config",
					Content:     "Cg==",
					Encoding:    "base64",
					Owner:       "root",
					Permissions: "0600",
				},
				{
					Path:        "/etc/certs/cacert",
					Content:     "Cg==",
					Encoding:    "base64",
					Owner:       "root",
					Permissions: "0600",
				},
			}
			kubeadmconfigte.Spec.Template.Spec.JoinConfiguration = &clusterkubeadm.JoinConfiguration{
				NodeRegistration: clusterkubeadm.NodeRegistrationOptions{
					Name: "'{{ local_hostname }}'",
					KubeletExtraArgs: map[string]string{
						"cloud-provider": "external",
						"cloud-config":   "/etc/kubernetes/cloud-config",
					},
				},
			}

			err = client.Create(ctx, kubeadmconfigte)
			if err != nil {
				return err
			}
			return nil
		}
	}

	return nil

}

// TODO sync ssh key
func syncSSH(ctx context.Context, client client.Client, plan *ecnsv1.Plan) error {
	// TODO get ssh secret by name  If not exist,then create ssh key
	_, _, err := utils.GetOrCreateSSHKeySecret(ctx, client, plan)
	if err != nil {
		return err
	}

	return nil
}

// TODO sync create  server group
func syncServerGroups(ctx context.Context, scope *scope.Scope, plan *ecnsv1.Plan) (string, string, error) {
	//TODO get server group by name  If not exist,then create server group
	// 1. get openstack client

	client, err := openstack.NewComputeV2(scope.ProviderClient, gophercloud.EndpointOpts{
		Region: scope.ProviderClientOpts.RegionName,
	})

	client.Microversion = "2.15"

	if err != nil {
		return "", "", err
	}
	var historyM, historyN *servergroups.ServerGroup
	severgroupCount := 0
	masterGroupName := fmt.Sprintf("%s_%s", plan.Spec.ClusterName, "master")
	nodeGroupName := fmt.Sprintf("%s_%s", plan.Spec.ClusterName, "work")
	err = servergroups.List(client, &servergroups.ListOpts{}).EachPage(func(page pagination.Page) (bool, error) {
		actual, err := servergroups.ExtractServerGroups(page)
		if err != nil {
			return false, errors.New("server group data list error,please check network")
		}
		for i, group := range actual {
			if group.Name == masterGroupName {
				severgroupCount++
				historyM = &actual[i]
			}
			if group.Name == nodeGroupName {
				severgroupCount++
				historyN = &actual[i]
			}
		}

		return true, nil
	})
	if err != nil {
		return "", "", err
	}
	if severgroupCount > 2 {
		return "", "", errors.New("please check serverGroups,has same name serverGroups")
	}

	if severgroupCount == 2 && historyM != nil && historyN != nil {
		return historyM.ID, historyN.ID, nil
	}

	sgMaster, err := servergroups.Create(client, &servergroups.CreateOpts{
		Name:     fmt.Sprintf("%s_%s", plan.Spec.ClusterName, "master"),
		Policies: []string{"anti-affinity"},
	}).Extract()
	if err != nil {
		return "", "", err

	}

	sgWork, err := servergroups.Create(client, &servergroups.CreateOpts{
		Name:     fmt.Sprintf("%s_%s", plan.Spec.ClusterName, "work"),
		Policies: []string{"soft-anti-affinity"},
	}).Extract()
	if err != nil {
		return "", "", err
	}

	return sgMaster.ID, sgWork.ID, nil

}

// TODO sync every machineset and other resource replicas to plan
func (r *PlanReconciler) syncMachine(ctx context.Context, sc *scope.Scope, cli client.Client, plan *ecnsv1.Plan, masterGroupID string, nodeGroupID string) error {
	// TODO get every machineset replicas to plan
	// 1. get machineset list
	sc.Logger.Info("syncMachine", "plan", plan.Name)
	labels := map[string]string{ecnsv1.MachineSetClusterLabelName: plan.Spec.ClusterName}
	machineSetList := &clusterapi.MachineSetList{}
	err := cli.List(ctx, machineSetList, client.InNamespace(plan.Namespace), client.MatchingLabels(labels))
	if err != nil {
		return err
	}
	if len(machineSetList.Items) != len(plan.Spec.MachineSets) {
		return fmt.Errorf("machineSetList length is not equal plan.Spec.MachineSets length")
	}
	var planBind = PlanMachineSetBind{}
	planBind.Plan = plan
	// 2. get every machineset replicas
	for _, PlanSet := range plan.Spec.MachineSets {
		setName := fmt.Sprintf("%s%s", plan.Spec.ClusterName, PlanSet.Role)
		for _, ApiSet := range machineSetList.Items {
			if ApiSet.Name == setName {
				fakeSet := ApiSet.DeepCopy()
				planBind.Bind = append(planBind.Bind, MachineSetBind{
					ApiSet:  fakeSet,
					PlanSet: PlanSet,
				})
			}

		}
	}
	// every ApiSet has one goroutine to scale replicas
	var wg sync.WaitGroup
	var errChan = make(chan error, 1)
	sc.Logger.Info("start sync machineSet replicas")
	for _, bind := range planBind.Bind {
		fmt.Println("scale replicas")
		wg.Add(1)
		go func(ctxFake context.Context, scope *scope.Scope, c client.Client, target *ecnsv1.MachineSetReconcile, actual *clusterapi.MachineSet, totalPlan *ecnsv1.Plan, wait *sync.WaitGroup, masterGroup string, nodeGroup string) {
			err = r.processWork(ctxFake, scope, c, target, *actual, plan, wait, masterGroup, nodeGroup)
			if err != nil {
				errChan <- err
			}
		}(ctx, sc, cli, bind.PlanSet, bind.ApiSet, plan, &wg, masterGroupID, nodeGroupID)
	}
	wg.Wait()
	select {
	case err := <-errChan:
		sc.Logger.Error(err, "sync machineSet replicas failed")
		return err
	default:
		sc.Logger.Info("sync machineSet replicas success")
		return nil
	}
}

// TODO  sync signal machineset replicas
func (r *PlanReconciler) processWork(ctx context.Context, sc *scope.Scope, c client.Client, target *ecnsv1.MachineSetReconcile, actual clusterapi.MachineSet, plan *ecnsv1.Plan, wait *sync.WaitGroup, mastergroup string, nodegroup string) error {
	defer func() {
		sc.Logger.Info("waitGroup done")
		wait.Done()
	}()
	// get machineset status now
	var acNow clusterapi.MachineSet
	var index int32

	diffMap, err := utils.GetAdoptInfra(ctx, c, target, plan)
	if err != nil {
		return err
	}
	for uid, adoptIn := range diffMap {
		err = c.Get(ctx, types.NamespacedName{Name: actual.Name, Namespace: actual.Namespace}, &acNow)
		if err != nil {
			return err
		}

		Fn := func(uid string) (int32, error) {
			for i, in := range target.Infra {
				if in.UID == uid {
					return int32(i), nil
				}
			}
			return -1, errors.New("infra not found")
		}

		index, err = Fn(uid)
		if err != nil {
			return err
		}

		var diff int64 = adoptIn.Diff

		switch {
		case diff == 0:
			return errors.New("the infra dont need reconcile,please check code")
		case diff > 0:
			for i := 0; i < int(diff); i++ {
				replicas := *acNow.Spec.Replicas + int32(i) + 1
				sc.Logger.Info("add pass into", "replicas", replicas)
				err = utils.AddReplicas(ctx, sc, c, target, actual.Name, plan, int(index), mastergroup, nodegroup, replicas)
				if err != nil {
					return err
				}
			}
		case diff < 0:
			diff *= -1
			if adoptIn.MachineHasDeleted != diff {
				return fmt.Errorf("please make sure the machine has annotation %s", utils.DeleteMachineAnnotation)
			}
			for i := 0; i < int(diff); i++ {
				replicas := *acNow.Spec.Replicas - int32(i) - 1
				sc.Logger.Info("add pass into", "replicas", replicas)
				err = utils.SubReplicas(ctx, sc, c, target, actual.Name, plan, int(index), replicas)
				if err != nil {
					return err
				}
			}
		}

	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
// need watch machine.
func (r *PlanReconciler) SetupWithManager(mgr ctrl.Manager, options controller.Options) error {
	ctx := context.Background()
	machineToInfraFn := utils.MachineToInfrastructureMapFunc(ctx, mgr.GetClient())
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(options).
		For(
			&ecnsv1.Plan{},
			builder.WithPredicates(
				predicate.Funcs{
					// Avoid reconciling if the event triggering the reconciliation is related to incremental status updates
					UpdateFunc: func(e event.UpdateEvent) bool {
						oldCluster := e.ObjectOld.(*ecnsv1.Plan).DeepCopy()
						newCluster := e.ObjectNew.(*ecnsv1.Plan).DeepCopy()
						oldCluster.Status = ecnsv1.PlanStatus{}
						newCluster.Status = ecnsv1.PlanStatus{}
						oldCluster.ObjectMeta.ResourceVersion = ""
						newCluster.ObjectMeta.ResourceVersion = ""
						return !reflect.DeepEqual(oldCluster, newCluster)
					},
				},
			),
		).
		Watches(
			&source.Kind{Type: &clusterapi.Machine{}},
			handler.EnqueueRequestsFromMapFunc(func(o client.Object) []reconcile.Request {
				requests := machineToInfraFn(o)
				if len(requests) < 1 {
					return nil
				}

				p := &ecnsv1.Plan{}
				if err := r.Client.Get(ctx, requests[0].NamespacedName, p); err != nil {
					return nil
				}
				return requests
			})).
		Complete(r)
}

func (r *PlanReconciler) deletePlanResource(ctx context.Context, scope *scope.Scope, plan *ecnsv1.Plan) error {
	// List all machineset for this plan
	err := deleteHA(ctx, r.Client, scope, plan)
	if err != nil {
		return err
	}

	err = deleteCluster(ctx, r.Client, scope, plan)
	if err != nil {
		return err
	}

	err = deleteMachineTemplate(ctx, r.Client, scope, plan)
	if err != nil {
		return err
	}

	err = deleteCloudInitSecret(ctx, r.Client, scope, plan)
	if err != nil {
		return err
	}

	err = deleteKubeadmConfig(ctx, scope, r.Client, plan)
	if err != nil {
		return err
	}

	err = deleteSSHKeySecert(ctx, scope, r.Client, plan)
	if err != nil {
		return err
	}

	err = deleteAppCre(ctx, scope, r.Client, plan)
	if err != nil {
		return err
	}

	err = deleteSeverGroup(ctx, r.Client, scope, plan)
	if err != nil {
		return err
	}

	err = deleteAnsiblePlan(ctx, r.Client, scope, plan)
	if err != nil {
		return err
	}

	return nil
}

func deleteHA(ctx context.Context, cli client.Client, scope *scope.Scope, plan *ecnsv1.Plan) error {
	if plan.Spec.LBEnable {
		// cluster delete has delete lb
		service, err := loadbalancer.NewService(scope)
		if err != nil {
			return err
		}
		for _, set := range plan.Spec.MachineSets {
			loadbalancerName := fmt.Sprintf("%s-%s", plan.Spec.ClusterName, set.Role)
			err = service.DeleteLoadBalancer(plan, loadbalancerName)
			if err != nil {
				return err
			}
		}

	} else {
		// delete HA ports fip
		service, err := networking.NewService(scope)
		if err != nil {
			return err
		}
		for _, infraS := range plan.Status.InfraMachine {
			if infraS.HAPublicIP != "" {
				err = service.DeleteFloatingIP(plan, infraS.HAPublicIP)
				if err != nil {
					return err
				}
			}
			// delete HA ports
			if infraS.HAPortID != "" {
				err = service.DeletePort(plan, infraS.HAPortID)
				if err != nil {
					return err
				}
			}

		}
	}
	return nil
}
func deleteSeverGroup(ctx context.Context, cli client.Client, scope *scope.Scope, plan *ecnsv1.Plan) error {
	op, err := openstack.NewComputeV2(scope.ProviderClient, gophercloud.EndpointOpts{
		Region: scope.ProviderClientOpts.RegionName,
	})
	if err != nil {
		return err
	}
	if plan.Status.ServerGroupID != nil && plan.Status.ServerGroupID.MasterServerGroupID != "" && plan.Status.ServerGroupID.WorkerServerGroupID != "" {
		err = servergroups.Delete(op, plan.Status.ServerGroupID.MasterServerGroupID).ExtractErr()
		if err != nil {
			if clusteropenstackerrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		err = servergroups.Delete(op, plan.Status.ServerGroupID.WorkerServerGroupID).ExtractErr()
		if err != nil {
			if clusteropenstackerrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		return nil
	}
	return nil
}

func deleteMachineTemplate(ctx context.Context, cli client.Client, scope *scope.Scope, plan *ecnsv1.Plan) error {
	var machineTemplates clusteropenstackapis.OpenStackMachineTemplateList
	labels := map[string]string{ecnsv1.MachineSetClusterLabelName: plan.Spec.ClusterName}
	err := cli.List(ctx, &machineTemplates, client.InNamespace(plan.Namespace), client.MatchingLabels(labels))
	if err != nil {
		return err
	}
	for _, machineTemplate := range machineTemplates.Items {
		err = cli.Delete(ctx, &machineTemplate)
		if err != nil {
			return err
		}
	}
	return nil
}

func deleteCloudInitSecret(ctx context.Context, client client.Client, scope *scope.Scope, plan *ecnsv1.Plan) error {
	machineSets, err := utils.ListMachineSets(ctx, client, plan)
	if err != nil {
		scope.Logger.Error(err, "List Machine Sets failed.")
		return err
	}

	if len(machineSets.Items) == 0 {
		// create all machineset Once
		for _, set := range plan.Spec.MachineSets {
			// create machineset
			var cloudInitSecret corev1.Secret
			secretName := fmt.Sprintf("%s-%s%s", plan.Spec.ClusterName, set.Name, utils.CloudInitSecretSuffix)
			err = client.Get(ctx, types.NamespacedName{
				Namespace: plan.Namespace,
				Name:      secretName,
			}, &cloudInitSecret)
			if err != nil {
				if apierrors.IsNotFound(err) {
					scope.Logger.Info(set.Name, utils.CloudInitSecretSuffix, " secret has already been deleted.")
					return nil
				}
			}
			err = client.Delete(ctx, &cloudInitSecret)
			if err != nil {
				scope.Logger.Error(err, "Delete cloud init secret failed.")
				return err
			}
		}
	}
	return nil
}

func deleteCluster(ctx context.Context, client client.Client, scope *scope.Scope, plan *ecnsv1.Plan) error {
	err := client.Delete(ctx, &clusterapi.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      plan.Spec.ClusterName,
			Namespace: plan.Namespace,
		},
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			scope.Logger.Info("Deleting the cluster succeeded")
			return nil
		}
		scope.Logger.Error(err, "Delete cluster failed.")
		return err
	}
	errR := utils.PollImmediate(utils.RetryDeleteClusterInterval, utils.DeleteClusterTimeout, func() (bool, error) {
		cluster := clusterapi.Cluster{}
		err = client.Get(ctx, types.NamespacedName{Name: plan.Spec.ClusterName, Namespace: plan.Namespace}, &cluster)
		if err != nil {
			if apierrors.IsNotFound(err) {
				scope.Logger.Info("Deleting the cluster succeeded")
				return true, nil
			}
			return false, nil
		}
		return false, nil
	})
	if errR != nil {
		scope.Logger.Error(err, "Deleting the cluster timeout,rejoin reconcile")
		return errR
	}

	return nil
}

func deleteAppCre(ctx context.Context, scope *scope.Scope, client client.Client, plan *ecnsv1.Plan) error {
	var (
		secretName = fmt.Sprintf("%s-%s", plan.Spec.ClusterName, ProjectAdminEtcSuffix)
		secret     = &corev1.Secret{}
	)

	IdentityClient, err := openstack.NewIdentityV3(scope.ProviderClient, gophercloud.EndpointOpts{
		Region: scope.ProviderClientOpts.RegionName,
	})
	if err != nil {
		return err
	}
	err = client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: plan.Namespace}, secret)
	if err != nil {
		if apierrors.IsNotFound(err) {
		}
	}

	err = utils.DeleteAppCre(ctx, scope, IdentityClient, secret.ObjectMeta.Labels["creId"])
	if err != nil {
		if clusteropenstackerrors.IsNotFound(err) {
			return nil
		}
		scope.Logger.Error(err, "Delete application credential failed.")
		return err
	}
	err = client.Delete(ctx, secret)
	if err != nil {
		if clusteropenstackerrors.IsNotFound(err) {
			return nil
		}
		scope.Logger.Error(err, "Delete application credential secret failed.")
		return err
	}

	return nil
}

func deleteKubeadmConfig(ctx context.Context, scope *scope.Scope, client client.Client, plan *ecnsv1.Plan) error {
	kubeadmconfigte := &clusterkubeadm.KubeadmConfigTemplate{}
	err := client.Get(ctx, types.NamespacedName{Name: plan.Spec.ClusterName, Namespace: plan.Namespace}, kubeadmconfigte)
	if err != nil {
		if apierrors.IsNotFound(err) {
			scope.Logger.Info("Cluster has already been deleted")
			return nil
		}
	}

	err = client.Delete(ctx, kubeadmconfigte)
	if err != nil {
		scope.Logger.Error(err, "Delete kubeadmin secert failed.")
		return err
	}

	return nil
}

func deleteSSHKeySecert(ctx context.Context, scope *scope.Scope, client client.Client, plan *ecnsv1.Plan) error {
	secretName := fmt.Sprintf("%s%s", plan.Name, utils.SSHSecretSuffix)
	//get secret by name secretName
	secret := &corev1.Secret{}
	err := client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: plan.Namespace}, secret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			scope.Logger.Info("SSHKeySecert has already been deleted")
			return nil
		}
	}

	err = client.Delete(ctx, secret)
	if err != nil {
		scope.Logger.Error(err, "Delete kubeadmin secert failed.")
		return err
	}

	return nil
}

func deleteAnsiblePlan(ctx context.Context, client client.Client, scope *scope.Scope, plan *ecnsv1.Plan) error {
	ansiblePlanName := fmt.Sprintf("%s%s", plan.Name, utils.SSHSecretSuffix)
	ansiblePlan := &ecnsv1.AnsiblePlan{}
	err := client.Get(ctx, types.NamespacedName{Name: ansiblePlanName, Namespace: plan.Namespace}, ansiblePlan)
	if err != nil {
		if apierrors.IsNotFound(err) {
			scope.Logger.Info("ansible plan has already been deleted")
			return nil
		}
	}

	err = client.Delete(ctx, ansiblePlan)
	if err != nil {
		scope.Logger.Error(err, "Delete ansible plan failed")
		return err
	}

	return nil

}
