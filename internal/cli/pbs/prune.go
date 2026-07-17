package pbs

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/optionschema"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"

	pbsadmin "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/admin"
	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
)

// newPruneCmd builds `pmx pbs prune` — remove or preview removal of backup
// snapshots on a datastore by retention policy, and manage scheduled prune
// job configurations (POST /admin/datastore/{store}/prune-datastore,
// /admin/datastore/{store}/prune, /config/prune, /admin/prune).
func newPruneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Prune backup snapshots by retention policy",
		Long: "Remove or preview removal of backup snapshots on a Proxmox Backup " +
			"Server datastore according to a --keep-* retention policy, and manage " +
			"scheduled prune job configurations.",
	}
	cmd.AddCommand(newPruneRunCmd(), newPruneSimulateCmd(), newPruneJobCmd())
	return cmd
}

// pruneGroupIDPattern matches a PBS backup-id: it must start with an
// alphanumeric character or underscore, followed by any run of
// alphanumerics, dots, underscores, or hyphens. This mirrors the pattern the
// PBS API itself enforces on the backup-id parameter.
var pruneGroupIDPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// pruneGroupTypes are the backup group types PBS accepts in a group
// reference (vm, ct, host).
var pruneGroupTypes = map[string]bool{"vm": true, "ct": true, "host": true}

// parsePruneGroupRef splits a "<type>/<id>" backup-group reference (e.g.
// "vm/100") into its type and ID, validating both against the shapes the PBS
// API accepts. It is named distinctly from any snapshot-ref parser elsewhere
// in this package since group refs and snapshot refs are different shapes.
// It rejects a ref with no separator, an unknown type, an empty ID, or an ID
// containing extra path segments or characters PBS disallows.
func parsePruneGroupRef(ref string) (backupType, backupID string, err error) {
	backupType, backupID, found := strings.Cut(ref, "/")
	if !found {
		return "", "", fmt.Errorf("invalid group reference %q: want <type>/<id> (e.g. vm/100)", ref)
	}

	if !pruneGroupTypes[backupType] {
		return "", "", fmt.Errorf("invalid group reference %q: type must be one of vm, ct, host", ref)
	}

	if backupID == "" || !pruneGroupIDPattern.MatchString(backupID) {
		return "", "", fmt.Errorf("invalid group reference %q: id must match %s", ref, pruneGroupIDPattern.String())
	}

	return backupType, backupID, nil
}

// pruneKeepArgs holds the retention flag values shared by every prune verb
// that accepts --keep-*. A field only reaches the API params when its flag
// was explicitly set — see pruneKeepPtrs.
type pruneKeepArgs struct {
	last, hourly, daily, weekly, monthly, yearly int64
}

// registerPruneKeepArgs binds the six --keep-* retention flags onto cmd.
func registerPruneKeepArgs(cmd *cobra.Command, a *pruneKeepArgs) {
	f := cmd.Flags()
	f.Int64Var(&a.last, "keep-last", 0, "number of most-recent backups to keep")
	f.Int64Var(&a.hourly, "keep-hourly", 0, "number of hourly backups to keep")
	f.Int64Var(&a.daily, "keep-daily", 0, "number of daily backups to keep")
	f.Int64Var(&a.weekly, "keep-weekly", 0, "number of weekly backups to keep")
	f.Int64Var(&a.monthly, "keep-monthly", 0, "number of monthly backups to keep")
	f.Int64Var(&a.yearly, "keep-yearly", 0, "number of yearly backups to keep")
}

// pruneKeepPtrs resolves a as six *int64 pointers using the flags registered
// by registerPruneKeepArgs, nil for any flag the caller did not set on cmd.
func pruneKeepPtrs(cmd *cobra.Command, a pruneKeepArgs) (last, hourly, daily, weekly, monthly, yearly *int64) {
	fl := cmd.Flags()

	if fl.Changed("keep-last") {
		last = int64Ptr(a.last)
	}

	if fl.Changed("keep-hourly") {
		hourly = int64Ptr(a.hourly)
	}

	if fl.Changed("keep-daily") {
		daily = int64Ptr(a.daily)
	}

	if fl.Changed("keep-weekly") {
		weekly = int64Ptr(a.weekly)
	}

	if fl.Changed("keep-monthly") {
		monthly = int64Ptr(a.monthly)
	}

	if fl.Changed("keep-yearly") {
		yearly = int64Ptr(a.yearly)
	}

	return last, hourly, daily, weekly, monthly, yearly
}

