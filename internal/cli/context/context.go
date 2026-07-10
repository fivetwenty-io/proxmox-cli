package context

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
)

// contextLong is the shared Long help for the context group and its hidden
// ctx alias, extracted so the two cannot drift apart.
const contextLong = `Manage named Proxmox contexts stored in the pmx config file.

A context records a target host, port, protocol, realm, authentication
credentials, optional per-context defaults for --node and --output, and the
product it targets: pve (Proxmox VE, port 8006), pbs (Proxmox Backup Server,
port 8007), or pdm (Proxmox Datacenter Manager, port 8443).

The persona binaries (pve, pbs, pdm) share this config: every context is
visible under every binary, and selecting a context whose product differs
from the binary warns without blocking.

All context verbs operate on the local config file and never contact a
Proxmox API; 'context validate --connect' is the one exception, probing the
configured endpoint live.`

// Group builds `pmx context` and attaches all sub-commands.
// The passed *cli.Deps is a placeholder for command-tree assembly; live deps
// are resolved per-invocation via cli.GetDeps.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage named Proxmox contexts",
		Long:  contextLong,
	}
	addSubcommands(cmd)
	return cmd
}

// CtxAlias builds the hidden `pmx ctx` alias for the context group.
func CtxAlias(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "ctx",
		Short:  "Alias for 'pmx context'",
		Long:   contextLong,
		Hidden: true,
	}
	addSubcommands(cmd)
	return cmd
}

// addSubcommands attaches all verb commands implemented in this package.
func addSubcommands(parent *cobra.Command) {
	parent.AddCommand(
		newAddCmd(),
		newLsCmd(),
		newShowCmd(),
		newSelectCmd(),
		newPreviousCmd(),
		newRmCmd(),
		newCopyCmd(),
		newRenameCmd(),
		newEditCmd(),
		newValidateCmd(),
	)
}
