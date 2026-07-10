package pbs

import (
	"fmt"

	"github.com/spf13/cobra"

	pbsnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// nodeAptPackageEntry mirrors one element of the JSON array PBS returns from
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

// nodeAptVersionEntry mirrors one element of the JSON array PBS returns from
// GET /nodes/{node}/apt/versions (installed package versions).
type nodeAptVersionEntry struct {
	Package       string `json:"package"`
	Version       string `json:"version"`
	OldVersion    string `json:"oldversion,omitempty"`
	RunningKernel string `json:"runningkernel,omitempty"`
}

// newNodeAptCmd builds `pmx pbs node apt` and its
// ls/update/repositories/repo-add/repo-update/versions/changelog verbs
// (/nodes/{node}/apt...).
func newNodeAptCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apt",
		Short: "Inspect and manage APT packages and repositories on the node",
		Long: "Inspect and manage APT packages and repositories on the node: list available " +
			"updates, refresh the package index, view and edit repository entries, list " +
			"installed package versions, and read a package's changelog.",
	}
	cmd.AddCommand(
		newNodeAptLsCmd(nf),
		newNodeAptUpdateCmd(nf),
		newNodeAptRepositoriesCmd(nf),
		newNodeAptRepoAddCmd(nf),
		newNodeAptRepoUpdateCmd(nf),
		newNodeAptVersionsCmd(nf),
		newNodeAptChangelogCmd(nf),
	)
	return cmd
}

// newNodeAptLsCmd builds `pmx pbs node apt ls` — list available package
// updates (GET /nodes/{node}/apt/update).
func newNodeAptLsCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List available APT package updates",
		Long: "List APT package updates available on the node, with each package's old and " +
			"new version, priority, and section.",
		Example: "  pmx pbs node apt ls",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Nodes.ListAptUpdate(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("list apt updates on node %q: %w", nf.node, err)
			}

			entries, err := nodeDecodeArray[nodeAptPackageEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode apt updates on node %q: %w", nf.node, err)
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

// newNodeAptUpdateCmd builds `pmx pbs node apt update` — refresh the local
// APT package index (POST /nodes/{node}/apt/update).
func newNodeAptUpdateCmd(nf *nodeFlags) *cobra.Command {
	var notify, quiet bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Refresh the node's APT package index",
		Long: "Refresh the local APT package database from the configured repositories. " +
			"Runs as an asynchronous task; the command blocks until it finishes unless --async is set.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			params := &pbsnodes.CreateAptUpdateParams{}
			if fl.Changed("notify") {
				params.Notify = &notify
			}
			if fl.Changed("quiet") {
				params.Quiet = &quiet
			}

			resp, err := deps.PBS.Nodes.CreateAptUpdate(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("refresh apt index on node %q: %w", nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("refresh apt index on node %q: empty response from server", nf.node)
			}

			msg := fmt.Sprintf("APT package index on node %q refreshed.", nf.node)
			return finishAsync(cmd, deps, *resp, msg)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&notify, "notify", false, "notify root@pam by e-mail about available updates")
	f.BoolVar(&quiet, "quiet", false, "only produce output suitable for logging")

	return cmd
}

// newNodeAptRepositoriesCmd builds `pmx pbs node apt repositories` — get
// parsed APT repository information (GET /nodes/{node}/apt/repositories).
func newNodeAptRepositoriesCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "repositories",
		Short: "Show parsed APT repository information",
		Long: "Show a summary of the node's parsed APT repository configuration: the config " +
			"digest, and counts of configured files, standard repositories, errors, and " +
			"informational notices.",
		Example: "  pmx pbs node apt repositories",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Nodes.ListAptRepositories(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("get apt repositories on node %q: %w", nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("get apt repositories on node %q: empty response from server", nf.node)
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

// newNodeAptRepoAddCmd builds `pmx pbs node apt repo-add` — add a standard
// repository identified by handle (PUT /nodes/{node}/apt/repositories).
func newNodeAptRepoAddCmd(nf *nodeFlags) *cobra.Command {
	var handle, digest string

	cmd := &cobra.Command{
		Use:   "repo-add",
		Short: "Add (or enable) a standard APT repository by handle",
		Long: "Add the standard repository identified by --handle. If the repository is " +
			"already configured, it is set to enabled.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbsnodes.UpdateAptRepositoriesParams{Handle: handle}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}

			err := deps.PBS.Nodes.UpdateAptRepositories(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("add apt repository %q on node %q: %w", handle, nf.node, err)
			}

			res := output.Result{Message: fmt.Sprintf("APT repository %q added on node %q.", handle, nf.node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&handle, "handle", "", "handle referencing a standard APT repository (required)")
	f.StringVar(&digest, "digest", "", "prevent changes if the config digest differs")
	cli.MustMarkRequired(cmd, "handle")

	return cmd
}

// newNodeAptRepoUpdateCmd builds `pmx pbs node apt repo-update` — change the
// properties of an existing repository entry, identified by file path and
// index (POST /nodes/{node}/apt/repositories).
func newNodeAptRepoUpdateCmd(nf *nodeFlags) *cobra.Command {
	var (
		path    string
		index   int64
		enabled bool
		digest  string
	)

	cmd := &cobra.Command{
		Use:   "repo-update",
		Short: "Change the enabled state of an existing repository entry",
		Long: "Change the properties (currently: enabled) of the repository entry at " +
			"--index within --path, the containing sources file.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			params := &pbsnodes.CreateAptRepositoriesParams{Index: index, Path: path}
			if fl.Changed("enabled") {
				params.Enabled = &enabled
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}

			err := deps.PBS.Nodes.CreateAptRepositories(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("update apt repository %q[%d] on node %q: %w", path, index, nf.node, err)
			}

			res := output.Result{
				Message: fmt.Sprintf("APT repository %q[%d] on node %q updated.", path, index, nf.node),
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

// newNodeAptVersionsCmd builds `pmx pbs node apt versions` — get package
// information for important PBS packages (GET /nodes/{node}/apt/versions).
func newNodeAptVersionsCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "versions",
		Short: "Show installed versions of important PBS packages",
		Long: "List the installed version, candidate old version, and running kernel (where " +
			"applicable) of every package the PBS installer considers important.",
		Example: "  pmx pbs node apt versions",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Nodes.ListAptVersions(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("list apt versions on node %q: %w", nf.node, err)
			}

			entries, err := nodeDecodeArray[nodeAptVersionEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode apt versions on node %q: %w", nf.node, err)
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

// newNodeAptChangelogCmd builds `pmx pbs node apt changelog --name X` —
// retrieve the changelog of a package (GET /nodes/{node}/apt/changelog).
func newNodeAptChangelogCmd(nf *nodeFlags) *cobra.Command {
	var name, version string

	cmd := &cobra.Command{
		Use:   "changelog",
		Short: "Show the changelog of an APT package",
		Long: "Fetch the Debian changelog text for --name at --version, defaulting to the " +
			"candidate version when --version is omitted.",
		Example: "  pmx pbs node apt changelog --name proxmox-backup-server",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbsnodes.ListAptChangelogParams{Name: name}
			if cmd.Flags().Changed("version") {
				params.Version = &version
			}

			resp, err := deps.PBS.Nodes.ListAptChangelog(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("get changelog for package %q on node %q: %w", name, nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("get changelog for package %q on node %q: empty response from server", name, nf.node)
			}

			text, err := nodeDecodeText(*resp)
			if err != nil {
				return fmt.Errorf("decode changelog for package %q on node %q: %w", name, nf.node, err)
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
