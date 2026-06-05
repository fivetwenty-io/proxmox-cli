package storage

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/clusterstorage"
)

func init() {
	cli.RegisterGroup(newGroupCmd)
}

// newGroupCmd builds the `pve storage` command and all of its sub-commands.
// The supplied *cli.Deps is a placeholder used only so cobra can build the
// command tree; live dependencies are obtained per-invocation via cli.GetDeps.
func newGroupCmd(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Manage cluster storage configuration",
		Long:  "List, inspect, create, update, and delete Proxmox VE cluster storage definitions.",
	}
	cmd.AddCommand(
		newListCmd(),
		newGetCmd(),
		newContentCmd(),
		newCreateCmd(),
		newSetCmd(),
		newDeleteCmd(),
		newPruneCmd(),
	)
	return cmd
}

// storageEntry is the subset of a storage definition rendered in list output.
// Storage definitions are returned by the API as untyped objects, so fields are
// decoded individually and absent fields render as empty cells.
type storageEntry struct {
	Storage string `json:"storage"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Path    string `json:"path"`
	Server  string `json:"server"`
	Export  string `json:"export"`
	Nodes   string `json:"nodes"`
	Shared  int    `json:"shared"`
	Disable int    `json:"disable"`
}

// pathOrServer returns the most descriptive location field for a storage entry:
// the filesystem path if present, otherwise the server (optionally with export).
func (e storageEntry) pathOrServer() string {
	if e.Path != "" {
		return e.Path
	}
	if e.Server != "" && e.Export != "" {
		return e.Server + ":" + e.Export
	}
	return e.Server
}

// newListCmd builds `pve storage list`.
func newListCmd() *cobra.Command {
	var typeFilter string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured cluster storage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &clusterstorage.ListStorageParams{}
			if typeFilter != "" {
				params.Type = &typeFilter
			}
			resp, err := deps.API.ClusterStorage.ListStorage(cmd.Context(), params)
			if err != nil {
				return err
			}

			entries := make([]storageEntry, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e storageEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode storage entry: %w", err)
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
					e.pathOrServer(),
					e.Nodes,
					boolCell(e.Shared == 1),
					boolCell(e.Disable == 0),
				})
			}

			res := output.Result{
				Headers: []string{"STORAGE", "TYPE", "CONTENT", "PATH/SERVER", "NODES", "SHARED", "ENABLED"},
				Rows:    rows,
				Raw:     rawList(resp),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&typeFilter, "type", "", "only list storage of the given type")
	return cmd
}

// newGetCmd builds `pve storage get <storage>`.
func newGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <storage>",
		Short: "Show a single storage definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.ClusterStorage.GetStorage(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			single := map[string]string{}
			if resp != nil {
				var fields map[string]any
				if err := json.Unmarshal(*resp, &fields); err != nil {
					return fmt.Errorf("decode storage: %w", err)
				}
				for k, v := range fields {
					single[k] = scalarString(v)
				}
			}

			res := output.Result{Single: single, Raw: rawSingle(resp)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// storageFlags collects the mutable storage attributes shared by create and set.
// Path and Export are only meaningful at creation time; they are bound on create
// but omitted from update, mirroring the underlying API which has no path or
// export parameter on its update endpoint.
type storageFlags struct {
	path    string
	server  string
	export  string
	content string
	nodes   string
	shared  bool
	enabled bool
}

// registerCreate binds every storage attribute flag, including the create-only
// path and export flags, onto cmd.
func (sf *storageFlags) registerCreate(cmd *cobra.Command) {
	sf.registerCommon(cmd)
	cmd.Flags().StringVar(&sf.path, "path", "", "file system path")
	cmd.Flags().StringVar(&sf.export, "export", "", "NFS export path")
}

// registerSet binds the storage attribute flags that the update endpoint accepts.
func (sf *storageFlags) registerSet(cmd *cobra.Command) {
	sf.registerCommon(cmd)
}

// registerCommon binds the attribute flags accepted by both create and update.
func (sf *storageFlags) registerCommon(cmd *cobra.Command) {
	cmd.Flags().StringVar(&sf.server, "server", "", "server IP or DNS name")
	cmd.Flags().StringVar(&sf.content, "content", "", "allowed content types (comma-separated)")
	cmd.Flags().StringVar(&sf.nodes, "nodes", "", "nodes the storage applies to (comma-separated)")
	cmd.Flags().BoolVar(&sf.shared, "shared", false, "mark the storage as shared")
	cmd.Flags().BoolVar(&sf.enabled, "enabled", true, "enable the storage")
}

// newCreateCmd builds `pve storage create`.
func newCreateCmd() *cobra.Command {
	var (
		storageID string
		stType    string
		sf        storageFlags
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new storage definition",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &clusterstorage.CreateStorageParams{
				Storage: storageID,
				Type:    stType,
			}
			if sf.path != "" {
				params.Path = strptr(sf.path)
			}
			if sf.server != "" {
				params.Server = strptr(sf.server)
			}
			if sf.export != "" {
				params.Export = strptr(sf.export)
			}
			if sf.content != "" {
				params.Content = strptr(sf.content)
			}
			if sf.nodes != "" {
				params.Nodes = strptr(sf.nodes)
			}
			if cmd.Flags().Changed("shared") {
				params.Shared = boolptr(sf.shared)
			}
			if cmd.Flags().Changed("enabled") {
				params.Disable = boolptr(!sf.enabled)
			}

			if _, err := deps.API.ClusterStorage.CreateStorage(cmd.Context(), params); err != nil {
				return err
			}
			res := output.Result{Message: fmt.Sprintf("Storage %q created.", storageID)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&storageID, "storage", "", "storage identifier (required)")
	cmd.Flags().StringVar(&stType, "type", "",
		"storage type: dir|nfs|cifs|rbd|lvm|lvmthin|zfspool|btrfs|pbs (required)")
	sf.registerCreate(cmd)
	_ = cmd.MarkFlagRequired("storage")
	_ = cmd.MarkFlagRequired("type")
	return cmd
}

// newSetCmd builds `pve storage set <storage>`.
func newSetCmd() *cobra.Command {
	var sf storageFlags
	cmd := &cobra.Command{
		Use:   "set <storage>",
		Short: "Update an existing storage definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			storageID := args[0]
			params := &clusterstorage.UpdateStorageParams{}
			if sf.server != "" {
				params.Server = strptr(sf.server)
			}
			if sf.content != "" {
				params.Content = strptr(sf.content)
			}
			if sf.nodes != "" {
				params.Nodes = strptr(sf.nodes)
			}
			if cmd.Flags().Changed("shared") {
				params.Shared = boolptr(sf.shared)
			}
			if cmd.Flags().Changed("enabled") {
				params.Disable = boolptr(!sf.enabled)
			}

			if _, err := deps.API.ClusterStorage.UpdateStorage(cmd.Context(), storageID, params); err != nil {
				return err
			}
			res := output.Result{Message: fmt.Sprintf("Storage %q updated.", storageID)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	sf.registerSet(cmd)
	return cmd
}

// newDeleteCmd builds `pve storage delete <storage>`.
func newDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <storage>",
		Short: "Delete a storage definition",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			storageID := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete storage %q without --yes", storageID)
			}
			if err := deps.API.ClusterStorage.DeleteStorage(cmd.Context(), storageID); err != nil {
				return err
			}
			res := output.Result{Message: fmt.Sprintf("Storage %q deleted.", storageID)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// --- helpers ---

// strptr returns a pointer to s.
func strptr(s string) *string { return &s }

// boolptr returns a pointer to b.
func boolptr(b bool) *bool { return &b }

// boolCell renders a boolean as the conventional table cell text.
func boolCell(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// rawList converts a list response into a slice of decoded objects for JSON and
// YAML output, preserving every field returned by the API. A nil response
// yields an empty slice.
func rawList(resp *clusterstorage.ListStorageResponse) any {
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

// rawSingle decodes a single storage response into a generic object for JSON and
// YAML output. A nil response yields an empty object.
func rawSingle(resp *clusterstorage.GetStorageResponse) any {
	out := map[string]any{}
	if resp == nil {
		return out
	}
	_ = json.Unmarshal(*resp, &out)
	return out
}

// scalarString renders an arbitrary JSON scalar as a display string. Numbers
// decoded as float64 with no fractional part render without a trailing ".0".
func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return strings.TrimSpace(fmt.Sprintf("%v", t))
		}
		return string(b)
	}
}
