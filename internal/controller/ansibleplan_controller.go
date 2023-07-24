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
	"easystack.com/plan/pkg/utils"
	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"reflect"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	easystackcomv1 "easystack.com/plan/api/v1"
)

// AnsiblePlanReconciler reconciles a AnsiblePlan object
type AnsiblePlanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=easystack.com,resources=ansibleplans,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=easystack.com,resources=ansibleplans/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=easystack.com,resources=ansibleplans/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the AnsiblePlan object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.4/pkg/reconcile
func (r *AnsiblePlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Info("start ansible reconcile")

	// Fetch the AnsiblePlan instance
	ansible := &easystackcomv1.AnsiblePlan{}
	err := r.Get(ctx, req.NamespacedName, ansible)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}
	if !ansible.Spec.AutoRun {
		log.Info("ansible plan auto run is false,skip reconcile")
		return ctrl.Result{}, nil
	}
	patchHelper, err := patch.NewHelper(ansible, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !ansible.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, log, patchHelper, ansible)
	}

	// Handle non-deleted clusters
	return r.reconcileNormal(ctx, log, patchHelper, ansible)

}

func (r *AnsiblePlanReconciler) reconcileNormal(ctx context.Context, log logr.Logger, patchHelper *patch.Helper, ansible *easystackcomv1.AnsiblePlan) (ctrl.Result, error) {
	// If the AnsiblePlan doesn't have finalizer, add it.
	controllerutil.AddFinalizer(ansible, easystackcomv1.MachineFinalizer)
	// Register the finalizer immediately to avoid orphaning plan resources on delete
	if err := patchHelper.Patch(ctx, ansible); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Reconciling ansible plan resource")
	if ansible.Spec.Done {
		log.Info("ansible plan is done,skip reconcile")
		return ctrl.Result{}, nil
	}

	err := utils.GetOrCreateSSHkeyFile(ctx, r.Client, ansible)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = utils.GetOrCreateInventoryFile(ctx, r.Client, ansible)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = utils.GetOrCreateVarsFile(ctx, r.Client, ansible)
	if err != nil {
		return ctrl.Result{}, err
	}

	//TODO start ansible plan process,write pid log to file
	err = utils.StartAnsiblePlan(ctx, r.Client, ansible)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AnsiblePlanReconciler) reconcileDelete(ctx context.Context, log logr.Logger, patchHelper *patch.Helper, ansible *easystackcomv1.AnsiblePlan) (ctrl.Result, error) {

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AnsiblePlanReconciler) SetupWithManager(mgr ctrl.Manager, options controller.Options) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(options).
		For(
			&easystackcomv1.AnsiblePlan{},
			builder.WithPredicates(
				predicate.Funcs{
					// Avoid reconciling if the event triggering the reconciliation is related to incremental status updates
					UpdateFunc: func(e event.UpdateEvent) bool {
						oldCluster := e.ObjectOld.(*easystackcomv1.AnsiblePlan).DeepCopy()
						newCluster := e.ObjectNew.(*easystackcomv1.AnsiblePlan).DeepCopy()
						oldCluster.Status = easystackcomv1.AnsiblePlanStatus{}
						newCluster.Status = easystackcomv1.AnsiblePlanStatus{}
						oldCluster.ObjectMeta.ResourceVersion = ""
						newCluster.ObjectMeta.ResourceVersion = ""
						return !reflect.DeepEqual(oldCluster, newCluster)
					},
				},
			),
		).
		Complete(r)
}
