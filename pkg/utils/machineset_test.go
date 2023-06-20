package utils

import (
	"bytes"
	"context"
	ecnsv1 "easystack.com/plan/api/v1"
	"easystack.com/plan/pkg/scope"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"testing"
	"text/template"
)

const AuthtmplFake = `clouds:
  {{.ClusterName}}:
    identity_api_version: 3
    auth:
      auth_url: {{.AuthUrl}}
      application_credential_id: {{.AppCredID}}
      application_credential_secret: {{.AppCredSecret}}
    region_name: {{.Region}}
`

type AuthConfigFake struct {
	// ClusterName is the name of cluster
	ClusterName string
	// AuthUrl is the auth url of keystone
	AuthUrl string
	// AppCredID is the application credential id
	AppCredID string
	// AppCredSecret is the application credential secret
	AppCredSecret string
	// Region is the region of keystone
	Region string
}

// test GetOrCreateCloudInitSecret

func TestGetOrCreateCloudInitSecret(t *testing.T) {
	var ctx = context.Background()
	var s scope.Scope
	var set ecnsv1.MachineSetReconcile
	var plan ecnsv1.Plan

	// get cli from kubeconfig path from /home/jian/.kube/config
	config, err := config.GetConfig()
	if err != nil {
		return
	}
	t.Log(config)
	plan.Name = "test-sample"
	plan.Namespace = "default"
	plan.Spec.ClusterName = "test"
	plan.Spec.UserInfo.AuthUrl = "http://keystone.openstack.svc.cluster.local/v3"
	plan.Spec.UserInfo.Region = "RegionOne"
	c, err := client.New(config, client.Options{})
	if err != nil {
		t.Errorf("Failed to create client: %v", err)
	}
	set.Name = "master"

	// create admin-etc secret
	secretName := fmt.Sprintf("%s-%s", plan.Spec.ClusterName, "admin-etc")
	secret := &corev1.Secret{}
	err = c.Get(ctx, types.NamespacedName{Name: secretName, Namespace: plan.Namespace}, secret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// create openstack application credential
			var auth AuthConfigFake = AuthConfigFake{
				ClusterName:   plan.Spec.ClusterName,
				AuthUrl:       plan.Spec.UserInfo.AuthUrl,
				AppCredID:     "xx",
				AppCredSecret: "testSecret",
				Region:        plan.Spec.UserInfo.Region,
			}
			var secretData = make(map[string][]byte)

			// Create a template object and parse the template string
			te, err := template.New("auth").Parse(AuthtmplFake)
			if err != nil {
				t.Errorf("Failed to parse template: %v", err)
			}
			var buf bytes.Buffer
			// Execute the template and write the output to the file
			err = te.Execute(&buf, auth)
			if err != nil {
				t.Errorf("Failed to execute template: %v", err)
			}
			t.Logf("auth: %v", buf.String())
			// base64 encode the buffer contents and return as a string
			secretData["clouds.yaml"] = buf.Bytes()
			secretData["cacert"] = []byte(fmt.Sprintf("\n"))
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: plan.Namespace,
				},
				Data: secretData,
			}
			err = c.Create(ctx, secret)
			if err != nil {
				t.Errorf("Failed to create secret: %v", err)
			}

		} else {
			t.Errorf("Failed to get secret: %v", err)
		}
	}

	_, _, err = GetOrCreateSSHKeySecret(ctx, c, &plan)
	if err != nil {
		t.Errorf("Failed to get or create ssh key secret: %v", err)
	}
	err = GetOrCreateCloudInitSecret(ctx, &s, c, &plan, &set)
	if err != nil {
		t.Errorf("Failed to get or create cloud init secret: %v", err)
	}
	t.Logf("Get or create cloud init secret successfully")

}