// newPruneRunCmd builds `pmx pbs prune run` — prune an entire datastore by
// retention policy (POST /admin/datastore/{store}/prune-datastore).
func newPruneRunCmd() *cobra.Command {
	var (
		store    string
		ns       string
		maxDepth int64
		ka       pruneKeepArgs
		dryRun   bool
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Prune a datastore's backup groups by retention policy",
		Long: "Run the datastore-wide prune job on a datastore (POST " +
			"/admin/datastore/{store}/prune-datastore): remove backup snapshots " +
			"that fall outside the --keep-* retention window across every group " +
			"under --ns, or the whole datastore. Runs as an asynchronous task; " +
			"the command blocks until it finishes unless --async is set.",
		Example: "  pmx pbs prune run --store tank --keep-daily 7",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if store == "" {
				return fmt.Errorf("--store is required")
			}

			fl := cmd.Flags()
			params := &pbsadmin.CreateDatastorePruneDatastoreParams{}
			if fl.Changed("ns") {
				params.Ns = strPtr(ns)
			}

			if fl.Changed("max-depth") {
				params.MaxDepth = int64Ptr(maxDepth)
			}

			if fl.Changed("dry-run") {
				params.DryRun = boolPtr(dryRun)
			}

			params.KeepLast, params.KeepHourly, params.KeepDaily,
				params.KeepWeekly, params.KeepMonthly, params.KeepYearly = pruneKeepPtrs(cmd, ka)

			resp, err := deps.PBS.Admin.CreateDatastorePruneDatastore(cmd.Context(), store, params)
			if err != nil {
				return fmt.Errorf("run prune on datastore %q: %w", store, err)
			}

			if resp == nil {
				return fmt.Errorf("run prune on datastore %q: nil response from PBS", store)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Prune of datastore %q finished.", store))
		},
	}

	cmd.Flags().StringVar(&store, "store", "", "datastore name (required)")
	cmd.Flags().StringVar(&ns, "ns", "", "namespace to prune")
	cmd.Flags().Int64Var(&maxDepth, "max-depth", 0,
		"namespace recursion depth (0 = no recursion, unset = full recursion)")
	registerPruneKeepArgs(cmd, &ka)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview the prune decisions without deleting anything")
	cli.MustMarkRequired(cmd, "store")
	return cmd
}

// prunePlanEntry is the decoded shape of one element of the array returned by
// POST /admin/datastore/{store}/prune (the group-level, dry-run-capable
// prune preview used by `prune simulate`).
type prunePlanEntry struct {
	BackupID   string `json:"backup-id"`
	BackupTime int64  `json:"backup-time"`
	BackupType string `json:"backup-type"`
	Keep       bool   `json:"keep"`
}

// newPruneSimulateCmd builds `pmx pbs prune simulate <type>/<id>` — preview
// prune decisions for one backup group without deleting anything (POST
// /admin/datastore/{store}/prune with dry-run forced true).
func newPruneSimulateCmd() *cobra.Command {
	var (
		store string
		ns    string
		ka    pruneKeepArgs
	)
	cmd := &cobra.Command{
		Use:   "simulate <type>/<id>",
		Short: "Preview prune decisions for one backup group",
		Long: "Preview which snapshots in a single backup group (POST " +
			"/admin/datastore/{store}/prune) would be kept or removed under the " +
			"given --keep-* retention window. This always runs as a dry run; " +
			"nothing is deleted, and there is no --async flag since the API " +
			"returns the plan synchronously.",
		Example: "  pmx pbs prune simulate vm/100 --store tank --keep-daily 7",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if store == "" {
				return fmt.Errorf("--store is required")
			}

			backupType, backupID, err := parsePruneGroupRef(args[0])
			if err != nil {
				return err
			}

			params := &pbsadmin.CreateDatastorePruneParams{
				BackupId:   backupID,
				BackupType: backupType,
				DryRun:     boolPtr(true),
			}

			fl := cmd.Flags()
			if fl.Changed("ns") {
				params.Ns = strPtr(ns)
			}

			params.KeepLast, params.KeepHourly, params.KeepDaily,
				params.KeepWeekly, params.KeepMonthly, params.KeepYearly = pruneKeepPtrs(cmd, ka)

			resp, err := deps.PBS.Admin.CreateDatastorePrune(cmd.Context(), store, params)
			if err != nil {
				return fmt.Errorf("simulate prune of group %q on datastore %q: %w", args[0], store, err)
			}

			entries := decodePrunePlan(resp)
			return renderPrunePlan(cmd, deps, entries)
		},
	}

	cmd.Flags().StringVar(&store, "store", "", "datastore name (required)")
	cmd.Flags().StringVar(&ns, "ns", "", "namespace containing the group")
	registerPruneKeepArgs(cmd, &ka)
	cli.MustMarkRequired(cmd, "store")
	return cmd
}

