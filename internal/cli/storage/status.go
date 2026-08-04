package storage

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newStorageStatusCmd builds `pmx pve storage status <storage>` — report used/total/avail
// space and current active/enabled flags for a storage on the resolved node
// (GET /nodes/{node}/storage/{storage}/status).
func newStorageStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <storage>",
		Short: "Show a storage's used, available, and total space",
		Long: "Query the live status of a storage. Reports used, available, and total " +
			"capacity in bytes along with the active and enabled flags. A node carrying " +
			"the storage is resolved from the cluster unless --node is passed explicitly.",
		Example: `  pmx pve storage status local-lvm`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			storage := args[0]
			node, err := resolveStorageNode(cmd, deps, storage)
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.ListStorageStatus(cmd.Context(), node, storage)
			if err != nil {
				return fmt.Errorf("get storage status %q on node %q: %w", storage, node, err)
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

// newStorageIdentityCmd builds `pmx pve storage identity <storage>` — return the
// low-level backend identity descriptor for a storage on the resolved node
// (GET /nodes/{node}/storage/{storage}/identity).
func newStorageIdentityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity <storage>",
		Short: "Show a storage's backend identity descriptor",
		Long: "Return the backend-plugin identity for a storage. The exact format depends " +
			"on the storage type (e.g. an RBD pool name or a filesystem path). A node " +
			"carrying the storage is resolved from the cluster unless --node is passed " +
			"explicitly.",
		Example: `  pmx pve storage identity local-lvm`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			storage := args[0]
			node, err := resolveStorageNode(cmd, deps, storage)
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.ListStorageIdentity(cmd.Context(), node, storage)
			if err != nil {
				return fmt.Errorf("get storage identity %q on node %q: %w", storage, node, err)
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
