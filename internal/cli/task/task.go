package task

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// Group builds the `pmx task` command and all of its sub-commands.
//
// The *cli.Deps argument is a placeholder used only so cobra can assemble the
// command tree; each sub-command resolves its live dependencies from the cobra
// context via cli.GetDeps.
func Group(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Inspect and control Proxmox VE tasks",
		Long: `Work with Proxmox VE tasks: list recent tasks on a node or across the
cluster, check a task's status, read its log, wait for it to finish, or stop
a running task. Requires a configured Proxmox VE API connection.

Tasks are identified by their UPID, which encodes the node they ran on; 'task
status' and 'task wait' resolve the node from the UPID and need no --node
flag. 'task list', 'task log', and 'task stop' operate on a single node,
selected via --node, PMX_NODE, or the active context's default node.`,
		Example: `  pmx pve task list --node pve1
  pmx pve task status UPID:pve1:00001234:0005678A:6660A1B2:vzdump:100:root@pam:
  pmx pve task wait UPID:pve1:00001234:0005678A:6660A1B2:vzdump:100:root@pam:
  pmx pve task log UPID:pve1:00001234:0005678A:6660A1B2:vzdump:100:root@pam: --limit 50`,
	}

	cmd.AddCommand(
		newListCmd(),
		newClusterListCmd(),
		newLogCmd(),
		newStatusCmd(),
		newWaitCmd(),
		newStopCmd(),
	)

	return cmd
}

// requireNode returns the resolved node name or an error instructing the user
// how to provide one.
func requireNode(node string) (string, error) {
	if node == "" {
		return "", fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure default-node")
	}

	return node, nil
}

// newListCmd builds `pmx task list`.
func newListCmd() *cobra.Command {
	var (
		vmid         int
		typeFilter   string
		statusFilter string
		since        int
		until        int
		limit        int
		start        int
		errorsOnly   bool
		source       string
		userFilter   string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent tasks on a node",
		Long: "List recent tasks on a single node, selected via --node, PMX_NODE, or the " +
			"active context's default node. Supports filtering by VM ID, task type, status, " +
			"user, time range, and source (archive, active, or all), plus --limit/--start " +
			"pagination.",
		Example: `  pmx pve task list --node pve1
  pmx pve task list --node pve1 --statusfilter error --limit 20`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			node, err := requireNode(deps.Node)
			if err != nil {
				return err
			}

			params := &nodes.ListTasksParams{}
			if cmd.Flags().Changed("vmid") {
				v := int64(vmid)
				params.Vmid = &v
			}
			if typeFilter != "" {
				params.Typefilter = &typeFilter
			}
			if statusFilter != "" {
				params.Statusfilter = &statusFilter
			}
			if cmd.Flags().Changed("since") {
				v := int64(since)
				params.Since = &v
			}
			if cmd.Flags().Changed("until") {
				v := int64(until)
				params.Until = &v
			}
			if cmd.Flags().Changed("limit") {
				v := int64(limit)
				params.Limit = &v
			}
			if cmd.Flags().Changed("start") {
				v := int64(start)
				params.Start = &v
			}
			if errorsOnly {
				params.Errors = &errorsOnly
			}
			if source != "" {
				params.Source = &source
			}
			if userFilter != "" {
				params.Userfilter = &userFilter
			}

			resp, err := deps.API.Nodes.ListTasks(cmd.Context(), node, params)
			if err != nil {
				return err
			}

			result, err := buildTaskListResult(resp)
			if err != nil {
				return err
			}

			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}

	cmd.Flags().IntVar(&vmid, "vmid", 0, "only list tasks for this VM")
	cmd.Flags().StringVar(&typeFilter, "typefilter", "", "only list tasks of this type")
	cmd.Flags().StringVar(&statusFilter, "statusfilter", "", "filter by status: ok|error|running")
	cmd.Flags().IntVar(&since, "since", 0, "only list tasks since this Unix timestamp")
	cmd.Flags().IntVar(&until, "until", 0, "only list tasks until this Unix timestamp")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of tasks to return")
	cmd.Flags().IntVar(&start, "start", 0, "list tasks beginning from this offset (pagination)")
	cmd.Flags().BoolVar(&errorsOnly, "errors", false, "only list tasks with a status of ERROR")
	cmd.Flags().StringVar(&source, "source", "", "list tasks from this source: archive|active|all")
	cmd.Flags().StringVar(&userFilter, "userfilter", "", "only list tasks from this user")

	return cmd
}

