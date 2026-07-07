package lxc

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newFeatureCmd builds `pve lxc feature <vmid> --feature FEAT [--snapname SNAP]`.
//
// Checks whether the container supports a given feature (e.g. clone, snapshot,
// copy). Useful as a pre-flight check before attempting the operation.
func newFeatureCmd() *cobra.Command {
	var feature, snapname string

	cmd := &cobra.Command{
		Use:   "feature <vmid|name>",
		Short: "Check whether a container supports a feature",
		Long: "Query PVE to determine whether a container supports a given feature. " +
			"--feature is required. Typical values: clone, snapshot, copy. " +
			"Pass --snapname to check feature support in the context of a snapshot.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			if !cmd.Flags().Changed("feature") {
				return fmt.Errorf("--feature is required: specify clone, snapshot, copy, or another supported feature name")
			}

			params := &nodes.ListLxcFeatureParams{Feature: feature}
			if cmd.Flags().Changed("snapname") {
				params.Snapname = &snapname
			}

			resp, err := deps.API.Nodes.ListLxcFeature(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("check feature %q for container %s on node %q: %w", feature, vmid, node, err)
			}
			if resp == nil {
				return fmt.Errorf("check feature %q for container %s on node %q: empty response", feature, vmid, node)
			}

			supported := "false"
			if bool(resp.HasFeature) {
				supported = "true"
			}

			res := output.Result{
				Single:  map[string]string{"hasFeature": supported},
				Raw:     resp,
				Message: fmt.Sprintf("Container %s hasFeature(%s)=%s", vmid, feature, supported),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().StringVar(&feature, "feature", "", "feature to check, e.g. clone, snapshot, copy (required)")
	cmd.Flags().StringVar(&snapname, "snapname", "", "snapshot name for snapshot-scoped feature check")
	return cmd
}
