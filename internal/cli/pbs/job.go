package pbs

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
	pbstape "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/tape"
)

// tapeJobNotificationModes are the notification-mode enum values PBS accepts
// for a tape backup job.
var tapeJobNotificationModes = []string{"legacy-sendmail", "notification-system"}

// newTapeJobCmd builds `pve pbs tape job` — create, inspect, update, delete,
// manually trigger, and report on the run status of scheduled tape backup
// job configurations (GET/POST/PUT/DELETE /config/tape-backup-job, POST
// /tape/backup/{id}, GET /tape/backup).
func newTapeJobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Manage scheduled tape backup job configurations",
		Long: "Create, inspect, update, delete, manually trigger, and report on the " +
			"run status of scheduled tape backup job configurations " +
			"(GET/POST/PUT/DELETE /config/tape-backup-job, POST /tape/backup/{id}, " +
			"GET /tape/backup).",
	}
	cmd.AddCommand(
		newTapeJobLsCmd(),
		newTapeJobShowCmd(),
		newTapeJobAddCmd(),
		newTapeJobUpdateCmd(),
		newTapeJobDeleteCmd(),
		newTapeJobRunCmd(),
		newTapeJobStatusCmd(),
	)
	return cmd
}

// tapeJobConfigEntry is the decoded shape of one element of GET
// /config/tape-backup-job: a tape backup job configuration with no run-status
// fields (that richer status view lives at `tape job status`, GET
// /tape/backup).
type tapeJobConfigEntry struct {
	Comment          *string      `json:"comment,omitempty"`
	Drive            string       `json:"drive"`
	EjectMedia       *pve.PVEBool `json:"eject-media,omitempty"`
	ExportMediaSet   *pve.PVEBool `json:"export-media-set,omitempty"`
	GroupFilter      []string     `json:"group-filter,omitempty"`
	Id               string       `json:"id"`
	LatestOnly       *pve.PVEBool `json:"latest-only,omitempty"`
	MaxDepth         *int64       `json:"max-depth,omitempty"`
	NotificationMode *string      `json:"notification-mode,omitempty"`
	NotifyUser       *string      `json:"notify-user,omitempty"`
	Ns               *string      `json:"ns,omitempty"`
	Pool             string       `json:"pool"`
	Schedule         *string      `json:"schedule,omitempty"`
	Store            string       `json:"store"`
	WorkerThreads    *int64       `json:"worker-threads,omitempty"`
}

// decodeTapeJobConfigEntries decodes a Config.ListTapeBackupJob response into
// typed entries, skipping any element that fails to decode.
func decodeTapeJobConfigEntries(resp *pbsconfig.ListTapeBackupJobResponse) []tapeJobConfigEntry {
	entries := make([]tapeJobConfigEntry, 0)
	if resp == nil {
		return entries
	}

	for _, raw := range *resp {
		var e tapeJobConfigEntry

		err := json.Unmarshal(raw, &e)
		if err != nil {
			continue
		}

		entries = append(entries, e)
	}

	return entries
}

