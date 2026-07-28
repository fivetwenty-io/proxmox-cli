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
		Long: "Inspect and manage Proxmox VE cluster-wide state. Requires a configured " +
			"Proxmox VE API connection.\n\n" +
			"Inspecting\n" +
			"  status, resources   quorum, nodes, and everything running\n" +
			"  log, tasks          the cluster log and recent tasks\n" +
			"  next-id             the next free guest ID\n\n" +
			"Scheduled and automated work\n" +
			"  backup        cluster-wide backup jobs\n" +
			"  replication   replication jobs between nodes\n" +
			"  ha            HA groups, rules, and resources\n" +
			"  bulk          start, shutdown, migrate across nodes\n\n" +
			"Cluster-wide configuration\n" +
			"  firewall             rules, aliases, ipsets, options\n" +
			"  notifications        targets, matchers, endpoints\n" +
			"  metrics              external metric servers\n" +
			"  mapping              PCI and USB resource mappings\n" +
			"  acme, cpu-models     certificates and CPU definitions\n\n" +
			"Sub-commands take whatever identifier the resource itself uses: an HA sid, a " +
			"mapping ID, a job ID, a node name. No --node flag is needed, since all of this " +
			"is cluster-wide.\n\n" +
			"Anything that submits a PVE task, backup jobs and the bulk verbs among them, " +
			"blocks until that task completes. Pass the global --async flag to print the " +
			"task UPID and return immediately.",
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
