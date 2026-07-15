package pdm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	pdmpve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/pve"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// newPveLxcCmd builds `pmx pdm pve lxc` — proxy operations against lxc
// containers on a managed PVE remote: listing, configuration, status,
// pending configuration, RRD metrics, lifecycle (start/shutdown/stop),
// snapshots, migration (in-cluster and cross-cluster), and guest firewall.
//
// GetRemotesLxc (GET .../lxc/{vmid}) is a directory-index leaf with no data
// of its own (returns only `error`, pve_gen.go:871-887, v3.6.0) and is
// excluded, matching every other product group in this package.
// CreateRemotesLxcTermproxy and ListRemotesLxcVncwebsocket exist solely to
// hand off an interactive shell/console session and have no meaningful CLI
// representation, so they are excluded too (see newPveCmd's identical
// exclusions). Unlike qemu, lxc has no resume verb (a container has no
// pause/suspend state) and no migrate-preconditions pre-flight check in the
// generated Service interface.
func newPveLxcCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lxc",
		Short: "Proxy operations against lxc containers on a managed PVE remote",
		Long: "Discover, inspect, and manage lxc containers on a PVE remote this Proxmox " +
			"Datacenter Manager instance manages: listing, configuration, status, " +
			"pending configuration, RRD metrics, lifecycle (start/shutdown/stop), " +
			"snapshots, migration (in-cluster and cross-cluster), and guest firewall.",
	}
	cmd.AddCommand(newPveGuestSharedCmds(pveGuestLxc, pveLxcOps())...)
	cmd.AddCommand(newPveLxcMigrateCmd(), newPveLxcRemoteMigrateCmd())
	return cmd
}

// pveLxcOps adapts the generated pdmpve.Service lxc methods to the shared
// guest command constructors in pve_proxy_guest_shared.go.
func pveLxcOps() pveGuestOps {
	return pveGuestOps{
		list:             pveLxcList,
		status:           pveLxcStatus,
		rrddata:          pveLxcRrddata,
		start:            pveLxcStart,
		shutdown:         pveLxcShutdown,
		stop:             pveLxcStop,
		snapshotList:     pveLxcSnapshotList,
		snapshotCreate:   pveLxcSnapshotCreate,
		snapshotDelete:   pveLxcSnapshotDelete,
		snapshotUpdate:   pveLxcSnapshotUpdate,
		snapshotRollback: pveLxcSnapshotRollback,
		firewallShow:     pveLxcFirewallOptionsShow,
		firewallUpdate:   pveLxcFirewallOptionsUpdate,
		firewallRules:    pveLxcFirewallRules,
	}
}

func pveLxcList(ctx context.Context, deps *cli.Deps, remote string, node *string) ([]json.RawMessage, error) {
	params := &pdmpve.ListRemotesLxcParams{}
	if node != nil {
		params.Node = node
	}

	resp, err := deps.PDM.Pve.ListRemotesLxc(ctx, remote, params)
	if err != nil {
		return nil, err
	}

	return rawItemsOf(resp), nil
}

func pveLxcStatus(ctx context.Context, deps *cli.Deps, remote, vmid string, node *string) (map[string]any, error) {
	params := &pdmpve.ListRemotesLxcStatusParams{}
	if node != nil {
		params.Node = node
	}

	resp, err := deps.PDM.Pve.ListRemotesLxcStatus(ctx, remote, vmid, params)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("empty response from server")
	}

	return flattenToMap(resp)
}

func pveLxcRrddata(ctx context.Context, deps *cli.Deps, remote, vmid, cf, timeframe string) ([]json.RawMessage, error) {
	params := &pdmpve.ListRemotesLxcRrddataParams{Cf: cf, Timeframe: timeframe}

	resp, err := deps.PDM.Pve.ListRemotesLxcRrddata(ctx, remote, vmid, params)
	if err != nil {
		return nil, err
	}

	return rawItemsOf(resp), nil
}

