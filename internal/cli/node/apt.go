package node

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newAptCmd builds the `pmx node apt` sub-tree: package update inspection,
// installed-version reporting, changelogs, the apt database refresh, and APT
// repository management for the resolved node.
func newAptCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apt",
		Short: "Manage APT packages and repositories on a node",
		Long: "Inspect pending package updates and installed versions, read package " +
			"changelogs, refresh the APT database, and manage configured repositories " +
			"on the resolved node.",
	}
	cmd.AddCommand(
		newAptListCmd(),
		newAptVersionsCmd(),
		newAptChangelogCmd(),
		newAptUpdateCmd(),
		newAptRepositoriesCmd(),
		newAptTemplatesCmd(),
	)
	return cmd
}

// aptPackageEntry is the subset of an apt update/versions list element rendered
// in the table. PVE uses capitalized keys (they mirror apt's package metadata);
// the full element is preserved in the JSON/Raw output.
type aptPackageEntry struct {
	Package      string `json:"Package"`
	Version      string `json:"Version"`
	OldVersion   string `json:"OldVersion"`
	Priority     string `json:"Priority"`
	Origin       string `json:"Origin"`
	CurrentState string `json:"CurrentState"`
}

// ---- list (pending updates) ------------------------------------------------

func newAptListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List packages with pending updates",
		Long: "List the packages on the resolved node that have an available update. " +
			"INSTALLED is the currently installed version and CANDIDATE is the version " +
			"the update would install.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListAptUpdate(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list pending updates on node %q: %w", deps.Node, err)
			}
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e aptPackageEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode update entry: %w", err)
					}
					rows = append(rows, []string{e.Package, e.OldVersion, e.Version, e.Priority, e.Origin})
				}
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{
					Headers: []string{"PACKAGE", "INSTALLED", "CANDIDATE", "PRIORITY", "ORIGIN"},
					Rows:    rows,
					Raw:     resp,
				}, deps.Format)
		},
	}
}

// ---- versions (installed) --------------------------------------------------

func newAptVersionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "versions",
		Short: "List installed versions of Proxmox-relevant packages",
		Long: "List the installed versions of the packages APT considers relevant to Proxmox " +
			"on the resolved node, with their current state and priority.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListAptVersions(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list installed versions on node %q: %w", deps.Node, err)
			}
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e aptPackageEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode version entry: %w", err)
					}
					rows = append(rows, []string{e.Package, e.Version, e.CurrentState, e.Priority, e.Origin})
				}
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{
					Headers: []string{"PACKAGE", "VERSION", "STATE", "PRIORITY", "ORIGIN"},
					Rows:    rows,
					Raw:     resp,
				}, deps.Format)
		},
	}
}

// ---- changelog -------------------------------------------------------------

func newAptChangelogCmd() *cobra.Command {
	var (
		name    string
		version string
	)
	cmd := &cobra.Command{
		Use:   "changelog",
		Short: "Show the changelog of a package",
		Long: "Show the changelog text of a package on the resolved node. --name is " +
			"required; --version defaults to the candidate (available update) version.",
		Example: `  pmx pve node apt changelog --name pve-manager
  pmx pve node apt changelog --name pve-manager --version 8.2.4`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.ListAptChangelogParams{Name: name}
			if cmd.Flags().Changed("version") {
				params.Version = &version
			}
			resp, err := deps.API.Nodes.ListAptChangelog(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("read changelog for package %q on node %q: %w", name, deps.Node, err)
			}
			// The changelog endpoint returns the text as a single JSON string.
			var text string
			if resp != nil && len(*resp) > 0 {
				if err := json.Unmarshal(*resp, &text); err != nil {
					// Not a bare string (unexpected); fall back to the raw body.
					text = string(*resp)
				}
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: text, Raw: text}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "package name (required)")
	f.StringVar(&version, "version", "", "package version (defaults to the candidate version)")
	cli.MustMarkRequired(cmd, "name")
	return cmd
}

// ---- update (refresh apt database) -----------------------------------------

func newAptUpdateCmd() *cobra.Command {
	var (
		notify bool
		quiet  bool
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Refresh the APT package database",
		Long: "Refresh the package database on the resolved node (equivalent to " +
			"`apt-get update`). The command blocks until the refresh task finishes " +
			"unless --async is set.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.CreateAptUpdateParams{}
			fl := cmd.Flags()
			if fl.Changed("notify") {
				params.Notify = &notify
			}
			if fl.Changed("quiet") {
				params.Quiet = &quiet
			}
			resp, err := deps.API.Nodes.CreateAptUpdate(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("refresh package database on node %q: %w", deps.Node, err)
			}
			var raw json.RawMessage
			if resp != nil {
				raw = json.RawMessage(*resp)
			}
			return renderAptUpdateTask(cmd, deps, raw)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&notify, "notify", false, "send a notification about new packages")
	f.BoolVar(&quiet, "quiet", false, "produce output suitable for logging, omitting progress indicators")
	return cmd
}

