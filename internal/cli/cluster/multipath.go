package cluster

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newMultipathCmd builds the `pmx pve cluster multipath` sub-tree, which manages
// the cluster-wide multipath configuration: the multipath maps by WWID and the
// verbatim override sections.
func newMultipathCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "multipath",
		Short: "Manage the cluster-wide multipath configuration",
		Long: "Manage the multipath configuration that the cluster distributes to its nodes. Each " +
			"LUN is identified by its WWID and can get a map name, a retry policy, the nodes that " +
			"assemble it, and the number of paths each node should see. Verbatim override sections " +
			"cover whatever the structured settings do not. Reads need Sys.Audit on /, and changes " +
			"need Sys.Modify on /. Every change accepts --digest, which makes the server refuse the " +
			"change when the configuration moved since it was read.",
		Example: `  pmx pve cluster multipath list
  pmx pve cluster multipath wwid list
  pmx pve cluster multipath override show`,
	}
	cmd.AddCommand(
		newMultipathListCmd(),
		newMultipathOverrideCmd(),
		newMultipathWwidCmd(),
	)
	return cmd
}

// newMultipathListCmd builds `pmx pve cluster multipath list` (GET /cluster/multipath).
func newMultipathListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the multipath areas of the cluster",
		Long: "List the multipath areas that the cluster reports. The configured maps are under " +
			"'wwid list'.",
		Example: `  pmx pve cluster multipath list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListMultipath(cmd.Context())
			if err != nil {
				return fmt.Errorf("list multipath areas: %w", err)
			}
			var raws []json.RawMessage
			if resp != nil {
				raws = *resp
			}
			res, err := rawUnionResult(raws)
			if err != nil {
				return fmt.Errorf("decode multipath areas: %w", err)
			}
			res.Raw = resp
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// ---- overrides --------------------------------------------------------------

func newMultipathOverrideCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "override",
		Short: "Show or replace the multipath override sections",
		Long: "Show or replace the verbatim multipath override sections that the cluster " +
			"distributes. The text is passed to multipath as written.",
		Example: `  pmx pve cluster multipath override show
  pmx pve cluster multipath override set --file overrides.conf`,
	}
	cmd.AddCommand(
		newMultipathOverrideShowCmd(),
		newMultipathOverrideSetCmd(),
	)
	return cmd
}

// newMultipathOverrideShowCmd builds `pmx pve cluster multipath override show`.
func newMultipathOverrideShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the multipath override sections",
		Long: "Show the verbatim override sections together with the digest of the configuration, " +
			"which 'override set --digest' and the wwid verbs accept to guard against concurrent changes.",
		Example: `  pmx pve cluster multipath override show`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListMultipathOverrides(cmd.Context())
			if err != nil {
				return fmt.Errorf("show multipath overrides: %w", err)
			}
			if resp == nil {
				return fmt.Errorf("show multipath overrides: empty response from server")
			}
			return renderObject(cmd, deps, resp)
		},
	}
}

// newMultipathOverrideSetCmd builds `pmx pve cluster multipath override set`.
func newMultipathOverrideSetCmd() *cobra.Command {
	var (
		overrides string
		file      string
		clearAll  bool
		digest    string
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Replace the multipath override sections",
		Long: "Replace the verbatim multipath override sections with the given text. Give the text " +
			"with --overrides or read it from a file with --file, or pass --clear to remove the " +
			"sections. The new text replaces the old text completely. --digest makes the server refuse " +
			"the change when the configuration differs from the digest 'override show' printed.",
		Example: `  pmx pve cluster multipath override set --file overrides.conf
  pmx pve cluster multipath override set --clear --digest 6e1f0c`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			given := 0
			for _, name := range []string{"overrides", "file", "clear"} {
				if fl.Changed(name) {
					given++
				}
			}
			if given != 1 {
				return fmt.Errorf("give exactly one of --overrides, --file, or --clear")
			}
			params := &pvecluster.UpdateMultipathOverridesParams{}
			switch {
			case fl.Changed("overrides"):
				params.Overrides = &overrides
			case fl.Changed("file"):
				data, err := os.ReadFile(file)
				if err != nil {
					return fmt.Errorf("read --file: %w", err)
				}
				text := string(data)
				params.Overrides = &text
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if err := deps.API.Cluster.UpdateMultipathOverrides(cmd.Context(), params); err != nil {
				return fmt.Errorf("set multipath overrides: %w", err)
			}
			msg := "Multipath overrides replaced."
			if clearAll {
				msg = "Multipath overrides cleared."
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&overrides, "overrides", "", "the override sections, as verbatim text")
	fl.StringVar(&file, "file", "", "read the override sections from this file")
	fl.BoolVar(&clearAll, "clear", false, "remove the override sections")
	fl.StringVar(&digest, "digest", "", "refuse the change when the configuration digest differs")
	return cmd
}

// ---- wwid maps --------------------------------------------------------------

func newMultipathWwidCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wwid",
		Short: "Manage multipath maps by WWID",
		Long: "List, show, create, change, and delete the multipath map of a LUN, which is named by " +
			"its WWID. 'pmx pve node san luns' shows the WWIDs that a node sees.",
		Example: `  pmx pve cluster multipath wwid list
  pmx pve cluster multipath wwid create 3600a0980383030 --alias data1 --paths-expected 4`,
	}
	cmd.AddCommand(
		newMultipathWwidListCmd(),
		newMultipathWwidShowCmd(),
		newMultipathWwidCreateCmd(),
		newMultipathWwidSetCmd(),
		newMultipathWwidDeleteCmd(),
	)
	return cmd
}

// multipathWwidFlags are the map settings that create and set share.
type multipathWwidFlags struct {
	alias         string
	digest        string
	noPathRetry   string
	nodes         string
	pathsExpected int64
}

func (m *multipathWwidFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&m.alias, "alias", "", "the name of the multipath map")
	fl.StringVar(&m.digest, "digest", "", "refuse the change when the configuration digest differs")
	fl.StringVar(&m.noPathRetry, "no-path-retry", "", "the action or number of retries when all paths are down")
	fl.StringVar(&m.nodes, "nodes", "", "the nodes that assemble the map, or all nodes when unset")
	fl.Int64Var(&m.pathsExpected, "paths-expected", 0, "the number of paths each node should have to the LUN")
}

// newMultipathWwidListCmd builds `pmx pve cluster multipath wwid list`.
func newMultipathWwidListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List the configured multipath maps",
		Long:    "List the multipath maps that are configured, one for each WWID.",
		Example: `  pmx pve cluster multipath wwid list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListMultipathWwid(cmd.Context())
			if err != nil {
				return fmt.Errorf("list multipath maps: %w", err)
			}
			var raws []json.RawMessage
			if resp != nil {
				raws = *resp
			}
			res, err := rawUnionResult(raws)
			if err != nil {
				return fmt.Errorf("decode multipath maps: %w", err)
			}
			res.Raw = resp
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newMultipathWwidShowCmd builds `pmx pve cluster multipath wwid show <wwid>`.
func newMultipathWwidShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "show <wwid>",
		Aliases: []string{"get"},
		Short:   "Show one multipath map",
		Long: "Show the multipath map of one LUN, with its alias, retry policy, nodes, expected path " +
			"count, and the configuration digest.",
		Example: `  pmx pve cluster multipath wwid show 3600a0980383030`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.GetMultipathWwid(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("show multipath map %q: %w", args[0], err)
			}
			if resp == nil {
				return fmt.Errorf("show multipath map %q: empty response from server", args[0])
			}
			return renderObject(cmd, deps, resp)
		},
	}
}

