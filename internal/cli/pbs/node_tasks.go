package pbs

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"

	pbsnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// nodeTaskEntry mirrors one element of the JSON array PBS returns from
// GET /nodes/{node}/tasks, using the same field shape as the single-task
// GET /nodes/{node}/tasks/{upid}/status response (ListTasksStatusResponse),
// since PBS's task-list and task-status objects share the TaskListItem
// schema.
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

// newNodeTasksCmd builds `pmx pbs node tasks` and its ls/show/log/delete
// verbs (/nodes/{node}/tasks...).
func newNodeTasksCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tasks",
		Short: "List, inspect, and stop background tasks on the node",
		Long: "List, inspect, and stop the background tasks (backups, GC, sync, prune, verify, " +
			"and more) recorded on the node, and read a task's log.",
	}
	cmd.AddCommand(
		newNodeTasksLsCmd(nf),
		newNodeTasksShowCmd(nf),
		newNodeTasksLogCmd(nf),
		newNodeTasksDeleteCmd(nf),
	)
	return cmd
}

// newNodeTasksLsCmd builds `pmx pbs node tasks ls` — list tasks, with every
// ListTasksParams filter exposed as a flag (GET /nodes/{node}/tasks).
func newNodeTasksLsCmd(nf *nodeFlags) *cobra.Command {
	var (
		errorsOnly   bool
		running      bool
		limit, start int64
		since, until int64
		statusfilter []string
		store        string
		typefilter   string
		userfilter   string
	)

	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List background tasks on the node",
		Long:  "List background tasks recorded on the node, optionally filtered by status, time range, store, or user.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			params := &pbsnodes.ListTasksParams{}
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
			if fl.Changed("store") {
				params.Store = strPtr(store)
			}
			if fl.Changed("type") {
				params.Typefilter = strPtr(typefilter)
			}
			if fl.Changed("user") {
				params.Userfilter = strPtr(userfilter)
			}

			resp, err := deps.PBS.Nodes.ListTasks(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("list tasks on node %q: %w", nf.node, err)
			}

			entries, err := nodeDecodeArray[nodeTaskEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode tasks on node %q: %w", nf.node, err)
			}

			headers := []string{"UPID", "TYPE", "ID", "USER", "STATUS", "STARTTIME", "ENDTIME"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					e.Upid,
					e.Type,
					pbsFormatOptionalString(e.Id),
					e.User,
					nodeTaskStatusCell(e),
					epochCellPtr(&e.Starttime),
					epochCellPtr(e.Endtime),
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
	f.StringVar(&store, "store", "", "only list tasks for this datastore")
	f.StringVar(&typefilter, "type", "", "only list tasks whose type contains this substring")
	f.StringVar(&userfilter, "user", "", "only list tasks started by this user")

	return cmd
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

// newNodeTasksShowCmd builds `pmx pbs node tasks show <upid>` — get task
// status (GET /nodes/{node}/tasks/{upid}/status).
func newNodeTasksShowCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "show <upid>",
		Short:   "Show the status of one task",
		Long:    "Show the full status record of one task identified by its UPID: type, user, status, and start/end time.",
		Example: "  pmx pbs node tasks show UPID:pbs:00001234:...",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			upid := args[0]

			resp, err := deps.PBS.Nodes.ListTasksStatus(cmd.Context(), nf.node, upid)
			if err != nil {
				return fmt.Errorf("get status of task %q on node %q: %w", upid, nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("get status of task %q on node %q: empty response from server", upid, nf.node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode status of task %q on node %q: %w", upid, nf.node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newNodeTasksLogCmd builds `pmx pbs node tasks log <upid>` — read a task's
// log (GET /nodes/{node}/tasks/{upid}/log).
//
// The generated Nodes.ListTasksLog binding discards its response body (the
// PBS API schema gives this endpoint no documented return type), so this
// bypasses it via the shared raw transport to recover the actual log lines.
func newNodeTasksLogCmd(nf *nodeFlags) *cobra.Command {
	var (
		start, limit int64
		download     bool
	)

	cmd := &cobra.Command{
		Use:   "log <upid>",
		Short: "Read a task's log",
		Long: "Read the log lines of a task identified by its UPID. Use --start and --limit to " +
			"page through a long log, or --download to fetch the raw log text instead of " +
			"paginated lines.",
		Example: "  pmx pbs node tasks log UPID:pbs:00001234:...",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			upid := args[0]
			fl := cmd.Flags()

			body := map[string]interface{}{}
			if fl.Changed("start") {
				body["start"] = start
			}
			if fl.Changed("limit") {
				body["limit"] = limit
			}
			if fl.Changed("download") {
				body["download"] = download
			}

			path := fmt.Sprintf("/nodes/%s/tasks/%s/log", url.PathEscape(nf.node), url.PathEscape(upid))

			raw, err := nodeRawCall(cmd.Context(), deps, http.MethodGet, path, body)
			if err != nil {
				return fmt.Errorf("read log of task %q on node %q: %w", upid, nf.node, err)
			}

			lines, err := nodeRawArrayItems(raw)
			if err != nil {
				// Not an array (e.g. --download requests a raw byte stream): render
				// the decoded text directly instead of failing the command.
				text, textErr := nodeDecodeText(raw)
				if textErr != nil {
					return fmt.Errorf("decode log of task %q on node %q: %w", upid, nf.node, err)
				}
				res := output.Result{Message: text, Raw: text}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			logLines, err := nodeDecodeArray[nodeLogLine](lines)
			if err != nil {
				return fmt.Errorf("decode log of task %q on node %q: %w", upid, nf.node, err)
			}

			headers := []string{"LINE"}
			rows := make([][]string, 0, len(logLines))
			for _, l := range logLines {
				rows = append(rows, []string{nodeLogLineText(l)})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: logLines}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&start, "start", 0, "start at this line when reading the log")
	f.Int64Var(&limit, "limit", 0, "number of lines to read (0 reads to the end of the file)")
	f.BoolVar(&download, "download", false, "download the raw task-log file instead of paginated lines")

	return cmd
}

// newNodeTasksDeleteCmd builds `pmx pbs node tasks delete <upid>` — try to
// stop a running task (DELETE /nodes/{node}/tasks/{upid}).
func newNodeTasksDeleteCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <upid>",
		Aliases: []string{"stop"},
		Short:   "Stop a running task",
		Long:    "Try to stop the running task identified by its UPID. Has no effect on a task that has already finished.",
		Example: "  pmx pbs node tasks delete UPID:pbs:00001234:...",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			upid := args[0]

			err := deps.PBS.Nodes.DeleteTasks(cmd.Context(), nf.node, upid)
			if err != nil {
				return fmt.Errorf("stop task %q on node %q: %w", upid, nf.node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Task %s stopped.", upid)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}
