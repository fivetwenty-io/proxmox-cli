package pdm

import (
	"fmt"

	"github.com/spf13/cobra"

	pdmpbs "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/pbs"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newPbsTaskCmd builds `pmx pdm pbs task` — list, inspect, and stop
// background tasks on a PBS remote (/pbs/remotes/{remote}/tasks...).
func newPbsTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "List, inspect, and stop background tasks on a PBS remote",
		Long:  "List, inspect, and stop the background tasks recorded on a PBS remote, and read a task's log.",
	}
	cmd.AddCommand(newPbsTaskLsCmd(), newPbsTaskStatusCmd(), newPbsTaskLogCmd(), newPbsTaskStopCmd())
	return cmd
}

// pbsTaskEntry mirrors one element of the JSON array returned by GET
// /pbs/remotes/{remote}/tasks.
type pbsTaskEntry struct {
	Endtime    *int64  `json:"endtime,omitempty"`
	Node       string  `json:"node"`
	Pid        int64   `json:"pid"`
	Pstart     int64   `json:"pstart"`
	Starttime  int64   `json:"starttime"`
	Status     *string `json:"status,omitempty"`
	Upid       string  `json:"upid"`
	User       string  `json:"user"`
	WorkerId   *string `json:"worker_id,omitempty"`
	WorkerType string  `json:"worker_type"`
}

// newPbsTaskLsCmd builds `pmx pdm pbs task ls <remote>` — get the list of
// tasks for a PBS remote (GET /pbs/remotes/{remote}/tasks). The server
// returns tasks in its own (reverse-chronological) order; this is preserved
// rather than re-sorted, matching remote_task.go's and node_tasks.go's ls
// precedent — a task list is a log, not a set of named entities.
func newPbsTaskLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls <remote>",
		Short:   "List background tasks on a PBS remote",
		Long:    "List the background tasks recorded on a PBS remote (GET /pbs/remotes/{remote}/tasks).",
		Example: "  pmx pdm pbs task ls pbs-main",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]

			resp, err := deps.PDM.Pbs.ListRemotesTasks(cmd.Context(), remote)
			if err != nil {
				return fmt.Errorf("list tasks on PBS remote %q: %w", remote, err)
			}

			entries, err := nodeDecodeArray[pbsTaskEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode tasks on PBS remote %q: %w", remote, err)
			}

			headers := []string{"UPID", "TYPE", "USER", "STATUS", "STARTTIME", "ENDTIME"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					e.Upid, e.WorkerType, e.User, strPtrString(e.Status),
					int64PtrString(&e.Starttime), int64PtrString(e.Endtime),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newPbsTaskStatusCmd builds `pmx pdm pbs task status <remote> <upid>` —
// get the status of a task on a PBS remote (GET
// /pbs/remotes/{remote}/tasks/{upid}/status).
func newPbsTaskStatusCmd() *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "status <remote> <upid>",
		Short: "Show the status of one task on a PBS remote",
		Long: "Show the status record of one task on a PBS remote identified by its UPID; " +
			"pass --wait to block until the task finishes before returning its result (GET " +
			"/pbs/remotes/{remote}/tasks/{upid}/status).",
		Example: "  pmx pdm pbs task status pbs-main UPID:pbs1:00001234:...",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, upid := args[0], args[1]

			params := &pdmpbs.ListRemotesTasksStatusParams{}
			if cmd.Flags().Changed("wait") {
				params.Wait = &wait
			}

			resp, err := deps.PDM.Pbs.ListRemotesTasksStatus(cmd.Context(), remote, upid, params)
			if err != nil {
				return fmt.Errorf("get status of task %q on PBS remote %q: %w", upid, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get status of task %q on PBS remote %q: empty response from server", upid, remote)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode status of task %q on PBS remote %q: %w", upid, remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "wait for the task to finish before returning its result")
	return cmd
}

// newPbsTaskLogCmd builds `pmx pdm pbs task log <remote> <upid>` — read a
// task's log on a PBS remote (GET /pbs/remotes/{remote}/tasks/{upid}/log).
func newPbsTaskLogCmd() *cobra.Command {
	var (
		start, limit int64
		download     bool
	)
	cmd := &cobra.Command{
		Use:   "log <remote> <upid>",
		Short: "Read a task's log on a PBS remote",
		Long: "Read the log lines of a task on a PBS remote identified by its UPID. Use " +
			"--start and --limit to page through a long log, or --download to fetch the raw " +
			"log text (GET /pbs/remotes/{remote}/tasks/{upid}/log).",
		Example: "  pmx pdm pbs task log pbs-main UPID:pbs1:00001234:...",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, upid := args[0], args[1]
			fl := cmd.Flags()

			params := &pdmpbs.ListRemotesTasksLogParams{}
			if fl.Changed("start") {
				params.Start = int64Ptr(start)
			}
			if fl.Changed("limit") {
				params.Limit = int64Ptr(limit)
			}
			if fl.Changed("download") {
				params.Download = boolPtr(download)
			}

			resp, err := deps.PDM.Pbs.ListRemotesTasksLog(cmd.Context(), remote, upid, params)
			if err != nil {
				return fmt.Errorf("read log of task %q on PBS remote %q: %w", upid, remote, err)
			}

			lines, err := nodeDecodeArray[nodeLogLine](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode log of task %q on PBS remote %q: %w", upid, remote, err)
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

// newPbsTaskStopCmd builds `pmx pdm pbs task stop <remote> <upid>` — try to
// stop a running task on a PBS remote (DELETE
// /pbs/remotes/{remote}/tasks/{upid}). DeleteRemotesTasks discards its
// response body (`_ = resp; return nil`, pbs_gen.go:937-953, v3.6.0), but
// unlike the apt-repositories discard this is expected: a stop request has
// nothing meaningful to report beyond success, matching node_tasks.go's
// `node task stop`.
func newPbsTaskStopCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "stop <remote> <upid>",
		Short: "Stop a running task on a PBS remote",
		Long:  "Try to stop a running background task on a PBS remote. This is destructive: pass --yes/-y to confirm.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, upid := args[0], args[1]

			if !yes {
				return fmt.Errorf("refusing to stop task %q on PBS remote %q without confirmation: pass --yes/-y", upid, remote)
			}

			err := deps.PDM.Pbs.DeleteRemotesTasks(cmd.Context(), remote, upid)
			if err != nil {
				return fmt.Errorf("stop task %q on PBS remote %q: %w", upid, remote, err)
			}

			res := output.Result{Message: fmt.Sprintf("Task %s on PBS remote %q stopped.", upid, remote)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
