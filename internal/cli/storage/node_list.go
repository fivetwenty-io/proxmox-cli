package storage

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// nodeStorageEntry is the subset of a /nodes/{node}/storage element rendered in
// the node-list table. Unlike `storage list` (cluster configuration), this
// endpoint reports the runtime availability and usage of each storage as seen
// from a single node.
type nodeStorageEntry struct {
	Storage string `json:"storage"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Active  int    `json:"active"`
	Enabled int    `json:"enabled"`
	Total   int64  `json:"total"`
	Used    int64  `json:"used"`
	Avail   int64  `json:"avail"`
}

// newStorageNodeListCmd builds `pmx storage node-list` — the storages available
// on the resolved node with their runtime status and usage
// (GET /nodes/{node}/storage).
func newStorageNodeListCmd() *cobra.Command {
	var (
		content   string
		enabled   bool
		format    bool
		storageID string
		target    string
	)
	cmd := &cobra.Command{
		Use:   "node-list",
		Short: "List storages available on a node with usage",
		Long: "List the storages available on the resolved node, including their " +
			"runtime active/enabled status and capacity usage. This differs from " +
			"`storage list`, which shows the cluster-wide storage configuration.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}

			params := &nodes.ListStorageParams{}
			fl := cmd.Flags()
			if fl.Changed("content") {
				params.Content = &content
			}
			if fl.Changed("enabled") {
				params.Enabled = &enabled
			}
			if fl.Changed("format") {
				params.Format = &format
			}
			if fl.Changed("storage-id") {
				params.Storage = &storageID
			}
			if fl.Changed("target-node") {
				params.Target = &target
			}

			resp, err := deps.API.Nodes.ListStorage(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("list storages on node %q: %w", deps.Node, err)
			}

			entries := make([]nodeStorageEntry, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e nodeStorageEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode node storage entry: %w", err)
					}
					entries = append(entries, e)
				}
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Storage < entries[j].Storage })

			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					e.Storage,
					e.Type,
					e.Content,
					boolCell(e.Active == 1),
					boolCell(e.Enabled == 1),
					strconv.FormatInt(e.Total, 10),
					strconv.FormatInt(e.Used, 10),
					strconv.FormatInt(e.Avail, 10),
				})
			}

			res := output.Result{
				Headers: []string{"STORAGE", "TYPE", "CONTENT", "ACTIVE", "ENABLED", "TOTAL", "USED", "AVAIL"},
				Rows:    rows,
				Raw:     nodeRawList(resp),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&content, "content", "", "only list storages supporting this content type")
	f.BoolVar(&enabled, "enabled", false, "only list storages that are enabled")
	f.BoolVar(&format, "format", false, "include information about supported formats")
	f.StringVar(&storageID, "storage-id", "", "only list status for this storage")
	f.StringVar(&target, "target-node", "",
		"list shared storages whose content is accessible on this target node")
	return cmd
}

// nodeRawList converts a node storage list response into a slice of decoded
// objects for JSON and YAML output, preserving every field returned by the API.
func nodeRawList(resp *nodes.ListStorageResponse) any {
	out := make([]map[string]any, 0)
	if resp == nil {
		return out
	}
	for _, raw := range *resp {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err == nil {
			out = append(out, obj)
		}
	}
	return out
}