// decodePrunePlan decodes a CreateDatastorePrune response into typed plan
// entries, skipping any element that fails to decode.
func decodePrunePlan(resp *pbsadmin.CreateDatastorePruneResponse) []prunePlanEntry {
	entries := make([]prunePlanEntry, 0)
	if resp == nil {
		return entries
	}

	for _, raw := range *resp {
		var e prunePlanEntry

		err := json.Unmarshal(raw, &e)
		if err != nil {
			continue
		}

		entries = append(entries, e)
	}

	return entries
}

// renderPrunePlan renders prune-plan entries as a table plus lossless raw
// output.
func renderPrunePlan(cmd *cobra.Command, deps *cli.Deps, entries []prunePlanEntry) error {
	headers := []string{"BACKUP-TYPE", "BACKUP-ID", "BACKUP-TIME", "DECISION"}
	rows := make([][]string, 0, len(entries))

	for _, e := range entries {
		decision := "remove"
		if e.Keep {
			decision = "keep"
		}

		rows = append(rows, []string{e.BackupType, e.BackupID, strconv.FormatInt(e.BackupTime, 10), decision})
	}

	res := output.Result{Headers: headers, Rows: rows, Raw: entries}
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}

// pruneJobEntry is the decoded shape of one prune job configuration/status
// element, shared by `prune job ls` (GET /admin/prune, status-rich) and
// decoded individually for `prune job show` (GET /config/prune/{id}).
type pruneJobEntry struct {
	Comment        *string `json:"comment,omitempty"`
	Disable        *bool   `json:"disable,omitempty"`
	Id             string  `json:"id"`
	KeepDaily      *int64  `json:"keep-daily,omitempty"`
	KeepHourly     *int64  `json:"keep-hourly,omitempty"`
	KeepLast       *int64  `json:"keep-last,omitempty"`
	KeepMonthly    *int64  `json:"keep-monthly,omitempty"`
	KeepWeekly     *int64  `json:"keep-weekly,omitempty"`
	KeepYearly     *int64  `json:"keep-yearly,omitempty"`
	LastRunEndtime *int64  `json:"last-run-endtime,omitempty"`
	LastRunState   *string `json:"last-run-state,omitempty"`
	LastRunUpid    *string `json:"last-run-upid,omitempty"`
	MaxDepth       *int64  `json:"max-depth,omitempty"`
	NextRun        *int64  `json:"next-run,omitempty"`
	Ns             *string `json:"ns,omitempty"`
	Schedule       string  `json:"schedule"`
	Store          string  `json:"store"`
}

// newPruneJobCmd builds `pmx pbs prune job` — create, inspect, update,
// delete, and manually trigger scheduled prune job configurations.
func newPruneJobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Manage scheduled prune job configurations",
		Long: "Create, inspect, update, delete, and manually trigger scheduled " +
			"prune job configurations (GET/POST/PUT/DELETE /config/prune, " +
			"GET /admin/prune, POST /admin/prune/{id}/run).",
	}
	cmd.AddCommand(
		newPruneJobLsCmd(),
		newPruneJobShowCmd(),
		newPruneJobAddCmd(),
		newPruneJobUpdateCmd(),
		newPruneJobDeleteCmd(),
		newPruneJobRunCmd(),
	)
	return cmd
}

// decodePruneJobEntries decodes an Admin.ListPrune response into typed
// entries, skipping any element that fails to decode.
func decodePruneJobEntries(resp *pbsadmin.ListPruneResponse) []pruneJobEntry {
	entries := make([]pruneJobEntry, 0)
	if resp == nil {
		return entries
	}

	for _, raw := range *resp {
		var e pruneJobEntry

		err := json.Unmarshal(raw, &e)
		if err != nil {
			continue
		}

		entries = append(entries, e)
	}

	return entries
}

