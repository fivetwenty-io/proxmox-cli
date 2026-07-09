package pdm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	pdmremotes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/remotes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// validRemoteTaskStatuses are the task-status enum values accepted by
// --status on `remote task ls`/`remote task statistics` (GET
// /remotes/tasks/list, GET /remotes/tasks/statistics), per the PDM API schema.
var validRemoteTaskStatuses = []string{"ok", "warning", "error", "unknown"}

// newRemoteTaskCmd builds `pmx pdm remote task` — list and refresh the task
// cache PDM keeps for its managed remotes (/remotes/tasks).
func newRemoteTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "List and refresh cached remote tasks",
		Long: "List background tasks collected from every managed remote, refresh " +
			"the task cache, and read aggregate task statistics.",
	}
	cmd.AddCommand(newRemoteTaskLsCmd(), newRemoteTaskRefreshCmd(), newRemoteTaskStatisticsCmd())
	return cmd
}

// remoteTaskEntry is the decoded shape of one element of GET /remotes/tasks/list.
type remoteTaskEntry struct {
	Endtime   *int64  `json:"endtime,omitempty"`
	Node      string  `json:"node"`
	Pid       int64   `json:"pid"`
	Pstart    int64   `json:"pstart"`
	Starttime int64   `json:"starttime"`
	Status    *string `json:"status,omitempty"`
	Upid      string  `json:"upid"`
	User      string  `json:"user"`
	WorkerId  *string `json:"worker_id,omitempty"`
}

// remoteTaskListFlags collects the filter flags shared by `task ls` and
// `task statistics`. Every field maps directly onto a ListTasksListParams /
// ListTasksStatisticsParams field of the same name.
type remoteTaskListFlags struct {
	errorsOnly   bool
	limit        int64
	remote       string
	running      bool
	since        int64
	start        int64
	statusfilter []string
	typefilter   string
	until        int64
	userfilter   string
	view         string
}

// register binds the filter flags shared by `task ls` and `task statistics`.
func (tf *remoteTaskListFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.BoolVar(&tf.errorsOnly, "errors", false, "only list erroneous tasks")
	f.Int64Var(&tf.limit, "limit", 0, "only list this many tasks (0 means no limit)")
	f.StringVar(&tf.remote, "remote", "", "only list tasks from this remote")
	f.BoolVar(&tf.running, "running", false, "only list running tasks")
	f.Int64Var(&tf.since, "since", 0, "only list tasks since this Unix epoch")
	f.Int64Var(&tf.start, "start", 0, "list tasks starting from this offset")
	f.StringArrayVar(&tf.statusfilter, "status", nil, "only list tasks with any of these statuses (repeatable)")
	f.StringVar(&tf.typefilter, "type", "", "only list tasks whose type contains this substring")
	f.Int64Var(&tf.until, "until", 0, "only list tasks until this Unix epoch")
	f.StringVar(&tf.userfilter, "user", "", "only list tasks started by this user")
	f.StringVar(&tf.view, "view", "", "view name")
}

// validate reports an error naming cmd if any --status value is not one of
// validRemoteTaskStatuses.
func (tf *remoteTaskListFlags) validate(cmd string) error {
	for _, s := range tf.statusfilter {
		if !stringInSlice(s, validRemoteTaskStatuses) {
			return fmt.Errorf("%s: --status must be one of %s (got %q)",
				cmd, strings.Join(validRemoteTaskStatuses, ", "), s)
		}
	}
	return nil
}

// applyList builds the ListTasksListParams payload, forwarding optional
// flags only when set.
func (tf *remoteTaskListFlags) applyList(cmd *cobra.Command, p *pdmremotes.ListTasksListParams) {
	fl := cmd.Flags()
	if fl.Changed("errors") {
		p.Errors = boolPtr(tf.errorsOnly)
	}
	if fl.Changed("limit") {
		p.Limit = int64Ptr(tf.limit)
	}
	if fl.Changed("remote") {
		p.Remote = strPtr(tf.remote)
	}
	if fl.Changed("running") {
		p.Running = boolPtr(tf.running)
	}
	if fl.Changed("since") {
		p.Since = int64Ptr(tf.since)
	}
	if fl.Changed("start") {
		p.Start = int64Ptr(tf.start)
	}
	if fl.Changed("status") {
		p.Statusfilter = tf.statusfilter
	}
	if fl.Changed("type") {
		p.Typefilter = strPtr(tf.typefilter)
	}
	if fl.Changed("until") {
		p.Until = int64Ptr(tf.until)
	}
	if fl.Changed("user") {
		p.Userfilter = strPtr(tf.userfilter)
	}
	if fl.Changed("view") {
		p.View = strPtr(tf.view)
	}
}

