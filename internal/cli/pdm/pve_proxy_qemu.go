package pdm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	pdmpve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/pve"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newPveQemuCmd builds `pmx pdm pve qemu` — proxy operations against qemu
// VMs on a managed PVE remote: listing, configuration, status, pending
// configuration, RRD metrics, lifecycle (start/shutdown/stop/resume),
// snapshots, migration (in-cluster and cross-cluster), and guest firewall.
//
// GetRemotesQemu (GET .../qemu/{vmid}) is a directory-index leaf with no data
// of its own (returns only `error`, pve_gen.go:4724-4740, v3.6.0) and is
// excluded, matching every other product group in this package.
// CreateRemotesQemuTermproxy, CreateRemotesQemuVncproxy, and
// ListRemotesQemuVncwebsocket exist solely to hand off an interactive
// shell/VNC/console session and have no meaningful CLI representation, so
// they are excluded too (see newPveCmd's identical exclusions).
func newPveQemuCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "qemu",
		Short: "Proxy operations against qemu VMs on a managed PVE remote",
		Long: "Discover, inspect, and manage qemu VMs on a PVE remote this Proxmox " +
			"Datacenter Manager instance manages: listing, configuration, status, " +
			"pending configuration, RRD metrics, lifecycle (start/shutdown/stop/resume), " +
			"snapshots, migration (in-cluster and cross-cluster), and guest firewall.",
	}
	cmd.AddCommand(newPveGuestSharedCmds(pveGuestQemu, pveQemuOps())...)
	cmd.AddCommand(
		newPveQemuResumeCmd(), newPveQemuMigratePreconditionsCmd(), newPveQemuMigrateCmd(), newPveQemuRemoteMigrateCmd(),
	)
	return cmd
}

// pveQemuOps adapts the generated pdmpve.Service qemu methods to the shared
// guest command constructors in pve_proxy_guest_shared.go.
func pveQemuOps() pveGuestOps {
	return pveGuestOps{
		list:             pveQemuList,
		status:           pveQemuStatus,
		rrddata:          pveQemuRrddata,
		start:            pveQemuStart,
		shutdown:         pveQemuShutdown,
		stop:             pveQemuStop,
		snapshotList:     pveQemuSnapshotList,
		snapshotCreate:   pveQemuSnapshotCreate,
		snapshotDelete:   pveQemuSnapshotDelete,
		snapshotUpdate:   pveQemuSnapshotUpdate,
		snapshotRollback: pveQemuSnapshotRollback,
		firewallShow:     pveQemuFirewallOptionsShow,
		firewallUpdate:   pveQemuFirewallOptionsUpdate,
		firewallRules:    pveQemuFirewallRules,
	}
}

func pveQemuList(ctx context.Context, deps *cli.Deps, remote string, node *string) ([]json.RawMessage, error) {
	params := &pdmpve.ListRemotesQemuParams{}
	if node != nil {
		params.Node = node
	}

	resp, err := deps.PDM.Pve.ListRemotesQemu(ctx, remote, params)
	if err != nil {
		return nil, err
	}

	return rawItemsOf(resp), nil
}

func pveQemuStatus(ctx context.Context, deps *cli.Deps, remote, vmid string, node *string) (map[string]any, error) {
	params := &pdmpve.ListRemotesQemuStatusParams{}
	if node != nil {
		params.Node = node
	}

	resp, err := deps.PDM.Pve.ListRemotesQemuStatus(ctx, remote, vmid, params)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("empty response from server")
	}

	return flattenToMap(resp)
}

func pveQemuRrddata(
	ctx context.Context, deps *cli.Deps, remote, vmid, cf, timeframe string,
) ([]json.RawMessage, error) {
	params := &pdmpve.ListRemotesQemuRrddataParams{Cf: cf, Timeframe: timeframe}

	resp, err := deps.PDM.Pve.ListRemotesQemuRrddata(ctx, remote, vmid, params)
	if err != nil {
		return nil, err
	}

	return rawItemsOf(resp), nil
}