// newMultipathWwidCreateCmd builds `pmx pve cluster multipath wwid create <wwid>`.
func newMultipathWwidCreateCmd() *cobra.Command {
	var f multipathWwidFlags
	cmd := &cobra.Command{
		Use:     "create <wwid>",
		Aliases: []string{"add"},
		Short:   "Create a multipath map for a LUN",
		Long: "Create the multipath map of one LUN, named by its WWID. The settings are optional, and " +
			"the nodes assemble the map as soon as the cluster distributes the configuration.",
		Example: `  pmx pve cluster multipath wwid create 3600a0980383030 --alias data1 --paths-expected 4
  pmx pve cluster multipath wwid create 3600a0980383030 --nodes pve1,pve2 --no-path-retry queue`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			params := &pvecluster.CreateMultipathWwidParams{Wwid: args[0]}
			if fl.Changed("alias") {
				params.Alias = &f.alias
			}
			if fl.Changed("digest") {
				params.Digest = &f.digest
			}
			if fl.Changed("no-path-retry") {
				params.NoPathRetry = &f.noPathRetry
			}
			if fl.Changed("nodes") {
				params.Nodes = &f.nodes
			}
			if fl.Changed("paths-expected") {
				params.PathsExpected = &f.pathsExpected
			}
			if err := deps.API.Cluster.CreateMultipathWwid(cmd.Context(), params); err != nil {
				return fmt.Errorf("create multipath map %q: %w", args[0], err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Multipath map %q created.", args[0])}, deps.Format)
		},
	}
	f.bind(cmd)
	return cmd
}

// newMultipathWwidSetCmd builds `pmx pve cluster multipath wwid set <wwid>`.
func newMultipathWwidSetCmd() *cobra.Command {
	var (
		f      multipathWwidFlags
		remove string
	)
	cmd := &cobra.Command{
		Use:     "set <wwid>",
		Aliases: []string{"update"},
		Short:   "Change a multipath map",
		Long: "Change settings of an existing multipath map. Only the settings that are given change. " +
			"--delete takes a comma-separated list of settings to unset, such as alias,nodes.",
		Example: `  pmx pve cluster multipath wwid set 3600a0980383030 --paths-expected 2
  pmx pve cluster multipath wwid set 3600a0980383030 --delete nodes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			params := &pvecluster.UpdateMultipathWwidParams{}
			changed := false
			if fl.Changed("alias") {
				params.Alias, changed = &f.alias, true
			}
			if fl.Changed("digest") {
				params.Digest = &f.digest
			}
			if fl.Changed("no-path-retry") {
				params.NoPathRetry, changed = &f.noPathRetry, true
			}
			if fl.Changed("nodes") {
				params.Nodes, changed = &f.nodes, true
			}
			if fl.Changed("paths-expected") {
				params.PathsExpected, changed = &f.pathsExpected, true
			}
			if fl.Changed("delete") {
				params.Delete, changed = &remove, true
			}
			if !changed {
				return fmt.Errorf("nothing to change: give at least one of --alias, --no-path-retry, " +
					"--nodes, --paths-expected, or --delete")
			}
			if err := deps.API.Cluster.UpdateMultipathWwid(cmd.Context(), args[0], params); err != nil {
				return fmt.Errorf("change multipath map %q: %w", args[0], err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Multipath map %q updated.", args[0])}, deps.Format)
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVar(&remove, "delete", "", "comma-separated list of settings to unset")
	return cmd
}

// newMultipathWwidDeleteCmd builds `pmx pve cluster multipath wwid delete <wwid>`.
func newMultipathWwidDeleteCmd() *cobra.Command {
	var (
		yes    bool
		digest string
	)
	cmd := &cobra.Command{
		Use:     "delete <wwid>",
		Aliases: []string{"rm"},
		Short:   "Delete a multipath map",
		Long: "Delete the multipath map of one LUN. Nodes stop assembling the map, so a LUN that " +
			"storage still uses can lose its multipath device, and the command asks for confirmation. " +
			"On a terminal it prompts, and anywhere else it refuses unless --yes/-y is passed. " +
			"--digest makes the server refuse the delete when the configuration changed since it was read.",
		Example: `  pmx pve cluster multipath wwid delete 3600a0980383030 --yes
  pmx pve cluster multipath wwid delete 3600a0980383030 --digest 6e1f0c --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			wwid := args[0]
			ok, err := cli.ConfirmDestructive(cmd, yes,
				fmt.Sprintf("delete multipath map %q", wwid),
				fmt.Sprintf("Delete the multipath map of %q? Nodes stop assembling it.", wwid))
			if err != nil {
				return err
			}
			if !ok {
				return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: "Aborted."}, deps.Format)
			}
			params := &pvecluster.DeleteMultipathWwidParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}
			if err := deps.API.Cluster.DeleteMultipathWwid(cmd.Context(), wwid, params); err != nil {
				return fmt.Errorf("delete multipath map %q: %w", wwid, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Multipath map %q deleted.", wwid)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the delete without prompting")
	cmd.Flags().StringVar(&digest, "digest", "", "refuse the delete when the configuration digest differs")
	return cmd
}