// applyStatistics builds the ListTasksStatisticsParams payload, forwarding
// optional flags only when set.
func (tf *remoteTaskListFlags) applyStatistics(cmd *cobra.Command, p *pdmremotes.ListTasksStatisticsParams) {
	fl := cmd.Flags()
	if fl.Changed("errors") {
		p.Errors = boolPtr(tf.errorsOnly)
	}
	if fl.Changed("limit") {
		p.Limit = int64Ptr(tf.limit)
	}
	if fl.Changed("remote") {
		p.Remote = strPtr(tf.remote)
	}
	if fl.Changed("running") {
		p.Running = boolPtr(tf.running)
	}
	if fl.Changed("since") {
		p.Since = int64Ptr(tf.since)
	}
	if fl.Changed("start") {
		p.Start = int64Ptr(tf.start)
	}
	if fl.Changed("status") {
		p.Statusfilter = tf.statusfilter
	}
	if fl.Changed("type") {
		p.Typefilter = strPtr(tf.typefilter)
	}
	if fl.Changed("until") {
		p.Until = int64Ptr(tf.until)
	}
	if fl.Changed("user") {
		p.Userfilter = strPtr(tf.userfilter)
	}
	if fl.Changed("view") {
		p.View = strPtr(tf.view)
	}
}

// newRemoteTaskLsCmd builds `pmx pdm remote task ls` — list cached tasks
// for all managed remotes (GET /remotes/tasks/list).
func newRemoteTaskLsCmd() *cobra.Command {
	var tf remoteTaskListFlags
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List cached remote tasks",
		Long:  "List background tasks collected from every managed remote (GET /remotes/tasks/list).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if err := tf.validate("list remote tasks"); err != nil {
				return err
			}

			params := &pdmremotes.ListTasksListParams{}
			tf.applyList(cmd, params)

			resp, err := deps.PDM.Remotes.ListTasksList(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list remote tasks: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]remoteTaskEntry, 0, len(items))

			for _, raw := range items {
				var e remoteTaskEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode remote task entry: %w", err)
				}

				entries = append(entries, e)
			}

			headers := []string{"UPID", "NODE", "USER", "STATUS", "STARTTIME", "ENDTIME", "WORKER-ID"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Upid, e.Node, e.User, strPtrString(e.Status),
					int64PtrString(&e.Starttime), int64PtrString(e.Endtime), strPtrString(e.WorkerId),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	tf.register(cmd)
	return cmd
}

// newRemoteTaskRefreshCmd builds `pmx pdm remote task refresh` — refresh
// the task cache PDM keeps for its managed remotes (POST
// /remotes/tasks/refresh).
func newRemoteTaskRefreshCmd() *cobra.Command {
	var remotes []string
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Refresh the cached remote task list",
		Long: "Refresh the task cache PDM keeps for its managed remotes (POST " +
			"/remotes/tasks/refresh). Without --remote, every remote the caller has " +
			"permission for is refreshed; with one or more --remote flags, only those " +
			"remotes are refreshed. This is an asynchronous task: by default the " +
			"command blocks until it completes; pass --async (persistent flag) to " +
			"return the UPID immediately instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmremotes.CreateTasksRefreshParams{}
			if cmd.Flags().Changed("remote") {
				params.Remotes = remotes
			}

			resp, err := deps.PDM.Remotes.CreateTasksRefresh(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("refresh remote tasks: %w", err)
			}
			if resp == nil {
				return fmt.Errorf("refresh remote tasks: empty response from server")
			}

			return finishAsync(cmd, deps, *resp, "Remote task cache refreshed.")
		},
	}
	cmd.Flags().StringArrayVar(&remotes, "remote", nil, "only refresh this remote's tasks (repeatable)")
	return cmd
}

// newRemoteTaskStatisticsCmd builds `pmx pdm remote task statistics` —
// show task counts by remote and by worker type (GET /remotes/tasks/statistics).
func newRemoteTaskStatisticsCmd() *cobra.Command {
	var tf remoteTaskListFlags
	cmd := &cobra.Command{
		Use:   "statistics",
		Short: "Show aggregate remote task statistics",
		Long:  "Show task counts by remote and by worker type for the given filters (GET /remotes/tasks/statistics).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if err := tf.validate("remote task statistics"); err != nil {
				return err
			}

			params := &pdmremotes.ListTasksStatisticsParams{}
			tf.applyStatistics(cmd, params)

			resp, err := deps.PDM.Remotes.ListTasksStatistics(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("get remote task statistics: %w", err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode remote task statistics: %w", err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	tf.register(cmd)
	return cmd
}

// newRemoteUpdatesCmd builds `pmx pdm remote updates` — show and refresh
// the available-package update summary PDM caches for its managed remotes
// (/remotes/updates).
func newRemoteUpdatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "updates",
		Short: "Show and refresh the available-package summary for managed remotes",
		Long: "Show the available-package update summary PDM has cached for its " +
			"managed remotes, and trigger a refresh.",
	}
	cmd.AddCommand(newRemoteUpdatesSummaryCmd(), newRemoteUpdatesRefreshCmd())
	return cmd
}

