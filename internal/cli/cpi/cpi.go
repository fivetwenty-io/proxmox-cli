// Package cpi implements the `pmx cpi` command group: read-only audits of
// the Proxmox VE BOSH CPI's resources.
//
// The CPI tracks persistent disks through guest configs and through
// <!--BOSH:{...}--> sentinels in VM descriptions, and parks a detached disk on
// a dedicated parker VM. The verbs here read that state back and classify it,
// so an operator can see which volumes are held, which are parked, and which
// no guest holds before deciding whether to delete anything.
package cpi

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// Group builds the `pmx cpi` command and its sub-commands. The passed
// *cli.Deps is a placeholder used only so cobra can assemble the command
// tree; live dependencies are resolved per-invocation via cli.GetDeps.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cpi",
		Short: "Commands for the Proxmox VE BOSH CPI's resources",
		Long: "Commands for the Proxmox VE BOSH CPI's resources. Requires a context with product: pve " +
			"and a configured Proxmox VE API connection.\n\n" +
			"  disk-audit   classify every CPI-managed persistent disk as attached, parked, or " +
			"free-floating, and report parker VMs and volumes more than one guest names\n\n" +
			"Every verb here is read-only: it issues GET requests and changes nothing in the cluster.",
		Example: `  pmx cpi disk-audit
  pmx cpi disk-audit -o json`,
	}
	cmd.AddCommand(newDiskAuditCmd())
	return cmd
}
