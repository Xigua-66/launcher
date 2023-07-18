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
	"sync"
	"text/template"
	"time"

	ecnsv1 "easystack.com/plan/api/v1"
	"easystack.com/plan/pkg/cloud/service/provider"
	"easystack.com/plan/pkg/scope"
	"easystack.com/plan/pkg/utils"
	clusteropenstackapis "github.com/easystack/cluster-api-provider-openstack/api/v1alpha6"
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
	clusterapi "sigs.k8s.io/cluster-api/api/v1beta1"
	clusterkubeadm "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
	clusterutils "sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
	Scheme *runtime.Scheme
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
func (r *PlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result,reterr error) {
	var (
		deletion = false
		log = log.FromContext(ctx)
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
		if err1 == nil{
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
			r.deletePlanResource(ctx, scope, plan)
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
					reterr = errors.Wrapf(err, "error patching OpenStackCluster %s/%s", plan.Namespace, plan.Name)
				}
			}
		}
		
	}()
	
	// Handle non-deleted clusters
	return r.reconcileNormal(ctx, scope, patchHelper, plan)
}

func (r *PlanReconciler) reconcileNormal(ctx context.Context, scope *scope.Scope, patchHelper *patch.Helper, plan *ecnsv1.Plan) (_ ctrl.Result, reterr error) {
	// get gopher cloud client
	// TODO Compare status.LastPlanMachineSets replicas with plan.Spec's MachineSetReconcile replicas and create AnsiblePlan,only when replicas change
	// get or create app credential
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


	// List all machineset for this plan
	machineSets, err := utils.ListMachineSets(ctx, r.Client, plan)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(machineSets.Items) == 0 {
		// create all machineset Once
		for _, set := range plan.Spec.MachineSets {
			// create machineset
			err := utils.CreateMachineSet(ctx, scope, r.Client, plan, set, mastergroupID, nodegroupID)
			if err != nil {
				if err.Error() == "openstack cluster is not ready" {
					return ctrl.Result{RequeueAfter: 10*time.Second},nil
				}
				return ctrl.Result{}, err
			}
		}
	} else {
		// skip create machineset
		scope.Logger.Info("Skip create machineset")

	}
	// Reconcile every machineset replicas
	err = r.syncMachine(ctx, scope, r.Client, plan, mastergroupID, nodegroupID)
	if err != nil {
		return ctrl.Result{}, err
	}

	// TODO check all machineset replicas is ready to num,create ansible plan
	// 1.check status replicas
	// 2.check ansible plan is exist,name=plan.Spec.ClusterName + plan.Spec.Version
	return ctrl.Result{}, nil
}

