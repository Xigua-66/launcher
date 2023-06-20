package scope

import (
	"github.com/go-logr/logr"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/utils/openstack/clientconfig"
)

// Scope is used to initialize Services from Controllers and includes the
// common objects required for this.
//
// The Gophercloud ProviderClient and ClientOpts are required to create new
// Gophercloud API Clients (e.g. for Networking/Neutron).
//
// The Logger includes context values such as the cluster name.
type Scope struct {
	ProviderClient     *gophercloud.ProviderClient
	ProviderClientOpts *clientconfig.ClientOpts
	ProjectID          string
	UserID             string

	Logger logr.Logger
}
