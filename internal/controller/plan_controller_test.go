package controller

import (
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/servergroups"
	"testing"
)

func TestGetServerGroup(t *testing.T) {
	opts := gophercloud.AuthOptions{
		IdentityEndpoint: "http://keystone.openstack.svc.cluster.local/v3",
		TokenID:          "gAAAAABkmoWcyNeoytz7Wvb0Y_mqqh9ZP2_Bw582M_iw2ICnmbKTThll5ghl1U8NBhfo58OARSuDeIXain7pXS94UZK0JoMIMvwgv1JLn1tCtVSPLqVxzqDbZFmCZDTSU-G7MtesFHpcIxFtVnNkkalARtzUq1WE9Ps7Fh0qJyPN0yys2ruDkr8",
	}

	provider, err := openstack.AuthenticatedClient(opts)
	if err != nil {
		t.Error(err)
	}
	t.Log(provider)
	client, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{
		Region: "RegionOne",
	})
	t.Log(client)
	group, err := servergroups.Get(client, "0364a4bf-4ff4-4877-a423-d15d51c33200").Extract()
	if err != nil {
		t.Error(err)
	}
	t.Log(group)

}
