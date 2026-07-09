package pdm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	pdmpve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/pve"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// pveTaskStatusPoller adapts pdmpve.Service.ListRemotesTasksStatus to a
// remoteTaskStatusFunc (pbs_proxy.go). Unlike ListRemotesTasksLog
// (newPveTaskLogCmd's Raw bypass below), ListRemotesTasksStatusResponse is
// correctly typed — its fields (Status, Exitstatus, ...) match
// pdm-apidoc.json's returns schema for this endpoint, verified 2026-07-08 —
// so the generated binding is used directly here.
func pveTaskStatusPoller(svc pdmpve.Service) remoteTaskStatusFunc {
	return func(ctx context.Context, remote, upid string) (*remoteTaskStatus, error) {
		status, err := svc.ListRemotesTasksStatus(ctx, remote, upid, nil)
		if err != nil {
			return nil, err
		}
		if status == nil {
			return nil, nil
		}
		return &remoteTaskStatus{Status: status.Status, Exitstatus: status.Exitstatus}, nil
	}
}

// finishPveRemoteAsync renders the outcome of an asynchronous task running
// on a managed PVE remote. When deps.Async is set it prints the UPID
// immediately; otherwise it blocks until the task reaches a terminal state
// (polling the pve group's ListRemotesTasksStatus, since the task lives on
// the remote, not on PDM's own node) and prints msg. A thin PVE-flavored
// wrapper around the shared finishRemoteTaskAsync core (pbs_proxy.go); see
// finishRemoteAsync for the PBS equivalent.
func finishPveRemoteAsync(cmd *cobra.Command, deps *cli.Deps, remote string, raw json.RawMessage, msg string) error {
	return finishRemoteTaskAsync(cmd, deps, "PVE", remote, raw, msg, pveTaskStatusPoller(deps.PDM.Pve))
}

// newPveTaskCmd builds `pmx pdm pve task` — list, inspect, and stop
// background tasks on a PVE remote (/pve/remotes/{remote}/tasks...).
//
// GetRemotesTasks (GET .../tasks/{upid}) is a directory-index leaf with no
// data of its own (returns only `error`, pve_gen.go:7128-7146, v3.6.0) and
// is excluded, matching every other product group in this package.
func newPveTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "List, inspect, and stop background tasks on a PVE remote",
	}
	cmd.AddCommand(newPveTaskLsCmd(), newPveTaskStatusCmd(), newPveTaskLogCmd(), newPveTaskStopCmd())
	return cmd
}

// pveTaskEntry mirrors one element of the JSON array returned by GET
// /pve/remotes/{remote}/tasks (pdm-apidoc.json, verified 2026-07-08).
type pveTaskEntry struct {
	Endtime   *int64  `json:"endtime,omitempty"`
	Id        string  `json:"id"`
	Node      string  `json:"node"`
	Pid       int64   `json:"pid"`
	Pstart    int64   `json:"pstart"`
	Starttime int64   `json:"starttime"`
	Status    *string `json:"status,omitempty"`
	Type      string  `json:"type"`
	Upid      string  `json:"upid"`
	User      string  `json:"user"`
}

