package utils

import (
	"bytes"
	"context"
	ecnsv1 "easystack.com/plan/api/v1"
	"fmt"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"text/template"
)

const AnsibleInventory = `## Configure 'ip' variable to bind kubernetes services on a
## different ip than the default iface
{{range .NodePools}}
{{.Name}}  ansible_ssh_host={{.AnsibleHost}} ansible_ssh_private_key_file={{.AnsibleSSHPrivateKeyFile}}  ip={{.AnsibleIP}}
{{end}}
[kube-master]
{{range .KubeMaster}}
{{.}}
{{end}}
[etcd]
{{range .Etcd}}
{{.}}
{{end}}
[kube-node]
{{range .KubeNode}}
{{.}}
{{end}}
[k8s-cluster:children]
kube-master
kube-node
[ingress]
{{range .KubeIngress}}
{{.}}
{{end}}
[prometheus]
{{range .KubePrometheus}}
{{.}}
{{end}}
[log]
{{range .KubeLog}}
{{.}}
{{end}}
{{range $key, $value := .OtherGroup}}
[{{$key}}]
{{range $value}}
{{.}}
{{end}}
{{end}}
`

const AnsibleVars = `
{{range $key, $value := .OtherAnsibleOpts}}
{{$key}}: {{$value}}
{{end}}
`

func GetOrCreateInventoryFile(ctx context.Context, cli client.Client, ansible *ecnsv1.AnsiblePlan) error {
	t, err := template.New("inventory").Parse(AnsibleInventory)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	// Execute the template and write the output to the file
	err = t.Execute(&buf, ansible.Spec.Install)
	if err != nil {
		return err
	}
	// get ansible cr uuid,create inventory file by uid
	File := fmt.Sprintf("/tmp/%s", ansible.UID)
	if FileExist(File) {
		//delete this path file
		err = os.RemoveAll(File)
		if err != nil {
			// 删除失败
			return err
		}
	}
	//create file
	f, err := os.Create(File)
	if err != nil {
		return err
	}
	_, err = f.Write(buf.Bytes())
	if err != nil {
		return err
	}
	return nil
}

func GetOrCreateVarsFile(ctx context.Context, cli client.Client, ansible *ecnsv1.AnsiblePlan) error {
	t, err := template.New("vars").Parse(AnsibleVars)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	// Execute the template and write the output to the file
	err = t.Execute(&buf, ansible.Spec.Install)
	if err != nil {
		return err
	}
	// get ansible cr uuid,create inventory file by uid
	File := fmt.Sprintf("/tmp/%s.vars", ansible.UID)
	if FileExist(File) {
		//delete this path file
		err = os.RemoveAll(File)
		if err != nil {
			// 删除失败
			return err
		}
	}
	//create file
	f, err := os.Create(File)
	if err != nil {
		return err
	}
	_, err = f.Write(buf.Bytes())
	if err != nil {
		return err
	}
	return nil
}

//	TODO start ansible plan
//
// 1.EXEC ANSIBLE COMMAND
// 2.print ansible log to one log file by ansible cr uid(one cluster only has one log file)
// 3.WAIT ANSIBLE task RETURN RESULT AND UPDATE ANSIBLE CR DONE=TRUE
func StartAnsiblePlan(ctx context.Context, cli client.Client, ansible *ecnsv1.AnsiblePlan) error {
	return nil
}
