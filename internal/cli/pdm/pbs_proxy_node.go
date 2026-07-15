package pdm

import (
	"fmt"

	"github.com/spf13/cobra"

	pdmpbs "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/pbs"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newPbsNodeCmd builds `pmx pdm pbs node` — inspect a PBS remote's node(s):
// APT packages/repositories and subscription status.
func newPbsNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Inspect a PBS remote's node(s)",
		Long:  "Inspect a PBS remote's node(s): APT packages and repositories, and subscription status.",
	}
	cmd.AddCommand(newPbsNodeAptCmd(), newPbsNodeSubscriptionCmd())
	return cmd
}

// newPbsNodeAptCmd builds `pmx pdm pbs node apt` — updates/update-database/
// repositories/changelog verbs (/pbs/remotes/{remote}/nodes/{node}/apt...).
func newPbsNodeAptCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apt",
		Short: "Inspect and manage APT packages and repositories on a PBS remote's node",
		Long: "Inspect and manage APT packages and repositories on a PBS remote's node: " +
			"available updates, the package index, configured repositories, and package " +
			"changelogs.",
	}
	cmd.AddCommand(
		newPbsNodeAptUpdatesCmd(),
		newPbsNodeAptUpdateDatabaseCmd(),
		newPbsNodeAptRepositoriesCmd(),
		newPbsNodeAptChangelogCmd(),
	)
	return cmd
}

// pbsNodeAptPackageEntry mirrors one element of the JSON array returned by
// GET /pbs/remotes/{remote}/nodes/{node}/apt/update. Field names are
// capitalized because PDM proxies the PBS host's own APT-update response
// verbatim here, unlike PDM's own /nodes/{node}/apt/update (lowercase field
// names — see nodeAptPackageEntry, node_apt.go).
type pbsNodeAptPackageEntry struct {
	Package    string `json:"Package"`
	OldVersion string `json:"OldVersion,omitempty"`
	Version    string `json:"Version"`
	Priority   string `json:"Priority"`
	Section    string `json:"Section"`
	Origin     string `json:"Origin"`
	Arch       string `json:"Arch,omitempty"`
	ExtraInfo  string `json:"ExtraInfo,omitempty"`
}

// newPbsNodeAptUpdatesCmd builds `pmx pdm pbs node apt updates <remote>
// <node>` — list available package updates for a remote PVE/PBS node (GET
// /pbs/remotes/{remote}/nodes/{node}/apt/update).
func newPbsNodeAptUpdatesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "updates <remote> <node>",
		Short: "List available APT package updates on a PBS remote's node",
		Long: "List available APT package updates for a PBS remote's node (GET " +
			"/pbs/remotes/{remote}/nodes/{node}/apt/update).",
		Example: "  pmx pdm pbs node apt updates pbs-main pbs1",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]

			resp, err := deps.PDM.Pbs.ListRemotesNodesAptUpdate(cmd.Context(), remote, node)
			if err != nil {
				return fmt.Errorf("list apt updates on node %q of PBS remote %q: %w", node, remote, err)
			}

			entries, err := nodeDecodeArray[pbsNodeAptPackageEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode apt updates on node %q of PBS remote %q: %w", node, remote, err)
			}

			headers := []string{"PACKAGE", "OLD-VERSION", "NEW-VERSION", "PRIORITY", "SECTION"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{e.Package, e.OldVersion, e.Version, e.Priority, e.Section})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newPbsNodeAptUpdateDatabaseCmd builds `pmx pdm pbs node apt
// update-database <remote> <node>` — update the APT database of a remote
// node (POST /pbs/remotes/{remote}/nodes/{node}/apt/update).
//
// CreateRemotesNodesAptUpdate returns CreateRemotesNodesAptUpdateResponse =
// json.RawMessage (pbs_gen.go:631-662, v3.6.0), carrying the task's UPID
// string — a data-bearing response, not a discarded one. This task runs on
// the PBS remote itself, not on PDM's own node, so its completion is polled
// via finishRemoteAsync (which polls the pbs group's
// ListRemotesTasksStatus) rather than finishAsync (which polls PDM's local
// node-task endpoint and would 404 for a remote-hosted UPID).
func newPbsNodeAptUpdateDatabaseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update-database <remote> <node>",
		Short: "Refresh the APT package database on a PBS remote's node",
		Long: "Refresh the local APT package database from the configured repositories " +
			"on a PBS remote's node (POST /pbs/remotes/{remote}/nodes/{node}/apt/update). " +
			"Runs as an asynchronous task on the remote; the command blocks until it " +
			"finishes unless --async (persistent flag) is set.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]

			resp, err := deps.PDM.Pbs.CreateRemotesNodesAptUpdate(cmd.Context(), remote, node)
			if err != nil {
				return fmt.Errorf("refresh apt index on node %q of PBS remote %q: %w", node, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("refresh apt index on node %q of PBS remote %q: empty response from server", node, remote)
			}

			msg := fmt.Sprintf("APT package index on node %q of PBS remote %q refreshed.", node, remote)
			return finishRemoteAsync(cmd, deps, remote, *resp, msg)
		},
	}
}

