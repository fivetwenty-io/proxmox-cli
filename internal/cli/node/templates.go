package node

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newAptTemplatesCmd builds `pve node apt templates`: list the available
// appliance/container templates (aplinfo) and download one to a storage.
func newAptTemplatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "templates",
		Short: "List and download container appliance templates",
		Long: "List the appliance templates available from the configured template " +
			"repositories and download a selected template to a storage.",
	}
	cmd.AddCommand(
		newAptTemplatesListCmd(),
		newAptTemplatesDownloadCmd(),
	)
	return cmd
}

// aptTemplateEntry is the subset of an aplinfo list element rendered in the
// table. The full element is preserved in the JSON/Raw output.
type aptTemplateEntry struct {
	Template string `json:"template"`
	Section  string `json:"section"`
	Os       string `json:"os"`
	Version  string `json:"version"`
	Headline string `json:"headline"`
}

func newAptTemplatesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available container appliance templates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListAplinfo(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list templates on node %q: %w", deps.Node, err)
			}
			headers := []string{"TEMPLATE", "SECTION", "OS", "VERSION", "HEADLINE"}
			entries := make([]aptTemplateEntry, 0)
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e aptTemplateEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode template entry: %w", err)
					}
					entries = append(entries, e)
					rows = append(rows, []string{e.Template, e.Section, e.Os, e.Version, e.Headline})
				}
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
}

func newAptTemplatesDownloadCmd() *cobra.Command {
	var (
		storage  string
		template string
	)
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download an appliance template to a storage",
		Long: "Download the template named by --template to the storage named by " +
			"--storage. The command blocks until the download task finishes unless " +
			"--async is set.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.CreateAplinfoParams{Storage: storage, Template: template}
			resp, err := deps.API.Nodes.CreateAplinfo(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("download template %q on node %q: %w", template, deps.Node, err)
			}
			var raw json.RawMessage
			if resp != nil {
				raw = json.RawMessage(*resp)
			}
			doneMsg := fmt.Sprintf("Template %q downloaded to %q on node %q.", template, storage, deps.Node)
			upid, err := apiclient.UPIDFromRaw(raw)
			if err != nil {
				return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
			}
			if deps.Async {
				return deps.Out.Render(cmd.OutOrStdout(),
					output.Result{
						Single:  map[string]string{"upid": upid},
						Raw:     map[string]string{"upid": upid},
						Message: upid,
					}, deps.Format)
			}
			if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
				return fmt.Errorf("download template %q on node %q: %w", template, deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&storage, "storage", "", "storage to download the template to (required)")
	f.StringVar(&template, "template", "", "template identifier to download, for example debian-12-standard (required)")
	cli.MustMarkRequired(cmd, "storage")
	cli.MustMarkRequired(cmd, "template")
	return cmd
}