func pveQemuStart(ctx context.Context, deps *cli.Deps, remote, vmid string, node *string) (*json.RawMessage, error) {
	params := &pdmpve.CreateRemotesQemuStartParams{}
	if node != nil {
		params.Node = node
	}

	return deps.PDM.Pve.CreateRemotesQemuStart(ctx, remote, vmid, params)
}

func pveQemuShutdown(ctx context.Context, deps *cli.Deps, remote, vmid string, node *string) (*json.RawMessage, error) {
	params := &pdmpve.CreateRemotesQemuShutdownParams{}
	if node != nil {
		params.Node = node
	}

	return deps.PDM.Pve.CreateRemotesQemuShutdown(ctx, remote, vmid, params)
}

func pveQemuStop(ctx context.Context, deps *cli.Deps, remote, vmid string, node *string) (*json.RawMessage, error) {
	params := &pdmpve.CreateRemotesQemuStopParams{}
	if node != nil {
		params.Node = node
	}

	return deps.PDM.Pve.CreateRemotesQemuStop(ctx, remote, vmid, params)
}

func pveQemuSnapshotList(
	ctx context.Context, deps *cli.Deps, remote, vmid string, node *string,
) ([]json.RawMessage, error) {
	params := &pdmpve.ListRemotesQemuSnapshotParams{}
	if node != nil {
		params.Node = node
	}

	resp, err := deps.PDM.Pve.ListRemotesQemuSnapshot(ctx, remote, vmid, params)
	if err != nil {
		return nil, err
	}

	return rawItemsOf(resp), nil
}

func pveQemuSnapshotCreate(
	ctx context.Context, deps *cli.Deps, remote, vmid, snapname string, node, description *string, vmstate *bool,
) (*json.RawMessage, error) {
	params := &pdmpve.CreateRemotesQemuSnapshotParams{Snapname: snapname}
	if node != nil {
		params.Node = node
	}
	if description != nil {
		params.Description = description
	}
	if vmstate != nil {
		params.Vmstate = vmstate
	}

	return deps.PDM.Pve.CreateRemotesQemuSnapshot(ctx, remote, vmid, params)
}

func pveQemuSnapshotDelete(
	ctx context.Context, deps *cli.Deps, remote, vmid, snapname string, node *string,
) (*json.RawMessage, error) {
	params := &pdmpve.DeleteRemotesQemuSnapshotParams{}
	if node != nil {
		params.Node = node
	}

	return deps.PDM.Pve.DeleteRemotesQemuSnapshot(ctx, remote, vmid, snapname, params)
}

func pveQemuSnapshotUpdate(
	ctx context.Context, deps *cli.Deps, remote, vmid, snapname string, node, description *string,
) error {
	params := &pdmpve.UpdateRemotesQemuSnapshotConfigParams{}
	if node != nil {
		params.Node = node
	}
	if description != nil {
		params.Description = description
	}

	return deps.PDM.Pve.UpdateRemotesQemuSnapshotConfig(ctx, remote, vmid, snapname, params)
}

func pveQemuSnapshotRollback(
	ctx context.Context, deps *cli.Deps, remote, vmid, snapname string, node *string, start *bool,
) (*json.RawMessage, error) {
	params := &pdmpve.CreateRemotesQemuSnapshotRollbackParams{}
	if node != nil {
		params.Node = node
	}
	if start != nil {
		params.Start = start
	}

	return deps.PDM.Pve.CreateRemotesQemuSnapshotRollback(ctx, remote, vmid, snapname, params)
}

func pveQemuFirewallOptionsShow(
	ctx context.Context, deps *cli.Deps, remote, vmid string, node *string,
) (map[string]any, error) {
	params := &pdmpve.ListRemotesQemuFirewallOptionsParams{}
	if node != nil {
		params.Node = node
	}

	resp, err := deps.PDM.Pve.ListRemotesQemuFirewallOptions(ctx, remote, vmid, params)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("empty response from server")
	}

	return flattenToMap(resp)
}

