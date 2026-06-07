package qemu

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newFeatureCmd builds `pve qemu feature <vmid> --feature FEAT [--snapname SNAP]`.
func newFeatureCmd() *cobra.Command {
	var (
		feature  string
		snapname string
	)
	cmd := &cobra.Command{
		Use:   "feature <vmid>",
		Short: "Check whether a VM supports a feature",
		Long: "Pre-flight check: query whether the given VM supports a specific " +
			"feature (e.g. clone, snapshot, copy) and which nodes can perform it. " +
			"--feature is required.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]
			if err := parseVMID(vmid); err != nil {
				return err
			}

			params := &nodes.ListQemuFeatureParams{Feature: feature}
			if cmd.Flags().Changed("snapname") {
				params.Snapname = strPtr(snapname)
			}

			resp, err := deps.API.Nodes.ListQemuFeature(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("feature check for VM %s on node %q: %w", vmid, node, err)
			}
			if resp == nil {
				return fmt.Errorf("feature check for VM %s on node %q: empty response", vmid, node)
			}

			single := map[string]string{
				"hasFeature": fmt.Sprintf("%v", bool(resp.HasFeature)),
				"nodes":      strings.Join(resp.Nodes, ", "),
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&feature, "feature", "", "feature to check: clone, snapshot, copy (required)")
	cmd.Flags().StringVar(&snapname, "snapname", "", "name of a snapshot to check feature against")
	if err := cmd.MarkFlagRequired("feature"); err != nil {
		panic(fmt.Sprintf("feature: mark --feature required: %v", err))
	}
	return cmd
}
