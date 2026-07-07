package cluster

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newHaStatusCmd builds the `pve cluster ha status` sub-tree: HA manager state.
// It reads the HA status views and exposes the cluster-wide arm/disarm controls.
func newHaStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Inspect and control HA manager status",
		Long: "Read the HA status views (overall, current, and manager status) and arm or disarm the " +
			"cluster-wide HA stack. Arm and disarm change HA behavior across the whole cluster.",
	}
	cmd.AddCommand(
		newHaStatusCurrentCmd(),
		newHaStatusManagerCmd(),
		newHaStatusArmCmd(),
		newHaStatusDisarmCmd(),
	)
	return cmd
}

// renderHaStatusList unmarshals a raw HA-status list and renders it as a
// dynamic-column table preserving every field across heterogeneous entries.
func renderHaStatusList(cmd *cobra.Command, raws []json.RawMessage, what string) error {
	deps := cli.GetDeps(cmd)
	entries := make([]map[string]any, 0, len(raws))
	for _, r := range raws {
		var m map[string]any
		if err := json.Unmarshal(r, &m); err != nil {
			return fmt.Errorf("decode %s: %w", what, err)
		}
		entries = append(entries, m)
	}
	headers, rows := dynamicTable(entries)
	return deps.Out.Render(cmd.OutOrStdout(),
		output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
}

// newHaStatusCurrentCmd builds `pve cluster ha status current`. It answers to
// list/ls as well: GET /cluster/ha/status is only a directory index of the
// status views, so the current view is the overview.
func newHaStatusCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "current",
		Aliases: []string{"list", "ls"},
		Short:   "Show the current HA status of resources and nodes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListHaStatusCurrent(cmd.Context())
			if err != nil {
				return fmt.Errorf("get current HA status: %w", err)
			}
			var raws []json.RawMessage
			if resp != nil {
				raws = *resp
			}
			return renderHaStatusList(cmd, raws, "current HA status entry")
		},
	}
}

// newHaStatusManagerCmd builds `pve cluster ha status manager`.
func newHaStatusManagerCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "manager",
		Aliases: []string{"manager-status"},
		Short:   "Show the raw HA manager status",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListHaStatusManagerStatus(cmd.Context())
			if err != nil {
				return fmt.Errorf("get HA manager status: %w", err)
			}

			var obj any
			if resp != nil {
				if err := json.Unmarshal(*resp, &obj); err != nil {
					return fmt.Errorf("decode HA manager status: %w", err)
				}
			}
			single, raw, err := objectToSingle(obj)
			if err != nil {
				return fmt.Errorf("render HA manager status: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

// newHaStatusArmCmd builds `pve cluster ha status arm`.
func newHaStatusArmCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "arm",
		Short: "Arm the cluster-wide HA stack",
		Long: "Re-enable HA management across the whole cluster after it was disarmed. This affects " +
			"every HA-managed resource, so it is guarded by --yes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to arm cluster HA without --yes: this affects every HA-managed resource")
			}
			if err := deps.API.Cluster.CreateHaStatusArmHa(cmd.Context()); err != nil {
				return fmt.Errorf("arm cluster HA: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: "Cluster HA armed."}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm arming cluster HA without prompting")
	return cmd
}

// newHaStatusDisarmCmd builds `pve cluster ha status disarm`.
func newHaStatusDisarmCmd() *cobra.Command {
	var (
		yes          bool
		resourceMode string
	)
	cmd := &cobra.Command{
		Use:   "disarm",
		Short: "Disarm the cluster-wide HA stack",
		Long: "Disable HA management across the whole cluster. --resource-mode controls how managed " +
			"resources are handled: 'freeze' holds new commands and state changes, 'ignore' removes " +
			"resources from HA tracking. This affects every HA-managed resource, so it is guarded by --yes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to disarm cluster HA without --yes: this affects every HA-managed resource")
			}
			if !cmd.Flags().Changed("resource-mode") {
				return fmt.Errorf("--resource-mode is required: freeze|ignore")
			}
			err := deps.API.Cluster.CreateHaStatusDisarmHa(cmd.Context(),
				&pvecluster.CreateHaStatusDisarmHaParams{ResourceMode: resourceMode})
			if err != nil {
				return fmt.Errorf("disarm cluster HA: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Cluster HA disarmed (resource-mode %q).", resourceMode)},
				deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm disarming cluster HA without prompting")
	cmd.Flags().StringVar(&resourceMode, "resource-mode", "",
		"how managed resources are handled while disarmed: freeze|ignore (required)")
	return cmd
}
