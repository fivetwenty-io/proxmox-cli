package qemu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// Group builds the `pmx pve qemu` command and all of its sub-commands.
// The *cli.Deps argument is a placeholder used only so cobra can build the
// command tree; live dependencies are obtained inside each RunE via cli.GetDeps.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "qemu",
		Short: "Manage QEMU virtual machines",
		Long: `Manage QEMU/KVM virtual machines on Proxmox VE: lifecycle (create, start,
stop, migrate, clone, delete), configuration, disks, snapshots, the guest
agent, cloud-init, firewall rules, and console access. Requires a configured
Proxmox VE API connection.

Commands take a VM by numeric vmid or name; when the VM's node cannot be
resolved automatically from the cluster, pass --node. Actions that submit a
PVE task (create, clone, migrate, delete, start, stop, and similar) block
until the task completes; pass --async to print the task UPID immediately
instead of waiting.`,
		Example: `  pmx pve qemu list
  pmx pve qemu start 100
  pmx pve qemu migrate 100 --target-node pve2 --online
  pmx pve qemu snapshot create 100 pre-upgrade`,
	}

	cmd.AddCommand(
		newListCmd(),
		newStatusCmd(),
		newConfigCmd(),
		newCreateCmd(),
		newCloneCmd(),
		newMigrateCmd(),
		newDiskCmd(),
		newFirewallCmd(),
		newSecurityCmd(),
		newPermissionsCmd(),
		newConsoleCmd(),
		newSSHCmd(),
		newAgentCmd(),
		newCloudinitCmd(),
		newTemplateCmd(),
		newStartCmd(),
		newStopCmd(),
		newRebootCmd(),
		newShutdownCmd(),
		newResetCmd(),
		newSuspendCmd(),
		newResumeCmd(),
		newDeleteCmd(),
		newSnapshotCmd(),
		newMetricsCmd(),
		newRrdCmd(),
		newFeatureCmd(),
		newMonitorCmd(),
		newSendkeyCmd(),
		newRemoteMigrateCmd(),
		newCpuCmd(),
		newMachineCmd(),
		newCpuFlagsCmd(),
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

// resolveGuest maps a <vmid|name> target to a numeric VMID and the node the VM
// runs on, auto-resolving the node from the cluster when it is not already known.
// See cli.ResolveGuest for the full lookup semantics.
func resolveGuest(ctx context.Context, deps *cli.Deps, target string) (vmid, node string, err error) {
	return cli.ResolveGuest(ctx, deps, target, cli.GuestQemu)
}

// finishAsync renders the outcome of an asynchronous task. When deps.Async is
// set it prints the UPID immediately; otherwise it blocks until the task
// completes and prints msg. The raw response carries the UPID JSON string.
func finishAsync(cmd *cobra.Command, deps *cli.Deps, raw json.RawMessage, msg string) error {
	upid, err := apiclient.UPIDFromRaw(raw)
	if err != nil {
		return err
	}

	if deps.Async {
		return deps.Out.Render(cmd.OutOrStdout(),
			output.Result{
				Single:  map[string]string{"upid": upid},
				Raw:     map[string]string{"upid": upid},
				Message: upid,
			}, deps.Format)
	}

	if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
		return err
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
}

// encodeSSHKeys percent-encodes cloud-init SSH keys for the PVE API. PVE
// uri_unescapes the value but does NOT treat '+' as a space, so spaces are
// encoded as %20 rather than '+'.
func encodeSSHKeys(keys string) string {
	return strings.ReplaceAll(url.QueryEscape(keys), "+", "%20")
}

// boolPtr returns a pointer to v.
func boolPtr(v bool) *bool { return &v }

// strPtr returns a pointer to v.
func strPtr(v string) *string { return &v }

// int64Ptr returns a pointer to v.
func int64Ptr(v int64) *int64 { return &v }
