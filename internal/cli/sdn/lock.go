package sdn

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newLockCmd builds `pve sdn lock` and its sub-commands.
func newLockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lock",
		Short: "Manage the global SDN configuration lock",
		Long: "Acquire or release the global SDN configuration lock. The lock prevents " +
			"concurrent modifications to the SDN configuration. Acquiring the lock " +
			"returns a token that must be supplied when releasing it.",
	}
	cmd.AddCommand(newLockAcquireCmd(), newLockReleaseCmd())
	return cmd
}

// newLockAcquireCmd builds `pve sdn lock acquire`.
func newLockAcquireCmd() *cobra.Command {
	var allowPending bool
	cmd := &cobra.Command{
		Use:   "acquire",
		Short: "Acquire the global SDN configuration lock",
		Long: "Acquire the global SDN configuration lock. Returns a lock token that must " +
			"be passed to `pve sdn lock release` (or via --lock-token on other commands) " +
			"to release the lock.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &cluster.CreateSdnLockParams{}
			fl := cmd.Flags()
			if fl.Changed("allow-pending") {
				params.AllowPending = boolPtr(allowPending)
			}
			resp, err := deps.API.Cluster.CreateSdnLock(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("acquire SDN lock: %w", err)
			}
			res := output.Result{Raw: resp}
			if resp != nil && len(*resp) > 0 {
				res.Message = fmt.Sprintf("SDN lock acquired. Token: %s", string(*resp))
			} else {
				res.Message = "SDN lock acquired."
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&allowPending, "allow-pending", false,
		"acquire lock even when there are pending (uncommitted) SDN changes")
	return cmd
}

// newLockReleaseCmd builds `pve sdn lock release`.
func newLockReleaseCmd() *cobra.Command {
	var (
		force     bool
		lockToken string
		yes       bool
	)
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Release the global SDN configuration lock",
		Long: "Release the global SDN configuration lock. Pass the token returned by " +
			"`pve sdn lock acquire` via --lock-token, or use --force to release without " +
			"a token (requires --yes).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to release SDN lock without confirmation: pass --yes")
			}
			params := &cluster.DeleteSdnLockParams{}
			fl := cmd.Flags()
			if fl.Changed("force") {
				params.Force = boolPtr(force)
			}
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if err := deps.API.Cluster.DeleteSdnLock(cmd.Context(), params); err != nil {
				return fmt.Errorf("release SDN lock: %w", err)
			}
			res := output.Result{Message: "SDN lock released."}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&force, "force", false, "release lock without providing the token")
	f.StringVar(&lockToken, "lock-token", "", "token returned when the lock was acquired")
	f.BoolVarP(&yes, "yes", "y", false, "confirm lock release without prompting")
	return cmd
}
