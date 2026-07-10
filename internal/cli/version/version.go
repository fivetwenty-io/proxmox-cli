package version

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	selfversion "github.com/fivetwenty-io/pmx-cli/internal/version"
)

// Group builds the `pmx version` command and its sub-commands.
//
// The group command itself reports the active context's server API version —
// a Proxmox VE cluster version, a Proxmox Backup Server version, or a Proxmox
// Datacenter Manager version, depending on which product the active context
// targets (see cli.ProductFromContext). The `client` sub-command reports this
// CLI's own build information and contacts nothing. The `ping` sub-command
// checks PBS connectivity and requires a PBS context.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show the connected server's API version",
		Long: "Show the API version reported by the active context's server (PVE cluster, PBS, or PDM).\n\n" +
			"Use `pmx version client` to show this CLI's own build information.",
		Example: `  pmx version
  pmx version --context lab
  pmx version client
  pmx version ping --context backup`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{cli.ProductAnnotation: cli.ProductFromContext},
		RunE:        runServerVersion,
	}
	cmd.AddCommand(newClientCmd(), newPingCmd())
	return cmd
}

// runServerVersion queries GET /version on the active context's client and
// renders the server API version. It branches on which client the root wired
// into deps: a PBS context populates deps.PBS, a PDM context populates
// deps.PDM, everything else populates deps.API.
func runServerVersion(cmd *cobra.Command, _ []string) error {
	deps := cli.GetDeps(cmd)

	if deps.PBS != nil {
		resp, err := deps.PBS.Version.Get(cmd.Context())
		if err != nil {
			return fmt.Errorf("get server version: %w", err)
		}

		single := map[string]string{"version": resp.Version, "release": resp.Release, "repoid": resp.Repoid}
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: resp}, deps.Format)
	}

	if deps.PDM != nil {
		resp, err := deps.PDM.Version.Get(cmd.Context())
		if err != nil {
			return fmt.Errorf("get server version: %w", err)
		}

		single := map[string]string{"version": resp.Version, "release": resp.Release, "repoid": resp.Repoid}
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single, Raw: resp}, deps.Format)
	}

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

// newPingCmd builds `pmx version ping` — a cheap connectivity check against
// the PBS API daemon (GET /ping). Ping has no PVE cluster equivalent, so this
// sub-command requires a PBS context and fails clearly otherwise.
func newPingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ping",
		Short: "Check connectivity to the PBS API",
		Long: "Send a dummy request that confirms the Proxmox Backup Server API daemon is online " +
			"(GET /ping). Requires a PBS context.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if deps.PBS == nil {
				return fmt.Errorf("ping is only available for a PBS context")
			}

			resp, err := deps.PBS.Ping.Ping(cmd.Context())
			if err != nil {
				return fmt.Errorf("ping PBS API: %w", err)
			}
			if resp == nil {
				return fmt.Errorf("ping PBS API: nil response from PBS")
			}

			pong := resp.Pong.Bool()
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{
				Single:  map[string]string{"pong": strconv.FormatBool(pong)},
				Raw:     resp,
				Message: fmt.Sprintf("pong=%v", pong),
			}, deps.Format)
		},
	}
}

// newClientCmd builds `pmx version client`, which reports CLI build info only.
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

// clientInfo is the structured shape rendered for `pmx version client` in JSON
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
