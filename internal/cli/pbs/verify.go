package pbs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/optionschema"
	"github.com/fivetwenty-io/pmx-cli/internal/output"

	pbsadmin "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/admin"
	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
)

// newVerifyCmd builds `pmx pbs verify` — run backup-chunk verification on a
// datastore and manage scheduled verification job configurations
// (POST /admin/datastore/{store}/verify, /config/verify, /admin/verify).
func newVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify backup snapshot integrity",
		Long: "Check backup snapshots on a Proxmox Backup Server datastore for " +
			"chunk corruption, and manage scheduled verification job configurations.",
	}
	cmd.AddCommand(newVerifyRunCmd(), newVerifyJobCmd())
	return cmd
}

// newVerifyRunCmd builds `pmx pbs verify run` — verify backups on a
// datastore, optionally scoped to a namespace or a single snapshot (POST
// /admin/datastore/{store}/verify).
func newVerifyRunCmd() *cobra.Command {
	var (
		store          string
		ns             string
		backupType     string
		backupID       string
		backupTime     int64
		ignoreVerified bool
		outdatedAfter  int64
		maxDepth       int64
		readThreads    int64
		verifyThreads  int64
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Verify backup snapshots on a datastore",
		Long: "Check chunk integrity for backup snapshots on a datastore (POST " +
			"/admin/datastore/{store}/verify), optionally scoped to a namespace " +
			"(--ns) or a single snapshot (--backup-type/--backup-id/--backup-time). " +
			"Runs as an asynchronous task; the command blocks until it finishes " +
			"unless --async is set.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if store == "" {
				return fmt.Errorf("--store is required")
			}

			fl := cmd.Flags()
			params := &pbsadmin.CreateDatastoreVerifyParams{}

			if fl.Changed("ns") {
				params.Ns = strPtr(ns)
			}

			if fl.Changed("backup-type") {
				params.BackupType = strPtr(backupType)
			}

			if fl.Changed("backup-id") {
				params.BackupId = strPtr(backupID)
			}

			if fl.Changed("backup-time") {
				params.BackupTime = int64Ptr(backupTime)
			}

			if fl.Changed("ignore-verified") {
				params.IgnoreVerified = boolPtr(ignoreVerified)
			}

			if fl.Changed("outdated-after") {
				params.OutdatedAfter = int64Ptr(outdatedAfter)
			}

			if fl.Changed("max-depth") {
				params.MaxDepth = int64Ptr(maxDepth)
			}

			if fl.Changed("read-threads") {
				params.ReadThreads = int64Ptr(readThreads)
			}

			if fl.Changed("verify-threads") {
				params.VerifyThreads = int64Ptr(verifyThreads)
			}

			resp, err := deps.PBS.Admin.CreateDatastoreVerify(cmd.Context(), store, params)
			if err != nil {
				return fmt.Errorf("run verify on datastore %q: %w", store, err)
			}

			if resp == nil {
				return fmt.Errorf("run verify on datastore %q: nil response from PBS", store)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Verification on datastore %q finished.", store))
		},
	}

	cmd.Flags().StringVar(&store, "store", "", "datastore name (required)")
	cmd.Flags().StringVar(&ns, "ns", "", "namespace to verify")
	cmd.Flags().StringVar(&backupType, "backup-type", "", "restrict to one backup group's type: vm|ct|host")
	cmd.Flags().StringVar(&backupID, "backup-id", "", "restrict to one backup group's ID")
	cmd.Flags().Int64Var(&backupTime, "backup-time", 0, "restrict to one snapshot's backup time (Unix epoch)")
	cmd.Flags().BoolVar(&ignoreVerified, "ignore-verified", false,
		"skip snapshots already verified and not outdated")
	cmd.Flags().Int64Var(&outdatedAfter, "outdated-after", 0,
		"days after which a verification is considered outdated")
	cmd.Flags().Int64Var(&maxDepth, "max-depth", 0, "namespace recursion depth (0 = no recursion)")
	cmd.Flags().Int64Var(&readThreads, "read-threads", 0, "number of threads used to read chunks")
	cmd.Flags().Int64Var(&verifyThreads, "verify-threads", 0, "number of threads used to verify chunks")
	cli.MustMarkRequired(cmd, "store")
	return cmd
}

