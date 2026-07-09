package pdm

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	pdmnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// nodeTaskEntry mirrors one element of the JSON array PDM returns from
// GET /nodes/{node}/tasks.
type nodeTaskEntry struct {
	Endtime    *int64  `json:"endtime,omitempty"`
	Exitstatus *string `json:"exitstatus,omitempty"`
	Id         *string `json:"id,omitempty"`
	Node       string  `json:"node"`
	Pid        int64   `json:"pid"`
	Pstart     int64   `json:"pstart"`
	Starttime  int64   `json:"starttime"`
	Status     string  `json:"status"`
	Tokenid    *string `json:"tokenid,omitempty"`
	Type       string  `json:"type"`
	Upid       string  `json:"upid"`
	User       string  `json:"user"`
}

// nodeTaskStatusCell renders a task-list entry's status, preferring
// exitstatus (set once the task has finished) over the bare running/stopped
// status field.
func nodeTaskStatusCell(e nodeTaskEntry) string {
	if e.Exitstatus != nil {
		return *e.Exitstatus
	}
	return e.Status
}

// newNodeTaskCmd builds `pmx pdm node task` and its ls/status/log/stop
// verbs (/nodes/{node}/tasks...).
func newNodeTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "List, inspect, and stop background tasks on the node",
	}
	cmd.AddCommand(
		newNodeTaskLsCmd(),
		newNodeTaskStatusCmd(),
		newNodeTaskLogCmd(),
		newNodeTaskStopCmd(),
	)
	return cmd
}

// newNodeTaskLsCmd builds `pmx pdm node task ls <node>` — list tasks, with
// every ListTasksParams filter exposed as a flag (GET /nodes/{node}/tasks).
// The server returns tasks in its own (reverse-chronological) order; this is
// preserved rather than re-sorted, matching remote_task.go's ls (GET
// /remotes/tasks/list) precedent.
func newNodeTaskLsCmd() *cobra.Command {
	var (
		errorsOnly   bool
		running      bool
		limit, start int64
		since, until int64
		statusfilter []string
		typefilter   string
		userfilter   string
	)

	cmd := &cobra.Command{
		Use:   "ls <node>",
		Short: "List background tasks on the node",
		Long:  "List background tasks recorded on the node, optionally filtered by status, time range, type, or user.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			fl := cmd.Flags()

			params := &pdmnodes.ListTasksParams{}
			if fl.Changed("errors") {
				params.Errors = boolPtr(errorsOnly)
			}
			if fl.Changed("running") {
				params.Running = boolPtr(running)
			}
			if fl.Changed("limit") {
				params.Limit = int64Ptr(limit)
			}
			if fl.Changed("start") {
				params.Start = int64Ptr(start)
			}
			if fl.Changed("since") {
				params.Since = int64Ptr(since)
			}
			if fl.Changed("until") {
				params.Until = int64Ptr(until)
			}
			if fl.Changed("status") {
				params.Statusfilter = statusfilter
			}
			if fl.Changed("type") {
				params.Typefilter = strPtr(typefilter)
			}
			if fl.Changed("user") {
				params.Userfilter = strPtr(userfilter)
			}

			resp, err := deps.PDM.Nodes.ListTasks(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("list tasks on node %q: %w", node, err)
			}

			entries, err := nodeDecodeArray[nodeTaskEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode tasks on node %q: %w", node, err)
			}

			headers := []string{"UPID", "TYPE", "ID", "USER", "STATUS", "STARTTIME", "ENDTIME"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					e.Upid, e.Type, strPtrString(e.Id), e.User, nodeTaskStatusCell(e),
					int64PtrString(&e.Starttime), int64PtrString(e.Endtime),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&errorsOnly, "errors", false, "only list erroneous tasks")
	f.BoolVar(&running, "running", false, "only list running tasks")
	f.Int64Var(&limit, "limit", 0, "only list this many tasks (0 means no limit)")
	f.Int64Var(&start, "start", 0, "list tasks starting from this offset")
	f.Int64Var(&since, "since", 0, "only list tasks since this Unix epoch")
	f.Int64Var(&until, "until", 0, "only list tasks until this Unix epoch")
	f.StringArrayVar(&statusfilter, "status", nil, "only list tasks with any of these statuses (repeatable)")
	f.StringVar(&typefilter, "type", "", "only list tasks whose type contains this substring")
	f.StringVar(&userfilter, "user", "", "only list tasks started by this user")

	return cmd
}

// newNodeTaskStatusCmd builds `pmx pdm node task status <node> <upid>` —
// get task status (GET /nodes/{node}/tasks/{upid}/status).
func newNodeTaskStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <node> <upid>",
		Short: "Show the status of one task",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, upid := args[0], args[1]

			resp, err := deps.PDM.Nodes.ListTasksStatus(cmd.Context(), node, upid)
			if err != nil {
				return fmt.Errorf("get status of task %q on node %q: %w", upid, node, err)
			}
			if resp == nil {
				return fmt.Errorf("get status of task %q on node %q: empty response from server", upid, node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode status of task %q on node %q: %w", upid, node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newNodeTaskLogCmd builds `pmx pdm node task log <node> <upid>` — read a
// task's log (GET /nodes/{node}/tasks/{upid}/log).
func newNodeTaskLogCmd() *cobra.Command {
	var (
		start, limit int64
		download     bool
	)

	cmd := &cobra.Command{
		Use:   "log <node> <upid>",
		Short: "Read a task's log",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, upid := args[0], args[1]
			fl := cmd.Flags()

			params := &pdmnodes.ListTasksLogParams{}
			if fl.Changed("start") {
				params.Start = int64Ptr(start)
			}
			if fl.Changed("limit") {
				params.Limit = int64Ptr(limit)
			}
			if fl.Changed("download") {
				params.Download = boolPtr(download)
			}

			resp, err := deps.PDM.Nodes.ListTasksLog(cmd.Context(), node, upid, params)
			if err != nil {
				return fmt.Errorf("read log of task %q on node %q: %w", upid, node, err)
			}

			items := rawItemsOf(resp)
			lines := make([]nodeLogLine, 0, len(items))
			for _, raw := range items {
				var l nodeLogLine

				err := json.Unmarshal(raw, &l)
				if err != nil {
					return fmt.Errorf("decode log of task %q on node %q: %w", upid, node, err)
				}

				lines = append(lines, l)
			}

			headers := []string{"LINE"}
			rows := make([][]string, 0, len(lines))
			for _, l := range lines {
				rows = append(rows, []string{nodeLogLineText(l)})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: lines}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&start, "start", 0, "start at this line when reading the log")
	f.Int64Var(&limit, "limit", 0, "number of lines to read (0 reads to the end of the file)")
	f.BoolVar(&download, "download", false, "download the raw task-log file instead of paginated lines")

	return cmd
}

// newNodeTaskStopCmd builds `pmx pdm node task stop <node> <upid>` — try to
// stop a running task (DELETE /nodes/{node}/tasks/{upid}).
func newNodeTaskStopCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "stop <node> <upid>",
		Short: "Stop a running task",
		Long:  "Try to stop a running background task. This is destructive: pass --yes/-y to confirm.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, upid := args[0], args[1]

			if !yes {
				return fmt.Errorf("refusing to stop task %q on node %q without confirmation: pass --yes/-y", upid, node)
			}

			err := deps.PDM.Nodes.DeleteTasks(cmd.Context(), node, upid)
			if err != nil {
				return fmt.Errorf("stop task %q on node %q: %w", upid, node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Task %s stopped.", upid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")

	return cmd
}
