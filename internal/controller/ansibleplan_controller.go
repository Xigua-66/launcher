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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"time"

	"github.com/shirou/gopsutil/process"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

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

	//TODO get plan instance
	var planInstance *easystackcomv1.Plan
	err := r.Client.Get(ctx, types.NamespacedName{
		Namespace: ansible.Namespace,
		Name:      ansible.Spec.Plan,
	}, planInstance)

	if err != nil {
		log.Error(err, "get plan instance failed,check spec.cluster field")
		return ctrl.Result{}, err
	}

	log.Info("Reconciling ansible plan resource")
	if ansible.Spec.Done {
		log.Info("ansible plan is done,skip reconcile")
		return ctrl.Result{}, nil
	}

	if ansible.Spec.ProcessPID != nil {
		log.Info("ansible plan is running,check status")
		status, err := process.NewProcess(*ansible.Spec.ProcessPID)
		if err != nil {
			// TODO if pid is not found, set status to failed and restart new ansible plan process
			if err == process.ErrorProcessNotRunning {
				log.Info("ansible plan process is not running,reset status and restart new process")
				// update status
				ansible.Status.ProcessStatus.ProcessStatus = easystackcomv1.PIDStatusStop
				ansible.Status.ProcessStatus.ProcessPID = nil
				ansible.Status.ProcessData = ""
				if err := patchHelper.Patch(ctx, ansible); err != nil {
					return ctrl.Result{ }, err
				}
				return ctrl.Result{RequeueAfter: 10*time.Second}, nil

			}
			log.Error(err, "get process status failed but not excepted")
			return ctrl.Result{}, err
		}
		ansible.Status.ProcessStatus.ProcessPID = &status.Pid
		running, err := status.IsRunning()
		if err != nil {
			log.Error(err, "get process status exec failed")
			return ctrl.Result{}, err
		}
		if running {
			ansible.Status.ProcessStatus.ProcessStatus = easystackcomv1.PIDStatusRunning
		} else {
			ansible.Status.ProcessStatus.ProcessStatus = easystackcomv1.PIDStatusStop
		}
		ansible.Status.ProcessData = status.String()
		if err := patchHelper.Patch(ctx, ansible); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil

	} else {
		//TODO write ssh key from secret if not exist the private file
		err := utils.GetOrCreateSSHkeyFile(ctx, r.Client, planInstance)
		if err != nil {
			return ctrl.Result{}, err
		}
		//TODO write inventory to captain file

		//TODO start ansible plan process,write pid log to file
		//TODO writeback pid
		//TODO update status
	}

	return ctrl.Result{}, nil
}

func (r *AnsiblePlanReconciler) reconcileDelete(ctx context.Context, log logr.Logger, patchHelper *patch.Helper, ansible *easystackcomv1.AnsiblePlan) (ctrl.Result, error) {

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AnsiblePlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&easystackcomv1.AnsiblePlan{}).
		Complete(r)
}
