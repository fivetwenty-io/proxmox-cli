package storage

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newAplinfoCmd builds the `pmx storage aplinfo` sub-group with its list and
// download sub-commands.
func newAplinfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "aplinfo",
		Short: "Manage appliance template index",
		Long:  "List available appliance templates and download them to a storage.",
	}
	cmd.AddCommand(
		newAplinfoListCmd(),
		newAplinfoDownloadCmd(),
	)
	return cmd
}

// aplinfoEntry is the subset of an aplinfo record rendered in the list table.
type aplinfoEntry struct {
	Package     string `json:"package"`
	Version     string `json:"version"`
	Section     string `json:"section"`
	Description string `json:"description"`
}

// newAplinfoListCmd builds `pmx storage aplinfo list` — fetch all available
// appliance templates from the Proxmox template index on the resolved node
// (GET /nodes/{node}/aplinfo).
func newAplinfoListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available appliance templates",
		Long: "List all appliance templates available from the Proxmox template index " +
			"on the resolved node (GET /nodes/{node}/aplinfo). Each row shows the " +
			"package name, version, section, and description.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}

			resp, err := deps.API.Nodes.ListAplinfo(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list aplinfo on node %q: %w", deps.Node, err)
			}

			var rows [][]string
			if resp != nil {
				for _, raw := range *resp {
					var e aplinfoEntry
					if jsonErr := json.Unmarshal(raw, &e); jsonErr == nil {
						rows = append(rows, []string{e.Package, e.Version, e.Section, e.Description})
					}
				}
			}

			res := output.Result{
				Headers: []string{"PACKAGE", "VERSION", "SECTION", "DESCRIPTION"},
				Rows:    rows,
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newAplinfoDownloadCmd builds `pmx storage aplinfo download` — instruct the
// resolved node to download the named appliance template to a storage
// (POST /nodes/{node}/aplinfo). The download runs as an asynchronous task;
// the command blocks until it finishes unless --async is set.
func newAplinfoDownloadCmd() *cobra.Command {
	var (
		storageID string
		template  string
	)
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download an appliance template to a storage",
		Long: "Instruct the resolved node to download the named appliance template to " +
			"the given storage (POST /nodes/{node}/aplinfo). The download runs as an " +
			"asynchronous task and the command blocks until it finishes unless --async is set.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}

			params := &nodes.CreateAplinfoParams{
				Storage:  storageID,
				Template: template,
			}

			resp, err := deps.API.Nodes.CreateAplinfo(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("download aplinfo template %q to storage %q on node %q: %w",
					template, storageID, deps.Node, err)
			}

			var raw json.RawMessage
			if resp != nil {
				raw = json.RawMessage(*resp)
			}
			upid, err := apiclient.UPIDFromRaw(raw)
			if err != nil {
				return fmt.Errorf("download aplinfo template %q to storage %q on node %q: %w",
					template, storageID, deps.Node, err)
			}

			return renderStorageTask(cmd, deps, upid,
				fmt.Sprintf("Downloaded template %q to storage %q on node %q.", template, storageID, deps.Node))
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&storageID, "storage", "", "storage to download the template to (required)")
	fl.StringVar(&template, "template", "", "appliance template to download (required)")
	cli.MustMarkRequired(cmd, "storage")
	cli.MustMarkRequired(cmd, "template")
	return cmd
}
