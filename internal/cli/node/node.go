package node

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// Group builds the `pmx pve node` command and all of its sub-commands.
// The *cli.Deps argument is a placeholder used only so cobra can build the
// command tree; live dependencies are obtained inside each RunE via
// cli.GetDeps.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Manage Proxmox VE nodes",
		Long: "Manage individual Proxmox VE nodes. Requires a configured Proxmox VE API " +
			"connection; the ssh, shell, console, and rsync verbs also need SSH access to " +
			"the node itself.\n\n" +
			"Inspecting and operating\n" +
			"  list, status         what nodes exist and how they are doing\n" +
			"  config, hardware     node configuration and hardware inventory\n" +
			"  reboot, shutdown     power control\n" +
			"  task, services       node-scoped tasks and system services\n" +
			"  vzdump               backups taken from this node\n\n" +
			"Reaching the node\n" +
			"  ssh, shell, console   interactive sessions\n" +
			"  exec, rsync           run a command, copy files\n\n" +
			"Configuration\n" +
			"  network, netstat, firewall   networking and rules\n" +
			"  disks, scan, ceph            storage and the Ceph cluster\n" +
			"  apt                          packages and repositories\n" +
			"  certificates                 TLS material\n" +
			"  dns, hosts, time, syslog     system settings\n\n" +
			"The node argument convention is mixed. Introspection and session commands " +
			"(status, ssh, shell, console, rsync, task, services) take the node name as a " +
			"positional argument. Most configuration commands (config, firewall, network, " +
			"disks, apt, ceph, hardware) act on the node chosen by --node, PMX_NODE, or the " +
			"active context's default.\n\n" +
			"Anything that submits a PVE task, such as vzdump, the bulk startall and stopall " +
			"and migrateall verbs, or apt update, blocks until that task completes. Pass the " +
			"global --async flag to print the task UPID and return immediately.",
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
