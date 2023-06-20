package utils

import (
	"context"
	ecnsv1 "easystack.com/plan/api/v1"
	"easystack.com/plan/pkg/scope"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/applicationcredentials"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/extensions/trusts"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func CreateAppCre(ctx context.Context, scope *scope.Scope, identityClient *gophercloud.ServiceClient, secretName string) (string, string, error) {
	appOpts := applicationcredentials.CreateOpts{
		Name:         secretName,
		Description:  "eks used app cre,dont delete",
		Unrestricted: false,
	}
	appCre, err := applicationcredentials.Create(identityClient, scope.UserID, appOpts).Extract()
	if err != nil {
		return "", "", err
	}

	return appCre.ID, appCre.Secret, nil

}

// GetTrustUser TODO get trust user in configmap by plan name
func GetTrustUser(ctx context.Context, client client.Client, plan *ecnsv1.Plan) (*trusts.Trust, error) {
	//TODO get trust user in configmap by plan name
	return nil, nil

}

// GetOrCreateOpenstackAuthConfig Get or Create Openstack config
func GetOrCreateOpenstackAuthConfig(ctx context.Context, cli client.Client, plan *ecnsv1.Plan, trust *trusts.Trust) error {
	return nil
}
