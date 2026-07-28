package lab

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// Group builds the `pmx lab` command and all of its sub-commands.
// The passed *cli.Deps is a placeholder used only so cobra can assemble the
// command tree; live dependencies are resolved per-invocation via cli.GetDeps.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lab",
		Short: "Manage per-member nested lab environments",
		Long: "Manage per-member nested lab environments running inside a Proxmox VE cluster: " +
			"each lab's SDN network, VMs, storage, DNS zone, access grants, and ZFS quota.\n\n" +
			"Labs are config-driven, resolved from the labs/labs_dir/include keys in " +
			"~/.config/pmx/config.yml (see `pmx lab config`). Most mutating verbs accept flags " +
			"that override individual config fields for a single invocation.\n\n" +
			"Lifecycle\n" +
			"  create    provision a lab's network, storage, pool, and VMs\n" +
			"  list      show every lab beside its live node-0 VM state\n" +
			"  status    break one lab down per node (and its QDevice VM)\n" +
			"  start     start a lab's VMs, node 0 first\n" +
			"  stop      power a lab's VMs off, in reverse order\n" +
			"  destroy   delete a lab's VMs, optionally its pool and storage\n\n" +
			"On the outer cluster\n" +
			"  net apply      reconcile the lab's outer SDN zone, vnet, subnet\n" +
			"  access grant   give a pve user pool-scoped access to a lab\n" +
			"  quota set      set the lab's ZFS refquota (no PVE API for it)\n" +
			"  config         scaffold and inspect lab definitions on disk\n" +
			"  context sync   register the `lab-<name>` context and its token\n\n" +
			"Inside a multi-node lab (topology.nodes > 1)\n" +
			"  cluster init/join/status   form and inspect the nested cluster\n" +
			"  qdevice add/remove         wire up the corosync tie-breaker\n" +
			"  hostnet apply              reconcile nested bonds and bridges\n" +
			"  sdn apply                  reconcile the inner VXLAN zone\n" +
			"  nfs attach/status/detach   register the shared NFS exports\n" +
			"  scale --nodes N            migrate the lab to a new node count\n\n" +
			"The cluster, qdevice, sdn, and nfs verbs run entirely over ssh into the lab " +
			"guests' own mgmt IPs, never against the outer Proxmox VE API. `hostnet apply` " +
			"talks to the nested cluster's own API instead, and `scale` drives all of them, " +
			"plus outer-API VM creation, in the right order.\n\n" +
			"Run `pmx lab <verb> --help` for the details of any one verb.",
		Example: `  pmx lab create wayne --node sm-0
  pmx lab status wayne
  pmx lab list
  pmx lab config add drgao --vxlan-tag 110 --cidr 10.10.2.0/24
  pmx lab net apply wayne
  pmx lab access grant wayne wayne@pve
  pmx lab quota set wayne --refquota-gb 600
  pmx lab destroy wayne --yes
  pmx lab cluster init wayne
  pmx lab cluster join wayne --node 1
  pmx lab qdevice add wayne
  pmx lab sdn apply wayne
  pmx lab nfs attach wayne
  pmx lab scale wayne --nodes 3`,
		Annotations: map[string]string{cli.ProductAnnotation: config.ProductPVE},
	}

	cmd.AddCommand(
		newCreateCmd(),
		newDestroyCmd(),
		newListCmd(),
		newStatusCmd(),
		newStartCmd(),
		newStopCmd(),
		newNetCmd(),
		newAccessCmd(),
		newQuotaCmd(),
		newConfigCmd(),
		newContextCmd(),
		newClusterCmd(),
		newQdeviceCmd(),
		newSdnCmd(),
		newNfsCmd(),
		newScaleCmd(),
		newHostnetCmd(),
	)

	return cmd
}