// newPveTaskLsCmd builds `pmx pdm pve task ls <remote>` — get the list of
// tasks either for a specific node, or query all at once (GET
// /pve/remotes/{remote}/tasks). The server returns tasks in its own
// (reverse-chronological) order; this is preserved rather than re-sorted,
// matching remote_task.go's and node_tasks.go's ls precedent — a task list
// is a log, not a set of named entities.
func newPveTaskLsCmd() *cobra.Command {
	var node string
	cmd := &cobra.Command{
		Use:   "ls <remote>",
		Short: "List background tasks on a PVE remote",
		Long: "Get the list of tasks either for a specific node, or query all at once " +
			"(GET /pve/remotes/{remote}/tasks).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]

			params := &pdmpve.ListRemotesTasksParams{}
			if cmd.Flags().Changed("node") {
				params.Node = &node
			}

			resp, err := deps.PDM.Pve.ListRemotesTasks(cmd.Context(), remote, params)
			if err != nil {
				return fmt.Errorf("list tasks on PVE remote %q: %w", remote, err)
			}

			entries, err := nodeDecodeArray[pveTaskEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode tasks on PVE remote %q: %w", remote, err)
			}

			headers := []string{"UPID", "TYPE", "USER", "STATUS", "STARTTIME", "ENDTIME"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					e.Upid, e.Type, e.User, strPtrString(e.Status),
					int64PtrString(&e.Starttime), int64PtrString(e.Endtime),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&node, "node", "", "only list tasks for this node (or 'localhost')")
	return cmd
}

// newPveTaskStatusCmd builds `pmx pdm pve task status <remote> <upid>` —
// get the status of a task from a Proxmox VE instance (GET
// /pve/remotes/{remote}/tasks/{upid}/status).
func newPveTaskStatusCmd() *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "status <remote> <upid>",
		Short: "Show the status of one task on a PVE remote",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, upid := args[0], args[1]

			params := &pdmpve.ListRemotesTasksStatusParams{}
			if cmd.Flags().Changed("wait") {
				params.Wait = &wait
			}

			resp, err := deps.PDM.Pve.ListRemotesTasksStatus(cmd.Context(), remote, upid, params)
			if err != nil {
				return fmt.Errorf("get status of task %q on PVE remote %q: %w", upid, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get status of task %q on PVE remote %q: empty response from server", upid, remote)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode status of task %q on PVE remote %q: %w", upid, remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "wait for the task to finish before returning its result")
	return cmd
}

// newPveTaskLogCmd builds `pmx pdm pve task log <remote> <upid>` — read a
// task log on a PVE remote (GET /pve/remotes/{remote}/tasks/{upid}/log).
//
// ListRemotesTasksLogResponse is byte-for-byte identical to
// ListRemotesTasksStatusResponse (Exitstatus/Id/Node/Pid/Pstart/Starttime/
// Status/Type/Upid/User — pve_gen.go:7156-7176 vs :7227-7247, v3.6.0), and
// pdm-apidoc.json's returns schema for this path is likewise byte-for-byte
// identical to the task status endpoint's (verified 2026-07-08) — a genuine
// upstream schema defect, not just a generator quirk: a real task log
// response is an array of {n, t} line objects (the same shape PBS's
// structurally identical ListRemotesTasksLogResponse = []json.RawMessage
// correctly declares, and nodeLogLine already decodes elsewhere in this
// package), not a single status object. Raw bypass recovers the actual
// array instead of silently decoding into the wrong struct shape.
func newPveTaskLogCmd() *cobra.Command {
	var (
		start, limit int64
		download     bool
	)
	cmd := &cobra.Command{
		Use:   "log <remote> <upid>",
		Short: "Read a task's log on a PVE remote",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, upid := args[0], args[1]
			fl := cmd.Flags()

			query := map[string]any{}
			if fl.Changed("start") {
				query["start"] = start
			}
			if fl.Changed("limit") {
				query["limit"] = limit
			}
			if fl.Changed("download") {
				query["download"] = download
			}

			path := pveRemotePath(remote, fmt.Sprintf("/tasks/%s/log", upid))

			resp, err := deps.PDM.Raw.GetRawCtx(cmd.Context(), path, query)
			if err != nil {
				return fmt.Errorf("read log of task %q on PVE remote %q: %w", upid, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("read log of task %q on PVE remote %q: empty response from server", upid, remote)
			}

			raw, err := json.Marshal(resp.Data)
			if err != nil {
				return fmt.Errorf("read log of task %q on PVE remote %q: re-marshal data: %w", upid, remote, err)
			}

			var items []json.RawMessage

			err = json.Unmarshal(raw, &items)
			if err != nil {
				return fmt.Errorf("decode log of task %q on PVE remote %q: %w", upid, remote, err)
			}

			lines, err := nodeDecodeArray[nodeLogLine](items)
			if err != nil {
				return fmt.Errorf("decode log of task %q on PVE remote %q: %w", upid, remote, err)
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

// newPveTaskStopCmd builds `pmx pdm pve task stop <remote> <upid>` — stop or
// cancel a task on a Proxmox VE instance (DELETE
// /pve/remotes/{remote}/tasks/{upid}). DeleteRemotesTasks discards its
// response body (`_ = resp; return nil`, pve_gen.go:7110-7127, v3.6.0), but
// unlike the apt-repositories/options/updates/etc. discards this is
// expected: a stop request has nothing meaningful to report beyond success,
// matching node_tasks.go's `node task stop` and pbs_proxy_task.go's `pbs
// task stop`.
func newPveTaskStopCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "stop <remote> <upid>",
		Short: "Stop a running task on a PVE remote",
		Long:  "Try to stop/cancel a running background task on a PVE remote. This is destructive: pass --yes/-y to confirm.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, upid := args[0], args[1]

			if !yes {
				return fmt.Errorf("refusing to stop task %q on PVE remote %q without confirmation: pass --yes/-y", upid, remote)
			}

			err := deps.PDM.Pve.DeleteRemotesTasks(cmd.Context(), remote, upid)
			if err != nil {
				return fmt.Errorf("stop task %q on PVE remote %q: %w", upid, remote, err)
			}

			res := output.Result{Message: fmt.Sprintf("Task %s on PVE remote %q stopped.", upid, remote)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