// verifyJobEntry is the decoded shape of one verification job
// configuration/status element, shared by `verify job ls` (GET /admin/verify,
// status-rich) and decoded individually for `verify job show`
// (GET /config/verify/{id}).
type verifyJobEntry struct {
	Comment        *string `json:"comment,omitempty"`
	Id             string  `json:"id"`
	IgnoreVerified *bool   `json:"ignore-verified,omitempty"`
	LastRunEndtime *int64  `json:"last-run-endtime,omitempty"`
	LastRunState   *string `json:"last-run-state,omitempty"`
	LastRunUpid    *string `json:"last-run-upid,omitempty"`
	MaxDepth       *int64  `json:"max-depth,omitempty"`
	NextRun        *int64  `json:"next-run,omitempty"`
	Ns             *string `json:"ns,omitempty"`
	OutdatedAfter  *int64  `json:"outdated-after,omitempty"`
	ReadThreads    *int64  `json:"read-threads,omitempty"`
	Schedule       *string `json:"schedule,omitempty"`
	Store          string  `json:"store"`
	VerifyThreads  *int64  `json:"verify-threads,omitempty"`
}

// newVerifyJobCmd builds `pmx pbs verify job` — create, inspect, update,
// delete, and manually trigger scheduled verification job configurations.
func newVerifyJobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Manage scheduled verification job configurations",
		Long: "Create, inspect, update, delete, and manually trigger scheduled " +
			"verification job configurations (GET/POST/PUT/DELETE /config/verify, " +
			"GET /admin/verify, POST /admin/verify/{id}/run).",
	}
	cmd.AddCommand(
		newVerifyJobLsCmd(),
		newVerifyJobShowCmd(),
		newVerifyJobAddCmd(),
		newVerifyJobUpdateCmd(),
		newVerifyJobDeleteCmd(),
		newVerifyJobRunCmd(),
	)
	return cmd
}

// decodeVerifyJobEntries decodes an Admin.ListVerify response into typed
// entries, skipping any element that fails to decode.
func decodeVerifyJobEntries(resp *pbsadmin.ListVerifyResponse) []verifyJobEntry {
	entries := make([]verifyJobEntry, 0)
	if resp == nil {
		return entries
	}

	for _, raw := range *resp {
		var e verifyJobEntry

		err := json.Unmarshal(raw, &e)
		if err != nil {
			continue
		}

		entries = append(entries, e)
	}

	return entries
}

