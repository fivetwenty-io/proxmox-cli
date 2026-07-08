// Command pmx is the Proxmox CLI binary.
// All logic lives in internal/cli; this file is a thin OS entry point.
package main

import (
	"os"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/api"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/context"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/initcmd"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/pbs"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/pdm"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/pve"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/remote"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/version"
)

// sharedFactories are the product-neutral commands present under every persona.
func sharedFactories() []cli.GroupFactory {
	return []cli.GroupFactory{
		context.Group, context.CtxAlias, initcmd.Group,
		api.Auth, api.Group, version.Group, remote.SSH, remote.Rsync,
	}
}

// factoriesFor returns the ordered factory slice for the given persona.
func factoriesFor(persona string) []cli.GroupFactory {
	f := sharedFactories()
	switch persona {
	case "pve":
		return append(f, pve.ChildFactories()...)
	case "pbs":
		return append(f, pbs.ChildFactories()...)
	case "pdm":
		return append(f, pdm.ChildFactories()...)
	default:
		return append(f, pve.Group, pbs.Group, pdm.Group)
	}
}

func main() {
	persona := cli.Persona(os.Args[0])
	os.Exit(cli.Main(persona, factoriesFor(persona)))
}
