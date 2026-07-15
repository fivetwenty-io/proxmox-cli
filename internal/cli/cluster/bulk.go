package cluster

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newBulkCmd builds the `pmx pve cluster bulk` sub-tree: cluster-wide guest power
// and migration actions. Each verb acts on every guest in the cluster unless
// narrowed with --vmids, so all of them require --yes and run as asynchronous
// tasks. By default the command blocks until the task completes; with --async
// it prints the task UPID and returns immediately.
func newBulkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "bulk",
		Aliases: []string{"bulk-action"},
		Short:   "Run cluster-wide guest power and migration actions",
		Long: "Start, shut down, suspend, or migrate guests across the whole cluster. " +
			"Without --vmids every guest is affected, so each action requires --yes. " +
			"Actions run as asynchronous tasks; pass --async to return the UPID immediately. " +
			"Use `pmx pve cluster resources --type vm` to preview which guests exist.",
	}
	cmd.AddCommand(
		newBulkStartCmd(),
		newBulkShutdownCmd(),
		newBulkSuspendCmd(),
		newBulkMigrateCmd(),
	)
	return cmd
}

// parseVMIDs splits a comma-separated list of VMIDs into int64 values. Empty
// fields are ignored so trailing commas and spacing do not cause errors.
func parseVMIDs(s string) ([]int64, error) {
	var out []int64
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid VMID %q: %w", part, err)
		}
		out = append(out, n)
	}
	return out, nil
}

// renderBulkTask renders the result of an asynchronous bulk action. The API
// returns a task UPID; the command blocks until the task completes unless
// --async was set. A non-UPID or empty response falls back to a plain message.
func renderBulkTask(cmd *cobra.Command, deps *cli.Deps, raw json.RawMessage, doneMsg string) error {
	upid, err := apiclient.UPIDFromRaw(raw)
	if err != nil {
		return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
	}
	if deps.Async {
		return deps.Out.Render(cmd.OutOrStdout(),
			output.Result{
				Single:  map[string]string{"upid": upid},
				Raw:     map[string]string{"upid": upid},
				Message: upid,
			}, deps.Format)
	}
	if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
		return fmt.Errorf("bulk action: %w", err)
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
}

func newBulkStartCmd() *cobra.Command {
	var (
		vmids      string
		timeout    int64
		maxWorkers int64
		yes        bool
	)
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start guests across the cluster",
		Long: "Start every guest in the cluster, or only those listed in --vmids. " +
			"Requires --yes because it affects all guests by default.",
		Example: `  pmx pve cluster bulk start --yes
  pmx pve cluster bulk start --vmids 100,101,102 --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to start cluster guests without confirmation: pass --yes/-y")
			}
			params := &pvecluster.CreateBulkActionGuestStartParams{}
			fl := cmd.Flags()
			if fl.Changed("vmids") {
				vms, err := parseVMIDs(vmids)
				if err != nil {
					return err
				}
				params.Vms = vms
			}
			if fl.Changed("timeout") {
				params.Timeout = &timeout
			}
			if fl.Changed("max-workers") {
				params.MaxWorkers = &maxWorkers
			}
			resp, err := deps.API.Cluster.CreateBulkActionGuestStart(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("bulk start guests: %w", err)
			}
			return renderBulkTask(cmd, deps, rawOrEmpty(resp), "Bulk start started.")
		},
	}
	f := cmd.Flags()
	f.StringVar(&vmids, "vmids", "", "comma-separated VMIDs to act on (default: all guests)")
	f.Int64Var(&timeout, "timeout", 0, "per-guest start timeout in seconds (VMs only)")
	f.Int64Var(&maxWorkers, "max-workers", 0, "maximum number of concurrent tasks")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the action without prompting")
	return cmd
}

func newBulkShutdownCmd() *cobra.Command {
	var (
		vmids      string
		timeout    int64
		maxWorkers int64
		forceStop  bool
		yes        bool
	)
	cmd := &cobra.Command{
		Use:   "shutdown",
		Short: "Shut down guests across the cluster",
		Long: "Gracefully shut down every guest in the cluster, or only those listed in " +
			"--vmids. Requires --yes because it affects all guests by default.",
		Example: `  pmx pve cluster bulk shutdown --yes
  pmx pve cluster bulk shutdown --vmids 100,101 --force-stop --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to shut down cluster guests without confirmation: pass --yes/-y")
			}
			params := &pvecluster.CreateBulkActionGuestShutdownParams{}
			fl := cmd.Flags()
			if fl.Changed("vmids") {
				vms, err := parseVMIDs(vmids)
				if err != nil {
					return err
				}
				params.Vms = vms
			}
			if fl.Changed("timeout") {
				params.Timeout = &timeout
			}
			if fl.Changed("max-workers") {
				params.MaxWorkers = &maxWorkers
			}
			if fl.Changed("force-stop") {
				params.ForceStop = &forceStop
			}
			resp, err := deps.API.Cluster.CreateBulkActionGuestShutdown(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("bulk shutdown guests: %w", err)
			}
			return renderBulkTask(cmd, deps, rawOrEmpty(resp), "Bulk shutdown started.")
		},
	}
	f := cmd.Flags()
	f.StringVar(&vmids, "vmids", "", "comma-separated VMIDs to act on (default: all guests)")
	f.Int64Var(&timeout, "timeout", 0, "per-guest shutdown timeout in seconds")
	f.Int64Var(&maxWorkers, "max-workers", 0, "maximum number of concurrent tasks")
	f.BoolVar(&forceStop, "force-stop", false, "force a hard stop once the timeout elapses")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the action without prompting")
	return cmd
}