// newRemoteUpdatesSummaryCmd builds `pmx pdm remote updates summary` — show
// the cached update summary for every managed remote (GET /remotes/updates/summary).
func newRemoteUpdatesSummaryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Show the cached update summary for every managed remote",
		Long: "Show the cached available-package update summary for every managed " +
			"remote (GET /remotes/updates/summary).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Remotes.ListUpdatesSummary(cmd.Context())
			if err != nil {
				return fmt.Errorf("get remote updates summary: %w", err)
			}
			if resp == nil {
				return fmt.Errorf("get remote updates summary: empty response from server")
			}

			var byRemote map[string]json.RawMessage
			if len(resp.Remotes) > 0 {
				err := json.Unmarshal(resp.Remotes, &byRemote)
				if err != nil {
					return fmt.Errorf("decode remote updates summary: %w", err)
				}
			}

			names := make([]string, 0, len(byRemote))
			for name := range byRemote {
				names = append(names, name)
			}
			sort.Strings(names)

			headers := []string{"REMOTE", "SUMMARY"}
			rows := make([][]string, 0, len(names))
			raw := make(map[string]any, len(names))

			for _, name := range names {
				rows = append(rows, []string{name, string(byRemote[name])})

				var v any
				if err := json.Unmarshal(byRemote[name], &v); err == nil {
					raw[name] = v
				}
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newRemoteUpdatesRefreshCmd builds `pmx pdm remote updates refresh` —
// refresh the update summary for every managed remote (POST /remotes/updates/refresh).
func newRemoteUpdatesRefreshCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Refresh the update summary for every managed remote",
		Long: "Refresh the available-package update summary PDM caches for every " +
			"managed remote (POST /remotes/updates/refresh). This is an asynchronous " +
			"task: by default the command blocks until it completes; pass --async " +
			"(persistent flag) to return the UPID immediately instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Remotes.CreateUpdatesRefresh(cmd.Context())
			if err != nil {
				return fmt.Errorf("refresh remote updates: %w", err)
			}
			if resp == nil {
				return fmt.Errorf("refresh remote updates: empty response from server")
			}

			return finishAsync(cmd, deps, *resp, "Remote update summary refreshed.")
		},
	}
	return cmd
}

// remoteMetricStatusEntry is the decoded shape of one element of
// GET /remotes/metric-collection/status.
type remoteMetricStatusEntry struct {
	Error          *string `json:"error,omitempty"`
	LastCollection *int64  `json:"last-collection,omitempty"`
	Remote         string  `json:"remote"`
}

// newRemoteMetricCollectionCmd builds `pmx pdm remote metric-collection` —
// show and trigger metric collection for managed remotes (/remotes/metric-collection).
func newRemoteMetricCollectionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "metric-collection",
		Short: "Show and trigger remote metric collection",
		Long:  "Show per-remote metric-collection status, and trigger an immediate collection.",
	}
	cmd.AddCommand(newRemoteMetricCollectionStatusCmd(), newRemoteMetricCollectionTriggerCmd())
	return cmd
}

// newRemoteMetricCollectionStatusCmd builds `pmx pdm remote
// metric-collection status` — show the last collection outcome for every
// managed remote (GET /remotes/metric-collection/status).
func newRemoteMetricCollectionStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show metric-collection status for every managed remote",
		Long: "Show the last metric-collection outcome for every managed remote " +
			"(GET /remotes/metric-collection/status).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Remotes.ListMetricCollectionStatus(cmd.Context())
			if err != nil {
				return fmt.Errorf("get remote metric-collection status: %w", err)
			}

			items := rawItemsOf(resp)
			type remoteMetricStatusRow struct {
				entry remoteMetricStatusEntry
				raw   map[string]any
			}
			table := make([]remoteMetricStatusRow, 0, len(items))

			for _, raw := range items {
				var e remoteMetricStatusEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode remote metric-collection status entry: %w", err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode remote metric-collection status entry: %w", err)
				}

				table = append(table, remoteMetricStatusRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Remote < table[j].entry.Remote })

			headers := []string{"REMOTE", "LAST-COLLECTION", "ERROR"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{e.Remote, int64PtrString(e.LastCollection), strPtrString(e.Error)})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newRemoteMetricCollectionTriggerCmd builds `pmx pdm remote
// metric-collection trigger` — trigger metric collection for one remote, or
// every managed remote when --remote is omitted (POST
// /remotes/metric-collection/trigger).
func newRemoteMetricCollectionTriggerCmd() *cobra.Command {
	var remote string
	cmd := &cobra.Command{
		Use:   "trigger",
		Short: "Trigger metric collection",
		Long: "Trigger metric collection for one remote, or for every managed " +
			"remote when --remote is omitted (POST /remotes/metric-collection/trigger).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmremotes.CreateMetricCollectionTriggerParams{}
			if cmd.Flags().Changed("remote") {
				params.Remote = strPtr(remote)
			}

			err := deps.PDM.Remotes.CreateMetricCollectionTrigger(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("trigger metric collection: %w", err)
			}

			msg := "Metric collection triggered for every managed remote."
			if remote != "" {
				msg = fmt.Sprintf("Metric collection triggered for remote %q.", remote)
			}

			res := output.Result{Message: msg}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "only trigger collection for this remote")
	return cmd
}
