package lxc

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newMigrateCmd builds the `pmx pve lxc migrate` group.
//
// Subcommands:
//   - (no subcommand) migrate <vmid> --target-node NODE: live/restart migration (POST)
//   - check <vmid>: pre-flight feasibility check (GET)
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
		Use:   "migrate <vmid|name>",
		Short: "Migrate an LXC container to another node",
		Long: "Migrate an LXC container to a different cluster node. " +
			"--target-node is required. A running container cannot be live-migrated; " +
			"pass --restart to migrate it by briefly restarting it on the target node. " +
			"The command blocks until the migration task completes unless --async is set. " +
			"Use `pmx pve lxc migrate check <vmid|name>` for a pre-flight feasibility check.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
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

	cmd.AddCommand(newMigrateCheckCmd())
	return cmd
}

// newMigrateCheckCmd builds `pmx pve lxc migrate check <vmid> [--target-node NODE]`.
//
// Performs the GET pre-flight check against the PVE migration endpoint. Returns
// feasibility information, allowed nodes, and local resource dependencies.
func newMigrateCheckCmd() *cobra.Command {
	var target string

	cmd := &cobra.Command{
		Use:   "check <vmid|name>",
		Short: "Check migration feasibility for a container",
		Long: "Query PVE for migration pre-flight information. " +
			"Returns allowed destination nodes, blocked nodes, local resource dependencies, " +
			"and whether the container is currently running.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.ListLxcMigrateParams{}
			if cmd.Flags().Changed("target-node") {
				params.Target = &target
			}

			resp, err := deps.API.Nodes.ListLxcMigrate(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("check migration feasibility for container %s: %w", vmid, err)
			}

			running := "false"
			if bool(resp.Running) {
				running = "true"
			}

			single := map[string]string{
				"running":       running,
				"allowed-nodes": strings.Join(resp.AllowedNodes, ", "),
			}
			if len(resp.DependentHaResources) > 0 {
				single["dependent-ha-resources"] = strings.Join(resp.DependentHaResources, ", ")
			}
			if len(resp.NotAllowedNodes) > 0 {
				raw, _ := json.Marshal(resp.NotAllowedNodes)
				single["not-allowed-nodes"] = string(raw)
			}

			res := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().StringVar(&target, "target-node", "", "filter results for this specific target node")
	return cmd
}
