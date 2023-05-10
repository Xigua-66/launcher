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
	clusteropenstack "github.com/easystack/cluster-api-provider-openstack/api/v1alpha6"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PlanSpec defines the desired state of Plan
type PlanSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

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
	// Maybe we should create a new ssh pair for every cluster
	// SshKey is the public key of the pair
	SshKey string `json:"ssh_key"`

	// UseFloatIP decied to get fips or no
	UseFloatIP bool `json:"use_float_ip"`

	// 公网IP带宽...
	// 公网IP资源池...

	MachineSets []*MachineSetReconcile `json:"machine_sets"`

	// Monitor is the pvc config of etcd
	Monitor MonitorConfig `json:"monitor"`

	// Repo is the repo of hub
	Repo string `json:"repo"`

	// CniType is the cni type
	CniType string `json:"cni_type"`

	// CniWorkMode is the cni work mode
	CniWorkMode string `json:"cni_work_mode"`

	// PodCidr is the pod cidr
	PodCidr string `json:"pod_cidr"`

	//SvcCidr is the svc cidr
	SvcCidr string `json:"svc_cidr"`

	// OtherAnsibleOpts is the ansible custome vars
	// OtherAnsibleOpts => ansible test/vars.yaml
	OtherAnsibleOpts map[string]string `json:"other_ansible_opts,omitempty"`
}

// MonitorConfig is the monitor other config
// include pvc cap
// include pvc type
// include auto clear days
type MonitorConfig struct {
	// PvcType is the pvc type
	PvcType string `json:"pvc_type"`
	// PvcCap is the pvc cap
	PvcCap string `json:"pvc_cap"`
	// AutoClearDays is the auto clear days
	AutoClearDays string `json:"auto_clear_days"`
}

// MachineSetReconcile is the  machine config
// Maybe we should create a Bastion machine for master machine to access
// and set the SSH  rsa  to the Bastion machine
type MachineSetReconcile struct {
	// image is the image of machine
	image string `json:"image"`
	// Flavor is the flavor of machine
	flavor string `json:"flavor"`
	// replica is the replica of machine
	replica int `json:"replica"`
	// Role is the role of machine
	role string `json:"role"`
	// AvailabilityZone are a set of failure domains for machines
	// decide the every machine's AZ
	AvailabilityZone []string `json:"availability_zone"`
	// subnets are a set of subnets for machines
	// decide the every machine's subnet
	// when NetMode == existed, the subnets is required
	subnets []*Subnet `json:"subnets,omitempty"`
	// floatingIPPool are the floating ip pool of machine
	floatingIPPool []*FloatIP `json:"floating_ip_pool,omitempty"`
	// Volumes are the volume type of machine,include root volume and data volume
	Volumes []volume `json:"volume_types,omitempty"`

	// CloudInit is the cloud init secret of machine,base64 file,can use it to config the machine
	// such as init disk...
	CloudInit string `json:"init,omitempty"`
}
type volume struct {
	// VolumeType is the volume type of machine
	VolumeType string `json:"volume_type"`
	// VolumeSize is the volume size of machine
	VolumeSize int `json:"volume_size"`
	// Index is the index of volume 0==root volume
	Index int `json:"index"`
}
type Subnet struct {
	// SubnetNetwork is the network of subnet
	SubnetNetwork string `json:"subnet_network"`
	// uuid is the subnet uuid of subnet
	SubnetUUID string `json:"subnet_uuid"`
}
type FloatIP struct {
	// Enable is the flag to decide to get float ip or no
	Enable string `json:"enable"`
	// fip is the float ip of machine
	Fip string `json:"fip"`
}

// PlanStatus defines the observed state of Plan
type PlanStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	//ServerGroupID is the server group id of cluster
	ServerGroupID string `json:"server_group_id,omitempty"`
	// MachineSets status
	MachineSets []*MachineSetStatus `json:"machine_sets"`
	// ClusterStatus is the status of cluster
	ClusterStatus *clusteropenstack.OpenStackClusterStatus `json:"cluster_status"`
	// FailureMessage are the all failure message
	FailureMessage []*string `json:"failureMessage,omitempty"`
}
type MachineSetStatus struct {
	// ready is the number of ready machine
	Ready int `json:"ready"`
	// replicasnoew is the number of replicas machines
	ReplicasNow int `json:"replicas_now"`
	// available is the number of available machine
	Available int `json:"available"`
	// ReadyMachines is the ready machine list
	ReadyMachines []MachineStatus `json:"ready_machines"`
}

type MachineStatus struct {
	// name is the name of machine
	Name   string                                   `json:"name,omitempty"`
	status *clusteropenstack.OpenStackMachineStatus `json:"status,omitempty"`
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
