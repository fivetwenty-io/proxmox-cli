package storage

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// contentEntry is the subset of a /nodes/{node}/storage/{storage}/content
// element rendered in the content list table.
type contentEntry struct {
	Volid  string `json:"volid"`
	Conten string `json:"content"`
	Format string `json:"format"`
	Size   int64  `json:"size"`
	Vmid   int64  `json:"vmid"`
}

// newContentCmd builds `pmx storage content <storage>` — the volumes stored on
// a storage on the resolved node (GET /nodes/{node}/storage/{storage}/content).
func newContentCmd() *cobra.Command {
	var (
		content string
		vmid    int64
	)
	cmd := &cobra.Command{
		Use:   "content <storage>",
		Short: "List the volumes stored on a storage",
		Long: "List the volumes stored on a storage as seen from the resolved node, showing " +
			"each volume's ID, content type, format, size, and owning guest. Requires a node " +
			"via --node, PMX_NODE, or the active context's default. Use --content to show " +
			"only one content type (for example iso, backup, or images) and --vmid to show " +
			"only volumes owned by a given guest.",
		Example: `  pmx pve storage content local --node pve1
  pmx pve storage content local --node pve1 --content backup --vmid 100`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}
			storage := args[0]

			params := &nodes.ListStorageContentParams{}
			if cmd.Flags().Changed("content") {
				params.Content = &content
			}
			if cmd.Flags().Changed("vmid") {
				params.Vmid = &vmid
			}

			resp, err := deps.API.Nodes.ListStorageContent(cmd.Context(), deps.Node, storage, params)
			if err != nil {
				return fmt.Errorf("list content of storage %q on node %q: %w", storage, deps.Node, err)
			}

			entries := make([]contentEntry, 0, len(*resp))
			for _, raw := range *resp {
				var e contentEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode content entry: %w", err)
				}
				entries = append(entries, e)
			}

			res := output.Result{
				Headers: []string{"VOLID", "CONTENT", "FORMAT", "SIZE", "VMID"},
				Raw:     entries,
			}
			for _, e := range entries {
				vmidCell := ""
				if e.Vmid != 0 {
					vmidCell = strconv.FormatInt(e.Vmid, 10)
				}
				res.Rows = append(res.Rows, []string{
					e.Volid, e.Conten, e.Format, strconv.FormatInt(e.Size, 10), vmidCell,
				})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&content, "content", "", "only list this content type (vztmpl|images|iso|backup|...)")
	cmd.Flags().Int64Var(&vmid, "vmid", 0, "only list volumes owned by this VM/CT id")
	return cmd
}
