package sdn

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newDryRunCmd builds `pve sdn dry-run` — preview the difference between the
// running and pending SDN configuration for a node before committing it with
// `pve sdn apply`. It is read-only: the diffs are computed without changing any
// configuration. Requires a node (--node, PVE_NODE, or a configured default).
func newDryRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dry-run",
		Short: "Preview pending SDN configuration changes",
		Long: "Show the difference between the running and pending SDN " +
			"configuration (the FRR config and /etc/network/interfaces.d/sdn) " +
			"for a node, without applying anything. Use this to review staged " +
			"changes before `pve sdn apply`. Requires a node.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PVE_NODE, or configure a default node")
			}

			resp, err := deps.API.Cluster.ListSdnDryRun(
				cmd.Context(), &cluster.ListSdnDryRunParams{Node: deps.Node})
			if err != nil {
				return fmt.Errorf("preview SDN configuration on node %q: %w", deps.Node, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
	return cmd
}

// newRollbackCmd builds `pve sdn rollback` — discard all pending (staged) SDN
// configuration changes cluster-wide, reverting to the running configuration.
// It is the inverse of `pve sdn apply` and drops every staged SDN edit, so it
// requires confirmation.
func newRollbackCmd() *cobra.Command {
	var (
		yes         bool
		lockToken   string
		releaseLock bool
	)
	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Discard pending SDN configuration changes",
		Long: "Revert all staged SDN configuration changes cluster-wide, " +
			"discarding everything not yet committed with `pve sdn apply`. This " +
			"affects every pending SDN edit, not just your own.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf(
					"refusing to roll back pending SDN configuration without confirmation: pass --yes/-y")
			}

			params := &cluster.CreateSdnRollbackParams{}
			fl := cmd.Flags()
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if fl.Changed("release-lock") {
				params.ReleaseLock = boolPtr(releaseLock)
			}

			if err := deps.API.Cluster.CreateSdnRollback(cmd.Context(), params); err != nil {
				return fmt.Errorf("roll back SDN configuration: %w", err)
			}
			res := output.Result{Message: "Pending SDN configuration changes discarded."}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm rollback without prompting")
	cmd.Flags().StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	cmd.Flags().BoolVar(&releaseLock, "release-lock", false,
		"release the SDN configuration lock after a successful rollback")
	return cmd
}
