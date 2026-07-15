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
		Long: `Manage per-member nested lab environments running inside a Proxmox VE
cluster: each lab's SDN network, VM, storage, DNS zone, access grants, and
ZFS quota. Labs are config-driven, resolved from the labs/labs_dir/include
keys in ~/.config/pmx/config.yml (see 'pmx lab config'); most mutating verbs
accept flags that override individual config fields for a single invocation.

'pmx lab create' idempotently provisions a lab's shared SDN zone, its own
vnet and subnet, storage, resource pool, and VM, in that order, skipping
anything already in place; it does not commit the SDN changes it stages.
'pmx lab net apply' reconciles a lab's SDN zone/vnet/subnet against its
config, always previews the pending changeset, and applies it. 'pmx lab
access grant' grants a pve-realm user pool-scoped access to a lab. 'pmx lab
quota set' sets the lab's ZFS dataset refquota over ssh, since Proxmox VE
has no API for ZFS dataset properties. 'pmx lab destroy' stops and deletes a
lab's VM, optionally purging its resource pool and storage definition too.
'pmx lab list'/'status'/'start'/'stop' inspect and control a lab's VM
lifecycle, joining each configured lab against its live state by resource
pool membership.`,
		Example: `  pmx lab create wayne --node sm-0
  pmx lab status wayne
  pmx lab list
  pmx lab config add drgao --vxlan-tag 110 --cidr 10.10.2.0/24
  pmx lab net apply wayne
  pmx lab access grant wayne wayne@pve
  pmx lab quota set wayne --refquota-gb 600
  pmx lab destroy wayne --yes`,
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
	)

	return cmd
}
