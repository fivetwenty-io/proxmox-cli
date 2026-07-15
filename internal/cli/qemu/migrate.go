package qemu

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newMigrateCheckCmd builds `pmx pve qemu migrate check <vmid> [--target-node NODE]`.
// It calls the GET /nodes/{n}/qemu/{v}/migrate pre-flight endpoint and returns
// feasibility information: allowed nodes, local resources, and local disks.
func newMigrateCheckCmd() *cobra.Command {
	var targetNode string
	cmd := &cobra.Command{
		Use:   "check <vmid|name>",
		Short: "Pre-flight check for migrating a VM",
		Long: "Query migration feasibility for a VM without performing the migration. " +
			"Returns allowed target nodes, local resources that block migration, " +
			"and local disks. Optionally filter results for a specific --target-node.",
		Example: `  pmx pve qemu migrate check 100
  pmx pve qemu migrate check 100 --target-node pve2`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.ListQemuMigrateParams{}
			if cmd.Flags().Changed("target-node") {
				params.Target = strPtr(targetNode)
			}

			resp, err := deps.API.Nodes.ListQemuMigrate(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("migrate check for VM %s on node %q: %w", vmid, node, err)
			}
			if resp == nil {
				return fmt.Errorf("migrate check for VM %s on node %q: empty response", vmid, node)
			}

			single := map[string]string{
				"running":          fmt.Sprintf("%v", bool(resp.Running)),
				"has-dbus-vmstate": fmt.Sprintf("%v", bool(resp.HasDbusVmstate)),
				"allowed_nodes":    strings.Join(resp.AllowedNodes, ", "),
				"local_resources":  strings.Join(resp.LocalResources, ", "),
				"mapped_resources": strings.Join(resp.MappedResources, ", "),
			}
			if len(resp.DependentHaResources) > 0 {
				single["dependent-ha-resources"] = strings.Join(resp.DependentHaResources, ", ")
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&targetNode, "target-node", "", "filter results for this target node")
	return cmd
}

// newMigrateCmd builds `pmx pve qemu migrate <vmid> --target NODE [flags]`.
//
// The migration is submitted as an asynchronous PVE task (UPID). The command
// blocks until the task reaches a terminal state unless --async is given. Only
// flags explicitly set by the caller are forwarded to the API.
func newMigrateCmd() *cobra.Command {
	var (
		async              bool
		target             string
		online             bool
		withLocalDisks     bool
		force              bool
		migrationNetwork   string
		migrationType      string
		withConntrackState bool
		bwlimit            int64
		targetstorage      string
	)
	cmd := &cobra.Command{
		Use:   "migrate <vmid|name>",
		Short: "Migrate a QEMU virtual machine to another node",
		Long: "Migrate a QEMU VM to a different cluster node. " +
			"--target-node is required. For running VMs pass --online to perform a " +
			"live migration; without it PVE will refuse to migrate a running VM " +
			"unless --force is also set. " +
			"The command blocks until the migration task completes unless --async is set.",
		Example: `  pmx pve qemu migrate 100 --target-node pve2
  pmx pve qemu migrate 100 --target-node pve2 --online`,
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
			if fl.Changed("migration-type") {
				params.MigrationType = strPtr(migrationType)
			}
			if fl.Changed("with-conntrack-state") {
				params.WithConntrackState = boolPtr(withConntrackState)
			}
			if fl.Changed("bwlimit") {
				params.Bwlimit = int64Ptr(bwlimit)
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
	cmd.Flags().StringVar(&migrationType, "migration-type", "",
		"migration traffic protection: secure (default) or insecure")
	cmd.Flags().BoolVar(&withConntrackState, "with-conntrack-state", false,
		"migrate conntrack state for running VMs with a configured firewall")
	cmd.Flags().Int64Var(&bwlimit, "bwlimit", 0,
		"override I/O bandwidth limit in KiB/s for migrated disks")
	cmd.Flags().StringVar(&targetstorage, "targetstorage", "",
		"target storage mapping; a single storage ID maps all source storages, "+
			"or '1' maps each source storage to itself")

	// Add the pre-flight check and capabilities leaf as sub-commands so both
	// `pmx pve qemu migrate 100 --target-node pve2` and `pmx pve qemu migrate check
	// 100` / `pmx pve qemu migrate capabilities` are valid.
	cmd.AddCommand(newMigrateCheckCmd(), newMigrateCapabilitiesCmd())
	return cmd
}

// newMigrateCapabilitiesCmd builds `pmx pve qemu migrate capabilities`. It
// mirrors `pmx pve node capabilities qemu migration` exactly (same headers/Raw
// shape) so the two spellings are interchangeable; `migrate check` already
// reads this data internally, this leaf just surfaces it directly.
func newMigrateCapabilitiesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "capabilities",
		Short: "List QEMU migration capabilities of a node",
		Long: "Show which migration features the node's QEMU build supports (e.g. " +
			"dbus-vmstate for switchover of attached daemons). Node-scoped: uses --node. " +
			"Same data as 'pmx pve node capabilities qemu migration'.",
		Example: `  pmx pve qemu migrate capabilities`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.ListCapabilitiesQemuMigration(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("get QEMU migration capabilities on node %q: %w", node, err)
			}
			if resp == nil {
				return fmt.Errorf("get QEMU migration capabilities on node %q: empty response", node)
			}

			single := map[string]string{"has-dbus-vmstate": boolYesNo(bool(resp.HasDbusVmstate))}
			raw := map[string]any{"has-dbus-vmstate": bool(resp.HasDbusVmstate)}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

// boolYesNo renders a bool as "yes"/"no", matching the node-level
// capabilities rendering (internal/cli/node) so `qemu migrate capabilities`
// and `node capabilities qemu migration` produce identical table output.
func boolYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
