package lxc

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
)

// newMigrateCmd builds `pve lxc migrate <vmid> --target-node NODE [flags]`.
//
// The migration is submitted as an asynchronous PVE task (UPID). The command
// blocks until the task reaches a terminal state unless --async is given. A
// running container cannot be live-migrated; pass --restart to migrate it by
// briefly restarting it on the target node. Only flags explicitly set by the
// caller are forwarded to the API.
func newMigrateCmd() *cobra.Command {
	var (
		async         bool
		target        string
		online        bool
		restart       bool
		targetStorage string
		timeout       int64
		bwlimit       float64
	)
	cmd := &cobra.Command{
		Use:   "migrate <vmid>",
		Short: "Migrate an LXC container to another node",
		Long: "Migrate an LXC container to a different cluster node. " +
			"--target-node is required. A running container cannot be live-migrated; " +
			"pass --restart to migrate it by briefly restarting it on the target node. " +
			"The command blocks until the migration task completes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := getDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			vmid := args[0]
			if _, err := strconv.ParseInt(vmid, 10, 64); err != nil {
				return fmt.Errorf("invalid vmid %q: %w", vmid, err)
			}
			if !cmd.Flags().Changed("target-node") {
				return fmt.Errorf("--target-node is required: provide the destination node name")
			}
			if cmd.Flags().Changed("async") {
				deps.Async = async
			}

			params := &nodes.CreateLxcMigrateParams{Target: target}
			fl := cmd.Flags()
			if fl.Changed("online") {
				params.Online = &online
			}
			if fl.Changed("restart") {
				params.Restart = &restart
			}
			if fl.Changed("targetstorage") {
				params.TargetStorage = &targetStorage
			}
			if fl.Changed("timeout") {
				params.Timeout = &timeout
			}
			if fl.Changed("bwlimit") {
				params.Bwlimit = &bwlimit
			}

			resp, err := deps.API.Nodes.CreateLxcMigrate(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("migrate container %s from node %q to %q: %w", vmid, node, target, err)
			}
			return emitTask(cmd, deps, *resp, fmt.Sprintf("Container %s migrated to node %q.", vmid, target))
		},
	}

	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().StringVar(&target, "target-node", "", "destination node name (required)")
	cmd.Flags().BoolVar(&online, "online", false, "use online/live migration")
	cmd.Flags().BoolVar(&restart, "restart", false, "migrate a running container by restarting it on the target node")
	cmd.Flags().StringVar(&targetStorage, "targetstorage", "",
		"target storage mapping; a single storage ID maps all source storages, "+
			"or '1' maps each source storage to itself")
	cmd.Flags().Int64Var(&timeout, "timeout", 0, "shutdown timeout in seconds for restart migration")
	cmd.Flags().Float64Var(&bwlimit, "bwlimit", 0, "override I/O bandwidth limit in KiB/s")
	return cmd
}
