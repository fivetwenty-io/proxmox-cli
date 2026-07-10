package lxc

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
)

// Group builds the `pmx lxc` command and all of its sub-commands.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lxc",
		Short: "Manage LXC containers",
		Long: `Manage LXC containers on Proxmox VE: lifecycle (create, start, stop,
migrate, clone, delete), configuration, disks, snapshots, network interfaces,
firewall rules, and console access. Requires a configured Proxmox VE API
connection.

Commands take a container by numeric vmid or name; when the container's node
cannot be resolved automatically from the cluster, pass --node. Actions that
submit a PVE task (create, clone, migrate, delete, start, stop, and similar)
block until the task completes; pass --async to print the task UPID
immediately instead of waiting.`,
		Example: `  pmx pve lxc list
  pmx pve lxc start 200
  pmx pve lxc migrate 200 --target-node pve2 --restart
  pmx pve lxc snapshot create 200 pre-upgrade`,
	}

	cmd.AddCommand(
		newListCmd(),
		newStatusCmd(),
		newConfigCmd(),
		newCreateCmd(),
		newCloneCmd(),
		newMigrateCmd(),
		newRemoteMigrateCmd(),
		newDiskCmd(),
		newFirewallCmd(),
		newConsoleCmd(),
		newInterfacesCmd(),
		newTemplateCmd(),
		newStartCmd(),
		newStopCmd(),
		newRebootCmd(),
		newShutdownCmd(),
		newSuspendCmd(),
		newResumeCmd(),
		newDeleteCmd(),
		newSnapshotCmd(),
		newMetricsCmd(),
		newFeatureCmd(),
		newSecurityCmd(),
		newPermissionsCmd(),
		newRrdCmd(),
		newToTemplateCmd(),
	)
	return cmd
}

// resolveNode returns the node from deps (flag > env > config), erroring when no
// node could be determined for an operation that targets a specific node.
func resolveNode(deps *cli.Deps) (string, error) {
	if deps.Node == "" {
		return "", fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
	}
	return deps.Node, nil
}

// resolveGuest maps a <vmid|name> target to a numeric VMID and the node the
// container runs on, auto-resolving the node from the cluster when it is not
// already known. See cli.ResolveGuest for the full lookup semantics.
func resolveGuest(ctx context.Context, deps *cli.Deps, target string) (vmid, node string, err error) {
	return cli.ResolveGuest(ctx, deps, target, cli.GuestLXC)
}
