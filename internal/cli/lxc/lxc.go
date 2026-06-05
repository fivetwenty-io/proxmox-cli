package lxc

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

func init() {
	cli.RegisterGroup(newGroupCmd)
}

// getDeps resolves the live *cli.Deps for a command. It is a package-level
// variable so tests can substitute a Deps without a full root wiring; production
// uses cli.GetDeps which reads the value stashed by the root PersistentPreRunE.
var getDeps = cli.GetDeps

// newGroupCmd builds the `pve lxc` command and all of its sub-commands.
func newGroupCmd(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lxc",
		Short: "Manage LXC containers",
		Long:  "List, inspect, configure, and control the lifecycle of LXC containers on a node.",
	}

	cmd.AddCommand(
		newListCmd(),
		newStatusCmd(),
		newConfigCmd(),
		newCreateCmd(),
		newCloneCmd(),
		newMigrateCmd(),
		newDiskCmd(),
		newTemplateCmd(),
		newStartCmd(),
		newStopCmd(),
		newRebootCmd(),
		newShutdownCmd(),
		newSuspendCmd(),
		newResumeCmd(),
		newDeleteCmd(),
		newSnapshotCmd(),
	)
	return cmd
}

// resolveNode returns the node from deps (flag > env > config), erroring when no
// node could be determined for an operation that targets a specific node.
func resolveNode(deps *cli.Deps) (string, error) {
	if deps.Node == "" {
		return "", fmt.Errorf("no node specified: use --node, set PVE_NODE, or configure a default node")
	}
	return deps.Node, nil
}
