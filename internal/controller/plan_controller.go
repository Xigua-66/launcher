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
	"context"
	"easystack.com/plan/pkg/cloud/service/provider"
	"easystack.com/plan/pkg/utils"
	"github.com/easystack/cluster-api-provider-openstack/pkg/cloud/services/compute"
	"github.com/easystack/cluster-api-provider-openstack/pkg/scope"
	clusterapi "sigs.k8s.io/cluster-api/api/v1beta1"
	clusterutils "sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ecnsv1 "easystack.com/plan/api/v1"
)

const defaultopenstackadminconfsecret = "openstack-admin-etc"

// PlanReconciler reconciles a Plan object
type PlanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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
func (r *PlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
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
		// set cluster.Spec.Paused = true
		// first get the clusterv1.Cluster, then set cluster.Spec.Paused = true
		// then update the cluster
		// Fetch the Cluster.
		cluster, err := clusterutils.GetClusterByName(ctx, r.Client, plan.Spec.ClusterName, plan.Namespace)
		if err != nil {
			return reconcile.Result{}, err
		}

		if cluster == nil {
			log.Info("Cluster Controller has not yet set OwnerRef")
			return reconcile.Result{}, nil
		}

		log = log.WithValues("cluster", cluster.Name)

		if cluster.Spec.Paused == true {
			log.Info("Cluster is already paused")
			return ctrl.Result{}, nil
		} else {
			cluster.Spec.Paused = true
			if err := r.Client.Update(ctx, cluster); err != nil {
				return ctrl.Result{}, err
			}

		}

		return ctrl.Result{}, nil
	}
	patchHelper, err := patch.NewHelper(plan, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	osProviderClient, clientOpts, projectID, err := provider.NewClientFromSecret(ctx, r.Client, req.Namespace, defaultopenstackadminconfsecret, "default")
	if err != nil {
		return reconcile.Result{}, err
	}
	scope := &scope.Scope{
		ProviderClient:     osProviderClient,
		ProviderClientOpts: clientOpts,
		ProjectID:          projectID,
		Logger:             log,
	}

	if !plan.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, scope, patchHelper, plan)
	}

	// Handle non-deleted clusters
	return r.reconcileNormal(ctx, scope, patchHelper, plan)

}

func (r *PlanReconciler) reconcileNormal(ctx context.Context, scope *scope.Scope, patchHelper *patch.Helper, plan *ecnsv1.Plan) (_ ctrl.Result, reterr error) {
	// If the OpenStackMachine doesn't have our finalizer, add it.
	controllerutil.AddFinalizer(plan, ecnsv1.MachineFinalizer)
	// Register the finalizer immediately to avoid orphaning plan resources on delete
	if err := patchHelper.Patch(ctx, plan); err != nil {
		return ctrl.Result{}, err
	}
	scope.Logger.Info("Reconciling plan openstack resource")
	_, err := compute.NewService(scope)
	if err != nil {
		return ctrl.Result{}, err
	}

	// get or create sshkeys secret
	_, _, err = utils.GetOrCreateSSHKeySecret(ctx, r.Client, plan)
	if err != nil {
		return ctrl.Result{}, err
	}
	// TODO get or create openstack auth config secret

	//TODO  get or create cluster.cluster.x-k8s.io

	//TODO  get or create openstackcluster.infrastructure.cluster.x-k8s.io

	//TODO  get or create KubeadmConfigTemplate ,no use

	//TODO  get or create server groups,master one,work one

	// List all machineset for this plan
	machineSets, err := utils.ListMachineSets(ctx, r.Client, plan)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(machineSets.Items) == 0 {
		// create all machineset
		for _, set := range plan.Spec.MachineSets {
			// create machineset
			err := utils.CreateMachineSet(ctx, r.Client, plan, set)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// skip create machineset

	}
	// Reconcile every machineset replicas
	err = r.syncMachine(machineSets, plan.Spec.MachineSets)
	if err != nil {
		return ctrl.Result{}, err
	}
	// update plan status
	err = r.updateStatus(ctx, plan)
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *PlanReconciler) reconcileDelete(ctx context.Context, scope *scope.Scope, patchHelper *patch.Helper, plan *ecnsv1.Plan) (_ ctrl.Result, reterr error) {
	return ctrl.Result{}, nil
}

// TODO sync every machineset and other resource replicas to plan
func (r *PlanReconciler) syncMachine(ms clusterapi.MachineSetList, es []*ecnsv1.MachineSetReconcile) error {

	return nil
}

// TODO update plan status
func (r *PlanReconciler) updateStatus(ctx context.Context, plan *ecnsv1.Plan) error {
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ecnsv1.Plan{}).
		Complete(r)
}