// newPbsNodeAptRepositoriesCmd builds `pmx pdm pbs node apt repositories
// <remote> <node>` — get configured APT repositories on a remote node (GET
// /pbs/remotes/{remote}/nodes/{node}/apt/repositories). The response is
// Raw-heavy (parsed source files, standard-repo status, warnings), so Single
// carries only summary counts while Raw renders the full structure
// losslessly, matching node_apt.go's `node apt repositories`.
func newPbsNodeAptRepositoriesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "repositories <remote> <node>",
		Short: "Show parsed APT repository information on a PBS remote's node",
		Long: "Show a PBS remote node's configured APT repositories, parsed from its sources " +
			"files, including standard-repo status and any warnings (GET " +
			"/pbs/remotes/{remote}/nodes/{node}/apt/repositories).",
		Example: "  pmx pdm pbs node apt repositories pbs-main pbs1",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]

			out, err := deps.PDM.Pbs.ListRemotesNodesAptRepositories(cmd.Context(), remote, node)
			if err != nil {
				return fmt.Errorf("get apt repositories on node %q of PBS remote %q: %w", node, remote, err)
			}
			if out == nil {
				return fmt.Errorf("get apt repositories on node %q of PBS remote %q: empty response from server", node, remote)
			}

			single := map[string]string{
				"digest":         out.Digest,
				"files":          fmt.Sprintf("%d", len(out.Files)),
				"standard-repos": fmt.Sprintf("%d", len(out.StandardRepos)),
				"errors":         fmt.Sprintf("%d", len(out.Errors)),
				"infos":          fmt.Sprintf("%d", len(out.Infos)),
			}

			res := output.Result{Single: single, Raw: out}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newPbsNodeAptChangelogCmd builds `pmx pdm pbs node apt changelog <remote>
// <node> <package>` — retrieve the changelog of a package on a remote node
// (GET /pbs/remotes/{remote}/nodes/{node}/apt/changelog).
func newPbsNodeAptChangelogCmd() *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "changelog <remote> <node> <package>",
		Short: "Show the changelog of an APT package on a PBS remote's node",
		Long: "Retrieve the changelog of an APT package on a PBS remote's node, optionally " +
			"at a specific --version (GET /pbs/remotes/{remote}/nodes/{node}/apt/changelog).",
		Example: "  pmx pdm pbs node apt changelog pbs-main pbs1 proxmox-backup-server",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node, pkg := args[0], args[1], args[2]

			params := &pdmpbs.ListRemotesNodesAptChangelogParams{Name: pkg}
			if cmd.Flags().Changed("version") {
				params.Version = &version
			}

			resp, err := deps.PDM.Pbs.ListRemotesNodesAptChangelog(cmd.Context(), remote, node, params)
			if err != nil {
				return fmt.Errorf("get changelog for package %q on node %q of PBS remote %q: %w", pkg, node, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get changelog for package %q on node %q of PBS remote %q: empty response from server",
					pkg, node, remote)
			}

			text, err := nodeDecodeText(*resp)
			if err != nil {
				return fmt.Errorf("decode changelog for package %q on node %q of PBS remote %q: %w", pkg, node, remote, err)
			}

			res := output.Result{Message: text, Raw: text}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "package version (defaults to the candidate version)")
	return cmd
}

// newPbsNodeSubscriptionCmd builds `pmx pdm pbs node subscription <remote>
// <node>` — get subscription info for the PBS remote (GET
// /pbs/remotes/{remote}/nodes/{node}/subscription). Unlike PDM's own node
// subscription group (node_subscription.go's show/update), this endpoint has
// no companion POST to trigger a refresh, so there is no `update` sub-command.
func newPbsNodeSubscriptionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subscription <remote> <node>",
		Short: "Show a PBS remote node's subscription info",
		Long: "Show subscription info for a PBS remote's node (GET " +
			"/pbs/remotes/{remote}/nodes/{node}/subscription).",
		Example: "  pmx pdm pbs node subscription pbs-main pbs1",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]

			resp, err := deps.PDM.Pbs.ListRemotesNodesSubscription(cmd.Context(), remote, node)
			if err != nil {
				return fmt.Errorf("get subscription for node %q of PBS remote %q: %w", node, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get subscription for node %q of PBS remote %q: empty response from server", node, remote)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode subscription for node %q of PBS remote %q: %w", node, remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}
