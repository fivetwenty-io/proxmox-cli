package qemu

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
)

// newMigrateCmd builds `pve qemu migrate <vmid> --target NODE [flags]`.
//
// The migration is submitted as an asynchronous PVE task (UPID). The command
// blocks until the task reaches a terminal state unless --async is given. Only
// flags explicitly set by the caller are forwarded to the API.
func newMigrateCmd() *cobra.Command {
	var (
		async            bool
		target           string
		online           bool
		withLocalDisks   bool
		force            bool
		migrationNetwork string
		targetstorage    string
	)
	cmd := &cobra.Command{
		Use:   "migrate <vmid>",
		Short: "Migrate a QEMU virtual machine to another node",
		Long: "Migrate a QEMU VM to a different cluster node. " +
			"--target-node is required. For running VMs pass --online to perform a " +
			"live migration; without it PVE will refuse to migrate a running VM " +
			"unless --force is also set. " +
			"The command blocks until the migration task completes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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

			params := &nodes.CreateQemuMigrateParams{Target: target}
			fl := cmd.Flags()
			if fl.Changed("online") {
				params.Online = boolPtr(online)
			}
			if fl.Changed("with-local-disks") {
				params.WithLocalDisks = boolPtr(withLocalDisks)
			}
			if fl.Changed("force") {
				params.Force = boolPtr(force)
			}
			if fl.Changed("migration-network") {
				params.MigrationNetwork = strPtr(migrationNetwork)
			}
			if fl.Changed("targetstorage") {
				params.Targetstorage = strPtr(targetstorage)
			}

			resp, err := deps.API.Nodes.CreateQemuMigrate(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("migrate VM %s from node %q to %q: %w", vmid, node, target, err)
			}
			return finishAsync(cmd, deps, json.RawMessage(*resp),
				fmt.Sprintf("VM %s migrated to node %q.", vmid, target))
		},
	}

	cmd.Flags().BoolVar(&async, "async", false, "return the task UPID immediately without waiting")
	cmd.Flags().StringVar(&target, "target-node", "", "destination node name (required)")
	cmd.Flags().BoolVar(&online, "online", false, "use live (online) migration for running VMs")
	cmd.Flags().BoolVar(&withLocalDisks, "with-local-disks", false,
		"enable live storage migration for local disks (requires --online)")
	cmd.Flags().BoolVar(&force, "force", false,
		"allow migration of VMs that use local devices (root only)")
	cmd.Flags().StringVar(&migrationNetwork, "migration-network", "",
		"CIDR of the sub-network to use for migration traffic")
	cmd.Flags().StringVar(&targetstorage, "targetstorage", "",
		"target storage mapping; a single storage ID maps all source storages, "+
			"or '1' maps each source storage to itself")
	return cmd
}
