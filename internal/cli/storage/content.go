package storage

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// contentEntry is the subset of a /nodes/{node}/storage/{storage}/content
// element rendered in the content list table.
type contentEntry struct {
	Volid  string     `json:"volid"`
	Conten string     `json:"content"`
	Format string     `json:"format"`
	Size   pve.PVEInt `json:"size"`
	Vmid   pve.PVEInt `json:"vmid"`
}

// newContentCmd builds `pmx pve storage content <storage>` — the volumes stored on
// a storage on the resolved node (GET /nodes/{node}/storage/{storage}/content).
func newContentCmd() *cobra.Command {
	var (
		content string
		vmid    int64
	)
	cmd := &cobra.Command{
		Use:   "content <storage>",
		Short: "List the volumes stored on a storage",
		Long: "List the volumes on a storage, with each volume's ID, content type, format, " +
			"size, and owning guest.\n\n" +
			"A node carrying the storage is resolved from the cluster unless --node is " +
			"passed explicitly.\n\n" +
			"Narrow the listing with --content for a single content type, such as iso or " +
			"backup or images, and with --vmid for the volumes one guest owns.",
		Example: `  pmx pve storage content local
  pmx pve storage content local --node pve1 --content backup --vmid 100
  pmx pve storage content local --content snippets`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			storage := args[0]
			node, err := resolveStorageNode(cmd, deps, storage)
			if err != nil {
				return err
			}

			params := &nodes.ListStorageContentParams{}
			if cmd.Flags().Changed("content") {
				params.Content = &content
			}
			if cmd.Flags().Changed("vmid") {
				params.Vmid = &vmid
			}

			resp, err := deps.API.Nodes.ListStorageContent(cmd.Context(), node, storage, params)
			if err != nil {
				return fmt.Errorf("list content of storage %q on node %q: %w", storage, node, err)
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
					vmidCell = strconv.FormatInt(int64(e.Vmid), 10)
				}
				res.Rows = append(res.Rows, []string{
					e.Volid, e.Conten, e.Format, strconv.FormatInt(int64(e.Size), 10), vmidCell,
				})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&content, "content", "", "only list this content type (vztmpl|images|iso|backup|...)")
	cmd.Flags().Int64Var(&vmid, "vmid", 0, "only list volumes owned by this VM/CT id")
	return cmd
}
