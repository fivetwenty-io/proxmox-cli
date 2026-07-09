package pdm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	pveclient "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pdmconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/config"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newConfigViewCmd builds `pmx pdm config view` — manage saved resource
// views: named include/exclude filter-rule sets that scope the dashboard's
// resource tree (/config/views CRUD).
func newConfigViewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Manage saved resource views",
		Long: "List, inspect, create, update, and delete saved resource views: named " +
			"include/exclude filter-rule sets that scope the dashboard's resource tree " +
			"(/config/views).",
	}
	cmd.AddCommand(
		newConfigViewLsCmd(),
		newConfigViewShowCmd(),
		newConfigViewAddCmd(),
		newConfigViewUpdateCmd(),
		newConfigViewDeleteCmd(),
	)
	return cmd
}

// viewEntry is the decoded shape of one element of GET /config/views, and
// also (aside from its ID field being named "id" in both) the shape of the
// single object returned by GET /config/views/{id} — both endpoints declare
// the identical "View definition" object schema in pdm-apidoc.json.
type viewEntry struct {
	Exclude    []string           `json:"exclude,omitempty"`
	Id         string             `json:"id"`
	Include    []string           `json:"include,omitempty"`
	IncludeAll *pveclient.PVEBool `json:"include-all,omitempty"`
	Layout     *string            `json:"layout,omitempty"`
}

// newConfigViewLsCmd builds `pmx pdm config view ls` — list every saved
// resource view (GET /config/views).
func newConfigViewLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List saved resource views",
		Long:  "List every saved resource view configured on this Proxmox Datacenter Manager (GET /config/views).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Config.ListViews(cmd.Context())
			if err != nil {
				return fmt.Errorf("list views: %w", err)
			}

			items := rawItemsOf(resp)
			type viewRow struct {
				entry viewEntry
				raw   map[string]any
			}
			table := make([]viewRow, 0, len(items))

			for _, raw := range items {
				var e viewEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode view entry: %w", err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode view entry: %w", err)
				}

				table = append(table, viewRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Id < table[j].entry.Id })

			headers := []string{"ID", "INCLUDE-ALL", "INCLUDE", "EXCLUDE", "LAYOUT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{
					e.Id, pveBoolPtrString(e.IncludeAll), strings.Join(e.Include, ","), strings.Join(e.Exclude, ","), strPtrString(e.Layout),
				})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newConfigViewShowCmd builds `pmx pdm config view show <id>` — show a
// single saved resource view (GET /config/views/{id}).
func newConfigViewShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a single saved resource view",
		Long:  "Show every field of a single saved resource view (GET /config/views/{id}).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			resp, err := deps.PDM.Config.GetViews(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("get view %q: %w", id, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode view %q: %w", id, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newConfigViewAddCmd builds `pmx pdm config view add <id>` — create a saved
// resource view (POST /config/views).
func newConfigViewAddCmd() *cobra.Command {
	var (
		include    []string
		exclude    []string
		includeAll bool
		layout     string
	)
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Create a saved resource view",
		Long: "Create a new saved resource view (POST /config/views). --include and " +
			"--exclude accept repeatable filter-rule strings, e.g. " +
			"'resource-type=qemu' or 'tag=production'.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			params := &pdmconfig.CreateViewsParams{Id: id}

			fl := cmd.Flags()
			if fl.Changed("include") {
				params.Include = include
			}
			if fl.Changed("exclude") {
				params.Exclude = exclude
			}
			if fl.Changed("include-all") {
				params.IncludeAll = boolPtr(includeAll)
			}
			if fl.Changed("layout") {
				params.Layout = strPtr(layout)
			}

			err := deps.PDM.Config.CreateViews(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create view %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("View %q created.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&include, "include", nil, "filter rule to include (repeatable)")
	f.StringArrayVar(&exclude, "exclude", nil, "filter rule to exclude (repeatable)")
	f.BoolVar(&includeAll, "include-all", false, "include all resources by default")
	f.StringVar(&layout, "layout", "", "dashboard layout, encoded as JSON")
	return cmd
}

// newConfigViewUpdateCmd builds `pmx pdm config view update <id>` — update a
// saved resource view (PUT /config/views/{id}). Only flags explicitly set
// are sent; use --delete to reset properties to their default instead.
func newConfigViewUpdateCmd() *cobra.Command {
	var (
		include    []string
		exclude    []string
		includeAll bool
		layout     string
		del        []string
		digest     string
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a saved resource view",
		Long: "Update an existing saved resource view (PUT /config/views/{id}). Only " +
			"flags explicitly set are sent; use --delete to reset properties to their " +
			"default instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			fl := cmd.Flags()
			if !anyFlagChanged(fl) {
				return fmt.Errorf("update view %q: no changes given: pass at least one flag", id)
			}

			if fl.Changed("delete") {
				for _, key := range del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pdmconfig.UpdateViewsParams{}
			if fl.Changed("include") {
				params.Include = include
			}
			if fl.Changed("exclude") {
				params.Exclude = exclude
			}
			if fl.Changed("include-all") {
				params.IncludeAll = boolPtr(includeAll)
			}
			if fl.Changed("layout") {
				params.Layout = strPtr(layout)
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PDM.Config.UpdateViews(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("update view %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("View %q updated.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&include, "include", nil, "filter rule to include (repeatable)")
	f.StringArrayVar(&exclude, "exclude", nil, "filter rule to exclude (repeatable)")
	f.BoolVar(&includeAll, "include-all", false, "include all resources by default")
	f.StringVar(&layout, "layout", "", "dashboard layout, encoded as JSON")
	f.StringArrayVar(&del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&digest, "digest", "", "only update if the current config digest matches")
	return cmd
}

// newConfigViewDeleteCmd builds `pmx pdm config view delete <id>` — remove a
// saved resource view (DELETE /config/views/{id}).
func newConfigViewDeleteCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a saved resource view",
		Long: "Remove a saved resource view (DELETE /config/views/{id}). This is " +
			"destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete view %q without confirmation: pass --yes/-y", id)
			}

			params := &pdmconfig.DeleteViewsParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PDM.Config.DeleteViews(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("delete view %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("View %q deleted.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
