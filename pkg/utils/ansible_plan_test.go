package utils

import (
	ecnsv1 "easystack.com/plan/api/v1"
	"fmt"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"strings"
	"testing"
)

// DiffReporter is a simple custom reporter that only records differences
// detected during comparison.
type DiffReporterFake struct {
	path            cmp.Path
	diffs           []string
	UpScale         bool
	DownScale       bool
	AdditionalNodes []*ecnsv1.AnsibleNode
	NodesUpdate     bool
}

func (r *DiffReporterFake) PushStep(ps cmp.PathStep) {
	r.path = append(r.path, ps)
}

func (r *DiffReporterFake) Report(rs cmp.Result) {
	if !rs.Equal() {
		vx, vy := r.path.Last().Values()
		r.diffs = append(r.diffs, fmt.Sprintf("%#v:\n\t-: %+v\n\t+: %+v\n", r.path, vx, vy))
		if vx.IsValid() && vx.Type().String() == "*v1.AnsibleNode" {
			r.AdditionalNodes = append(r.AdditionalNodes, vx.Interface().(*ecnsv1.AnsibleNode))
			r.DownScale = true
		} else if vy.IsValid() && vy.Type().String() == "*v1.AnsibleNode" {
			r.AdditionalNodes = append(r.AdditionalNodes, vy.Interface().(*ecnsv1.AnsibleNode))
			r.UpScale = true
		} else {
			r.NodesUpdate = true
		}

	}
}

func (r *DiffReporterFake) PopStep() {
	r.path = r.path[:len(r.path)-1]
}

func (r *DiffReporterFake) String() string {
	return strings.Join(r.diffs, "\n")
}

func TestCmp(t *testing.T) {
	var ansiblep1 ecnsv1.AnsiblePlan
	var ansiblep2 ecnsv1.AnsiblePlan

	var v1 []*ecnsv1.AnsibleNode = make([]*ecnsv1.AnsibleNode, 0)
	v1 = append(v1, &ecnsv1.AnsibleNode{
		Name:                     "node1",
		AnsibleHost:              "10.6.0.1",
		AnsibleIP:                "10.6.0.2",
		AnsibleSSHPrivateKeyFile: "/root/.ssh/id_rsa",
		MemoryReserve:            -4,
	})
	v1 = append(v1, &ecnsv1.AnsibleNode{
		Name:                     "node2",
		AnsibleHost:              "10.6.0.2",
		AnsibleIP:                "10.6.0.2",
		AnsibleSSHPrivateKeyFile: "/root/.ssh/id_rsa",
		MemoryReserve:            -4,
	})
	v1 = append(v1, &ecnsv1.AnsibleNode{
		Name:                     "node4",
		AnsibleHost:              "10.6.0.4",
		AnsibleIP:                "10.6.0.4",
		AnsibleSSHPrivateKeyFile: "/root/.ssh/id_rsa",
		MemoryReserve:            -4,
	})

	var v2 []*ecnsv1.AnsibleNode = make([]*ecnsv1.AnsibleNode, 0)
	v2 = append(v2, &ecnsv1.AnsibleNode{
		Name:                     "node2",
		AnsibleHost:              "10.6.0.2",
		AnsibleIP:                "10.6.0.2",
		AnsibleSSHPrivateKeyFile: "/root/.ssh/id_rsa",
		MemoryReserve:            -4,
	})
	v2 = append(v2, &ecnsv1.AnsibleNode{
		Name:                     "node1",
		AnsibleHost:              "10.6.0.1",
		AnsibleIP:                "10.6.0.1",
		AnsibleSSHPrivateKeyFile: "/root/.ssh/id_rsa",
		MemoryReserve:            -4,
	})
	v2 = append(v2, &ecnsv1.AnsibleNode{
		Name:                     "node3",
		AnsibleHost:              "10.6.0.3",
		AnsibleIP:                "10.6.0.3",
		AnsibleSSHPrivateKeyFile: "/root/.ssh/id_rsa",
		MemoryReserve:            -4,
	})

	DiffRr := &DiffReporterFake{}
	ansiblep1.Spec.Install = &ecnsv1.AnsibleInstall{}
	ansiblep2.Spec.Install = &ecnsv1.AnsibleInstall{}
	ansiblep1.Spec.Install.OtherAnsibleOpts = make(map[string]string)
	ansiblep2.Spec.Install.OtherAnsibleOpts = make(map[string]string)
	ansiblep1.Spec.Install.NodePools = v1
	ansiblep2.Spec.Install.NodePools = v2
	ansiblep1.Spec.Install.OtherGroup = make(map[string][]string)
	ansiblep2.Spec.Install.OtherGroup = make(map[string][]string)

	option := cmpopts.SortSlices(func(i, j *ecnsv1.AnsibleNode) bool { return i.Name < j.Name })
	ignore := cmpopts.IgnoreFields(ecnsv1.AnsibleNode{}, "MemoryReserve", "AnsibleSSHPrivateKeyFile")
	fmt.Println(cmp.Diff(ansiblep1.Spec.Install.NodePools, ansiblep2.Spec.Install.NodePools, option, ignore, cmp.Reporter(DiffRr)))
	fmt.Println(DiffRr.AdditionalNodes[0])
	fmt.Println(DiffRr.NodesUpdate)
	fmt.Println(DiffRr.UpScale)
	fmt.Println(DiffRr.DownScale)
}
