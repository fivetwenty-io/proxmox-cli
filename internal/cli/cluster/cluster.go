package cluster

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// Group builds the `pmx pve cluster` command and its sub-commands.
// The *cli.Deps argument is a placeholder used only so cobra can build the
// command tree; live dependencies are obtained inside each RunE via cli.GetDeps.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Inspect Proxmox VE cluster state",
		Long: `Inspect and manage Proxmox VE cluster-wide state: quorum status, cluster
resources, the cluster log, recent tasks, and the next free guest ID.
Configure cluster-wide backup jobs, HA groups/rules/resources, firewall
rules, resource mappings, replication jobs, metric servers, notification
targets, ACME accounts, CPU models, and bulk guest actions across every node.
Requires a configured Proxmox VE API connection.

Sub-commands take whatever identifier the resource uses (an HA sid, mapping
ID, job ID, or node name); no --node flag is needed since these operate
cluster-wide. Actions that submit a PVE task (backup jobs, bulk start/
shutdown/migrate) block until the task completes; pass the global --async
flag to print the task UPID immediately instead of waiting.`,
		Example: `  pmx pve cluster status
  pmx pve cluster resources --type vm
  pmx pve cluster next-id
  pmx pve cluster log --max 20`,
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
		newClusterQemuCmd(),
	)

	return cmd
}