func pveQemuFirewallOptionsUpdate(
	ctx context.Context, deps *cli.Deps, remote, vmid string,
	fl *pflag.FlagSet, node *string, ff pveGuestFirewallOptionsFlags,
) error {
	params := &pdmpve.UpdateRemotesQemuFirewallOptionsParams{}
	if node != nil {
		params.Node = node
	}
	if fl.Changed("delete") {
		params.Delete = ff.del
	}
	if fl.Changed("digest") {
		params.Digest = &ff.digest
	}
	if fl.Changed("dhcp") {
		params.Dhcp = &ff.dhcp
	}
	if fl.Changed("enable") {
		params.Enable = &ff.enable
	}
	if fl.Changed("ipfilter") {
		params.Ipfilter = &ff.ipfilter
	}
	if fl.Changed("log-level-in") {
		params.LogLevelIn = &ff.logLevelIn
	}
	if fl.Changed("log-level-out") {
		params.LogLevelOut = &ff.logLevelOut
	}
	if fl.Changed("macfilter") {
		params.Macfilter = &ff.macfilter
	}
	if fl.Changed("ndp") {
		params.Ndp = &ff.ndp
	}
	if fl.Changed("policy-in") {
		params.PolicyIn = &ff.policyIn
	}
	if fl.Changed("policy-out") {
		params.PolicyOut = &ff.policyOut
	}
	if fl.Changed("radv") {
		params.Radv = &ff.radv
	}

	return deps.PDM.Pve.UpdateRemotesQemuFirewallOptions(ctx, remote, vmid, params)
}

func pveQemuFirewallRules(
	ctx context.Context, deps *cli.Deps, remote, vmid string, node *string,
) ([]json.RawMessage, error) {
	params := &pdmpve.ListRemotesQemuFirewallRulesParams{}
	if node != nil {
		params.Node = node
	}

	resp, err := deps.PDM.Pve.ListRemotesQemuFirewallRules(ctx, remote, vmid, params)
	if err != nil {
		return nil, err
	}

	return rawItemsOf(resp), nil
}

// newPveQemuResumeCmd builds `pmx pdm pve qemu resume <remote> <vmid>` —
// resume a paused or suspended remote qemu VM (POST
// /pve/remotes/{remote}/qemu/{vmid}/resume). qemu-only: lxc containers have
// no pause/suspend state to resume from.
func newPveQemuResumeCmd() *cobra.Command {
	return newPveGuestLifecycleCmd(pveGuestQemu, "resume", "resumed",
		false, pveQemuResume)
}

func pveQemuResume(ctx context.Context, deps *cli.Deps, remote, vmid string, node *string) (*json.RawMessage, error) {
	params := &pdmpve.CreateRemotesQemuResumeParams{}
	if node != nil {
		params.Node = node
	}

	return deps.PDM.Pve.CreateRemotesQemuResume(ctx, remote, vmid, params)
}

