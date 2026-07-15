package pdm

import (
	"fmt"

	"github.com/spf13/cobra"

	pdmnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// nodeAptPackageEntry mirrors one element of the JSON array PDM returns from
// GET /nodes/{node}/apt/update (available package updates).
type nodeAptPackageEntry struct {
	Package    string `json:"package"`
	OldVersion string `json:"oldversion"`
	Version    string `json:"version"`
	Priority   string `json:"priority"`
	Section    string `json:"section"`
	Origin     string `json:"origin"`
	ExtraInfo  string `json:"extrainfo,omitempty"`
}

// nodeAptVersionEntry mirrors one element of the JSON array PDM returns from
// GET /nodes/{node}/apt/versions (installed package versions).
type nodeAptVersionEntry struct {
	Package       string `json:"package"`
	Version       string `json:"version"`
	OldVersion    string `json:"oldversion,omitempty"`
	RunningKernel string `json:"runningkernel,omitempty"`
}

// newNodeAptCmd builds `pmx pdm node apt` and its
// updates/update-database/repositories/repository/versions/changelog verbs
// (/nodes/{node}/apt...).
func newNodeAptCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apt",
		Short: "Inspect and manage APT packages and repositories on the node",
		Long: "Inspect and manage APT packages and repositories on this Proxmox Datacenter " +
			"Manager's own node: available updates, the package index, configured " +
			"repositories, and package changelogs.",
	}
	cmd.AddCommand(
		newNodeAptUpdatesCmd(),
		newNodeAptUpdateDatabaseCmd(),
		newNodeAptRepositoriesCmd(),
		newNodeAptRepositoryCmd(),
		newNodeAptVersionsCmd(),
		newNodeAptChangelogCmd(),
	)
	return cmd
}

// newNodeAptUpdatesCmd builds `pmx pdm node apt updates <node>` — list
// available package updates (GET /nodes/{node}/apt/update).
func newNodeAptUpdatesCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "updates <node>",
		Short:   "List available APT package updates",
		Long:    "List the APT package updates available on the node (GET /nodes/{node}/apt/update).",
		Example: "  pmx pdm node apt updates pdm-01",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			resp, err := deps.PDM.Nodes.ListAptUpdate(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("list apt updates on node %q: %w", node, err)
			}

			entries, err := nodeDecodeArray[nodeAptPackageEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode apt updates on node %q: %w", node, err)
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

// newNodeAptUpdateDatabaseCmd builds `pmx pdm node apt update-database
// <node>` — refresh the local APT package index (POST
// /nodes/{node}/apt/update).
//
// CreateAptUpdate runs as an asynchronous task: its returns.pattern in the
// PDM API schema is the UPID regex (pdm-apidoc.json, verified 2026-07-08),
// and nodes_gen.go types CreateAptUpdateResponse as `= json.RawMessage`
// (nodes_gen.go:463-464,467-506, v3.6.0), matching the PBS analog which is
// also treated as asynchronous (internal/cli/pbs/node_apt.go:87-127).
func newNodeAptUpdateDatabaseCmd() *cobra.Command {
	var notify, quiet bool

	cmd := &cobra.Command{
		Use:   "update-database <node>",
		Short: "Refresh the node's APT package index",
		Long: "Refresh the local APT package database from the configured repositories " +
			"(POST /nodes/{node}/apt/update). Runs as an asynchronous task; the command " +
			"blocks until it finishes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			fl := cmd.Flags()

			params := &pdmnodes.CreateAptUpdateParams{}
			if fl.Changed("notify") {
				params.Notify = &notify
			}
			if fl.Changed("quiet") {
				params.Quiet = &quiet
			}

			resp, err := deps.PDM.Nodes.CreateAptUpdate(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("refresh apt index on node %q: %w", node, err)
			}
			if resp == nil {
				return fmt.Errorf("refresh apt index on node %q: empty response from server", node)
			}

			msg := fmt.Sprintf("APT package index on node %q refreshed.", node)
			return finishAsync(cmd, deps, *resp, msg)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&notify, "notify", false, "notify root@pam by e-mail about available updates")
	f.BoolVar(&quiet, "quiet", false, "only produce output suitable for logging")

	return cmd
}

// newNodeAptRepositoriesCmd builds `pmx pdm node apt repositories <node>` —
// get parsed APT repository information (GET /nodes/{node}/apt/repositories).
// The response is Raw-heavy (parsed source files, standard-repo status,
// warnings), so Single carries only summary counts while Raw renders the
// full structure losslessly.
func newNodeAptRepositoriesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "repositories <node>",
		Short: "Show parsed APT repository information",
		Long: "Show the node's configured APT repositories, parsed from its sources files, " +
			"including standard-repo status and any warnings (GET /nodes/{node}/apt/repositories).",
		Example: "  pmx pdm node apt repositories pdm-01",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			resp, err := deps.PDM.Nodes.ListAptRepositories(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("get apt repositories on node %q: %w", node, err)
			}
			if resp == nil {
				return fmt.Errorf("get apt repositories on node %q: empty response from server", node)
			}

			single := map[string]string{
				"digest":         resp.Digest,
				"files":          fmt.Sprintf("%d", len(resp.Files)),
				"standard-repos": fmt.Sprintf("%d", len(resp.StandardRepos)),
				"errors":         fmt.Sprintf("%d", len(resp.Errors)),
				"infos":          fmt.Sprintf("%d", len(resp.Infos)),
			}

			res := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newNodeAptRepositoryCmd builds `pmx pdm node apt repository` and its
// add/change verbs. The bindings' method naming is inverted from HTTP intent
// (see newNodeAptRepositoryAddCmd's and newNodeAptRepositoryChangeCmd's
// comments): the generator's Create*/Update* prefixes follow the HTTP verb
// (POST/PUT), not the operation's semantics.
func newNodeAptRepositoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repository",
		Short: "Add or change an APT repository entry",
		Long: "Add a standard APT repository by handle, or change the enabled state of an " +
			"existing repository entry.",
	}
	cmd.AddCommand(newNodeAptRepositoryAddCmd(), newNodeAptRepositoryChangeCmd())
	return cmd
}

