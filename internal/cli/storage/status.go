package storage

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newStorageStatusCmd builds `pmx storage status <storage>` — report used/total/avail
// space and current active/enabled flags for a storage on the resolved node
// (GET /nodes/{node}/storage/{storage}/status).
func newStorageStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <storage>",
		Short: "Show a storage's used, available, and total space",
		Long: "Query the live status of a storage on the resolved node. " +
			"Reports used, available, and total capacity in bytes along with " +
			"the active and enabled flags.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}
			storage := args[0]

			resp, err := deps.API.Nodes.ListStorageStatus(cmd.Context(), deps.Node, storage)
			if err != nil {
				return fmt.Errorf("get storage status %q on node %q: %w", storage, deps.Node, err)
			}

			// Build a flat string map for table/yaml/text rendering.
			single := map[string]string{
				"storage": storage,
				"type":    resp.Type,
				"content": resp.Content,
			}
			if resp.Total != nil {
				single["total"] = strconv.FormatInt(resp.Total.Int(), 10)
			}
			if resp.Used != nil {
				single["used"] = strconv.FormatInt(resp.Used.Int(), 10)
			}
			if resp.Avail != nil {
				single["avail"] = strconv.FormatInt(resp.Avail.Int(), 10)
			}
			if resp.Active != nil {
				single["active"] = strconv.FormatBool(resp.Active.Bool())
			}
			if resp.Enabled != nil {
				single["enabled"] = strconv.FormatBool(resp.Enabled.Bool())
			}
			if resp.Shared != nil {
				single["shared"] = strconv.FormatBool(resp.Shared.Bool())
			}

			res := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newStorageIdentityCmd builds `pmx storage identity <storage>` — return the
// low-level backend identity descriptor for a storage on the resolved node
// (GET /nodes/{node}/storage/{storage}/identity).
func newStorageIdentityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity <storage>",
		Short: "Show a storage's backend identity descriptor",
		Long: "Return the backend-plugin identity for a storage on the resolved node. " +
			"The exact format depends on the storage type (e.g. an RBD pool name or " +
			"a filesystem path).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}
			storage := args[0]

			resp, err := deps.API.Nodes.ListStorageIdentity(cmd.Context(), deps.Node, storage)
			if err != nil {
				return fmt.Errorf("get storage identity %q on node %q: %w", storage, deps.Node, err)
			}

			single := map[string]string{
				"id":   resp.Id,
				"type": resp.Type,
			}
			res := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
