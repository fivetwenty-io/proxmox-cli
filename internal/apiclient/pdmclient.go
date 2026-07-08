package apiclient

import (
	"log/slog"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm"
	pdmaccess "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/access"
	pdmautoinstall "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/autoinstall"
	pdmceph "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/ceph"
	pdmconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/config"
	pdmnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/nodes"
	pdmpbs "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/pbs"
	pdmping "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/ping"
	pdmpve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/pve"
	pdmremotes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/remotes"
	pdmresources "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/resources"
	pdmsdn "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/sdn"
	pdmsubscriptions "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/subscriptions"
	pdmversion "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/version"
)

// PDMClient holds all 13 PDM service handles plus the raw client shared with
// the PVE-side APIClient. It is constructed once in the cobra root
// PersistentPreRunE (for pdm-flavored contexts) and passed via cobra.Command
// context to every sub-command under "pmx pdm ...".
//
// Package aliases below are prefixed with "pdm" because the PDM bindings
// reuse PVE-shaped package names (access, config, nodes, ping, version) that
// would otherwise collide with the PVE-side imports used elsewhere in this
// package.
type PDMClient struct {
	// Raw is the underlying proxmox-apiclient-go client, configured for a PDM
	// endpoint (port 8443, PDMAPIToken/PDMAuthCookie credential names).
	Raw pve.Client

	// Access is the /access namespace service (tickets, users, ACLs, TFA,
	// OpenID/realm domains).
	Access pdmaccess.Service

	// AutoInstall is the /auto-install namespace service (automated
	// installation answer files, tokens, prepared configurations).
	AutoInstall pdmautoinstall.Service

	// Ceph is the /ceph namespace service (registered Ceph cluster status,
	// pools, MDS/MGR/MON/OSD listings).
	Ceph pdmceph.Service

	// Config is the /config namespace service (realm, ACME, certificate,
	// notes, and view configuration).
	Config pdmconfig.Service

	// Nodes is the /nodes namespace service (PDM's own node status, APT,
	// certificates, network, tasks).
	Nodes pdmnodes.Service

	// Pbs is the /pbs namespace service (managed PBS remotes and their
	// datastores, nodes, tasks).
	Pbs pdmpbs.Service

	// Ping is the /ping namespace service.
	Ping pdmping.Service

	// Pve is the /pve namespace service (managed PVE remotes and their
	// guests, nodes, storage, tasks).
	Pve pdmpve.Service

	// Remotes is the /remotes namespace service (remote registration,
	// version, tasks, metrics).
	Remotes pdmremotes.Service

	// Resources is the /resources namespace service (aggregated resource
	// listing and status across all managed remotes).
	Resources pdmresources.Service

	// Sdn is the /sdn namespace service (aggregated SDN controllers, VNets,
	// zones across managed remotes).
	Sdn pdmsdn.Service

	// Subscriptions is the /subscriptions namespace service (subscription
	// key pool management).
	Subscriptions pdmsubscriptions.Service

	// Version is the /version namespace service.
	Version pdmversion.Service
}

// NewPDMClient constructs a PDMClient from a pre-built pve.Options.
// It applies the PDM wire-protocol defaults (port 8443, PDMAPIToken,
// PDMAuthCookie — see pdm.DefaultOptions) to any zero-valued field of opts,
// creates the raw client, and wires all 13 service handles; no network calls
// are made during construction.
func NewPDMClient(opts pve.Options) (*PDMClient, error) {
	raw, err := pdm.NewClient(opts)
	if err != nil {
		return nil, err
	}

	return &PDMClient{
		Raw:           raw,
		Access:        pdmaccess.New(raw),
		AutoInstall:   pdmautoinstall.New(raw),
		Ceph:          pdmceph.New(raw),
		Config:        pdmconfig.New(raw),
		Nodes:         pdmnodes.New(raw),
		Pbs:           pdmpbs.New(raw),
		Ping:          pdmping.New(raw),
		Pve:           pdmpve.New(raw),
		Remotes:       pdmremotes.New(raw),
		Resources:     pdmresources.New(raw),
		Sdn:           pdmsdn.New(raw),
		Subscriptions: pdmsubscriptions.New(raw),
		Version:       pdmversion.New(raw),
	}, nil
}

// SetSlogLogger installs an *slog.Logger as the PDM client's HTTP logger and
// opts into the library's redaction controls, reusing the same adapter and
// redaction configuration as APIClient.SetSlogLogger. It is a no-op when l is
// nil. The returned bool reports whether a logger was installed.
func (pc *PDMClient) SetSlogLogger(l *slog.Logger) bool {
	adapter := newSlogAdapter(l)
	if adapter == nil {
		return false
	}
	pc.Raw.SetLogger(adapter)
	pc.Raw.SetLogConfig(pve.LogConfig{
		Enabled:           true,
		RedactHeaders:     []string{"authorization", "cookie", "csrfpreventiontoken"},
		RedactParams:      []string{"password", "token", "secret"},
		LogRequestHeader:  false,
		LogResponseHeader: false,
		LogBody:           false,
		LogResponseBody:   false,
	})
	return true
}
