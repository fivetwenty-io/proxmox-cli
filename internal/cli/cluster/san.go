package cluster

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newSanCmd builds the `pmx pve cluster san` sub-tree, which reads the storage
// area network view that spans every node of the cluster.
func newSanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "san",
		Short: "Inspect the SAN state of the cluster",
		Long: "Read the storage area network state across the whole cluster. 'list' names the SAN " +
			"areas, and 'status' shows every LUN or NVMe namespace by its WWID with the path health " +
			"each node reports and the overall cluster state. The per-node view lives under " +
			"'pmx pve node san'.",
		Example: `  pmx pve cluster san list
  pmx pve cluster san status`,
	}
	cmd.AddCommand(
		newSanListCmd(),
		newSanStatusCmd(),
	)
	return cmd
}

// newSanListCmd builds `pmx pve cluster san list` (GET /cluster/san).
func newSanListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the SAN areas of the cluster",
		Long: "List the SAN areas that the cluster reports. Reading needs Sys.Audit on / or " +
			"Datastore.Allocate on /storage.",
		Example: `  pmx pve cluster san list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListSan(cmd.Context())
			if err != nil {
				return fmt.Errorf("list cluster SAN areas: %w", err)
			}
			var raws []json.RawMessage
			if resp != nil {
				raws = *resp
			}
			res, err := rawUnionResult(raws)
			if err != nil {
				return fmt.Errorf("decode cluster SAN areas: %w", err)
			}
			res.Raw = resp
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newSanStatusCmd builds `pmx pve cluster san status` (GET /cluster/san/status).
func newSanStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the SAN status of every LUN in the cluster",
		Long: "Show one row for each LUN or NVMe namespace in the cluster, identified by its WWID, " +
			"with its path health on each node and the overall cluster state. The json and yaml " +
			"output also carries the SAN status of every node, keyed by node name. Reading needs " +
			"Sys.Audit on / or Datastore.Allocate on /storage.",
		Example: `  pmx pve cluster san status
  pmx pve cluster san status -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListSanStatus(cmd.Context())
			if err != nil {
				return fmt.Errorf("read cluster SAN status: %w", err)
			}
			var raws []json.RawMessage
			if resp != nil {
				raws = resp.Luns
			}
			res, err := rawUnionResult(raws)
			if err != nil {
				return fmt.Errorf("decode cluster SAN status: %w", err)
			}
			res.Raw = resp
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// renderObject renders any API object as a key/value block, keeping the
// lossless response for the json and yaml formats.
func renderObject(cmd *cobra.Command, deps *cli.Deps, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	single := make(map[string]string, len(obj))
	for k, val := range obj {
		single[k] = output.Cell(val)
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: v}, deps.Format)
}
