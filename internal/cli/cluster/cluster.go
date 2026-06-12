package cluster

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

func init() {
	cli.RegisterGroup(newClusterCmd)
}

// newClusterCmd builds the `pve cluster` command and its sub-commands.
// The *cli.Deps argument is a placeholder used only so cobra can build the
// command tree; live dependencies are obtained inside each RunE via cli.GetDeps.
func newClusterCmd(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Inspect Proxmox VE cluster state",
		Long: "Show cluster quorum status, list cluster-wide resources, read the cluster log, " +
			"list recent tasks, and obtain the next free guest ID.",
	}

	cmd.AddCommand(
		newStatusCmd(),
		newResourcesCmd(),
		newNextIDCmd(),
		newLogCmd(),
		newTasksCmd(),
		newBackupCmd(),
		newClusterBackupInfoCmd(),
		newHaCmd(),
		newFirewallCmd(),
		newOptionsCmd(),
		newConfigCmd(),
		newReplicationCmd(),
		newMetricsCmd(),
		newNotificationsCmd(),
		newMappingCmd(),
		newJobsCmd(),
		newAcmeCmd(),
		newCephCmd(),
		newBulkCmd(),
		newCpuModelCmd(),
	)

	return cmd
}
