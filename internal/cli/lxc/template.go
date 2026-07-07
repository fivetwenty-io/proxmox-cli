package lxc

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// aplinfoEntry is the subset of a /nodes/{node}/aplinfo element rendered in the
// available-template list.
type aplinfoEntry struct {
	Template string `json:"template"`
	Section  string `json:"section"`
	Os       string `json:"os"`
	Version  string `json:"version"`
	Headline string `json:"headline"`
}

// newTemplateCmd builds `pve lxc template` and its sub-commands.
func newTemplateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "List and download LXC container templates",
	}
	cmd.AddCommand(newTemplateListCmd(), newTemplateDownloadCmd())
	return cmd
}

// newTemplateListCmd builds `pve lxc template list` — the appliance templates
// available to download (GET /nodes/{node}/aplinfo).
func newTemplateListCmd() *cobra.Command {
	var filter string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List downloadable container templates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.ListAplinfo(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("list available templates on node %q: %w", node, err)
			}

			entries := make([]aplinfoEntry, 0, len(*resp))
			for _, raw := range *resp {
				var e aplinfoEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode template entry: %w", err)
				}
				if filter != "" && !strings.Contains(strings.ToLower(e.Template), strings.ToLower(filter)) {
					continue
				}
				entries = append(entries, e)
			}

			res := output.Result{
				Headers: []string{"TEMPLATE", "SECTION", "OS", "VERSION", "HEADLINE"},
				Raw:     entries,
			}
			for _, e := range entries {
				res.Rows = append(res.Rows, []string{e.Template, e.Section, e.Os, e.Version, e.Headline})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&filter, "filter", "", "only show templates whose name contains this substring")
	return cmd
}

// newTemplateDownloadCmd builds `pve lxc template download <storage> <template>`
// (POST /nodes/{node}/aplinfo). The download runs as an asynchronous task.
func newTemplateDownloadCmd() *cobra.Command {
	var async bool
	cmd := &cobra.Command{
		Use:   "download <storage> <template>",
		Short: "Download a container template to a storage",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			storage, template := args[0], args[1]
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.CreateAplinfoParams{Storage: storage, Template: template}
			resp, err := deps.API.Nodes.CreateAplinfo(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("download template %q to storage %q on node %q: %w",
					template, storage, node, err)
			}
			return emitTask(cmd, deps, *resp,
				fmt.Sprintf("Template %s downloaded to %s.", template, storage))
		},
	}
	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	return cmd
}