// taskEntry is the subset of a task list entry rendered by `task list`.
type taskEntry struct {
	UPID      string `json:"upid"`
	Type      string `json:"type"`
	ID        string `json:"id"`
	Node      string `json:"node"`
	StartTime int64  `json:"starttime"`
	EndTime   int64  `json:"endtime"`
	Status    string `json:"status"`
	User      string `json:"user"`
}

// buildTaskListResult converts the raw task list response into a renderable
// Result, preserving the raw payload for JSON/YAML output.
func buildTaskListResult(resp *nodes.ListTasksResponse) (output.Result, error) {
	if resp == nil {
		return buildTaskRowsFromRaw(nil)
	}

	return buildTaskRowsFromRaw(*resp)
}

// buildTaskRowsFromRaw renders a slice of raw task entries (shared by node and
// cluster task listings, both of which return identically-shaped entries).
func buildTaskRowsFromRaw(raws []json.RawMessage) (output.Result, error) {
	headers := []string{"UPID", "TYPE", "ID", "NODE", "STARTTIME", "ENDTIME", "STATUS", "USER"}

	entries := make([]taskEntry, 0, len(raws))
	rows := make([][]string, 0, len(raws))
	for i, raw := range raws {
		var e taskEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			return output.Result{}, fmt.Errorf("decode task entry %d: %w", i, err)
		}
		entries = append(entries, e)
		rows = append(rows, []string{
			e.UPID,
			e.Type,
			e.ID,
			e.Node,
			formatTimestamp(e.StartTime),
			formatTimestamp(e.EndTime),
			e.Status,
			e.User,
		})
	}

	return output.Result{Headers: headers, Rows: rows, Raw: entries}, nil
}

