package storage

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/plugins"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// storagePluginTypes are the storage types the server's plugin index names, in
// the order the API documents them. They feed shell completion and the help
// text, and the server stays the judge of what a given release accepts.
var storagePluginTypes = []string{
	"btrfs", "cephfs", "cifs", "dir", "esxi", "iscsi", "iscsidirect", "lvm", "lvmthin", "nfs",
	"nvmeof", "pbs", "rbd", "zfs", "zfspool",
}

// storagePluginOrigins are the two places a storage plugin can come from.
var storagePluginOrigins = []string{"builtin", "custom"}

// pluginContent is the content block of a storage plugin: every content type
// the plugin supports, and the ones a new storage enables by default.
type pluginContent struct {
	Default   []string `json:"default"`
	Supported []string `json:"supported"`
}

// pluginEntry is the part of a storage plugin record that the list table shows.
// The schema stays out of the table and remains in the json and yaml output.
type pluginEntry struct {
	Type        string        `json:"type"`
	DisplayName string        `json:"display-name"`
	Origin      string        `json:"origin"`
	Content     pluginContent `json:"content"`
}

// newPluginCmd builds the `pmx pve storage plugin` sub-group, which reads the
// plugin index the server publishes under /plugins/storage/plugin.
func newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Inspect the storage plugins the server knows",
		Long: "List the storage plugins that the Proxmox VE server knows, and show the content " +
			"types and the property schema of one of them. The index describes the plugin types " +
			"themselves and not the storage definitions that use them, so it needs no node and " +
			"works for every authenticated user.",
		Example: `  pmx pve storage plugin list
  pmx pve storage plugin show nfs`,
	}
	cmd.AddCommand(
		newPluginListCmd(),
		newPluginShowCmd(),
	)
	return cmd
}

// newPluginListCmd builds `pmx pve storage plugin list` (GET /plugins/storage/plugin).
func newPluginListCmd() *cobra.Command {
	var origin string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the storage plugins",
		Long: "List the storage plugins that the server knows, with the content types each one " +
			"supports and the ones it enables by default. --origin keeps only the plugins that ship " +
			"with Proxmox VE (builtin) or the ones that were installed separately (custom). The " +
			"full property schema of a plugin is in the json and yaml output, and 'plugin show' " +
			"prints one plugin on its own.",
		Example: `  pmx pve storage plugin list
  pmx pve storage plugin list --origin custom`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &plugins.ListStoragePluginParams{}
			if cmd.Flags().Changed("origin") {
				if origin != "builtin" && origin != "custom" {
					return fmt.Errorf("invalid --origin %q: want builtin or custom", origin)
				}
				params.Origin = &origin
			}
			resp, err := deps.API.Plugins.ListStoragePlugin(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list storage plugins: %w", err)
			}

			var rows [][]string
			if resp != nil {
				for _, raw := range *resp {
					var e pluginEntry
					if jsonErr := json.Unmarshal(raw, &e); jsonErr != nil {
						return fmt.Errorf("decode storage plugin: %w", jsonErr)
					}
					rows = append(rows, []string{
						e.Type, e.DisplayName, e.Origin,
						strings.Join(e.Content.Supported, ","), strings.Join(e.Content.Default, ","),
					})
				}
			}
			res := output.Result{
				Headers: []string{"TYPE", "NAME", "ORIGIN", "CONTENT", "DEFAULT"},
				Rows:    rows,
				Raw:     resp,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&origin, "origin", "", "only list plugins of this origin, builtin or custom")
	_ = cmd.RegisterFlagCompletionFunc("origin", staticCompletion(storagePluginOrigins))
	return cmd
}

// newPluginShowCmd builds `pmx pve storage plugin show <type>`
// (GET /plugins/storage/plugin/{type}).
func newPluginShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <type>",
		Short: "Show one storage plugin",
		Long: "Show one storage plugin by its storage type, such as nfs, lvmthin, or nvmeof, with " +
			"the content types it supports and enables by default. The property schema of the " +
			"plugin is in the json and yaml output.",
		Example: `  pmx pve storage plugin show nfs
  pmx pve storage plugin show nvmeof -o json`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: pluginTypeCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			typ := args[0]
			resp, err := deps.API.Plugins.GetStoragePlugin(cmd.Context(), typ)
			if err != nil {
				return fmt.Errorf("show storage plugin %q: %w", typ, err)
			}
			if resp == nil {
				return fmt.Errorf("show storage plugin %q: empty response from server", typ)
			}

			var content pluginContent
			if len(resp.Content) > 0 {
				if jsonErr := json.Unmarshal(resp.Content, &content); jsonErr != nil {
					return fmt.Errorf("decode content of storage plugin %q: %w", typ, jsonErr)
				}
			}
			name := ""
			if resp.DisplayName != nil {
				name = *resp.DisplayName
			}
			single := map[string]string{
				"type":              resp.Type,
				"display-name":      name,
				"origin":            resp.Origin,
				"content-supported": strings.Join(content.Supported, ","),
				"content-default":   strings.Join(content.Default, ","),
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}
	return cmd
}

// staticCompletion returns a flag completion function over a fixed value list.
func staticCompletion(values []string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

// pluginTypeCompletion completes the storage type argument of `plugin show`.
func pluginTypeCompletion(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return storagePluginTypes, cobra.ShellCompDirectiveNoFileComp
}
