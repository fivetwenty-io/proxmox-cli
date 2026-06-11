package context

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

// resolveDeps is the package-level indirection over cli.GetDeps so that tests
// can inject a pre-built *cli.Deps without driving root PersistentPreRunE.
var resolveDeps = cli.GetDeps

func init() {
	cli.RegisterGroup(newContextCmd)
	cli.RegisterGroup(newCtxAliasCmd)
}

// newContextCmd builds `pve context` and attaches all sub-commands.
// The passed *cli.Deps is a placeholder for command-tree assembly; live deps
// are resolved per-invocation via resolveDeps (cli.GetDeps).
func newContextCmd(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage named pve contexts",
		Long: `Manage named Proxmox VE contexts stored in the pve config file.

A context records a target host, port, protocol, realm, authentication
credentials, and optional per-context defaults for --node and --output.

All context verbs operate on the local config file and never contact the
Proxmox VE API.`,
	}
	addSubcommands(cmd)
	return cmd
}

// newCtxAliasCmd builds the hidden `pve ctx` alias for the context group.
func newCtxAliasCmd(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "ctx",
		Short:  "Alias for 'pve context'",
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
		newEditCmd(),
		newValidateCmd(),
	)
}