// newPveQemuMigratePreconditionsCmd builds `pmx pdm pve qemu
// migrate-preconditions <remote> <vmid>` — query qemu (local) migrate
// preconditions (GET /pve/remotes/{remote}/qemu/{vmid}/migrate). qemu-only:
// there is no lxc equivalent of this pre-flight check in the generated
// Service interface.
func newPveQemuMigratePreconditionsCmd() *cobra.Command {
	var (
		node, target string
	)
	cmd := &cobra.Command{
		Use:   "migrate-preconditions <remote> <vmid>",
		Short: "Query a PVE remote VM's migration preconditions",
		Long: "Qemu (local) migrate preconditions: allowed target nodes, local resources/disks " +
			"that block migration, and mapped resources (GET /pve/remotes/{remote}/qemu/{vmid}/migrate).",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid := args[0], args[1]
			fl := cmd.Flags()

			params := &pdmpve.ListRemotesQemuMigrateParams{}
			if fl.Changed("node") {
				params.Node = &node
			}
			if fl.Changed("target") {
				params.Target = &target
			}

			resp, err := deps.PDM.Pve.ListRemotesQemuMigrate(cmd.Context(), remote, vmid, params)
			if err != nil {
				return fmt.Errorf("get migrate preconditions for VM %s on PVE remote %q: %w", vmid, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get migrate preconditions for VM %s on PVE remote %q: empty response from server", vmid, remote)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode migrate preconditions for VM %s on PVE remote %q: %w", vmid, remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	pveGuestNodeFlag(cmd, &node)
	f.StringVar(&target, "target", "", "filter results for this target node")
	return cmd
}

// newPveQemuMigrateCmd builds `pmx pdm pve qemu migrate <remote> <vmid>` —
// perform an in-cluster migration of a qemu VM (POST
// /pve/remotes/{remote}/qemu/{vmid}/migrate). Destructive in effect
// (interrupts/relocates a running workload): --yes/-y is required.
func newPveQemuMigrateCmd() *cobra.Command {
	var (
		node, targetNode, migrationNetwork, migrationType string
		online, force, withLocalDisks                     bool
		bwlimit                                           int64
		targetStorage                                     []string
		yes                                               bool
	)
	cmd := &cobra.Command{
		Use:   "migrate <remote> <vmid>",
		Short: "Migrate a PVE remote VM to another node in the same cluster",
		Long: "Perform an in-cluster migration of a qemu VM (POST " +
			"/pve/remotes/{remote}/qemu/{vmid}/migrate). --target-node is required. " +
			"Pass --yes/-y to confirm this destructive operation.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid := args[0], args[1]
			fl := cmd.Flags()

			if !fl.Changed("target-node") {
				return fmt.Errorf("migrate VM %s on PVE remote %q: --target-node is required", vmid, remote)
			}
			if !yes {
				return fmt.Errorf("refusing to migrate VM %s on PVE remote %q without confirmation: pass --yes/-y", vmid, remote)
			}

			params := &pdmpve.CreateRemotesQemuMigrateParams{Target: targetNode}
			if fl.Changed("node") {
				params.Node = &node
			}
			if fl.Changed("bwlimit") {
				params.Bwlimit = &bwlimit
			}
			if fl.Changed("force") {
				params.Force = &force
			}
			if fl.Changed("migration-network") {
				params.MigrationNetwork = &migrationNetwork
			}
			if fl.Changed("migration-type") {
				params.MigrationType = &migrationType
			}
			if fl.Changed("online") {
				params.Online = &online
			}
			if fl.Changed("target-storage") {
				params.TargetStorage = targetStorage
			}
			if fl.Changed("with-local-disks") {
				params.WithLocalDisks = &withLocalDisks
			}

			resp, err := deps.PDM.Pve.CreateRemotesQemuMigrate(cmd.Context(), remote, vmid, params)
			if err != nil {
				return fmt.Errorf("migrate VM %s on PVE remote %q to node %q: %w", vmid, remote, targetNode, err)
			}
			if resp == nil {
				return fmt.Errorf("migrate VM %s on PVE remote %q to node %q: empty response from server", vmid, remote, targetNode)
			}

			msg := fmt.Sprintf("VM %s on PVE remote %q migrated to node %q.", vmid, remote, targetNode)
			return finishPveRemoteAsync(cmd, deps, remote, *resp, msg)
		},
	}
	f := cmd.Flags()
	pveGuestNodeFlag(cmd, &node)
	f.StringVar(&targetNode, "target-node", "", "destination node name (required)")
	f.Int64Var(&bwlimit, "bwlimit", 0, "override I/O bandwidth limit in KiB/s")
	f.BoolVar(&force, "force", false, "allow migration of VMs with local devices")
	f.StringVar(&migrationNetwork, "migration-network", "", "CIDR of the sub-network to use for migration traffic")
	f.StringVar(&migrationType, "migration-type", "", "migration traffic protection: secure|insecure")
	f.BoolVar(&online, "online", false, "perform an online (live) migration if the VM is running")
	f.StringArrayVar(&targetStorage, "target-storage", nil, "storage mapping (repeatable)")
	f.BoolVar(&withLocalDisks, "with-local-disks", false, "enable live storage migration for local disks")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newPveQemuRemoteMigrateCmd builds `pmx pdm pve qemu remote-migrate
// <remote> <vmid>` — perform a remote (cross-cluster) migration of a qemu VM
// (POST /pve/remotes/{remote}/qemu/{vmid}/remote-migrate). Destructive and
// irreversible when --delete is set: --yes/-y is required.
func newPveQemuRemoteMigrateCmd() *cobra.Command {
	var (
		node                        string
		targetRemote                string
		targetEndpoint              string
		targetBridge, targetStorage []string
		targetVmid                  int64
		online, deleteSource        bool
		bwlimit                     int64
		yes                         bool
	)
	cmd := &cobra.Command{
		Use:   "remote-migrate <remote> <vmid>",
		Short: "Migrate a PVE remote VM to a different (remote) cluster",
		Long: "Perform a remote (cross-cluster) migration of a qemu VM (POST " +
			"/pve/remotes/{remote}/qemu/{vmid}/remote-migrate). --target-remote, " +
			"--target-bridge, and --target-storage are required. Pass --yes/-y to confirm " +
			"this destructive, cross-cluster operation.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid := args[0], args[1]
			fl := cmd.Flags()

			if !fl.Changed("target-remote") {
				return fmt.Errorf("remote-migrate VM %s on PVE remote %q: --target-remote is required", vmid, remote)
			}
			if !fl.Changed("target-bridge") {
				return fmt.Errorf("remote-migrate VM %s on PVE remote %q: --target-bridge is required", vmid, remote)
			}
			if !fl.Changed("target-storage") {
				return fmt.Errorf("remote-migrate VM %s on PVE remote %q: --target-storage is required", vmid, remote)
			}
			if !yes {
				return fmt.Errorf("refusing to remote-migrate VM %s on PVE remote %q without confirmation: pass --yes/-y",
					vmid, remote)
			}

			params := &pdmpve.CreateRemotesQemuRemoteMigrateParams{
				Target: targetRemote, TargetBridge: targetBridge, TargetStorage: targetStorage,
			}
			if fl.Changed("node") {
				params.Node = &node
			}
			if fl.Changed("target-endpoint") {
				params.TargetEndpoint = &targetEndpoint
			}
			if fl.Changed("target-vmid") {
				params.TargetVmid = &targetVmid
			}
			if fl.Changed("online") {
				params.Online = &online
			}
			if fl.Changed("delete") {
				params.Delete = &deleteSource
			}
			if fl.Changed("bwlimit") {
				params.Bwlimit = &bwlimit
			}

			resp, err := deps.PDM.Pve.CreateRemotesQemuRemoteMigrate(cmd.Context(), remote, vmid, params)
			if err != nil {
				return fmt.Errorf("remote-migrate VM %s on PVE remote %q to remote %q: %w", vmid, remote, targetRemote, err)
			}
			if resp == nil {
				return fmt.Errorf("remote-migrate VM %s on PVE remote %q to remote %q: empty response from server",
					vmid, remote, targetRemote)
			}

			msg := fmt.Sprintf("VM %s on PVE remote %q remote-migrated to %q.", vmid, remote, targetRemote)
			return finishPveRemoteAsync(cmd, deps, remote, *resp, msg)
		},
	}
	f := cmd.Flags()
	pveGuestNodeFlag(cmd, &node)
	f.StringVar(&targetRemote, "target-remote", "", "destination remote ID (required)")
	f.StringVar(&targetEndpoint, "target-endpoint", "", "the target endpoint to use for the connection")
	f.StringArrayVar(&targetBridge, "target-bridge", nil, "bridge mapping (repeatable, required)")
	f.StringArrayVar(&targetStorage, "target-storage", nil, "storage mapping (repeatable, required)")
	f.Int64Var(&targetVmid, "target-vmid", 0, "VMID to use on the target cluster (defaults to source VMID)")
	f.BoolVar(&online, "online", false, "perform an online (live) migration if the VM is running")
	f.BoolVar(&deleteSource, "delete", false, "delete the original VM and related data after successful migration")
	f.Int64Var(&bwlimit, "bwlimit", 0, "override I/O bandwidth limit in KiB/s")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive, cross-cluster operation without prompting")
	return cmd
}
