// Package sdn implements the `pve sdn` command group: software-defined network
// zones, vnets, and subnets, plus the apply step that commits pending changes.
//
// PVE SDN configuration is staged: creating or deleting a zone, vnet, or subnet
// only edits the pending config. The changes take effect on the nodes only
// after `pve sdn apply` (PUT /cluster/sdn) reloads the network configuration.
package sdn

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

func init() {
	cli.RegisterGroup(newGroupCmd)
}

// newGroupCmd builds the `pve sdn` command and all of its sub-commands. The
// passed *cli.Deps is a placeholder used only so cobra can assemble the command
// tree; live dependencies are resolved per-invocation via cli.GetDeps.
func newGroupCmd(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sdn",
		Short: "Manage software-defined networking (zones, vnets, subnets)",
		Long: "List, create, and delete SDN zones, vnets, and subnets. Changes are " +
			"staged until committed with `pve sdn apply`.",
	}
	cmd.AddCommand(
		newApplyCmd(),
		newZoneCmd(),
		newVnetCmd(),
		newSubnetCmd(),
	)
	return cmd
}

// strPtr returns a pointer to v.
func strPtr(v string) *string { return &v }

// boolPtr returns a pointer to v.
func boolPtr(v bool) *bool { return &v }

// int64Ptr returns a pointer to v.
func int64Ptr(v int64) *int64 { return &v }
