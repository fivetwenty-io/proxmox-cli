package node

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
)

// Group builds the `pmx node` command and all of its sub-commands.
// The *cli.Deps argument is a placeholder used only so cobra can build the
// command tree; live dependencies are obtained inside each RunE via
// cli.GetDeps.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Manage Proxmox VE nodes",
		Long: `Manage individual Proxmox VE nodes: status, configuration, storage disks,
network, firewall rules, certificates, APT packages, Ceph, hardware, and
system settings (DNS, hosts, time, syslog). Also opens SSH, shell, console,
and rsync sessions to a node, and lists or manages node-scoped tasks and
services. Requires a configured Proxmox VE API connection; ssh/shell/console/
rsync additionally require SSH access to the node.

The node argument convention is mixed: introspection and session commands
(status, ssh, shell, console, rsync, task, services) take the node name as a
positional argument, while most configuration commands (config, firewall,
network, disks, apt, ceph, hardware) operate on the node selected via the
--node flag, PMX_NODE, or the active context's default node. Actions that
submit a PVE task (vzdump, bulk startall/stopall/migrateall, apt update, and
similar) block until the task completes; pass the global --async flag to
print the task UPID immediately instead of waiting.`,
		Example: `  pmx pve node list
  pmx pve node status pve1
  pmx pve node ssh pve1
  pmx pve node task list pve1`,
	}

	cmd.AddCommand(
		newListCmd(),
		newStatusCmd(),
		newNodeConfigCmd(),
		newRebootCmd(),
		newShutdownCmd(),
		newSSHCmd(),
		newShellCmd(),
		newConsoleCmd(),
		newRsyncCmd(),
		newExecCmd(),
		newTaskCmd(),
		newServicesCmd(),
		newVzdumpCmd(),
		newFirewallCmd(),
		newNetworkCmd(),
		newNetstatCmd(),
		newRrddataCmd(),
		newQueryUrlMetadataCmd(),
		newAptCmd(),
		newDisksCmd(),
		newScanCmd(),
		newHardwareCmd(),
		newDnsCmd(),
		newHostsCmd(),
		newTimeCmd(),
		newSyslogCmd(),
		newJournalCmd(),
		newReportCmd(),
		newSubscriptionCmd(),
		newCertCmd(),
		newReplicationCmd(),
		newCephCmd(),
		newOciCmd(),
		newCapabilitiesCmd(),
		newStartallCmd(),
		newStopallCmd(),
		newSuspendallCmd(),
		newMigrateallCmd(),
		newWakeonlanCmd(),
		newNodeExecuteCmd(),
		newTermproxyCmd(),
		newVncshellCmd(),
		newSpiceshellCmd(),
		newPermissionsCmd(),
	)

	return cmd
}
