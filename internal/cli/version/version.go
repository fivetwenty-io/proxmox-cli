package version

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	selfversion "github.com/fivetwenty-io/pve-cli/internal/version"
)

func init() {
	cli.RegisterGroup(newGroupCmd)
}

// newGroupCmd builds the `pve version` command and its sub-commands.
//
// The group command itself reports the Proxmox VE cluster API version (it
// contacts the server). The `client` sub-command reports this CLI's own build
// information and contacts nothing.
func newGroupCmd(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show the Proxmox VE cluster API version",
		Long: "Show the Proxmox VE cluster API version reported by the target.\n\n" +
			"Use `pve version client` to show this CLI's own build information.",
		Args: cobra.NoArgs,
		RunE: runClusterVersion,
	}
	cmd.AddCommand(newClientCmd())
	return cmd
}

// runClusterVersion queries GET /version and renders the cluster API version.
func runClusterVersion(cmd *cobra.Command, _ []string) error {
	deps := cli.GetDeps(cmd)

	resp, err := deps.API.Version.Get(cmd.Context())
	if err != nil {
		return fmt.Errorf("get cluster version: %w", err)
	}

	console := ""
	if resp.Console != nil {
		console = *resp.Console
	}

	result := output.Result{
		Headers: []string{"VERSION", "RELEASE", "REPOID", "CONSOLE"},
		Rows:    [][]string{{resp.Version, resp.Release, resp.Repoid, console}},
		Raw:     resp,
	}
	return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
}

// newClientCmd builds `pve version client`, which reports CLI build info only.
// It is annotated noClient so the root skips API client construction.
func newClientCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "client",
		Short:       "Show this CLI's build information",
		Long:        "Show this CLI's version, commit, build date, and Go toolchain details.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{"noClient": "true"},
		RunE:        runClientVersion,
	}
}

// clientInfo is the structured shape rendered for `pve version client` in JSON
// and YAML output.
type clientInfo struct {
	// Version is the human-readable CLI release tag.
	Version string `json:"version"`
	// Commit is the VCS commit hash at build time.
	Commit string `json:"commit"`
	// Date is the build timestamp.
	Date string `json:"date"`
	// GoVersion is the Go toolchain used to compile the binary.
	GoVersion string `json:"go_version"`
	// OS is the target operating system.
	OS string `json:"os"`
	// Arch is the target CPU architecture.
	Arch string `json:"arch"`
}

// runClientVersion renders this CLI's build information.
func runClientVersion(cmd *cobra.Command, _ []string) error {
	deps := cli.GetDeps(cmd)
	info := selfversion.GetInfo()

	result := output.Result{
		Single: map[string]string{
			"VERSION": info.Version,
			"COMMIT":  info.Commit,
			"DATE":    info.Date,
			"GO":      info.GoVersion,
			"OS/ARCH": fmt.Sprintf("%s/%s", info.OS, info.Arch),
		},
		Raw: clientInfo{
			Version:   info.Version,
			Commit:    info.Commit,
			Date:      info.Date,
			GoVersion: info.GoVersion,
			OS:        info.OS,
			Arch:      info.Arch,
		},
	}
	return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
}
