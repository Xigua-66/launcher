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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PlanSpec defines the desired state of Plan
type PlanSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of Plan. Edit plan_types.go to remove/update
	Foo string `json:"foo,omitempty"`

	// NetMode is an flag to indicate the mode of the plan
	// one is  use existed network, the other is created new network.
	// value is "existed" or "new"

	NetMode string `json:"mode"`

	// K8sVersion is the version of kubernetes => ansible kubernetes tag
	K8sVersion string `json:"k8sVersion"`

	// ClusterName is the cluster name => clusters.cluster.x-k8s.io
	// --------------------------------=> openstackclusters.infrastructure.cluster.x-k8s.io
	ClusterName string `json:"cluster_name"`

	// SshKey is the all cluster machine's public key
	SshKey string 	`json:"ssh_key"`

	// UseFloatIP decied to get fips or no
	UseFloatIP string `json:"use_float_ip"`

	// 公网IP带宽...
	// 公网IP资源池...

	MachineSets []*MachineSet `json:"machine_sets"`

	// Monitor is the pvc config of etcd
	Monitor MonitorConfig `json:"monitor"`

	// OtherAnsibleOpts is the ansible custome vars
	// OtherAnsibleOpts => ansible test/vars.yaml
	OtherAnsibleOpts map[string]string `json:"other_ansible_opts,omitempty"`


}

// MonitorConfig is the monitor other config
// include pvc cap
// include pvc type
// include auto clear days
type  MonitorConfig struct {

}

// MachineSet is the  machine config
type  MachineSet struct {

}

// PlanStatus defines the observed state of Plan
type PlanStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Plan is the Schema for the plans API
type Plan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PlanSpec   `json:"spec,omitempty"`
	Status PlanStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PlanList contains a list of Plan
type PlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Plan `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Plan{}, &PlanList{})
}