func newBulkSuspendCmd() *cobra.Command {
	var (
		vmids        string
		maxWorkers   int64
		toDisk       bool
		stateStorage string
		yes          bool
	)
	cmd := &cobra.Command{
		Use:   "suspend",
		Short: "Suspend guests across the cluster",
		Long: "Suspend every guest in the cluster, or only those listed in --vmids. " +
			"With --to-disk the guests are suspended to disk and resumed on next start. " +
			"Requires --yes because it affects all guests by default.",
		Example: `  pmx pve cluster bulk suspend --yes
  pmx pve cluster bulk suspend --vmids 100,101 --to-disk --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to suspend cluster guests without confirmation: pass --yes/-y")
			}
			params := &pvecluster.CreateBulkActionGuestSuspendParams{}
			fl := cmd.Flags()
			if fl.Changed("vmids") {
				vms, err := parseVMIDs(vmids)
				if err != nil {
					return err
				}
				params.Vms = vms
			}
			if fl.Changed("max-workers") {
				params.MaxWorkers = &maxWorkers
			}
			if fl.Changed("to-disk") {
				params.ToDisk = &toDisk
			}
			if fl.Changed("statestorage") {
				params.Statestorage = &stateStorage
			}
			resp, err := deps.API.Cluster.CreateBulkActionGuestSuspend(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("bulk suspend guests: %w", err)
			}
			return renderBulkTask(cmd, deps, rawOrEmpty(resp), "Bulk suspend started.")
		},
	}
	f := cmd.Flags()
	f.StringVar(&vmids, "vmids", "", "comma-separated VMIDs to act on (default: all guests)")
	f.Int64Var(&maxWorkers, "max-workers", 0, "maximum number of concurrent tasks")
	f.BoolVar(&toDisk, "to-disk", false, "suspend to disk (resumed on next start)")
	f.StringVar(&stateStorage, "statestorage", "", "storage for the VM state (with --to-disk)")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the action without prompting")
	return cmd
}

func newBulkMigrateCmd() *cobra.Command {
	var (
		vmids          string
		targetNode     string
		maxWorkers     int64
		online         bool
		withLocalDisks bool
		yes            bool
	)
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate guests across the cluster",
		Long: "Migrate every guest in the cluster to --target-node, or only those listed " +
			"in --vmids. With --online VMs are live-migrated and containers are restart-" +
			"migrated. Requires --yes because it affects all guests by default.",
		Example: `  pmx pve cluster bulk migrate --target-node pve2 --yes
  pmx pve cluster bulk migrate --vmids 100,101 --target-node pve2 --online --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to migrate cluster guests without confirmation: pass --yes/-y")
			}
			params := &pvecluster.CreateBulkActionGuestMigrateParams{Target: targetNode}
			fl := cmd.Flags()
			if fl.Changed("vmids") {
				vms, err := parseVMIDs(vmids)
				if err != nil {
					return err
				}
				params.Vms = vms
			}
			if fl.Changed("max-workers") {
				params.MaxWorkers = &maxWorkers
			}
			if fl.Changed("online") {
				params.Online = &online
			}
			if fl.Changed("with-local-disks") {
				params.WithLocalDisks = &withLocalDisks
			}
			resp, err := deps.API.Cluster.CreateBulkActionGuestMigrate(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("bulk migrate guests: %w", err)
			}
			return renderBulkTask(cmd, deps, rawOrEmpty(resp), "Bulk migration started.")
		},
	}
	f := cmd.Flags()
	f.StringVar(&vmids, "vmids", "", "comma-separated VMIDs to act on (default: all guests)")
	f.StringVar(&targetNode, "target-node", "", "node to migrate guests to (required)")
	f.Int64Var(&maxWorkers, "max-workers", 0, "maximum number of concurrent tasks")
	f.BoolVar(&online, "online", false, "live-migrate VMs / restart-migrate containers")
	f.BoolVar(&withLocalDisks, "with-local-disks", false, "migrate local disks as well")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the action without prompting")
	cli.MustMarkRequired(cmd, "target-node")
	return cmd
}

// rawOrEmpty dereferences a *json.RawMessage task response, returning an empty
// message when the pointer is nil so renderBulkTask can fall back gracefully.
func rawOrEmpty(resp *json.RawMessage) json.RawMessage {
	if resp == nil {
		return nil
	}
	return *resp
}