// newNodeAptRepositoryAddCmd builds `pmx pdm node apt repository add
// <node>` — add (or enable) a standard repository identified by handle (PUT
// /nodes/{node}/apt/repositories).
//
// This binds Nodes.UpdateAptRepositories despite the "Update" name: PUT
// /nodes/{node}/apt/repositories's schema description is "Add the repository
// identified by the handle... If already configured, it will be set to
// enabled" (pdm-apidoc.json, verified 2026-07-08), and its params type is
// UpdateAptRepositoriesParams{Handle, Digest} (nodes_gen.go:384-420, v3.6.0)
// — the generator names bindings after the HTTP verb, not the operation, so
// the PUT (add) binding is named Update* and the POST (change) binding is
// named Create* (see newNodeAptRepositoryChangeCmd). Runs synchronously:
// returns.type is "null" (verified 2026-07-08).
func newNodeAptRepositoryAddCmd() *cobra.Command {
	var handle, digest string

	cmd := &cobra.Command{
		Use:   "add <node>",
		Short: "Add (or enable) a standard APT repository by handle",
		// add enables a well-known repository rather than creating one;
		// a "create" alias would misdescribe the command.
		Annotations: map[string]string{cli.AnnotationNoVerbAlias: "true"},
		Long: "Add the standard repository identified by --handle. If the repository is " +
			"already configured, it is set to enabled (PUT /nodes/{node}/apt/repositories).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			params := &pdmnodes.UpdateAptRepositoriesParams{Handle: handle}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}

			err := deps.PDM.Nodes.UpdateAptRepositories(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("add apt repository %q on node %q: %w", handle, node, err)
			}

			res := output.Result{Message: fmt.Sprintf("APT repository %q added on node %q.", handle, node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&handle, "handle", "", "handle referencing a standard APT repository (required)")
	f.StringVar(&digest, "digest", "", "prevent changes if the config digest differs")
	cli.MustMarkRequired(cmd, "handle")

	return cmd
}

// newNodeAptRepositoryChangeCmd builds `pmx pdm node apt repository change
// <node>` — change the properties (currently: enabled) of an existing
// repository entry, identified by file path and index (POST
// /nodes/{node}/apt/repositories).
//
// This binds Nodes.CreateAptRepositories despite the "Create" name — see
// newNodeAptRepositoryAddCmd's comment for the generator's HTTP-verb-based
// naming. Runs synchronously: returns.type is "null" (verified 2026-07-08).
func newNodeAptRepositoryChangeCmd() *cobra.Command {
	var (
		path    string
		index   int64
		enabled bool
		digest  string
	)

	cmd := &cobra.Command{
		Use:   "change <node>",
		Short: "Change the enabled state of an existing repository entry",
		Long: "Change the properties (currently: enabled) of the repository entry at " +
			"--index within --path, the containing sources file (POST " +
			"/nodes/{node}/apt/repositories).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			fl := cmd.Flags()

			params := &pdmnodes.CreateAptRepositoriesParams{Index: index, Path: path}
			if fl.Changed("enabled") {
				params.Enabled = &enabled
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}

			err := deps.PDM.Nodes.CreateAptRepositories(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("change apt repository %q[%d] on node %q: %w", path, index, node, err)
			}

			res := output.Result{
				Message: fmt.Sprintf("APT repository %q[%d] on node %q changed.", path, index, node),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&path, "path", "", "path to the containing sources file (required)")
	f.Int64Var(&index, "index", 0, "index of the repository entry within the file (required)")
	f.BoolVar(&enabled, "enabled", false, "whether the repository should be enabled")
	f.StringVar(&digest, "digest", "", "prevent changes if the config digest differs")
	cli.MustMarkRequired(cmd, "path")
	cli.MustMarkRequired(cmd, "index")

	return cmd
}

// newNodeAptVersionsCmd builds `pmx pdm node apt versions <node>` — get
// package information for important PDM packages (GET
// /nodes/{node}/apt/versions).
func newNodeAptVersionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "versions <node>",
		Short:   "Show installed versions of important PDM packages",
		Long:    "Show installed versions of important PDM packages (GET /nodes/{node}/apt/versions).",
		Example: "  pmx pdm node apt versions pdm-01",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			resp, err := deps.PDM.Nodes.ListAptVersions(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("list apt versions on node %q: %w", node, err)
			}

			entries, err := nodeDecodeArray[nodeAptVersionEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode apt versions on node %q: %w", node, err)
			}

			headers := []string{"PACKAGE", "VERSION", "OLD-VERSION", "RUNNING-KERNEL"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{e.Package, e.Version, e.OldVersion, e.RunningKernel})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newNodeAptChangelogCmd builds `pmx pdm node apt changelog <node>
// --name X` — retrieve the changelog of a package (GET
// /nodes/{node}/apt/changelog).
func newNodeAptChangelogCmd() *cobra.Command {
	var name, version string

	cmd := &cobra.Command{
		Use:   "changelog <node>",
		Short: "Show the changelog of an APT package",
		Long: "Retrieve the changelog of an APT package identified by --name, optionally at a " +
			"specific --version (GET /nodes/{node}/apt/changelog).",
		Example: "  pmx pdm node apt changelog pdm-01 --name proxmox-datacenter-manager",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			params := &pdmnodes.ListAptChangelogParams{Name: name}
			if cmd.Flags().Changed("version") {
				params.Version = &version
			}

			resp, err := deps.PDM.Nodes.ListAptChangelog(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("get changelog for package %q on node %q: %w", name, node, err)
			}
			if resp == nil {
				return fmt.Errorf("get changelog for package %q on node %q: empty response from server", name, node)
			}

			text, err := nodeDecodeText(*resp)
			if err != nil {
				return fmt.Errorf("decode changelog for package %q on node %q: %w", name, node, err)
			}

			res := output.Result{Message: text, Raw: text}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "package name (required)")
	f.StringVar(&version, "version", "", "package version (defaults to the candidate version)")
	cli.MustMarkRequired(cmd, "name")

	return cmd
}