// newVerifyJobLsCmd builds `pmx pbs verify job ls` — list every verification
// job configuration with its most recent run status (GET /admin/verify).
func newVerifyJobLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List scheduled verification jobs and their run status",
		Long: "List every verification job configuration visible to the caller " +
			"along with its most recent run state (GET /admin/verify).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Admin.ListVerify(cmd.Context(), nil)
			if err != nil {
				return fmt.Errorf("list verify jobs: %w", err)
			}

			entries := decodeVerifyJobEntries(resp)
			sort.Slice(entries, func(i, j int) bool { return entries[i].Id < entries[j].Id })

			headers := []string{"ID", "STORE", "NS", "SCHEDULE", "LAST-RUN-STATE", "NEXT-RUN"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Id, e.Store, pbsFormatOptionalString(e.Ns), pbsFormatOptionalString(e.Schedule),
					pbsFormatOptionalString(e.LastRunState), pbsFormatOptionalInt64(e.NextRun),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newVerifyJobShowCmd builds `pmx pbs verify job show <id>` — show one
// verification job's full configuration (GET /config/verify/{id}).
func newVerifyJobShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one verification job's configuration",
		Long: "Show the full configuration of one verification job (GET " +
			"/config/verify/{id}). The PBS API omits options left at their built-in " +
			"defaults; pass --defaults to also list those, with the value they " +
			"effectively have.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}

			resp, err := deps.PBS.Config.GetVerify(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("show verify job %q: %w", id, err)
			}

			if resp == nil {
				return fmt.Errorf("show verify job %q: nil response from PBS", id)
			}

			single := map[string]string{
				"id":    resp.Id,
				"store": resp.Store,
			}
			if resp.Comment != nil {
				single["comment"] = *resp.Comment
			}

			if resp.IgnoreVerified != nil {
				single["ignore-verified"] = strconv.FormatBool(resp.IgnoreVerified.Bool())
			}

			if resp.MaxDepth != nil {
				single["max-depth"] = strconv.FormatInt(resp.MaxDepth.Int(), 10)
			}

			if resp.Ns != nil {
				single["ns"] = *resp.Ns
			}

			if resp.OutdatedAfter != nil {
				single["outdated-after"] = strconv.FormatInt(resp.OutdatedAfter.Int(), 10)
			}

			if resp.ReadThreads != nil {
				single["read-threads"] = strconv.FormatInt(resp.ReadThreads.Int(), 10)
			}

			if resp.Schedule != nil {
				single["schedule"] = *resp.Schedule
			}

			if resp.VerifyThreads != nil {
				single["verify-threads"] = strconv.FormatInt(resp.VerifyThreads.Int(), 10)
			}

			var raw any = resp
			if withDefaults {
				single, raw = optionschema.MergeDefaults(verifyJobOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// newVerifyJobAddCmd builds `pmx pbs verify job add <id>` — create a
// scheduled verification job configuration (POST /config/verify).
func newVerifyJobAddCmd() *cobra.Command {
	var (
		store          string
		schedule       string
		ns             string
		maxDepth       int64
		outdatedAfter  int64
		readThreads    int64
		verifyThreads  int64
		ignoreVerified bool
		comment        string
	)
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Create a scheduled verification job",
		Long: "Create a new verification job configuration (POST " +
			"/config/verify). --store is required; every other option is " +
			"optional and only forwarded when explicitly set.",
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

			params := &pbsconfig.CreateVerifyParams{
				Id:    id,
				Store: store,
			}

			fl := cmd.Flags()
			if fl.Changed("schedule") {
				params.Schedule = strPtr(schedule)
			}

			if fl.Changed("ns") {
				params.Ns = strPtr(ns)
			}

			if fl.Changed("max-depth") {
				params.MaxDepth = int64Ptr(maxDepth)
			}

			if fl.Changed("outdated-after") {
				params.OutdatedAfter = int64Ptr(outdatedAfter)
			}

			if fl.Changed("read-threads") {
				params.ReadThreads = int64Ptr(readThreads)
			}

			if fl.Changed("verify-threads") {
				params.VerifyThreads = int64Ptr(verifyThreads)
			}

			if fl.Changed("ignore-verified") {
				params.IgnoreVerified = boolPtr(ignoreVerified)
			}

			if fl.Changed("comment") {
				params.Comment = strPtr(comment)
			}

			err := deps.PBS.Config.CreateVerify(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create verify job %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Verify job %q created.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().StringVar(&store, "store", "", "datastore name (required)")
	cmd.Flags().StringVar(&schedule, "schedule", "", "calendar-event schedule, e.g. 'daily'")
	cmd.Flags().StringVar(&ns, "ns", "", "namespace to verify")
	cmd.Flags().Int64Var(&maxDepth, "max-depth", 0, "namespace recursion depth (0 = no recursion)")
	cmd.Flags().Int64Var(&outdatedAfter, "outdated-after", 0,
		"days after which a verification is considered outdated")
	cmd.Flags().Int64Var(&readThreads, "read-threads", 0, "number of threads used to read chunks")
	cmd.Flags().Int64Var(&verifyThreads, "verify-threads", 0, "number of threads used to verify chunks")
	cmd.Flags().BoolVar(&ignoreVerified, "ignore-verified", false,
		"skip snapshots already verified and not outdated")
	cmd.Flags().StringVar(&comment, "comment", "", "job comment")
	cli.MustMarkRequired(cmd, "store")
	return cmd
}

// newVerifyJobUpdateCmd builds `pmx pbs verify job update <id>` — update a
// scheduled verification job configuration (PUT /config/verify/{id}).
func newVerifyJobUpdateCmd() *cobra.Command {
	var (
		store          string
		schedule       string
		ns             string
		maxDepth       int64
		outdatedAfter  int64
		readThreads    int64
		verifyThreads  int64
		ignoreVerified bool
		comment        string
		digest         string
		del            []string
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a scheduled verification job",
		Long: "Update an existing verification job configuration (PUT " +
			"/config/verify/{id}). Only flags explicitly set are sent; use " +
			"--delete to reset properties to their default instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}

			fl := cmd.Flags()
			params := &pbsconfig.UpdateVerifyParams{}

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

			if fl.Changed("outdated-after") {
				params.OutdatedAfter = int64Ptr(outdatedAfter)
			}

			if fl.Changed("read-threads") {
				params.ReadThreads = int64Ptr(readThreads)
			}

			if fl.Changed("verify-threads") {
				params.VerifyThreads = int64Ptr(verifyThreads)
			}

			if fl.Changed("ignore-verified") {
				params.IgnoreVerified = boolPtr(ignoreVerified)
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

			err := deps.PBS.Config.UpdateVerify(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("update verify job %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Verify job %q updated.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().StringVar(&store, "store", "", "datastore name")
	cmd.Flags().StringVar(&schedule, "schedule", "", "calendar-event schedule, e.g. 'daily'")
	cmd.Flags().StringVar(&ns, "ns", "", "namespace to verify")
	cmd.Flags().Int64Var(&maxDepth, "max-depth", 0, "namespace recursion depth (0 = no recursion)")
	cmd.Flags().Int64Var(&outdatedAfter, "outdated-after", 0,
		"days after which a verification is considered outdated")
	cmd.Flags().Int64Var(&readThreads, "read-threads", 0, "number of threads used to read chunks")
	cmd.Flags().Int64Var(&verifyThreads, "verify-threads", 0, "number of threads used to verify chunks")
	cmd.Flags().BoolVar(&ignoreVerified, "ignore-verified", false,
		"skip snapshots already verified and not outdated")
	cmd.Flags().StringVar(&comment, "comment", "", "job comment")
	cmd.Flags().StringVar(&digest, "digest", "", "only update if the current config digest matches")
	cmd.Flags().StringSliceVar(&del, "delete", nil, "property names to reset to default")
	return cmd
}

// newVerifyJobDeleteCmd builds `pmx pbs verify job delete <id>` — remove a
// verification job configuration (DELETE /config/verify/{id}).
func newVerifyJobDeleteCmd() *cobra.Command {
	var digest string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a scheduled verification job",
		Long: "Remove a verification job configuration (DELETE /config/verify/{id}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}
			if !yes {
				return fmt.Errorf("refusing to delete verify job %q without confirmation: pass --yes/-y", id)
			}

			params := &pbsconfig.DeleteVerifyParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Config.DeleteVerify(cmd.Context(), id, params)
			if err != nil {
				return fmt.Errorf("delete verify job %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Verify job %q deleted.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newVerifyJobRunCmd builds `pmx pbs verify job run <id>` — manually trigger
// a scheduled verification job (POST /admin/verify/{id}/run).
func newVerifyJobRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <id>",
		Short: "Manually trigger a scheduled verification job",
		Long: "Immediately run a configured verification job (POST " +
			"/admin/verify/{id}/run). This endpoint reports only success or " +
			"failure, not a task UPID, so the command always completes " +
			"synchronously and --async has no effect here.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if id == "" {
				return fmt.Errorf("job id must not be empty")
			}

			err := deps.PBS.Admin.CreateVerifyRun(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("run verify job %q: %w", id, err)
			}

			res := output.Result{Message: fmt.Sprintf("Verify job %q started.", id)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
