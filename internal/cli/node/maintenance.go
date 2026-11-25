package node

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newMaintenanceCmd builds the `pmx pve node maintenance` sub-tree, which turns
// the node's HA maintenance mode on and off (PUT /nodes/{node}/maintenance).
func newMaintenanceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "maintenance",
		Short: "Enable or disable maintenance mode on a node",
		Long: "Put the resolved node into HA maintenance mode, or take it out again. While a node " +
			"is in maintenance mode, the high availability stack moves its HA-managed guests to " +
			"other nodes and keeps new ones away, so the node can be rebooted or serviced. " +
			"Guests that are not managed by HA are not moved.",
		Example: `  pmx pve node maintenance enable --node pve1 --yes
  pmx pve node maintenance disable --node pve1`,
	}
	cmd.AddCommand(
		newMaintenanceEnableCmd(),
		newMaintenanceDisableCmd(),
	)
	return cmd
}

// newMaintenanceEnableCmd builds `pmx pve node maintenance enable`.
func newMaintenanceEnableCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Put the node into maintenance mode",
		Long: "Put the resolved node into HA maintenance mode. The high availability stack then " +
			"migrates the HA-managed guests off the node, which loads the other nodes and " +
			"interrupts the services that cannot migrate live, so the command asks for " +
			"confirmation. On a terminal it prompts, and anywhere else it refuses unless --yes/-y " +
			"is passed. Maintenance mode needs Sys.Modify on /.",
		Example: `  pmx pve node maintenance enable --node pve1 --yes`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			ok, err := cli.ConfirmDestructive(cmd, yes,
				fmt.Sprintf("enable maintenance mode on node %q", deps.Node),
				fmt.Sprintf("Enable maintenance mode on node %q? Its HA-managed guests are migrated away.", deps.Node))
			if err != nil {
				return err
			}
			if !ok {
				return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: "Aborted."}, deps.Format)
			}
			return setMaintenance(cmd, deps, "enable", "enabled")
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the HA migration without prompting")
	return cmd
}

// newMaintenanceDisableCmd builds `pmx pve node maintenance disable`.
func newMaintenanceDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable",
		Short: "Take the node out of maintenance mode",
		Long: "Take the resolved node out of HA maintenance mode, so the high availability stack " +
			"can place HA-managed guests on it again, according to their HA rules. " +
			"Maintenance mode needs Sys.Modify on /.",
		Example: `  pmx pve node maintenance disable --node pve1`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			return setMaintenance(cmd, deps, "disable", "disabled")
		},
	}
}

// setMaintenance sends the maintenance-mode value, which the API names enable
// or disable, and reports the result in the past tense.
func setMaintenance(cmd *cobra.Command, deps *cli.Deps, mode, done string) error {
	params := &nodes.UpdateMaintenanceParams{MaintenanceMode: mode}
	if err := deps.API.Nodes.UpdateMaintenance(cmd.Context(), deps.Node, params); err != nil {
		return fmt.Errorf("%s maintenance mode on node %q: %w", mode, deps.Node, err)
	}
	return deps.Out.Render(cmd.OutOrStdout(),
		output.Result{Message: fmt.Sprintf("Maintenance mode %s on node %q.", done, deps.Node)}, deps.Format)
}
