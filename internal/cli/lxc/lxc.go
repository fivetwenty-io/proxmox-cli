package lxc

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// Group builds the `pmx pve lxc` command and all of its sub-commands.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lxc",
		Short: "Manage LXC containers",
		Long: "Manage LXC containers on a Proxmox VE cluster. Requires a configured Proxmox " +
			"VE API connection.\n\n" +
			"Lifecycle\n" +
			"  create, clone, template   bring a container into being\n" +
			"  start, stop, reboot       run it, with shutdown/suspend/resume\n" +
			"  migrate, remote-migrate   move it to another node or cluster\n" +
			"  delete                    remove it\n\n" +
			"Inspecting\n" +
			"  list, status   what exists and what state it is in\n" +
			"  config         read and change configuration options\n" +
			"  metrics, rrd   current and historical resource use\n" +
			"  feature        ask what the container's storage supports\n\n" +
			"Composition\n" +
			"  disk         attach, resize, move, and detach volumes\n" +
			"  snapshot     take, list, roll back to, and delete snapshots\n" +
			"  interfaces   the container's network interfaces\n\n" +
			"Access and hardening\n" +
			"  console       reach the container\n" +
			"  firewall      rules, aliases, ipsets, and options\n" +
			"  security      privilege level, features, capabilities\n" +
			"  permissions   ACL entries on the container's path\n" +
			"  hookscript    the host-side lifecycle hook\n\n" +
			"Commands take a container by numeric vmid or by name. Pass --node when the " +
			"container's node cannot be resolved from the cluster on its own.\n\n" +
			"Anything that submits a PVE task, create and clone and migrate and delete and " +
			"start and stop among them, blocks until that task completes. Pass --async to " +
			"print the task UPID and return immediately.",
		Example: `  pmx pve lxc list
  pmx pve lxc start 200
  pmx pve lxc migrate 200 --target-node pve2 --restart
  pmx pve lxc snapshot create 200 pre-upgrade`,
	}

	cmd.AddCommand(
		newListCmd(),
		newStatusCmd(),
		newConfigCmd(),
		newHookscriptCmd(),
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

// resolveGuestSource maps a migration source <vmid|name> to its VMID and the
// node the container actually runs on. Unlike resolveGuest, an ambient default
// node (PMX_NODE or the context default-node) is not trusted as the container's
// location; the cluster inventory is consulted unless --node was passed
// explicitly. When the node was auto-resolved (not pinned by an explicit
// --node), a note is printed to stderr naming the resolved node, so the
// operator can see where the migration is about to run. See
// cli.ResolveGuestSource for the full semantics.
func resolveGuestSource(cmd *cobra.Command, deps *cli.Deps, target string) (vmid, node string, err error) {
	nodeExplicit := cmd.Flags().Changed("node")
	vmid, node, err = cli.ResolveGuestSource(cmd.Context(), deps, target, cli.GuestLXC, nodeExplicit)
	if err == nil && !nodeExplicit {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "note: auto-resolved source node %q for container %s\n", node, vmid)
	}
	return vmid, node, err
}
