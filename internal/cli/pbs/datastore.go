package pbs

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	pbsadmin "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/admin"
	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/optionschema"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// validRrdTimeframes are the RRD time-frame enum values accepted by
// GET /admin/datastore/{store}/rrd, per the PBS API schema.
var validRrdTimeframes = []string{"hour", "day", "week", "month", "year", "decade"}

// validRrdConsolidations are the RRD consolidation-function enum values
// accepted by GET /admin/datastore/{store}/rrd, per the PBS API schema.
var validRrdConsolidations = []string{"MAX", "AVERAGE"}

// newDatastoreCmd builds `pmx pbs datastore` and its verbs: manage Proxmox
// Backup Server datastore configuration (config/admin/status namespaces).
func newDatastoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "datastore",
		Short: "Manage Proxmox Backup Server datastores",
		Long: "List, inspect, create, update, and delete Proxmox Backup Server datastore " +
			"configurations, and read their space usage, garbage-collection status, and " +
			"RRD statistics.",
	}
	cmd.AddCommand(
		newDatastoreLsCmd(),
		newDatastoreShowCmd(),
		newDatastoreCreateCmd(),
		newDatastoreUpdateCmd(),
		newDatastoreDeleteCmd(),
		newDatastoreStatusCmd(),
		newDatastoreUsageCmd(),
		newDatastoreRrdCmd(),
	)
	return cmd
}

// datastoreListEntry is the subset of a datastore configuration rendered in
// `ls` table output. Absent optional fields decode to their zero value ("").
type datastoreListEntry struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	Comment       string `json:"comment"`
	GcSchedule    string `json:"gc-schedule"`
	PruneSchedule string `json:"prune-schedule"`
}