func pveLxcStart(ctx context.Context, deps *cli.Deps, remote, vmid string, node *string) (*json.RawMessage, error) {
	params := &pdmpve.CreateRemotesLxcStartParams{}
	if node != nil {
		params.Node = node
	}

	return deps.PDM.Pve.CreateRemotesLxcStart(ctx, remote, vmid, params)
}

func pveLxcShutdown(ctx context.Context, deps *cli.Deps, remote, vmid string, node *string) (*json.RawMessage, error) {
	params := &pdmpve.CreateRemotesLxcShutdownParams{}
	if node != nil {
		params.Node = node
	}

	return deps.PDM.Pve.CreateRemotesLxcShutdown(ctx, remote, vmid, params)
}

func pveLxcStop(ctx context.Context, deps *cli.Deps, remote, vmid string, node *string) (*json.RawMessage, error) {
	params := &pdmpve.CreateRemotesLxcStopParams{}
	if node != nil {
		params.Node = node
	}

	return deps.PDM.Pve.CreateRemotesLxcStop(ctx, remote, vmid, params)
}

func pveLxcSnapshotList(
	ctx context.Context, deps *cli.Deps, remote, vmid string, node *string,
) ([]json.RawMessage, error) {
	params := &pdmpve.ListRemotesLxcSnapshotParams{}
	if node != nil {
		params.Node = node
	}

	resp, err := deps.PDM.Pve.ListRemotesLxcSnapshot(ctx, remote, vmid, params)
	if err != nil {
		return nil, err
	}

	return rawItemsOf(resp), nil
}

// pveLxcSnapshotCreate ignores vmstate: CreateRemotesLxcSnapshotParams has no
// Vmstate field (a container has no RAM state to snapshot).
func pveLxcSnapshotCreate(
	ctx context.Context, deps *cli.Deps, remote, vmid, snapname string, node, description *string, _ *bool,
) (*json.RawMessage, error) {
	params := &pdmpve.CreateRemotesLxcSnapshotParams{Snapname: snapname}
	if node != nil {
		params.Node = node
	}
	if description != nil {
		params.Description = description
	}

	return deps.PDM.Pve.CreateRemotesLxcSnapshot(ctx, remote, vmid, params)
}

func pveLxcSnapshotDelete(
	ctx context.Context, deps *cli.Deps, remote, vmid, snapname string, node *string,
) (*json.RawMessage, error) {
	params := &pdmpve.DeleteRemotesLxcSnapshotParams{}
	if node != nil {
		params.Node = node
	}

	return deps.PDM.Pve.DeleteRemotesLxcSnapshot(ctx, remote, vmid, snapname, params)
}

func pveLxcSnapshotUpdate(
	ctx context.Context, deps *cli.Deps, remote, vmid, snapname string, node, description *string,
) error {
	params := &pdmpve.UpdateRemotesLxcSnapshotConfigParams{}
	if node != nil {
		params.Node = node
	}
	if description != nil {
		params.Description = description
	}

	return deps.PDM.Pve.UpdateRemotesLxcSnapshotConfig(ctx, remote, vmid, snapname, params)
}

func pveLxcSnapshotRollback(
	ctx context.Context, deps *cli.Deps, remote, vmid, snapname string, node *string, start *bool,
) (*json.RawMessage, error) {
	params := &pdmpve.CreateRemotesLxcSnapshotRollbackParams{}
	if node != nil {
		params.Node = node
	}
	if start != nil {
		params.Start = start
	}

	return deps.PDM.Pve.CreateRemotesLxcSnapshotRollback(ctx, remote, vmid, snapname, params)
}

func pveLxcFirewallOptionsShow(
	ctx context.Context, deps *cli.Deps, remote, vmid string, node *string,
) (map[string]any, error) {
	params := &pdmpve.ListRemotesLxcFirewallOptionsParams{}
	if node != nil {
		params.Node = node
	}

	resp, err := deps.PDM.Pve.ListRemotesLxcFirewallOptions(ctx, remote, vmid, params)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("empty response from server")
	}

	return flattenToMap(resp)
}

