package node

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// renderReplicationTask renders the asynchronous worker started by an on-demand
// replication run. schedule_now returns a worker UPID; honour --async and
// otherwise block on the task, tolerating a non-UPID or empty body by falling
// back to a plain success message.
func renderReplicationTask(cmd *cobra.Command, deps *cli.Deps, raw json.RawMessage, doneMsg string) error {
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
		return fmt.Errorf("replication run on node %q: %w", deps.Node, err)
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: doneMsg}, deps.Format)
}

func newReplicationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "replication",
		Aliases: []string{"repl"},
		Short:   "Inspect and trigger storage replication on the node",
		Long: "View the state of the storage-replication jobs whose source is the resolved node, read a " +
			"job's status and log, and trigger an immediate run. Replication jobs themselves are defined " +
			"cluster-wide via `pve cluster replication`.",
	}
	cmd.AddCommand(newReplicationListCmd(), newReplicationGetCmd(), newReplicationStatusCmd(), newReplicationLogCmd(), newReplicationRunCmd())
	return cmd
}

// newReplicationGetCmd builds `pve node replication get <id>`.
//
// GET /nodes/{node}/replication/{id} is only a directory index (status, log,
// schedule_now); the job configuration lives cluster-wide at
// /cluster/replication/{id}.
func newReplicationGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show the configuration of a replication job",
		Long: "Show the full configuration of a single storage-replication job, including its target, " +
			"schedule, rate limit, remove-job setting, and comment. Job configuration is cluster-wide; " +
			"this is equivalent to `pve cluster replication get`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.GetReplication(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get replication job %q: %w", args[0], err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
}

func newReplicationListCmd() *cobra.Command {
	var guest int64
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List replication job states on the node",
		Long: "Show every storage-replication job whose source is the resolved node, including its target, " +
			"schedule, last and next sync, and any error. Pass --guest to filter to a single guest.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.ListReplicationParams{}
			if cmd.Flags().Changed("guest") {
				params.Guest = &guest
			}
			resp, err := deps.API.Nodes.ListReplication(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("list replication jobs on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	cmd.Flags().Int64Var(&guest, "guest", 0, "only list replication jobs for this guest (VMID)")
	return cmd
}

func newReplicationStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <id>",
		Short: "Show the runtime status of a replication job",
		Long:  "Show the current runtime status of a single replication job on the resolved node, including last and next sync, duration, fail count, and any error.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListReplicationStatus(cmd.Context(), deps.Node, args[0])
			if err != nil {
				return fmt.Errorf("get replication status for job %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
}

func newReplicationLogCmd() *cobra.Command {
	var (
		limit int64
		start int64
	)
	cmd := &cobra.Command{
		Use:   "log <id>",
		Short: "Show the log of a replication job",
		Long:  "Show the log lines recorded for a single replication job on the resolved node. Use --limit and --start to page through the log.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			fl := cmd.Flags()
			params := &nodes.ListReplicationLogParams{}
			if fl.Changed("limit") {
				params.Limit = &limit
			}
			if fl.Changed("start") {
				params.Start = &start
			}
			resp, err := deps.API.Nodes.ListReplicationLog(cmd.Context(), deps.Node, args[0], params)
			if err != nil {
				return fmt.Errorf("get replication log for job %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&limit, "limit", 0, "maximum number of log lines to return")
	f.Int64Var(&start, "start", 0, "line number to start reading the log from")
	return cmd
}

func newReplicationRunCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "run <id>",
		Short: "Trigger an immediate run of a replication job",
		Long: "Schedule a single replication job to run immediately on the resolved node instead of waiting " +
			"for its next scheduled sync. The job must already be defined; this only triggers an early run.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, "trigger an immediate replication run"); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.CreateReplicationScheduleNow(cmd.Context(), deps.Node, args[0])
			if err != nil {
				return fmt.Errorf("trigger replication run for job %q on node %q: %w", args[0], deps.Node, err)
			}
			return renderReplicationTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Replication job %q scheduled to run now on node %q.", args[0], deps.Node))
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm triggering an immediate replication run")
	return cmd
}
