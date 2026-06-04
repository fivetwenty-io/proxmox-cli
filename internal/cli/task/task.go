package task

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/tasks"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

func init() {
	cli.RegisterGroup(NewGroupCmd)
}

// NewGroupCmd builds the `pve task` command and all of its sub-commands.
//
// The *cli.Deps argument is a placeholder used only so cobra can assemble the
// command tree; each sub-command resolves its live dependencies from the cobra
// context via cli.GetDeps.
func NewGroupCmd(_ *cli.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Inspect and control Proxmox VE tasks",
		Long: `Work with Proxmox VE tasks: list recent tasks on a node, read a task's
log, wait for a task to finish, or stop a running task.`,
	}

	cmd.AddCommand(
		newListCmd(),
		newLogCmd(),
		newWaitCmd(),
		newStopCmd(),
	)

	return cmd
}

// requireNode returns the resolved node name or an error instructing the user
// how to provide one.
func requireNode(node string) (string, error) {
	if node == "" {
		return "", fmt.Errorf("no node specified: use --node, set PVE_NODE, or configure default-node")
	}

	return node, nil
}

// newListCmd builds `pve task list`.
func newListCmd() *cobra.Command {
	var (
		vmid         int
		typeFilter   string
		statusFilter string
		since        int
		until        int
		limit        int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent tasks on a node",
		Args:  cobra.NoArgs,
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
	headers := []string{"UPID", "TYPE", "ID", "NODE", "STARTTIME", "ENDTIME", "STATUS", "USER"}

	if resp == nil {
		return output.Result{Headers: headers, Raw: []taskEntry{}}, nil
	}

	entries := make([]taskEntry, 0, len(*resp))
	rows := make([][]string, 0, len(*resp))
	for i, raw := range *resp {
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

// formatTimestamp renders a Unix timestamp as a string; zero renders as "-".
func formatTimestamp(ts int64) string {
	if ts == 0 {
		return "-"
	}

	return strconv.FormatInt(ts, 10)
}

// newLogCmd builds `pve task log <upid>`.
func newLogCmd() *cobra.Command {
	var (
		limit int
		start int
	)

	cmd := &cobra.Command{
		Use:   "log <upid>",
		Short: "Read the log of a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			node, err := requireNode(deps.Node)
			if err != nil {
				return err
			}

			upid := args[0]
			params := &nodes.ListTasksLogParams{}
			if cmd.Flags().Changed("limit") {
				v := int64(limit)
				params.Limit = &v
			}
			if cmd.Flags().Changed("start") {
				v := int64(start)
				params.Start = &v
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

// newStopCmd builds `pve task stop <upid>`.
func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop <upid>",
		Short: "Stop a running task",
		Args:  cobra.ExactArgs(1),
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

// newWaitCmd builds `pve task wait <upid>`.
func newWaitCmd() *cobra.Command {
	var (
		timeout  int
		interval int
	)

	cmd := &cobra.Command{
		Use:   "wait <upid>",
		Short: "Wait for a task to finish",
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
				return err
			}

			result := buildWaitResult(status)
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}

	cmd.Flags().IntVar(&timeout, "timeout", 300, "maximum seconds to wait")
	cmd.Flags().IntVar(&interval, "interval", 500, "polling interval in milliseconds")

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