// newClusterListCmd builds `pmx task cluster-list`.
func newClusterListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster-list",
		Short: "List recent tasks across the whole cluster",
		Long: "List recent tasks across every node in the cluster, not just one. Needs no " +
			"--node; unlike `pmx pve task list`, it takes no filter flags.",
		Example: `  pmx pve task cluster-list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.API.Cluster.ListTasks(cmd.Context())
			if err != nil {
				return err
			}

			var raws []json.RawMessage
			if resp != nil {
				raws = *resp
			}
			result, err := buildTaskRowsFromRaw(raws)
			if err != nil {
				return err
			}

			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}

	return cmd
}

// formatTimestamp renders a Unix timestamp as a string; zero renders as "-".
func formatTimestamp(ts int64) string {
	if ts == 0 {
		return "-"
	}

	return strconv.FormatInt(ts, 10)
}

// newLogCmd builds `pmx task log <upid>`.
func newLogCmd() *cobra.Command {
	var (
		limit    int
		start    int
		download bool
	)

	cmd := &cobra.Command{
		Use:   "log <upid>",
		Short: "Read the log of a task",
		Long: "Read a task's log lines from the node the task ran on, selected via --node, " +
			"PMX_NODE, or the active context's default node. --limit and --start page " +
			"through the log; --download instead fetches the full log file and cannot be " +
			"combined with --limit or --start.",
		Example: `  pmx pve task log UPID:pve1:00001234:0005678A:6660A1B2:vzdump:100:root@pam: --node pve1
  pmx pve task log UPID:pve1:00001234:0005678A:6660A1B2:vzdump:100:root@pam: --node pve1 --limit 50 --start 100`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			node, err := requireNode(deps.Node)
			if err != nil {
				return err
			}

			upid := args[0]
			if download && (cmd.Flags().Changed("limit") || cmd.Flags().Changed("start")) {
				return fmt.Errorf("--download cannot be combined with --limit or --start")
			}
			params := &nodes.ListTasksLogParams{}
			if cmd.Flags().Changed("limit") {
				v := int64(limit)
				params.Limit = &v
			}
			if cmd.Flags().Changed("start") {
				v := int64(start)
				params.Start = &v
			}
			if download {
				params.Download = &download
			}

			resp, err := deps.API.Nodes.ListTasksLog(cmd.Context(), node, upid, params)
			if err != nil {
				return err
			}

			result, err := buildTaskLogResult(resp)
			if err != nil {
				return err
			}

			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 500, "number of log lines to read")
	cmd.Flags().IntVar(&start, "start", 0, "start reading from this line offset")
	cmd.Flags().BoolVar(&download, "download", false,
		"download the full tasklog file (cannot be combined with --limit/--start)")

	return cmd
}

// logLine is one entry of a task log: a line number and its text.
type logLine struct {
	N int64  `json:"n"`
	T string `json:"t"`
}

// buildTaskLogResult converts the raw task log response into a renderable Result.
func buildTaskLogResult(resp *nodes.ListTasksLogResponse) (output.Result, error) {
	headers := []string{"N", "T"}

	if resp == nil {
		return output.Result{Headers: headers, Raw: []logLine{}}, nil
	}

	lines := make([]logLine, 0, len(*resp))
	rows := make([][]string, 0, len(*resp))
	for i, raw := range *resp {
		var l logLine
		if err := json.Unmarshal(raw, &l); err != nil {
			return output.Result{}, fmt.Errorf("decode log line %d: %w", i, err)
		}
		lines = append(lines, l)
		rows = append(rows, []string{strconv.FormatInt(l.N, 10), l.T})
	}

	return output.Result{Headers: headers, Rows: rows, Raw: lines}, nil
}

// newStopCmd builds `pmx task stop <upid>`.
func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop <upid>",
		Short: "Stop a running task",
		Long: "Request that a running task be stopped, on the node selected via --node, " +
			"PMX_NODE, or the active context's default node. Returns as soon as the stop " +
			"request is accepted; it does not wait for the task to actually finish exiting.",
		Example: `  pmx pve task stop UPID:pve1:00001234:0005678A:6660A1B2:vzdump:100:root@pam: --node pve1`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			node, err := requireNode(deps.Node)
			if err != nil {
				return err
			}

			upid := args[0]
			if err := deps.API.Nodes.DeleteTasks(cmd.Context(), node, upid); err != nil {
				return err
			}

			result := output.Result{Message: fmt.Sprintf("Task %s stopped.", upid)}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}

	return cmd
}

// newWaitCmd builds `pmx task wait <upid>`.
func newWaitCmd() *cobra.Command {
	var (
		timeout     int
		interval    int
		backoff     bool
		maxInterval int
	)

	cmd := &cobra.Command{
		Use:   "wait <upid>",
		Short: "Wait for a task to finish",
		Long: "Poll a task until it finishes or --timeout elapses, then print its final " +
			"status. The node is resolved from the UPID, so --node is not required. Pass " +
			"--backoff to exponentially increase the polling interval up to --max-interval " +
			"instead of polling at a fixed --interval.",
		Example: `  pmx pve task wait UPID:pve1:00001234:0005678A:6660A1B2:vzdump:100:root@pam:
  pmx pve task wait UPID:pve1:00001234:0005678A:6660A1B2:vzdump:100:root@pam: --timeout 600 --backoff`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			upid := args[0]
			opts := &tasks.WaitOptions{
				TimeoutSeconds:    timeout,
				IntervalMillis:    interval,
				Backoff:           backoff,
				MaxIntervalMillis: maxInterval,
			}

			status, err := deps.API.Tasks.WaitForUPID(cmd.Context(), upid, opts)
			if err != nil {
				return err
			}

			result := buildWaitResult(status)
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}

	cmd.Flags().IntVar(&timeout, "timeout", 300, "maximum seconds to wait")
	cmd.Flags().IntVar(&interval, "interval", 500, "polling interval in milliseconds")
	cmd.Flags().BoolVar(&backoff, "backoff", false, "exponentially back off the polling interval")
	cmd.Flags().IntVar(&maxInterval, "max-interval", 0,
		"cap the backoff interval in milliseconds (default 5000 when --backoff is set)")

	return cmd
}

// buildWaitResult renders a terminal task status as a single key/value result.
func buildWaitResult(status *tasks.Status) output.Result {
	single := map[string]string{
		"UPID":        status.UpID,
		"STATUS":      status.Status,
		"EXIT-STATUS": status.ExitStatus,
		"WARNED":      strconv.FormatBool(status.Warned),
	}

	return output.Result{Single: single, Raw: single}
}
