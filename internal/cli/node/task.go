package node

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newTaskCmd builds the `pve node task` sub-group.
func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Inspect and control node tasks",
	}
	cmd.AddCommand(
		newTaskListCmd(),
		newTaskLogCmd(),
		newTaskStopCmd(),
		newTaskWaitCmd(),
		newTaskStatusCmd(),
	)
	return cmd
}

// taskListEntry is the minimal decoded shape of a node task list entry.
type taskListEntry struct {
	UPID      string `json:"upid"`
	Type      string `json:"type"`
	ID        string `json:"id"`
	Node      string `json:"node"`
	Starttime int64  `json:"starttime"`
	Endtime   int64  `json:"endtime"`
	Status    string `json:"status"`
	User      string `json:"user"`
}

// newTaskListCmd builds `pve node task list <node>`.
func newTaskListCmd() *cobra.Command {
	var (
		vmid         int64
		typefilter   string
		statusfilter string
		since        int64
		until        int64
		limit        int64
	)
	cmd := &cobra.Command{
		Use:   "list <node>",
		Short: "List tasks on a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			params := &nodes.ListTasksParams{}
			if cmd.Flags().Changed("vmid") {
				params.Vmid = &vmid
			}
			if cmd.Flags().Changed("typefilter") {
				params.Typefilter = &typefilter
			}
			if cmd.Flags().Changed("statusfilter") {
				params.Statusfilter = &statusfilter
			}
			if cmd.Flags().Changed("since") {
				params.Since = &since
			}
			if cmd.Flags().Changed("until") {
				params.Until = &until
			}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}

			resp, err := deps.API.Nodes.ListTasks(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("list tasks on node %q: %w", node, err)
			}

			headers := []string{"UPID", "TYPE", "ID", "NODE", "STARTTIME", "ENDTIME", "STATUS", "USER"}
			entries := make([]taskListEntry, 0)
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e taskListEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode task entry: %w", err)
					}
					entries = append(entries, e)
					rows = append(rows, []string{
						e.UPID,
						e.Type,
						e.ID,
						e.Node,
						strconv.FormatInt(e.Starttime, 10),
						strconv.FormatInt(e.Endtime, 10),
						e.Status,
						e.User,
					})
				}
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}

	cmd.Flags().Int64Var(&vmid, "vmid", 0, "only list tasks for this VMID")
	cmd.Flags().StringVar(&typefilter, "typefilter", "", "only list tasks of this type")
	cmd.Flags().StringVar(&statusfilter, "statusfilter", "", "only list tasks with this status (ok|error|running)")
	cmd.Flags().Int64Var(&since, "since", 0, "only list tasks since this UNIX timestamp")
	cmd.Flags().Int64Var(&until, "until", 0, "only list tasks until this UNIX timestamp")
	cmd.Flags().Int64Var(&limit, "limit", 50, "maximum number of tasks to list")

	return cmd
}

// taskLogLine is the minimal decoded shape of a task log line.
type taskLogLine struct {
	N int64  `json:"n"`
	T string `json:"t"`
}

// newTaskLogCmd builds `pve node task log <node> <upid>`.
func newTaskLogCmd() *cobra.Command {
	var (
		limit int64
		start int64
	)
	cmd := &cobra.Command{
		Use:   "log <node> <upid>",
		Short: "Show the log of a task",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			upid := args[1]

			params := &nodes.ListTasksLogParams{}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}
			if cmd.Flags().Changed("start") {
				params.Start = &start
			}

			resp, err := deps.API.Nodes.ListTasksLog(cmd.Context(), node, upid, params)
			if err != nil {
				return fmt.Errorf("get log for task %q on node %q: %w", upid, node, err)
			}

			headers := []string{"N", "T"}
			lines := make([]taskLogLine, 0)
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var line taskLogLine
					if err := json.Unmarshal(raw, &line); err != nil {
						return fmt.Errorf("decode task log line: %w", err)
					}
					lines = append(lines, line)
					rows = append(rows, []string{strconv.FormatInt(line.N, 10), line.T})
				}
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: lines}, deps.Format)
		},
	}

	cmd.Flags().Int64Var(&limit, "limit", 500, "number of log lines to read")
	cmd.Flags().Int64Var(&start, "start", 0, "start reading the log at this line")

	return cmd
}

// newTaskStopCmd builds `pve node task stop <node> <upid>`.
func newTaskStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <node> <upid>",
		Short: "Stop a running task",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			upid := args[1]

			if err := deps.API.Nodes.DeleteTasks(cmd.Context(), node, upid); err != nil {
				return fmt.Errorf("stop task %q on node %q: %w", upid, node, err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Task %s stopped.", upid)}, deps.Format)
		},
	}
}

// newTaskStatusCmd builds `pve node task status <upid>`. The node is resolved
// from deps.Node (--node flag, PVE_NODE env, or configured default).
func newTaskStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <upid>",
		Short: "Show the runtime status of a task by UPID",
		Long: "Query the current status of a single task on the resolved node by its UPID. " +
			"Reports whether the task is still running and, when finished, the exit status.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			upid := args[0]
			resp, err := deps.API.Nodes.ListTasksStatus(cmd.Context(), deps.Node, upid)
			if err != nil {
				return fmt.Errorf("get status of task %q on node %q: %w", upid, deps.Node, err)
			}
			single := map[string]string{
				"STATUS": resp.Status,
				"ID":     resp.Id,
				"TYPE":   resp.Type,
				"USER":   resp.User,
				"NODE":   resp.Node,
				"PID":    strconv.FormatInt(resp.Pid.Int(), 10),
				"UPID":   resp.Upid,
			}
			if resp.Exitstatus != nil {
				single["EXITSTATUS"] = *resp.Exitstatus
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}
}

// newTaskWaitCmd builds `pve node task wait <upid>`. The node is parsed from the
// UPID by the wait helper, so no positional node is required.
func newTaskWaitCmd() *cobra.Command {
	var (
		timeout  int
		interval int
	)
	cmd := &cobra.Command{
		Use:   "wait <upid>",
		Short: "Wait for a task to complete",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			upid := args[0]

			opts := &tasks.WaitOptions{
				TimeoutSeconds: timeout,
				IntervalMillis: interval,
			}

			status, err := deps.API.Tasks.WaitForUPID(cmd.Context(), upid, opts)
			if err != nil {
				return fmt.Errorf("wait for task %q: %w", upid, err)
			}

			single := map[string]string{
				"UPID":        upid,
				"STATUS":      status.Status,
				"EXIT-STATUS": status.ExitStatus,
				"WARNED":      strconv.FormatBool(status.Warned),
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single}, deps.Format)
		},
	}

	cmd.Flags().IntVar(&timeout, "timeout", 300, "maximum seconds to wait")
	cmd.Flags().IntVar(&interval, "interval", 500, "poll interval in milliseconds")

	return cmd
}
