package utils

import (
	"context"
	ecnsv1 "easystack.com/plan/api/v1"
	"fmt"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/extensions/trusts"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/users"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

func CreateTrustUser(ctx context.Context, identityClient *gophercloud.ServiceClient, projectID string, clusterUUID string, userID string) (string, error) {

	//TODO RANDOM PASSWORD and set secret to save in configmap
	// Avoid create second time
	passwd := "123456789123456789"

	createOpts := users.CreateOpts{
		Name:             fmt.Sprintf("%s_%s", clusterUUID, projectID),
		DefaultProjectID: projectID,
		Enabled:          gophercloud.Enabled,
		Password:         passwd,
	}

	user, err := users.Create(identityClient, createOpts).Extract()
	if err != nil {
		return "", err
	}

	//TODO set not expire
	expiresAt := time.Date(2019, 12, 1, 14, 0, 0, 999999999, time.UTC)
	trustcreateOpts := trusts.CreateOpts{
		ExpiresAt:         &expiresAt,
		Impersonation:     true,
		AllowRedelegation: true,
		ProjectID:         "9b71012f5a4a4aef9193f1995fe159b2",
		Roles: []trusts.Role{
			{
				Name: "member",
			},
		},
		TrusteeUserID:     user.ID,
		TrustorUserID:     userID,
		RedelegationCount: 0,
	}

	trust, err := trusts.Create(identityClient, trustcreateOpts).Extract()
	if err != nil {
		return "", err
	}

	fmt.Printf("Trust: %+v\n", trust)

	return trust.ID, nil

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
