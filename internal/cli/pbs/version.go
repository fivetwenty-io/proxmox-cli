package pbs

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newVersionCmd builds `pmx pbs version` — report the Proxmox Backup Server
// API version, release, and repository id (GET /version).
func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show the PBS server API version",
		Long:  "Show the Proxmox Backup Server API version, release, and repository id (GET /version).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Version.Get(cmd.Context())
			if err != nil {
				return fmt.Errorf("get server version: %w", err)
			}

			if resp == nil {
				return fmt.Errorf("get server version: nil response from PBS")
			}

			single := map[string]string{
				"version": resp.Version,
				"release": resp.Release,
				"repoid":  resp.Repoid,
			}

			res := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newPingCmd builds `pmx pbs ping` — a cheap, unauthenticated connectivity
// check against the PBS API daemon (GET /ping).
func newPingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ping",
		Short: "Check connectivity to the PBS API",
		Long: "Send a dummy request that confirms the Proxmox Backup Server API daemon " +
			"is online and responding (GET /ping). Requires no permissions.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Ping.Ping(cmd.Context())
			if err != nil {
				return fmt.Errorf("ping PBS API: %w", err)
			}

			if resp == nil {
				return fmt.Errorf("ping PBS API: nil response from PBS")
			}

			pong := resp.Pong.Bool()
			single := map[string]string{"pong": strconv.FormatBool(pong)}

			res := output.Result{
				Single:  single,
				Raw:     resp,
				Message: fmt.Sprintf("pong=%v", pong),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
