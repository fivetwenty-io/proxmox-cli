package node

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// renderBulkTask renders the result of an asynchronous node bulk action. The
// API returns a task UPID; the command blocks until the task completes unless
// --async was set. A non-UPID or empty response (for example the MAC address
// returned by wakeonlan) falls back to a plain message.
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
		return fmt.Errorf("bulk action on node %q: %w", deps.Node, err)
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
}

func newStartallCmd() *cobra.Command {
	var (
		vmids      string
		force      bool
		maxWorkers int64
		yes        bool
	)
	cmd := &cobra.Command{
		Use:   "startall",
		Short: "Start all guests on the node",
		Long: "Start every guest on the resolved node, or only those listed in --vmids. " +
			"Requires --yes because it affects all guests by default.",
		Example: `  pmx pve node startall --yes
  pmx pve node startall --vmids 100,101,102 --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("refusing to start all guests on node %q without confirmation: pass --yes/-y", deps.Node)
			}
			params := &nodes.CreateStartallParams{}
			fl := cmd.Flags()
			if fl.Changed("vmids") {
				params.Vms = &vmids
			}
			if fl.Changed("force") {
				params.Force = &force
			}
			if fl.Changed("max-workers") {
				params.MaxWorkers = &maxWorkers
			}
			resp, err := deps.API.Nodes.CreateStartall(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("start all guests on node %q: %w", deps.Node, err)
			}
			return renderBulkTask(cmd, deps, rawOrNil(resp), "Start-all started.")
		},
	}
	f := cmd.Flags()
	f.StringVar(&vmids, "vmids", "", "comma-separated VMIDs to act on (default: all guests)")
	f.BoolVar(&force, "force", false, "start guests even if 'onboot' is not set")
	f.Int64Var(&maxWorkers, "max-workers", 0, "maximum number of concurrent tasks")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the action without prompting")
	return cmd
}

func newStopallCmd() *cobra.Command {
	var (
		vmids      string
		forceStop  bool
		timeout    int64
		maxWorkers int64
		yes        bool
	)
	cmd := &cobra.Command{
		Use:   "stopall",
		Short: "Stop all guests on the node",
		Long: "Shut down every guest on the resolved node, or only those listed in --vmids. " +
			"Requires --yes because it affects all guests by default.",
		Example: `  pmx pve node stopall --yes
  pmx pve node stopall --vmids 100,101,102 --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("refusing to stop all guests on node %q without confirmation: pass --yes/-y", deps.Node)
			}
			params := &nodes.CreateStopallParams{}
			fl := cmd.Flags()
			if fl.Changed("vmids") {
				params.Vms = &vmids
			}
			if fl.Changed("force-stop") {
				params.ForceStop = &forceStop
			}
			if fl.Changed("timeout") {
				params.Timeout = &timeout
			}
			if fl.Changed("max-workers") {
				params.MaxWorkers = &maxWorkers
			}
			resp, err := deps.API.Nodes.CreateStopall(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("stop all guests on node %q: %w", deps.Node, err)
			}
			return renderBulkTask(cmd, deps, rawOrNil(resp), "Stop-all started.")
		},
	}
	f := cmd.Flags()
	f.StringVar(&vmids, "vmids", "", "comma-separated VMIDs to act on (default: all guests)")
	f.BoolVar(&forceStop, "force-stop", false, "force a hard stop once the timeout elapses")
	f.Int64Var(&timeout, "timeout", 0, "per-guest shutdown timeout in seconds")
	f.Int64Var(&maxWorkers, "max-workers", 0, "maximum number of concurrent tasks")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the action without prompting")
	return cmd
}