// renderAptUpdateTask renders the result of the asynchronous apt refresh. The
// endpoint returns a task UPID; with --async the UPID is printed immediately,
// otherwise the command blocks on the task. A non-UPID payload falls back to a
// plain success message.
func renderAptUpdateTask(cmd *cobra.Command, deps *cli.Deps, raw json.RawMessage) error {
	doneMsg := fmt.Sprintf("Package database refreshed on node %q.", deps.Node)
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
		return fmt.Errorf("refresh package database on node %q: %w", deps.Node, err)
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
}

// ---- repositories ----------------------------------------------------------

func newAptRepositoriesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "repositories",
		Aliases: []string{"repos"},
		Short:   "Manage configured APT repositories",
		Long: "List the standard APT repositories and their status, add a standard " +
			"repository, or enable/disable a configured repository on the resolved node.",
	}
	cmd.AddCommand(
		newAptReposListCmd(),
		newAptReposAddCmd(),
		newAptReposEnableCmd(),
	)
	return cmd
}

// aptStandardRepo is the subset of a standard-repos element rendered in the
// table. status is 1 when the repository is configured and enabled, 0 when
// configured but disabled, and absent when it is not configured at all.
type aptStandardRepo struct {
	Handle string `json:"handle"`
	Name   string `json:"name"`
	Status *int64 `json:"status"`
}

func aptRepoStatusCell(status *int64) string {
	if status == nil {
		return "not configured"
	}
	if *status == 0 {
		return "disabled"
	}
	return "enabled"
}

func newAptReposListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the standard APT repositories and their status",
		Long: "List the standard Proxmox APT repositories and, for each, whether it is " +
			"enabled, disabled, or not configured on the resolved node.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListAptRepositories(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list APT repositories on node %q: %w", deps.Node, err)
			}
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range resp.StandardRepos {
					var r aptStandardRepo
					if err := json.Unmarshal(raw, &r); err != nil {
						return fmt.Errorf("decode standard repository entry: %w", err)
					}
					rows = append(rows, []string{r.Handle, r.Name, aptRepoStatusCell(r.Status)})
				}
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{
					Headers: []string{"HANDLE", "NAME", "STATUS"},
					Rows:    rows,
					Raw:     resp,
				}, deps.Format)
		},
	}
}

func newAptReposAddCmd() *cobra.Command {
	var (
		handle string
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a standard repository to the configuration",
		Long: "Add a standard Proxmox repository, identified by --handle, to the resolved " +
			"node's APT configuration. Refuses to run without --yes/-y.",
		Example: `  pmx pve node apt repositories add --handle no-subscription --yes`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("refusing to modify APT repositories on node %q without confirmation: pass --yes/-y", deps.Node)
			}
			params := &nodes.UpdateAptRepositoriesParams{Handle: handle}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}
			if err := deps.API.Nodes.UpdateAptRepositories(cmd.Context(), deps.Node, params); err != nil {
				return fmt.Errorf("add APT repository %q on node %q: %w", handle, deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Standard repository %q added on node %q.", handle, deps.Node)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&handle, "handle", "", "standard repository handle, for example no-subscription (required)")
	f.StringVar(&digest, "digest", "", "expected configuration digest to guard against concurrent edits")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the change without prompting")
	cli.MustMarkRequired(cmd, "handle")
	return cmd
}

func newAptReposEnableCmd() *cobra.Command {
	var (
		path    string
		index   int64
		enabled bool
		digest  string
		yes     bool
	)
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Enable or disable a configured repository",
		Long: "Enable (the default) or disable (--enabled=false) the repository at the " +
			"given --index within the repository file at --path.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("refusing to modify APT repositories on node %q without confirmation: pass --yes/-y", deps.Node)
			}
			params := &nodes.CreateAptRepositoriesParams{Path: path, Index: index}
			fl := cmd.Flags()
			if fl.Changed("enabled") {
				params.Enabled = &enabled
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if err := deps.API.Nodes.CreateAptRepositories(cmd.Context(), deps.Node, params); err != nil {
				return fmt.Errorf("update APT repository %s[%d] on node %q: %w", path, index, deps.Node, err)
			}
			state := "enabled"
			if fl.Changed("enabled") && !enabled {
				state = "disabled"
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Repository %s[%d] %s on node %q.", path, index, state, deps.Node)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&path, "path", "", "path to the repository file (required)")
	f.Int64Var(&index, "index", 0, "index of the repository within the file, starting from 0 (required)")
	f.BoolVar(&enabled, "enabled", true, "whether the repository should be enabled")
	f.StringVar(&digest, "digest", "", "expected configuration digest to guard against concurrent edits")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the change without prompting")
	cli.MustMarkRequired(cmd, "path")
	cli.MustMarkRequired(cmd, "index")
	return cmd
}
