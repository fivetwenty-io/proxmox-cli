package apiclient

import (
	"log/slog"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs"
	pbsaccess "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/access"
	pbsadmin "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/admin"
	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
	pbsnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/nodes"
	pbsping "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/ping"
	pbspull "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/pull"
	pbspush "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/push"
	pbsstatus "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/status"
	pbstape "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/tape"
	pbsversion "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/version"
)

// PBSClient holds all 10 PBS service handles plus the raw client shared with
// the PVE-side APIClient. It is constructed once in the cobra root
// PersistentPreRunE (for pbs-flavored contexts) and passed via cobra.Command
// context to every sub-command under "pmx pbs ...".
//
// Package aliases below are prefixed with "pbs" because the PBS bindings
// reuse PVE-shaped package names (access, config, nodes, status, version)
// that would otherwise collide with the PVE-side imports used elsewhere in
// this package.
type PBSClient struct {
	// Raw is the underlying proxmox-apiclient-go client, configured for a PBS
	// endpoint (port 8007, PBSAPIToken/PBSAuthCookie credential names).
	Raw pve.Client

	// Access is the /access namespace service (tickets, users, ACLs, TFA).
	Access pbsaccess.Service

	// Admin is the /admin namespace service (datastore administration:
	// prune, gc, verify, namespaces, snapshots).
	Admin pbsadmin.Service

	// Config is the /config namespace service (datastore, ACME, access,
	// metrics, notification, media pool and drive/changer configuration).
	Config pbsconfig.Service

	// Nodes is the /nodes namespace service.
	Nodes pbsnodes.Service

	// Status is the /status namespace service (datastore usage, metrics).
	Status pbsstatus.Service

	// Tape is the /tape namespace service (tape backup, restore, media,
	// drive and changer operations).
	Tape pbstape.Service

	// Version is the /version namespace service.
	Version pbsversion.Service

	// Ping is the /ping namespace service.
	Ping pbsping.Service

	// Pull is the /pull namespace service (sync jobs, pull direction).
	Pull pbspull.Service

	// Push is the /push namespace service (sync jobs, push direction).
	Push pbspush.Service
}

// NewPBSClient constructs a PBSClient from a pre-built pve.Options.
// It applies the PBS wire-protocol defaults (port 8007, PBSAPIToken,
// PBSAuthCookie — see pbs.DefaultOptions) to any zero-valued field of opts,
// creates the raw client, and wires all 10 service handles; no network calls
// are made during construction.
func NewPBSClient(opts pve.Options) (*PBSClient, error) {
	raw, err := pbs.NewClient(opts)
	if err != nil {
		return nil, err
	}

	return &PBSClient{
		Raw:     raw,
		Access:  pbsaccess.New(raw),
		Admin:   pbsadmin.New(raw),
		Config:  pbsconfig.New(raw),
		Nodes:   pbsnodes.New(raw),
		Status:  pbsstatus.New(raw),
		Tape:    pbstape.New(raw),
		Version: pbsversion.New(raw),
		Ping:    pbsping.New(raw),
		Pull:    pbspull.New(raw),
		Push:    pbspush.New(raw),
	}, nil
}

// SetSlogLogger installs an *slog.Logger as the PBS client's HTTP logger and
// opts into the library's redaction controls, reusing the same adapter and
// redaction configuration as APIClient.SetSlogLogger. It is a no-op when l is
// nil. The returned bool reports whether a logger was installed.
func (pc *PBSClient) SetSlogLogger(l *slog.Logger) bool {
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
