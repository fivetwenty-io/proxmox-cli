package lxc

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/apiclient"
	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
)

// newRemoteMigrateCmd builds `pmx lxc remote-migrate <vmid> --target-endpoint EP
// --target-storage ST --target-bridge BR [flags]`.
//
// Cross-cluster container migration. This operation is irreversible: the
// container is moved to a remote PVE cluster. Requires --yes to proceed.
// The response is a UPID; the command blocks until the task completes unless
// --async is given.
func newRemoteMigrateCmd() *cobra.Command {
	var (
		yes            bool
		async          bool
		targetEndpoint string
		targetStorage  string
		targetBridge   string
		targetVmid     int64
		online         bool
		restart        bool
		deleteSource   bool
		bwlimit        float64
		timeout        int64
	)

	cmd := &cobra.Command{
		Use:   "remote-migrate <vmid|name>",
		Short: "Migrate a container to a remote PVE cluster",
		Long: "Migrate an LXC container to a different PVE cluster. " +
			"--target-endpoint, --target-storage, and --target-bridge are required. " +
			"This operation moves the container across cluster boundaries and is irreversible " +
			"unless --delete is omitted (the default keeps a stopped copy on the source). " +
			"Pass --yes to confirm. The command blocks until the task completes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			if !yes {
				return fmt.Errorf(
					"refusing to remote-migrate container %s without confirmation: pass --yes to proceed",
					vmid,
				)
			}

			fl := cmd.Flags()
			if !fl.Changed("target-endpoint") {
				return fmt.Errorf("--target-endpoint is required: provide the remote cluster API endpoint URL")
			}
			if !fl.Changed("target-storage") {
				return fmt.Errorf("--target-storage is required: provide a target storage ID or mapping")
			}
			if !fl.Changed("target-bridge") {
				return fmt.Errorf("--target-bridge is required: provide a target bridge ID or mapping")
			}

			if fl.Changed("async") {
				deps.Async = async
			}

			params := &nodes.CreateLxcRemoteMigrateParams{
				TargetEndpoint: targetEndpoint,
				TargetStorage:  targetStorage,
				TargetBridge:   targetBridge,
			}
			if fl.Changed("target-vmid") {
				params.TargetVmid = &targetVmid
			}
			if fl.Changed("online") {
				params.Online = &online
			}
			if fl.Changed("restart") {
				params.Restart = &restart
			}
			if fl.Changed("delete") {
				params.Delete = &deleteSource
			}
			if fl.Changed("bwlimit") {
				params.Bwlimit = &bwlimit
			}
			if fl.Changed("timeout") {
				params.Timeout = &timeout
			}

			resp, err := deps.API.Nodes.CreateLxcRemoteMigrate(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("remote-migrate container %s from node %q: %w", vmid, node, err)
			}

			// The response is a json.RawMessage alias; attempt UPID parsing first.
			// If the raw value is a quoted UPID string, delegate to emitTask.
			if resp != nil && len(*resp) > 0 {
				raw := *resp
				if _, uerr := apiclient.UPIDFromRaw(raw); uerr == nil {
					return emitTask(cmd, deps, raw,
						fmt.Sprintf("Container %s remote migration started.", vmid))
				}
				// Non-UPID response: render whatever came back.
				res := output.Result{Raw: *resp}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			res := output.Result{Message: fmt.Sprintf("Container %s remote migration submitted.", vmid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the cross-cluster migration without prompting")
	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().StringVar(&targetEndpoint, "target-endpoint", "", "remote cluster API endpoint URL (required)")
	cmd.Flags().StringVar(&targetStorage, "target-storage", "",
		"storage ID on the target cluster; '1' maps each source storage to itself (required)")
	cmd.Flags().StringVar(&targetBridge, "target-bridge", "",
		"bridge ID on the target cluster; '1' maps each source bridge to itself (required)")
	cmd.Flags().Int64Var(&targetVmid, "target-vmid", 0, "VMID to use on the target cluster (default: same as source)")
	cmd.Flags().BoolVar(&online, "online", false, "use online migration")
	cmd.Flags().BoolVar(&restart, "restart", false, "use restart migration")
	cmd.Flags().BoolVar(&deleteSource, "delete", false,
		"delete the source container after a successful migration (default: keep a stopped copy)")
	cmd.Flags().Float64Var(&bwlimit, "bwlimit", 0, "override I/O bandwidth limit in KiB/s")
	cmd.Flags().Int64Var(&timeout, "timeout", 0, "shutdown timeout in seconds for restart migration")
	return cmd
}