// newTapeJobLsCmd builds `pve pbs tape job ls` — list every tape backup job
// configuration, with no run-status fields (GET /config/tape-backup-job).
func newTapeJobLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List tape backup job configurations",
		Long: "List every tape backup job configuration visible to the caller (GET " +
			"/config/tape-backup-job). This is the plain configuration listing; use " +
			"`pve pbs tape job status` for run-status fields.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListTapeBackupJob(cmd.Context())
			if err != nil {
				return fmt.Errorf("list tape backup jobs: %w", err)
			}

			entries := decodeTapeJobConfigEntries(resp)
			sort.Slice(entries, func(i, j int) bool { return entries[i].Id < entries[j].Id })

			headers := []string{"ID", "STORE", "POOL", "DRIVE", "SCHEDULE", "NS", "COMMENT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Id, e.Store, e.Pool, e.Drive, pbsFormatOptionalString(e.Schedule),
					pbsFormatOptionalString(e.Ns), pbsFormatOptionalString(e.Comment),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newTapeJobShowCmd builds `pve pbs tape job show <id>` — show one tape
// backup job's full configuration (GET /config/tape-backup-job/{id}).
func newTapeJobShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one tape backup job's configuration",
		Long: "Show every populated field of a single tape backup job configuration " +
			"(GET /config/tape-backup-job/{id}).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}

			resp, err := deps.PBS.Config.GetTapeBackupJob(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("show tape backup job %q: %w", id, err)
			}

			if resp == nil {
				return fmt.Errorf("show tape backup job %q: nil response from PBS", id)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode tape backup job %q: %w", id, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// tapeJobFlags collects the tape-backup-job attribute flags shared by `job
// add` and `job update`. Every field maps directly onto a
// CreateTapeBackupJobParams / UpdateTapeBackupJobParams field of the same
// name.
type tapeJobFlags struct {
	// shared tunables (add + update)
	comment          string
	drive            string
	ejectMedia       bool
	exportMediaSet   bool
	groupFilter      []string
	latestOnly       bool
	maxDepth         int64
	notificationMode string
	notifyUser       string
	ns               string
	pool             string
	schedule         string
	store            string
	workerThreads    int64

	// update-only
	del    []string
	digest string
}

// registerCommon binds the attribute flags accepted by both `job add` and
// `job update`.
func (jf *tapeJobFlags) registerCommon(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&jf.comment, "comment", "", "comment")
	f.StringVar(&jf.drive, "drive", "", "drive identifier")
	f.BoolVar(&jf.ejectMedia, "eject-media", false, "eject media upon job completion")
	f.BoolVar(&jf.exportMediaSet, "export-media-set", false, "export media set upon job completion")
	f.StringArrayVar(&jf.groupFilter, "group-filter", nil,
		"group filter, e.g. 'type:vm' or 'group:vm/100' (repeatable)")
	f.BoolVar(&jf.latestOnly, "latest-only", false, "backup latest snapshots only")
	f.Int64Var(&jf.maxDepth, "max-depth", 0, "namespace recursion depth (0 = no recursion)")
	f.StringVar(&jf.notificationMode, "notification-mode", "",
		"notification mode: legacy-sendmail|notification-system")
	f.StringVar(&jf.notifyUser, "notify-user", "", "user ID to notify")
	f.StringVar(&jf.ns, "ns", "", "namespace")
	f.StringVar(&jf.pool, "pool", "", "media pool name")
	f.StringVar(&jf.schedule, "schedule", "", "calendar-event schedule, e.g. 'daily'")
	f.StringVar(&jf.store, "store", "", "datastore name")
	f.Int64Var(&jf.workerThreads, "worker-threads", 0, "number of worker threads for the tape backup job")
}

// registerAdd binds every flag `job add` accepts (the common set only; id,
// drive, pool, and store are supplied via the positional argument and
// required --drive/--pool/--store flags, validated in the RunE).
func (jf *tapeJobFlags) registerAdd(cmd *cobra.Command) {
	jf.registerCommon(cmd)
}

// registerUpdate binds every flag `job update` accepts, including the
// update-only delete/digest fields.
func (jf *tapeJobFlags) registerUpdate(cmd *cobra.Command) {
	jf.registerCommon(cmd)
	f := cmd.Flags()
	f.StringArrayVar(&jf.del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&jf.digest, "digest", "", "only update if the current config digest matches")
}

// validateNotificationMode reports an error if --notification-mode was set
// to a value outside the accepted enum.
func (jf *tapeJobFlags) validateNotificationMode(cmd *cobra.Command) error {
	if cmd.Flags().Changed("notification-mode") && !stringInSlice(jf.notificationMode, tapeJobNotificationModes) {
		return fmt.Errorf("--notification-mode must be one of legacy-sendmail, notification-system (got %q)",
			jf.notificationMode)
	}

	return nil
}

// applyAdd builds the create payload, forwarding optional flags only when set.
func (jf *tapeJobFlags) applyAdd(cmd *cobra.Command, p *pbsconfig.CreateTapeBackupJobParams) {
	fl := cmd.Flags()
	if fl.Changed("comment") {
		p.Comment = strPtr(jf.comment)
	}

	if fl.Changed("eject-media") {
		p.EjectMedia = boolPtr(jf.ejectMedia)
	}

	if fl.Changed("export-media-set") {
		p.ExportMediaSet = boolPtr(jf.exportMediaSet)
	}

	if fl.Changed("group-filter") {
		p.GroupFilter = jf.groupFilter
	}

	if fl.Changed("latest-only") {
		p.LatestOnly = boolPtr(jf.latestOnly)
	}

	if fl.Changed("max-depth") {
		p.MaxDepth = int64Ptr(jf.maxDepth)
	}

	if fl.Changed("notification-mode") {
		p.NotificationMode = strPtr(jf.notificationMode)
	}

	if fl.Changed("notify-user") {
		p.NotifyUser = strPtr(jf.notifyUser)
	}

	if fl.Changed("ns") {
		p.Ns = strPtr(jf.ns)
	}

	if fl.Changed("schedule") {
		p.Schedule = strPtr(jf.schedule)
	}

	if fl.Changed("worker-threads") {
		p.WorkerThreads = int64Ptr(jf.workerThreads)
	}
}

// applyUpdate builds the update payload, forwarding optional flags only when set.
func (jf *tapeJobFlags) applyUpdate(cmd *cobra.Command, p *pbsconfig.UpdateTapeBackupJobParams) {
	fl := cmd.Flags()
	if fl.Changed("comment") {
		p.Comment = strPtr(jf.comment)
	}

	if fl.Changed("drive") {
		p.Drive = strPtr(jf.drive)
	}

	if fl.Changed("eject-media") {
		p.EjectMedia = boolPtr(jf.ejectMedia)
	}

	if fl.Changed("export-media-set") {
		p.ExportMediaSet = boolPtr(jf.exportMediaSet)
	}

	if fl.Changed("group-filter") {
		p.GroupFilter = jf.groupFilter
	}

	if fl.Changed("latest-only") {
		p.LatestOnly = boolPtr(jf.latestOnly)
	}

	if fl.Changed("max-depth") {
		p.MaxDepth = int64Ptr(jf.maxDepth)
	}

	if fl.Changed("notification-mode") {
		p.NotificationMode = strPtr(jf.notificationMode)
	}

	if fl.Changed("notify-user") {
		p.NotifyUser = strPtr(jf.notifyUser)
	}

	if fl.Changed("ns") {
		p.Ns = strPtr(jf.ns)
	}

	if fl.Changed("pool") {
		p.Pool = strPtr(jf.pool)
	}

	if fl.Changed("schedule") {
		p.Schedule = strPtr(jf.schedule)
	}

	if fl.Changed("store") {
		p.Store = strPtr(jf.store)
	}

	if fl.Changed("worker-threads") {
		p.WorkerThreads = int64Ptr(jf.workerThreads)
	}

	if fl.Changed("delete") {
		p.Delete = jf.del
	}

	if fl.Changed("digest") {
		p.Digest = strPtr(jf.digest)
	}
}

// newTapeJobAddCmd builds `pve pbs tape job add <id>` — create a scheduled
// tape backup job configuration (POST /config/tape-backup-job). --drive,
// --pool, and --store are required; every other option is optional and only
// forwarded when explicitly set.
func newTapeJobAddCmd() *cobra.Command {
	var jf tapeJobFlags
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Create a scheduled tape backup job",
		Long: "Create a new tape backup job configuration (POST " +
			"/config/tape-backup-job). --drive, --pool, and --store are required; " +
			"every other option is optional and only forwarded when explicitly set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}

			if jf.drive == "" {
				return fmt.Errorf("--drive is required")
			}

			if jf.pool == "" {
				return fmt.Errorf("--pool is required")
			}

			if jf.store == "" {
				return fmt.Errorf("--store is required")
			}

			err := jf.validateNotificationMode(cmd)
			if err != nil {
				return err
			}

			params := &pbsconfig.CreateTapeBackupJobParams{
				Id:    id,
				Drive: jf.drive,
				Pool:  jf.pool,
				Store: jf.store,
			}
			jf.applyAdd(cmd, params)

			err = deps.PBS.Config.CreateTapeBackupJob(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create tape backup job %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Tape backup job %q created.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	jf.registerAdd(cmd)
	cli.MustMarkRequired(cmd, "drive")
	cli.MustMarkRequired(cmd, "pool")
	cli.MustMarkRequired(cmd, "store")
	return cmd
}

// newTapeJobUpdateCmd builds `pve pbs tape job update <id>` — update a
// scheduled tape backup job configuration (PUT
// /config/tape-backup-job/{id}). Only flags explicitly set are sent; use
// --delete to reset properties to their default.
func newTapeJobUpdateCmd() *cobra.Command {
	var jf tapeJobFlags
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a scheduled tape backup job",
		Long: "Update an existing tape backup job configuration (PUT " +
			"/config/tape-backup-job/{id}). Only flags explicitly set are sent; use " +
			"--delete to reset properties to their default instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update tape backup job %q: no changes given: pass at least one flag", id)
			}

			if cmd.Flags().Changed("delete") {
				for _, name := range jf.del {
					if name == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			err := jf.validateNotificationMode(cmd)
			if err != nil {
				return err
			}

			params := &pbsconfig.UpdateTapeBackupJobParams{}
			jf.applyUpdate(cmd, params)

			err = deps.PBS.Config.UpdateTapeBackupJob(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("update tape backup job %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Tape backup job %q updated.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	jf.registerUpdate(cmd)
	return cmd
}

// newTapeJobDeleteCmd builds `pve pbs tape job delete <id>` — remove a tape
// backup job configuration (DELETE /config/tape-backup-job/{id}).
func newTapeJobDeleteCmd() *cobra.Command {
	var digest string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a scheduled tape backup job",
		Long: "Remove a tape backup job configuration (DELETE /config/tape-backup-job/{id}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}
			if !yes {
				return fmt.Errorf("refusing to delete tape backup job %q without confirmation: pass --yes/-y", id)
			}

			params := &pbsconfig.DeleteTapeBackupJobParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Config.DeleteTapeBackupJob(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("delete tape backup job %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Tape backup job %q deleted.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newTapeJobRunCmd builds `pve pbs tape job run <id>` — manually trigger a
// scheduled tape backup job (POST /tape/backup/{id}).
//
// The generated Tape.CreateBackup2 binding is error-only: it discards the
// response body entirely. The PBS API documentation under-declares this
// endpoint's return type as null, but PBS in practice returns the run's
// UPID as a JSON string (the same situation as `sync job run`). To surface
// that UPID and support --async / task-wait semantics, this command bypasses
// the generated binding and issues the POST directly through the shared raw
// client (deps.PBS.Raw), which every generated service method is itself
// built on top of, so the request shape (path, auth, error handling) is
// identical.
//
// Because the documented return type really is null on some PBS versions,
// this command tolerates an empty or JSON-null response body by printing a
// plain success message instead of treating the absence of a UPID as an
// error.
func newTapeJobRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <id>",
		Short: "Manually trigger a scheduled tape backup job",
		Long: "Immediately run a configured tape backup job (POST /tape/backup/{id}). " +
			"Runs as an asynchronous task; the command blocks until it finishes unless " +
			"--async is set. If the server reports no UPID for this run (some PBS " +
			"versions document this endpoint as returning null), a plain success " +
			"message is printed instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}

			path := "/tape/backup/" + url.PathEscape(id)

			resp, err := deps.PBS.Raw.PostRawCtx(cmd.Context(), path, nil)
			if err != nil {
				return fmt.Errorf("run tape backup job %q: %w", id, err)
			}

			if resp == nil {
				return fmt.Errorf("run tape backup job %q: nil response from PBS", id)
			}

			finishedMsg := fmt.Sprintf("Tape backup job %q finished.", id)

			if resp.Data == nil {
				res := output.Result{Message: finishedMsg}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			raw, err := json.Marshal(resp.Data)
			if err != nil {
				return fmt.Errorf("run tape backup job %q: encode response: %w", id, err)
			}

			if len(raw) == 0 || string(raw) == "null" {
				res := output.Result{Message: finishedMsg}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			return finishAsync(cmd, deps, raw, finishedMsg)
		},
	}
	return cmd
}

// tapeJobStatusEntry is the decoded shape of one element of GET /tape/backup:
// a tape backup job configuration together with its most recent run status.
// Every field but id, drive, pool, and store is optional per the PBS API
// schema.
type tapeJobStatusEntry struct {
	Comment          *string  `json:"comment,omitempty"`
	Drive            string   `json:"drive"`
	EjectMedia       *bool    `json:"eject-media,omitempty"`
	ExportMediaSet   *bool    `json:"export-media-set,omitempty"`
	GroupFilter      []string `json:"group-filter,omitempty"`
	Id               string   `json:"id"`
	LastRunEndtime   *int64   `json:"last-run-endtime,omitempty"`
	LastRunState     *string  `json:"last-run-state,omitempty"`
	LastRunUpid      *string  `json:"last-run-upid,omitempty"`
	LatestOnly       *bool    `json:"latest-only,omitempty"`
	MaxDepth         *int64   `json:"max-depth,omitempty"`
	NextMediaLabel   *string  `json:"next-media-label,omitempty"`
	NextRun          *int64   `json:"next-run,omitempty"`
	NotificationMode *string  `json:"notification-mode,omitempty"`
	NotifyUser       *string  `json:"notify-user,omitempty"`
	Ns               *string  `json:"ns,omitempty"`
	Pool             string   `json:"pool"`
	Schedule         *string  `json:"schedule,omitempty"`
	Store            string   `json:"store"`
	WorkerThreads    *int64   `json:"worker-threads,omitempty"`
}

// decodeTapeJobStatusEntries decodes a Tape.ListBackup response into typed
// entries, skipping any element that fails to decode.
func decodeTapeJobStatusEntries(resp *pbstape.ListBackupResponse) []tapeJobStatusEntry {
	entries := make([]tapeJobStatusEntry, 0)
	if resp == nil {
		return entries
	}

	for _, raw := range *resp {
		var e tapeJobStatusEntry

		err := json.Unmarshal(raw, &e)
		if err != nil {
			continue
		}

		entries = append(entries, e)
	}

	return entries
}

// newTapeJobStatusCmd builds `pve pbs tape job status` — list every
// configured tape backup job together with its most recent run status (GET
// /tape/backup).
func newTapeJobStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "List tape backup jobs and their run status",
		Long: "List every configured tape backup job visible to the caller together " +
			"with its most recent run state (GET /tape/backup). This is the " +
			"status-rich view; use `pve pbs tape job ls` for the plain configuration " +
			"listing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Tape.ListBackup(cmd.Context())
			if err != nil {
				return fmt.Errorf("list tape backup job status: %w", err)
			}

			entries := decodeTapeJobStatusEntries(resp)
			sort.Slice(entries, func(i, j int) bool { return entries[i].Id < entries[j].Id })

			headers := []string{
				"ID", "STORE", "POOL", "DRIVE", "SCHEDULE", "LAST-RUN-STATE", "NEXT-RUN", "NEXT-MEDIA-LABEL",
			}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Id, e.Store, e.Pool, e.Drive, pbsFormatOptionalString(e.Schedule),
					pbsFormatOptionalString(e.LastRunState), pbsFormatOptionalInt64(e.NextRun),
					pbsFormatOptionalString(e.NextMediaLabel),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
