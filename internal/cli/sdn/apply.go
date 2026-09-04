package sdn

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newApplyCmd builds `pmx pve sdn apply` — commit pending SDN configuration by
// reloading the network config on all nodes (PUT /cluster/sdn). The reload runs
// as an asynchronous task; the command blocks until it completes unless --async
// is set.
func newApplyCmd() *cobra.Command {
	var (
		async       bool
		lockToken   string
		releaseLock bool
	)
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Commit pending SDN configuration",
		Long: "Reload the network configuration on every cluster node, so that pending SDN " +
			"changes take effect: zones, vnets, subnets, controllers, IPAM and DNS " +
			"providers, and fabrics.\n\n" +
			"The reload touches every node in the cluster, so preview what is pending first " +
			"with `pmx pve sdn dry-run`.\n\n" +
			"The command submits a PVE task and blocks until it completes; --async prints " +
			"the task UPID and returns immediately. " + cli.WaitBoundHelp + " Some PVE versions " +
			"return no task at all and report the reload as applied on the spot.",
		Example: `  pmx pve sdn dry-run
  pmx pve sdn apply
  pmx pve sdn apply --async`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			if fl.Changed("async") {
				deps.Async = async
			}

			params := &cluster.UpdateSdnParams{}
			if fl.Changed("lock-token") {
				params.LockToken = new(lockToken)
			}
			if fl.Changed("release-lock") {
				params.ReleaseLock = new(releaseLock)
			}

			cli.WarnIfInquorate(cmd.Context(), deps, cmd.ErrOrStderr(), "the SDN reload")
			resp, err := deps.API.Cluster.UpdateSdn(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("apply SDN configuration: %w", err)
			}

			// PUT /cluster/sdn returns a UPID string for the reload task. Older
			// servers may return null/empty; treat that as an immediate success.
			var raw json.RawMessage
			if resp != nil {
				raw = *resp
			}
			upid, perr := apiclient.UPIDFromRaw(raw)
			if perr != nil || upid == "" {
				res := output.Result{Message: "SDN configuration applied."}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			if deps.Async {
				res := output.Result{
					Single:  map[string]string{"upid": upid},
					Raw:     map[string]string{"upid": upid},
					Message: upid,
				}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}
			if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
				return err
			}
			res := output.Result{Message: "SDN configuration applied."}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&async, "async", false, "return the reload task UPID immediately without waiting")
	cmd.Flags().StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	cmd.Flags().BoolVar(&releaseLock, "release-lock", false, "release the lock after a successful commit (requires --lock-token)")
	return cmd
}
