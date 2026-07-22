// Package persona maps a resolved persona name to the ordered group-factory
// slice defining that persona's command surface. It is the single source of
// truth shared by the shipping binary (cmd/pmx) and the man-page generator
// (cmd/docgen).
package persona

import (
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/api"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/context"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/initcmd"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/lab"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/logs"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/pbs"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/pdm"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/pve"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/remote"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/version"
)

// Shared returns the product-neutral factories present under every persona.
func Shared() []cli.GroupFactory {
	return []cli.GroupFactory{
		context.Group, context.CtxAlias, initcmd.Group,
		api.Auth, api.Group, version.Group, logs.Group, remote.SSH, remote.Rsync,
	}
}

// Factories returns the ordered factory slice for the given persona
// ("pmx", "pve", "pbs", or "pdm"; see cli.Persona).
func Factories(name string) []cli.GroupFactory {
	f := Shared()
	switch name {
	case "pve":
		return append(f, pve.ChildFactories()...)
	case "pbs":
		return append(f, pbs.ChildFactories()...)
	case "pdm":
		return append(f, pdm.ChildFactories()...)
	default:
		return append(f, pve.Group, lab.Group, pbs.Group, pdm.Group)
	}
}

// Names lists every persona the CLI recognises, pmx (the superset) first.
func Names() []string { return []string{"pmx", "pve", "pbs", "pdm"} }
