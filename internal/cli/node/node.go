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
		Long:  "List nodes, inspect node status, open SSH/rsync sessions, and manage node tasks and services.",
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