// newPruneJobLsCmd builds `pmx pbs prune job ls` — list every prune job
// configuration with its most recent run status (GET /admin/prune).
func newPruneJobLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List scheduled prune jobs and their run status",
		Long: "List every prune job configuration visible to the caller along " +
			"with its most recent run state (GET /admin/prune).",
		Example: "  pmx pbs prune job ls",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Admin.ListPrune(cmd.Context(), nil)
			if err != nil {
				return fmt.Errorf("list prune jobs: %w", err)
			}

			entries := decodePruneJobEntries(resp)
			sort.Slice(entries, func(i, j int) bool { return entries[i].Id < entries[j].Id })

			headers := []string{"ID", "STORE", "NS", "SCHEDULE", "DISABLE", "LAST-RUN-STATE", "NEXT-RUN"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Id, e.Store, pbsFormatOptionalString(e.Ns), e.Schedule,
					pbsFormatOptionalBool(e.Disable), pbsFormatOptionalString(e.LastRunState),
					epochCellPtr(e.NextRun),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newPruneJobShowCmd builds `pmx pbs prune job show <id>` — show one prune
// job's full configuration (GET /config/prune/{id}).
func newPruneJobShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one prune job's configuration",
		Long: "Show the full configuration of one prune job (GET /config/prune/{id}). " +
			"The PBS API omits options left at their built-in defaults; pass " +
			"--defaults to also list those, with the value they effectively have.",
		Example: "  pmx pbs prune job show daily-prune",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}

			resp, err := deps.PBS.Config.GetPrune(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("show prune job %q: %w", id, err)
			}

			if resp == nil {
				return fmt.Errorf("show prune job %q: nil response from PBS", id)
			}

			single := map[string]string{
				"id":       resp.Id,
				"store":    resp.Store,
				"schedule": resp.Schedule,
			}
			if resp.Comment != nil {
				single["comment"] = *resp.Comment
			}

			if resp.Disable != nil {
				single["disable"] = strconv.FormatBool(resp.Disable.Bool())
			}

			if resp.KeepLast != nil {
				single["keep-last"] = strconv.FormatInt(resp.KeepLast.Int(), 10)
			}

			if resp.KeepHourly != nil {
				single["keep-hourly"] = strconv.FormatInt(resp.KeepHourly.Int(), 10)
			}

			if resp.KeepDaily != nil {
				single["keep-daily"] = strconv.FormatInt(resp.KeepDaily.Int(), 10)
			}

			if resp.KeepWeekly != nil {
				single["keep-weekly"] = strconv.FormatInt(resp.KeepWeekly.Int(), 10)
			}

			if resp.KeepMonthly != nil {
				single["keep-monthly"] = strconv.FormatInt(resp.KeepMonthly.Int(), 10)
			}

			if resp.KeepYearly != nil {
				single["keep-yearly"] = strconv.FormatInt(resp.KeepYearly.Int(), 10)
			}

			if resp.MaxDepth != nil {
				single["max-depth"] = strconv.FormatInt(resp.MaxDepth.Int(), 10)
			}

			if resp.Ns != nil {
				single["ns"] = *resp.Ns
			}

			var raw any = resp
			if withDefaults {
				single, raw = optionschema.MergeDefaults(pruneJobOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// newPruneJobAddCmd builds `pmx pbs prune job add <id>` — create a scheduled
// prune job configuration (POST /config/prune).
func newPruneJobAddCmd() *cobra.Command {
	var (
		store    string
		schedule string
		ns       string
		maxDepth int64
		ka       pruneKeepArgs
		disable  bool
		comment  string
	)
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Create a scheduled prune job",
		Long: "Create a new prune job configuration (POST /config/prune). " +
			"--store and --schedule are required; every --keep-* option is " +
			"optional and only forwarded when explicitly set.",
		Example: `  pmx pbs prune job add daily-prune --store tank --schedule daily \
  --keep-daily 7`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}

			if store == "" {
				return fmt.Errorf("--store is required")
			}

			if schedule == "" {
				return fmt.Errorf("--schedule is required")
			}

			params := &pbsconfig.CreatePruneParams{
				Id:       id,
				Store:    store,
				Schedule: schedule,
			}

			fl := cmd.Flags()
			if fl.Changed("ns") {
				params.Ns = strPtr(ns)
			}

			if fl.Changed("max-depth") {
				params.MaxDepth = int64Ptr(maxDepth)
			}

			if fl.Changed("disable") {
				params.Disable = boolPtr(disable)
			}

			if fl.Changed("comment") {
				params.Comment = strPtr(comment)
			}

			params.KeepLast, params.KeepHourly, params.KeepDaily,
				params.KeepWeekly, params.KeepMonthly, params.KeepYearly = pruneKeepPtrs(cmd, ka)

			err := deps.PBS.Config.CreatePrune(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create prune job %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Prune job %q created.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().StringVar(&store, "store", "", "datastore name (required)")
	cmd.Flags().StringVar(&schedule, "schedule", "", "calendar-event schedule, e.g. 'daily' (required)")
	cmd.Flags().StringVar(&ns, "ns", "", "namespace to prune")
	cmd.Flags().Int64Var(&maxDepth, "max-depth", 0,
		"namespace recursion depth (0 = no recursion, unset = full recursion)")
	registerPruneKeepArgs(cmd, &ka)
	cmd.Flags().BoolVar(&disable, "disable", false, "create the job disabled")
	cmd.Flags().StringVar(&comment, "comment", "", "job comment")
	cli.MustMarkRequired(cmd, "store")
	cli.MustMarkRequired(cmd, "schedule")
	return cmd
}

// newPruneJobUpdateCmd builds `pmx pbs prune job update <id>` — update a
// scheduled prune job configuration (PUT /config/prune/{id}).
func newPruneJobUpdateCmd() *cobra.Command {
	var (
		store    string
		schedule string
		ns       string
		maxDepth int64
		ka       pruneKeepArgs
		disable  bool
		comment  string
		digest   string
		del      []string
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a scheduled prune job",
		Long: "Update an existing prune job configuration (PUT " +
			"/config/prune/{id}). Only flags explicitly set are sent; use " +
			"--delete to reset properties to their default instead.",
		Example: "  pmx pbs prune job update daily-prune --keep-daily 14",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}

			fl := cmd.Flags()
			params := &pbsconfig.UpdatePruneParams{}

			if fl.Changed("store") {
				params.Store = strPtr(store)
			}

			if fl.Changed("schedule") {
				params.Schedule = strPtr(schedule)
			}

			if fl.Changed("ns") {
				params.Ns = strPtr(ns)
			}

			if fl.Changed("max-depth") {
				params.MaxDepth = int64Ptr(maxDepth)
			}

			if fl.Changed("disable") {
				params.Disable = boolPtr(disable)
			}

			if fl.Changed("comment") {
				params.Comment = strPtr(comment)
			}

			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}

			if fl.Changed("delete") {
				for _, name := range del {
					if name == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}

				params.Delete = del
			}

			params.KeepLast, params.KeepHourly, params.KeepDaily,
				params.KeepWeekly, params.KeepMonthly, params.KeepYearly = pruneKeepPtrs(cmd, ka)

			err := deps.PBS.Config.UpdatePrune(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("update prune job %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Prune job %q updated.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().StringVar(&store, "store", "", "datastore name")
	cmd.Flags().StringVar(&schedule, "schedule", "", "calendar-event schedule, e.g. 'daily'")
	cmd.Flags().StringVar(&ns, "ns", "", "namespace to prune")
	cmd.Flags().Int64Var(&maxDepth, "max-depth", 0,
		"namespace recursion depth (0 = no recursion, unset = full recursion)")
	registerPruneKeepArgs(cmd, &ka)
	cmd.Flags().BoolVar(&disable, "disable", false, "disable the job")
	cmd.Flags().StringVar(&comment, "comment", "", "job comment")
	cmd.Flags().StringVar(&digest, "digest", "", "only update if the current config digest matches")
	cmd.Flags().StringSliceVar(&del, "delete", nil, "property names to reset to default")
	return cmd
}

// newPruneJobDeleteCmd builds `pmx pbs prune job delete <id>` — remove a
// prune job configuration (DELETE /config/prune/{id}).
func newPruneJobDeleteCmd() *cobra.Command {
	var digest string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a scheduled prune job",
		Long: "Remove a prune job configuration (DELETE /config/prune/{id}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Example: "  pmx pbs prune job delete daily-prune --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}
			if !yes {
				return fmt.Errorf("refusing to delete prune job %q without confirmation: pass --yes/-y", id)
			}

			params := &pbsconfig.DeletePruneParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Config.DeletePrune(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("delete prune job %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Prune job %q deleted.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newPruneJobRunCmd builds `pmx pbs prune job run <id>` — manually trigger a
// scheduled prune job (POST /admin/prune/{id}/run).
func newPruneJobRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <id>",
		Short: "Manually trigger a scheduled prune job",
		Long: "Immediately run a configured prune job (POST /admin/prune/{id}/run). " +
			"This endpoint reports only success or failure, not a task UPID, so the " +
			"command always completes synchronously and --async has no effect here.",
		Example: "  pmx pbs prune job run daily-prune",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}

			err := deps.PBS.Admin.CreatePruneRun(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("run prune job %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Prune job %q started.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