func pveLxcFirewallOptionsUpdate(
	ctx context.Context, deps *cli.Deps, remote, vmid string,
	fl *pflag.FlagSet, node *string, ff pveGuestFirewallOptionsFlags,
) error {
	params := &pdmpve.UpdateRemotesLxcFirewallOptionsParams{}
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

	return deps.PDM.Pve.UpdateRemotesLxcFirewallOptions(ctx, remote, vmid, params)
}

func pveLxcFirewallRules(
	ctx context.Context, deps *cli.Deps, remote, vmid string, node *string,
) ([]json.RawMessage, error) {
	params := &pdmpve.ListRemotesLxcFirewallRulesParams{}
	if node != nil {
		params.Node = node
	}

	resp, err := deps.PDM.Pve.ListRemotesLxcFirewallRules(ctx, remote, vmid, params)
	if err != nil {
		return nil, err
	}

	return rawItemsOf(resp), nil
}

// newPveLxcMigrateCmd builds `pmx pdm pve lxc migrate <remote> <vmid>` —
// perform an in-cluster migration of an lxc container (POST
// /pve/remotes/{remote}/lxc/{vmid}/migrate). Destructive in effect
// (interrupts/relocates a running workload): --yes/-y is required.
// CreateRemotesLxcMigrateParams diverges from its qemu counterpart: it has
// --restart/--timeout (restart-migration for a running container) instead
// of qemu's --force/--migration-network/--migration-type/--with-local-disks
// (pve_gen.go, v3.6.0), so this is not built through the shared lifecycle
// constructor.
func newPveLxcMigrateCmd() *cobra.Command {
	var (
		node, targetNode string
		online, restart  bool
		bwlimit, timeout int64
		targetStorage    []string
		yes              bool
	)
	cmd := &cobra.Command{
		Use:   "migrate <remote> <vmid>",
		Short: "Migrate a PVE remote container to another node in the same cluster",
		Long: "Perform an in-cluster migration of an lxc container (POST " +
			"/pve/remotes/{remote}/lxc/{vmid}/migrate). --target-node is required. " +
			"Pass --yes/-y to confirm this destructive operation.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid := args[0], args[1]
			fl := cmd.Flags()

			if !fl.Changed("target-node") {
				return fmt.Errorf("migrate container %s on PVE remote %q: --target-node is required", vmid, remote)
			}
			if !yes {
				return fmt.Errorf("refusing to migrate container %s on PVE remote %q without confirmation: pass --yes/-y",
					vmid, remote)
			}

			params := &pdmpve.CreateRemotesLxcMigrateParams{Target: targetNode}
			if fl.Changed("node") {
				params.Node = &node
			}
			if fl.Changed("bwlimit") {
				params.Bwlimit = &bwlimit
			}
			if fl.Changed("online") {
				params.Online = &online
			}
			if fl.Changed("restart") {
				params.Restart = &restart
			}
			if fl.Changed("target-storage") {
				params.TargetStorage = targetStorage
			}
			if fl.Changed("timeout") {
				params.Timeout = &timeout
			}

			resp, err := deps.PDM.Pve.CreateRemotesLxcMigrate(cmd.Context(), remote, vmid, params)
			if err != nil {
				return fmt.Errorf("migrate container %s on PVE remote %q to node %q: %w", vmid, remote, targetNode, err)
			}
			if resp == nil {
				return fmt.Errorf("migrate container %s on PVE remote %q to node %q: empty response from server",
					vmid, remote, targetNode)
			}

			msg := fmt.Sprintf("Container %s on PVE remote %q migrated to node %q.", vmid, remote, targetNode)
			return finishPveRemoteAsync(cmd, deps, remote, *resp, msg)
		},
	}
	f := cmd.Flags()
	pveGuestNodeFlag(cmd, &node)
	f.StringVar(&targetNode, "target-node", "", "destination node name (required)")
	f.Int64Var(&bwlimit, "bwlimit", 0, "override I/O bandwidth limit in KiB/s")
	f.BoolVar(&online, "online", false, "attempt an online migration if the container is running")
	f.BoolVar(&restart, "restart", false, "perform a restart-migration if the container is running")
	f.StringArrayVar(&targetStorage, "target-storage", nil, "storage mapping (repeatable)")
	f.Int64Var(&timeout, "timeout", 0, "shutdown timeout in seconds for restart-migrations")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newPveLxcRemoteMigrateCmd builds `pmx pdm pve lxc remote-migrate <remote>
// <vmid>` — perform a remote (cross-cluster) migration of an lxc container
// (POST /pve/remotes/{remote}/lxc/{vmid}/remote-migrate). Destructive and
// irreversible when --delete is set: --yes/-y is required.
func newPveLxcRemoteMigrateCmd() *cobra.Command {
	var (
		node                        string
		targetRemote                string
		targetEndpoint              string
		targetBridge, targetStorage []string
		targetVmid                  int64
		online, deleteSource        bool
		restart                     bool
		bwlimit, timeout            int64
		yes                         bool
	)
	cmd := &cobra.Command{
		Use:   "remote-migrate <remote> <vmid>",
		Short: "Migrate a PVE remote container to a different (remote) cluster",
		Long: "Perform a remote (cross-cluster) migration of an lxc container (POST " +
			"/pve/remotes/{remote}/lxc/{vmid}/remote-migrate). --target-remote, " +
			"--target-bridge, and --target-storage are required. Pass --yes/-y to confirm " +
			"this destructive, cross-cluster operation.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, vmid := args[0], args[1]
			fl := cmd.Flags()

			if !fl.Changed("target-remote") {
				return fmt.Errorf("remote-migrate container %s on PVE remote %q: --target-remote is required", vmid, remote)
			}
			if !fl.Changed("target-bridge") {
				return fmt.Errorf("remote-migrate container %s on PVE remote %q: --target-bridge is required", vmid, remote)
			}
			if !fl.Changed("target-storage") {
				return fmt.Errorf("remote-migrate container %s on PVE remote %q: --target-storage is required", vmid, remote)
			}
			if !yes {
				return fmt.Errorf("refusing to remote-migrate container %s on PVE remote %q without confirmation: pass --yes/-y",
					vmid, remote)
			}

			params := &pdmpve.CreateRemotesLxcRemoteMigrateParams{
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
			if fl.Changed("restart") {
				params.Restart = &restart
			}
			if fl.Changed("bwlimit") {
				params.Bwlimit = &bwlimit
			}
			if fl.Changed("timeout") {
				params.Timeout = &timeout
			}

			resp, err := deps.PDM.Pve.CreateRemotesLxcRemoteMigrate(cmd.Context(), remote, vmid, params)
			if err != nil {
				return fmt.Errorf("remote-migrate container %s on PVE remote %q to remote %q: %w", vmid, remote, targetRemote, err)
			}
			if resp == nil {
				return fmt.Errorf("remote-migrate container %s on PVE remote %q to remote %q: empty response from server",
					vmid, remote, targetRemote)
			}

			msg := fmt.Sprintf("Container %s on PVE remote %q remote-migrated to %q.", vmid, remote, targetRemote)
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
	f.BoolVar(&online, "online", false, "perform an online migration if the container is running")
	f.BoolVar(&deleteSource, "delete", false, "delete the original VM and related data after successful migration")
	f.BoolVar(&restart, "restart", false, "perform a restart-migration")
	f.Int64Var(&bwlimit, "bwlimit", 0, "override I/O bandwidth limit in KiB/s")
	f.Int64Var(&timeout, "timeout", 0, "shutdown timeout in seconds for the restart-migration")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the destructive, cross-cluster operation without prompting")
	return cmd
}
