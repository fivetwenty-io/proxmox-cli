package apiclient

import (
	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/access"
	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/clusterstorage"
	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/pools"
	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/storage"
	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/tasks"
	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/version"
	pve "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/client"
)

// APIClient holds all 8 service handles plus the raw pve client.
// It is constructed once in the cobra root PersistentPreRunE and passed via
// cobra.Command context to every sub-command.
type APIClient struct {
	// Raw is the underlying pve-apiclient-go client.
	Raw pve.Client

	// Access is the /access namespace service.
	Access access.Service

	// Cluster is the /cluster namespace service.
	Cluster cluster.Service

	// ClusterStorage is the /cluster/storage namespace service.
	ClusterStorage clusterstorage.Service

	// Nodes is the /nodes namespace service.
	Nodes nodes.Service

	// Pools is the /pools namespace service.
	Pools pools.Service

	// Storage is the node-scoped storage service (multipart file upload and
	// volume operations on /nodes/{node}/storage/{storage}).
	Storage storage.Service

	// Tasks is the task-wait helper service.
	Tasks tasks.Service

	// Version is the /version namespace service.
	Version version.Service
}

// NewAPIClient constructs an APIClient from a pre-built pve.Options.
// It creates the raw pve.Client and wires all 8 service handles; no network
// calls are made during construction.
//
// pve.NewClient applies opts.CSRFToken to the authenticator at construction
// time, so a ticket-authenticated client with a configured CSRF token already
// carries the CSRFPreventionToken header on non-GET requests without further
// action here.
func NewAPIClient(opts pve.Options) (*APIClient, error) {
	raw, err := pve.NewClient(opts)
	if err != nil {
		return nil, err
	}

	return &APIClient{
		Raw:            raw,
		Access:         access.New(raw),
		Cluster:        cluster.New(raw),
		ClusterStorage: clusterstorage.New(raw),
		Nodes:          nodes.New(raw),
		Pools:          pools.New(raw),
		Storage:        storage.New(raw),
		Tasks:          tasks.New(raw),
		Version:        version.New(raw),
	}, nil
}