// newDatastoreLsCmd builds `pmx pbs datastore ls` — list configured
// datastores (GET /config/datastore).
func newDatastoreLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List configured datastores",
		Long:  "List the datastores configured on this Proxmox Backup Server.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListDatastore(cmd.Context())
			if err != nil {
				return fmt.Errorf("list datastores: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]datastoreListEntry, 0, len(items))
			for _, raw := range items {
				var e datastoreListEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode datastore entry: %w", err)
				}
				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{e.Name, e.Path, e.Comment, e.GcSchedule, e.PruneSchedule})
			}

			res := output.Result{
				Headers: []string{"NAME", "PATH", "COMMENT", "GC-SCHEDULE", "PRUNE-SCHEDULE"},
				Rows:    rows,
				Raw:     decodeRawList(items),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newDatastoreShowCmd builds `pmx pbs datastore show <name>` — show a single
// datastore configuration (GET /config/datastore/{name}).
func newDatastoreShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a single datastore configuration",
		Long: "Show every populated field of a single Proxmox Backup Server datastore " +
			"configuration. The PBS API omits options left at their built-in defaults; " +
			"pass --defaults to also list those, with the value they effectively have.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			resp, err := deps.PBS.Config.GetDatastore(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("get datastore %q: %w", name, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode datastore %q: %w", name, err)
			}

			single := stringMap(fields)
			var raw any = fields
			if withDefaults {
				single, raw = optionschema.MergeDefaults(datastoreOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// datastoreFlags collects the datastore attribute flags shared by create and
// update. Every field maps directly onto a CreateDatastoreParams /
// UpdateDatastoreParams field of the same name.
type datastoreFlags struct {
	// shared tunables (create + update)
	comment                string
	counterResetSchedule   string
	gcOnUnmount            bool
	gcSchedule             string
	keepDaily              int64
	keepHourly             int64
	keepLast               int64
	keepMonthly            int64
	keepWeekly             int64
	keepYearly             int64
	maintenanceMode        string
	notificationMode       string
	notificationThresholds string
	notify                 string
	notifyUser             string
	pruneSchedule          string
	tuning                 string
	verifyNew              bool

	// create-only
	backend        string
	backingDevice  string
	overwriteInUse bool
	reuseDatastore bool

	// update-only
	del    []string
	digest string
}

// registerCommon binds the attribute flags accepted by both create and update.
func (df *datastoreFlags) registerCommon(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&df.comment, "comment", "", "comment")
	f.StringVar(&df.counterResetSchedule, "counter-reset-schedule", "",
		"reset notification threshold counters at this schedule")
	f.BoolVar(&df.gcOnUnmount, "gc-on-unmount", false,
		"run garbage collection before unmounting a removable datastore")
	f.StringVar(&df.gcSchedule, "gc-schedule", "", "run garbage collection at this schedule")
	f.Int64Var(&df.keepDaily, "keep-daily", 0, "number of daily backups to keep")
	f.Int64Var(&df.keepHourly, "keep-hourly", 0, "number of hourly backups to keep")
	f.Int64Var(&df.keepLast, "keep-last", 0, "number of backups to keep")
	f.Int64Var(&df.keepMonthly, "keep-monthly", 0, "number of monthly backups to keep")
	f.Int64Var(&df.keepWeekly, "keep-weekly", 0, "number of weekly backups to keep")
	f.Int64Var(&df.keepYearly, "keep-yearly", 0, "number of yearly backups to keep")
	f.StringVar(&df.maintenanceMode, "maintenance-mode", "", "maintenance mode: offline or read-only")
	f.StringVar(&df.notificationMode, "notification-mode", "",
		"notification delivery mode: legacy-sendmail or notification-system")
	f.StringVar(&df.notificationThresholds, "notification-thresholds", "", "threshold values for notifications")
	f.StringVar(&df.notify, "notify", "", "notification setting: always, never, or error")
	f.StringVar(&df.notifyUser, "notify-user", "", "user ID to notify")
	f.StringVar(&df.pruneSchedule, "prune-schedule", "", "run the prune job at this schedule")
	f.StringVar(&df.tuning, "tuning", "", "datastore tuning options")
	f.BoolVar(&df.verifyNew, "verify-new", false, "verify all new backups right after completion")
}

// registerCreate binds every flag CreateDatastoreParams accepts, including
// the create-only identity fields.
func (df *datastoreFlags) registerCreate(cmd *cobra.Command) {
	df.registerCommon(cmd)
	f := cmd.Flags()
	f.StringVar(&df.backend, "backend", "", "datastore backend config (property string)")
	f.StringVar(&df.backingDevice, "backing-device", "",
		"UUID of the filesystem partition for removable datastores")
	f.BoolVar(&df.overwriteInUse, "overwrite-in-use", false, "overwrite the in-use marker (S3-backed datastores only)")
	f.BoolVar(&df.reuseDatastore, "reuse-datastore", false, "re-use an existing datastore directory")
}

// registerUpdate binds every flag UpdateDatastoreParams accepts, including
// the update-only delete/digest fields.
func (df *datastoreFlags) registerUpdate(cmd *cobra.Command) {
	df.registerCommon(cmd)
	f := cmd.Flags()
	f.StringArrayVar(&df.del, "delete", nil, "config key to reset to its default (repeatable)")
	f.StringVar(&df.digest, "digest", "", "prevent changes if the config digest differs")
}

// applyCreate builds the create payload, forwarding optional flags only when set.
func (df *datastoreFlags) applyCreate(cmd *cobra.Command, p *pbsconfig.CreateDatastoreParams) {
	fl := cmd.Flags()
	if fl.Changed("comment") {
		p.Comment = &df.comment
	}
	if fl.Changed("counter-reset-schedule") {
		p.CounterResetSchedule = &df.counterResetSchedule
	}
	if fl.Changed("gc-on-unmount") {
		p.GcOnUnmount = &df.gcOnUnmount
	}
	if fl.Changed("gc-schedule") {
		p.GcSchedule = &df.gcSchedule
	}
	if fl.Changed("keep-daily") {
		p.KeepDaily = &df.keepDaily
	}
	if fl.Changed("keep-hourly") {
		p.KeepHourly = &df.keepHourly
	}
	if fl.Changed("keep-last") {
		p.KeepLast = &df.keepLast
	}
	if fl.Changed("keep-monthly") {
		p.KeepMonthly = &df.keepMonthly
	}
	if fl.Changed("keep-weekly") {
		p.KeepWeekly = &df.keepWeekly
	}
	if fl.Changed("keep-yearly") {
		p.KeepYearly = &df.keepYearly
	}
	if fl.Changed("maintenance-mode") {
		p.MaintenanceMode = &df.maintenanceMode
	}
	if fl.Changed("notification-mode") {
		p.NotificationMode = &df.notificationMode
	}
	if fl.Changed("notification-thresholds") {
		p.NotificationThresholds = &df.notificationThresholds
	}
	if fl.Changed("notify") {
		p.Notify = &df.notify
	}
	if fl.Changed("notify-user") {
		p.NotifyUser = &df.notifyUser
	}
	if fl.Changed("prune-schedule") {
		p.PruneSchedule = &df.pruneSchedule
	}
	if fl.Changed("tuning") {
		p.Tuning = &df.tuning
	}
	if fl.Changed("verify-new") {
		p.VerifyNew = &df.verifyNew
	}
	if fl.Changed("backend") {
		p.Backend = &df.backend
	}
	if fl.Changed("backing-device") {
		p.BackingDevice = &df.backingDevice
	}
	if fl.Changed("overwrite-in-use") {
		p.OverwriteInUse = &df.overwriteInUse
	}
	if fl.Changed("reuse-datastore") {
		p.ReuseDatastore = &df.reuseDatastore
	}
}

// applyUpdate builds the update payload, forwarding optional flags only when set.
func (df *datastoreFlags) applyUpdate(cmd *cobra.Command, p *pbsconfig.UpdateDatastoreParams) {
	fl := cmd.Flags()
	if fl.Changed("comment") {
		p.Comment = &df.comment
	}
	if fl.Changed("counter-reset-schedule") {
		p.CounterResetSchedule = &df.counterResetSchedule
	}
	if fl.Changed("gc-on-unmount") {
		p.GcOnUnmount = &df.gcOnUnmount
	}
	if fl.Changed("gc-schedule") {
		p.GcSchedule = &df.gcSchedule
	}
	if fl.Changed("keep-daily") {
		p.KeepDaily = &df.keepDaily
	}
	if fl.Changed("keep-hourly") {
		p.KeepHourly = &df.keepHourly
	}
	if fl.Changed("keep-last") {
		p.KeepLast = &df.keepLast
	}
	if fl.Changed("keep-monthly") {
		p.KeepMonthly = &df.keepMonthly
	}
	if fl.Changed("keep-weekly") {
		p.KeepWeekly = &df.keepWeekly
	}
	if fl.Changed("keep-yearly") {
		p.KeepYearly = &df.keepYearly
	}
	if fl.Changed("maintenance-mode") {
		p.MaintenanceMode = &df.maintenanceMode
	}
	if fl.Changed("notification-mode") {
		p.NotificationMode = &df.notificationMode
	}
	if fl.Changed("notification-thresholds") {
		p.NotificationThresholds = &df.notificationThresholds
	}
	if fl.Changed("notify") {
		p.Notify = &df.notify
	}
	if fl.Changed("notify-user") {
		p.NotifyUser = &df.notifyUser
	}
	if fl.Changed("prune-schedule") {
		p.PruneSchedule = &df.pruneSchedule
	}
	if fl.Changed("tuning") {
		p.Tuning = &df.tuning
	}
	if fl.Changed("verify-new") {
		p.VerifyNew = &df.verifyNew
	}
	if fl.Changed("delete") {
		p.Delete = df.del
	}
	if fl.Changed("digest") {
		p.Digest = &df.digest
	}
}

// newDatastoreCreateCmd builds `pmx pbs datastore create <name>` — create a
// datastore configuration (POST /config/datastore).
func newDatastoreCreateCmd() *cobra.Command {
	var (
		path string
		df   datastoreFlags
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a datastore configuration",
		Long: "Create a new Proxmox Backup Server datastore configuration. --path is required: " +
			"either the absolute path to the datastore directory, or a relative on-device path " +
			"for removable datastores.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			if path == "" {
				return fmt.Errorf("create datastore %q: --path is required", name)
			}

			params := &pbsconfig.CreateDatastoreParams{Name: name, Path: path}
			df.applyCreate(cmd, params)

			if err := deps.PBS.Config.CreateDatastore(cmd.Context(), params); err != nil {
				return fmt.Errorf("create datastore %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Datastore %q created.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "datastore directory path (required)")
	df.registerCreate(cmd)
	return cmd
}

// newDatastoreUpdateCmd builds `pmx pbs datastore update <name>` — update a
// datastore configuration (PUT /config/datastore/{name}).
func newDatastoreUpdateCmd() *cobra.Command {
	var df datastoreFlags
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update a datastore configuration",
		Long:  "Update settings on an existing Proxmox Backup Server datastore configuration.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update datastore %q: no changes requested: pass at least one flag", name)
			}

			params := &pbsconfig.UpdateDatastoreParams{}
			df.applyUpdate(cmd, params)

			if err := deps.PBS.Config.UpdateDatastore(cmd.Context(), name, params); err != nil {
				return fmt.Errorf("update datastore %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Datastore %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	df.registerUpdate(cmd)
	return cmd
}

// newDatastoreDeleteCmd builds `pmx pbs datastore delete <name>` — remove a
// datastore configuration and optionally its underlying contents
// (DELETE /config/datastore/{name}).
func newDatastoreDeleteCmd() *cobra.Command {
	var (
		destroyData    bool
		keepJobConfigs bool
		digest         string
		yes            bool
	)
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a datastore configuration",
		Long: "Remove a datastore configuration. Pass --destroy-data to also delete the " +
			"datastore's underlying contents; without it, only the configuration entry is removed " +
			"and the on-disk data is left in place. This is an asynchronous task: by default the " +
			"command blocks until it completes; pass --async (persistent flag) to return the UPID " +
			"immediately instead. This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			fl := cmd.Flags()

			if !yes {
				return fmt.Errorf("refusing to delete datastore %q without confirmation: pass --yes/-y", name)
			}

			params := &pbsconfig.DeleteDatastoreParams{}
			if fl.Changed("destroy-data") {
				params.DestroyData = &destroyData
			}
			if fl.Changed("keep-job-configs") {
				params.KeepJobConfigs = &keepJobConfigs
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}

			resp, err := deps.PBS.Config.DeleteDatastore(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("delete datastore %q: %w", name, err)
			}
			if resp == nil {
				return fmt.Errorf("delete datastore %q: empty response from server", name)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Datastore %q deleted.", name))
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&destroyData, "destroy-data", false, "also delete the datastore's underlying contents")
	fl.BoolVar(&keepJobConfigs, "keep-job-configs", false, "keep job configurations related to this datastore")
	fl.StringVar(&digest, "digest", "", "prevent changes if the config digest differs")
	fl.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newDatastoreStatusCmd builds `pmx pbs datastore status <name>` — show
// space usage and garbage-collection status (GET /admin/datastore/{store}/status).
func newDatastoreStatusCmd() *cobra.Command {
	var verbose bool
	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show datastore space usage and garbage-collection status",
		Long: "Show total/used/available space for a datastore. Pass --verbose to also include " +
			"snapshot counts and garbage-collection status.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			params := &pbsadmin.ListDatastoreStatusParams{}
			if cmd.Flags().Changed("verbose") {
				params.Verbose = &verbose
			}

			resp, err := deps.PBS.Admin.ListDatastoreStatus(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("get datastore status %q: %w", name, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode datastore status %q: %w", name, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&verbose, "verbose", false, "include snapshot counts and garbage-collection status")
	return cmd
}

// datastoreUsageEntry is one element of the GET /status/datastore-usage
// response. Fields that PBS marks optional in its API schema (all but store,
// backend-type, and mount-status) are pointers so an absent value renders as
// an empty table cell instead of a misleading zero.
type datastoreUsageEntry struct {
	Store             string  `json:"store"`
	BackendType       string  `json:"backend-type"`
	MountStatus       string  `json:"mount-status"`
	Total             *int64  `json:"total,omitempty"`
	Used              *int64  `json:"used,omitempty"`
	Avail             *int64  `json:"avail,omitempty"`
	Error             *string `json:"error,omitempty"`
	EstimatedFullDate *int64  `json:"estimated-full-date,omitempty"`
}

// newDatastoreUsageCmd builds `pmx pbs datastore usage` — list space usage
// and full-date estimates for every datastore (GET /status/datastore-usage).
func newDatastoreUsageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "List datastore space usage and full-date estimates",
		Long:  "List usage totals, availability, and estimated-full dates for every datastore.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Status.ListDatastoreUsage(cmd.Context())
			if err != nil {
				return fmt.Errorf("list datastore usage: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]datastoreUsageEntry, 0, len(items))
			for _, raw := range items {
				var e datastoreUsageEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode datastore usage entry: %w", err)
				}
				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Store < entries[j].Store })

			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					e.Store,
					e.BackendType,
					e.MountStatus,
					int64PtrString(e.Total),
					int64PtrString(e.Used),
					int64PtrString(e.Avail),
					strPtrString(e.Error),
				})
			}

			res := output.Result{
				Headers: []string{"STORE", "BACKEND", "MOUNT", "TOTAL", "USED", "AVAIL", "ERROR"},
				Rows:    rows,
				Raw:     decodeRawList(items),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newDatastoreRrdCmd builds `pmx pbs datastore rrd <name>` — read RRD usage
// statistics for a datastore (GET /admin/datastore/{store}/rrd).
//
// The generated Admin.ListDatastoreRrd binding discards the response body:
// the PBS API schema declares this endpoint's return type as untyped/dynamic
// (it varies with the requested time frame), so the code generator that
// produced pkg/pbs/admin could not model a response type and emits a method
// that returns only error. To surface the actual statistics this command
// bypasses that binding and issues the GET directly through the shared raw
// client (deps.PBS.Raw), which every generated service method — including
// ListDatastoreRrd — is itself built on top of, so the request shape (path,
// query encoding, auth, error handling) is identical either way.
func newDatastoreRrdCmd() *cobra.Command {
	var (
		timeframe string
		cf        string
	)
	cmd := &cobra.Command{
		Use:   "rrd <name>",
		Short: "Show RRD usage statistics for a datastore",
		Long: "Read RRD (round-robin database) usage statistics for a datastore over the given " +
			"time frame and consolidation function. The response shape is dynamic (PBS does not " +
			"publish a fixed schema for it), so it is rendered as raw JSON.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			if !stringInSlice(timeframe, validRrdTimeframes) {
				return fmt.Errorf("--timeframe must be one of %s (got %q)",
					strings.Join(validRrdTimeframes, ", "), timeframe)
			}
			if !stringInSlice(cf, validRrdConsolidations) {
				return fmt.Errorf("--cf must be one of %s (got %q)",
					strings.Join(validRrdConsolidations, ", "), cf)
			}

			path := fmt.Sprintf("/admin/datastore/%s/rrd", url.PathEscape(name))
			params := map[string]interface{}{"cf": cf, "timeframe": timeframe}

			resp, err := deps.PBS.Raw.GetRawCtx(cmd.Context(), path, params)
			if err != nil {
				return fmt.Errorf("get datastore rrd %q: %w", name, err)
			}
			if resp == nil {
				return fmt.Errorf("get datastore rrd %q: empty response from server", name)
			}

			res := output.Result{
				Message: fmt.Sprintf("RRD stats for datastore %q (timeframe=%s, cf=%s).", name, timeframe, cf),
				Raw:     resp.Data,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&timeframe, "timeframe", "", "RRD time frame: hour|day|week|month|year|decade (required)")
	fl.StringVar(&cf, "cf", "AVERAGE", "RRD consolidation function: MAX|AVERAGE")
	cli.MustMarkRequired(cmd, "timeframe")
	return cmd
}

// --- helpers -----------------------------------------------------------------

// anyFlagChanged reports whether at least one flag on fl was explicitly set.
func anyFlagChanged(fl *pflag.FlagSet) bool {
	changed := false
	fl.Visit(func(*pflag.Flag) { changed = true })
	return changed
}

// stringInSlice reports whether v equals one of allowed.
func stringInSlice(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// rawItemsOf dereferences a *[]json.RawMessage-shaped response type, returning
// an empty (nil) slice for a nil response instead of panicking.
func rawItemsOf[T ~[]json.RawMessage](resp *T) []json.RawMessage {
	if resp == nil {
		return nil
	}
	return []json.RawMessage(*resp)
}

// decodeRawList decodes each element of items into a generic map, preserving
// every field the API returned (unlike a typed struct, which only captures
// fields it declares). Elements that fail to decode as an object are skipped
// rather than aborting the whole list.
func decodeRawList(items []json.RawMessage) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, raw := range items {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err == nil {
			out = append(out, obj)
		}
	}
	return out
}

// flattenToMap re-marshals v (a typed API response struct) and unmarshals the
// result into a generic map, so every populated field — including nested
// json.RawMessage sub-objects — is available for Single/Raw rendering without
// hand-maintaining a field-by-field projection.
func flattenToMap(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	fields := map[string]any{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return fields, nil
}

// stringMap renders every value in fields via scalarString, for output.Result.Single.
func stringMap(fields map[string]any) map[string]string {
	single := make(map[string]string, len(fields))
	for k, v := range fields {
		single[k] = scalarString(v)
	}
	return single
}

// scalarString renders an arbitrary JSON scalar as a display string. Numbers
// decoded as float64 with no fractional part render without a trailing ".0".
// Non-scalar values (nested objects/arrays, e.g. gc-status) render as compact
// JSON text.
func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return strings.TrimSpace(fmt.Sprintf("%v", t))
		}
		return string(b)
	}
}

// int64PtrString renders a possibly-nil *int64 for a table cell.
func int64PtrString(p *int64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatInt(*p, 10)
}

// strPtrString renders a possibly-nil *string for a table cell.
func strPtrString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