// TODO sync app cre
func syncAppCre(ctx context.Context, scope *scope.Scope, cli client.Client, plan *ecnsv1.Plan) error {
	// TODO get openstack application credential secret by name  If not exist,then create openstack application credential and its secret.
	// create openstack application credential
	var (
		secretName = fmt.Sprintf("%s-%s", plan.Spec.ClusterName, ProjectAdminEtcSuffix)
		secret = &corev1.Secret{}
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
				creLabels = make(map[string]string)
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
			cluster.Name = plan.Spec.ClusterName
			cluster.Namespace = plan.Namespace
			cluster.Spec.ClusterNetwork = &clusterapi.ClusterNetwork{}
			cluster.Spec.ClusterNetwork.Pods = &clusterapi.NetworkRanges{}
			cluster.Spec.ClusterNetwork.Pods.CIDRBlocks = []string{plan.Spec.PodCidr}
			cluster.Spec.ClusterNetwork.ServiceDomain = "cluster.local"
			cluster.Spec.ControlPlaneEndpoint = clusterapi.APIEndpoint{
				Host: "0.0.0.0",
				Port: 6443,
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
			// TODO create openstackcluster resource
			openstackCluster.Name = plan.Spec.ClusterName
			openstackCluster.Namespace = plan.Namespace
			openstackCluster.Spec.DisableAPIServerFloatingIP = true
			openstackCluster.Spec.APIServerLoadBalancer.Enabled = false
			openstackCluster.Spec.APIServerFixedIP = "0.0.0.0"
			openstackCluster.Spec.CloudName = plan.Spec.ClusterName
			openstackCluster.Spec.DNSNameservers = plan.Spec.DNSNameservers
			if plan.Spec.UseFloatIP == true {
				openstackCluster.Spec.ExternalNetworkID = plan.Spec.ExternalNetworkId
			} else {
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
			openstackCluster.Spec.AllowAllInClusterTraffic = true
			err := client.Create(ctx, &openstackCluster)
			if err != nil {
				return err
			}
			return nil
		}
		return err
	}
	return nil

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
					Content: "Cg==",
					Encoding: "base64",
					Owner: "root",
					Permissions: "0600",

				},
				{
					Path:        "/etc/certs/cacert",
					Content: "Cg==",
					Encoding: "base64",
					Owner: "root",
					Permissions: "0600",
				},

			}
			kubeadmconfigte.Spec.Template.Spec.JoinConfiguration = &clusterkubeadm.JoinConfiguration{
				NodeRegistration: clusterkubeadm.NodeRegistrationOptions{
					Name: "'{{ local_hostname }}'",
					KubeletExtraArgs: map[string]string{
						"cloud-provider": "external",
						"cloud-config": "/etc/kubernetes/cloud-config",
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
	if err != nil {
		return "", "", err
	}
	var historyM,historyN *servergroups.ServerGroup
	severgroupCount :=0
	masterGroupName := fmt.Sprintf("%s_%s", plan.Spec.ClusterName, "master")
	nodeGroupName := fmt.Sprintf("%s_%s", plan.Spec.ClusterName, "work")
	err = servergroups.List(client, &servergroups.ListOpts{}).EachPage(func(page pagination.Page) (bool, error) {
		actual, err := servergroups.ExtractServerGroups(page)
		if err!=nil {
			return false,errors.New("server group data list error,please check network")
		}
		for i,group := range actual {
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
	if err!=nil{
		return "","",err
	}
	if severgroupCount > 2 {
		return "","",errors.New("please check serverGroups,has same name serverGroups")
	}

	if severgroupCount ==2 && historyM !=nil && historyN !=nil {
		return historyM.ID,historyN.ID,nil
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
		Policies: []string{"anti-affinity"},
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
	sc.Logger.Info("start sync machineSet replicas")
	for _, bind := range planBind.Bind {
		if *bind.ApiSet.Spec.Replicas < bind.PlanSet.Replica {
			fmt.Println("scale replicas")
			wg.Add(1)
			go func(ctxfake context.Context, scope *scope.Scope, c client.Client, target *ecnsv1.MachineSetReconcile, actual *clusterapi.MachineSet, totalplan *ecnsv1.Plan, wait *sync.WaitGroup, mastergroup string, nodegroup string) {
				err = r.processWork(ctxfake, scope, c, target, *actual, plan, wait, mastergroup, nodegroup)
				if err != nil {
					sc.Logger.Error(err, "sync machineSet replicas failed")
				}
			}(ctx, sc, cli, bind.PlanSet, bind.ApiSet, plan, &wg, masterGroupID, nodeGroupID)
		}
	}
	wg.Wait()
	sc.Logger.Info("sync machineSet replicas success")

	return nil
}

// TODO  sync signal machineset replicas
func (r *PlanReconciler) processWork(ctx context.Context, sc *scope.Scope, c client.Client, target *ecnsv1.MachineSetReconcile, actual clusterapi.MachineSet, plan *ecnsv1.Plan, wait *sync.WaitGroup, mastergroup string, nodegroup string) error {
	defer func() {
		sc.Logger.Info("waitGroup done")
		wait.Done()
	}()
	// get machineset status now
	var acNow clusterapi.MachineSet
	err := c.Get(ctx, types.NamespacedName{Name: actual.Name, Namespace: actual.Namespace}, &acNow)
	if err != nil {
		return err
	}
	diff := target.Replica - *acNow.Spec.Replicas
	switch {
	case diff == 0:
		return nil
	case diff > 0:
		for i := 0; i < int(diff); i++ {
			index := *acNow.Spec.Replicas+int32(i)
			err = utils.AddReplicas(ctx, sc, c, target, actual.Name, plan, int(index), mastergroup, nodegroup)
			if err != nil {
				return err
			}
		}
	case diff < 0:
		return errors.New("please check replicas,can not scale down replicas")
	}

	return nil

}

// SetupWithManager sets up the controller with the Manager.
func (r *PlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ecnsv1.Plan{}).
		Complete(r)
}

func (r *PlanReconciler)deletePlanResource(ctx context.Context, scope *scope.Scope, plan *ecnsv1.Plan) error {
	// List all machineset for this plan

	err := deleteCluster(ctx, r.Client, scope, plan)
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

	return nil	
}

func deleteCloudInitSecret(ctx context.Context, client client.Client, scope *scope.Scope, plan *ecnsv1.Plan) error{
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
			err := client.Get(ctx, types.NamespacedName{
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
	err := utils.PollImmediate(utils.RetryDeleteClusterInterval, utils.DeleteClusterTimeout, func() (bool, error) {
		cluster := clusterapi.Cluster{}
		err := client.Get(ctx, types.NamespacedName{Name: plan.Spec.ClusterName, Namespace: plan.Namespace}, &cluster)
		if err != nil {
			if apierrors.IsNotFound(err) {
				scope.Logger.Error(err, "Deleting the cluster succeeded")
				return true, nil
			}
		}
		if !cluster.ObjectMeta.DeletionTimestamp.IsZero() {
			return false, nil
		}

		err = client.Delete(ctx, &cluster)
		if err != nil {
			scope.Logger.Error(err, "Delete cluster failed.")
			return false, err
		}
		return false, nil
	})

	if err != nil {
		scope.Logger.Error(err, "Failed to delete the cluster")
		return err
	}

	return nil
}

func deleteAppCre(ctx context.Context, scope *scope.Scope, client client.Client, plan *ecnsv1.Plan) error {
	var (
		secretName = fmt.Sprintf("%s-%s", plan.Spec.ClusterName, ProjectAdminEtcSuffix)
		secret = &corev1.Secret{}
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
		scope.Logger.Error(err, "Delete application credential failed.")
		return err
	}
	err = client.Delete(ctx, secret)
	if err != nil {
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
		if apierrors.IsNotFound(err){
			log.Log.Info("SSHKeySecert has already been deleted")
			return nil
		}
	}

	err = client.Delete(ctx, secret)
	if err != nil {
		return err
	}
	
	return nil
}