func newSuspendallCmd() *cobra.Command {
	var (
		vmids      string
		maxWorkers int64
		yes        bool
	)
	cmd := &cobra.Command{
		Use:   "suspendall",
		Short: "Suspend all guests on the node",
		Long: "Suspend every guest on the resolved node, or only those listed in --vmids. " +
			"Requires --yes because it affects all guests by default.",
		Example: `  pmx pve node suspendall --yes`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("refusing to suspend all guests on node %q without confirmation: pass --yes/-y", deps.Node)
			}
			params := &nodes.CreateSuspendallParams{}
			fl := cmd.Flags()
			if fl.Changed("vmids") {
				params.Vms = &vmids
			}
			if fl.Changed("max-workers") {
				params.MaxWorkers = &maxWorkers
			}
			resp, err := deps.API.Nodes.CreateSuspendall(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("suspend all guests on node %q: %w", deps.Node, err)
			}
			return renderBulkTask(cmd, deps, rawOrNil(resp), "Suspend-all started.")
		},
	}
	f := cmd.Flags()
	f.StringVar(&vmids, "vmids", "", "comma-separated VMIDs to act on (default: all guests)")
	f.Int64Var(&maxWorkers, "max-workers", 0, "maximum number of concurrent tasks")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the action without prompting")
	return cmd
}

func newMigrateallCmd() *cobra.Command {
	var (
		vmids          string
		targetNode     string
		maxWorkers     int64
		withLocalDisks bool
		yes            bool
	)
	cmd := &cobra.Command{
		Use:   "migrateall",
		Short: "Migrate all guests off the node",
		Long: "Migrate every guest on the resolved node to --target-node, or only those " +
			"listed in --vmids. Requires --yes because it affects all guests by default.",
		Example: `  pmx pve node migrateall --target-node pve2 --yes`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("refusing to migrate all guests on node %q without confirmation: pass --yes/-y", deps.Node)
			}
			params := &nodes.CreateMigrateallParams{Target: targetNode}
			fl := cmd.Flags()
			if fl.Changed("vmids") {
				params.Vms = &vmids
			}
			if fl.Changed("max-workers") {
				params.MaxWorkers = &maxWorkers
			}
			if fl.Changed("with-local-disks") {
				params.WithLocalDisks = &withLocalDisks
			}
			resp, err := deps.API.Nodes.CreateMigrateall(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("migrate all guests on node %q: %w", deps.Node, err)
			}
			return renderBulkTask(cmd, deps, rawOrNil(resp), "Migrate-all started.")
		},
	}
	f := cmd.Flags()
	f.StringVar(&vmids, "vmids", "", "comma-separated VMIDs to act on (default: all guests)")
	f.StringVar(&targetNode, "target-node", "", "node to migrate guests to (required)")
	f.Int64Var(&maxWorkers, "max-workers", 0, "maximum number of concurrent tasks")
	f.BoolVar(&withLocalDisks, "with-local-disks", false, "migrate local disks as well")
	f.BoolVarP(&yes, "yes", "y", false, "confirm the action without prompting")
	cli.MustMarkRequired(cmd, "target-node")
	return cmd
}

func newWakeonlanCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "wakeonlan",
		Short: "Send a Wake-on-LAN packet to the node",
		Long: "Send a Wake-on-LAN magic packet to power on the resolved node. The node's " +
			"wake-on-LAN MAC address must be configured. Requires --yes.",
		Example: `  pmx pve node wakeonlan --yes`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("refusing to wake node %q without confirmation: pass --yes/-y", deps.Node)
			}
			resp, err := deps.API.Nodes.CreateWakeonlan(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("wake node %q: %w", deps.Node, err)
			}
			// The response is the MAC address the magic packet was sent to, not a
			// task UPID, so render it directly instead of waiting on a task.
			msg := fmt.Sprintf("Wake-on-LAN packet sent to node %q.", deps.Node)
			if mac := strings.Trim(string(rawOrNil(resp)), `"`); mac != "" {
				msg = fmt.Sprintf("Wake-on-LAN packet sent to node %q (%s).", deps.Node, mac)
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the action without prompting")
	return cmd
}
