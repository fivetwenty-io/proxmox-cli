package cluster

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newJobsCmd builds the `pmx pve cluster jobs` sub-tree for managing scheduled
// cluster jobs. Currently this exposes realm-sync jobs, which periodically
// synchronize users and groups from an authentication realm (LDAP/AD).
func newJobsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "Manage scheduled cluster jobs",
		Long:  "Manage scheduled cluster jobs, such as periodic realm (LDAP/AD) user synchronization.",
	}
	cmd.AddCommand(
		newJobsRealmSyncCmd(),
		newJobsScheduleAnalyzeCmd(),
	)
	return cmd
}

func newJobsRealmSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "realm-sync",
		Short: "Manage scheduled realm synchronization jobs",
		Long: "List, create, inspect, update, and delete realm-sync jobs. A job " +
			"periodically syncs users and groups from an authentication realm on a schedule.",
	}
	cmd.AddCommand(
		newJobsRealmSyncListCmd(),
		newJobsRealmSyncGetCmd(),
		newJobsRealmSyncCreateCmd(),
		newJobsRealmSyncSetCmd(),
		newJobsRealmSyncDeleteCmd(),
	)
	return cmd
}

// realmSyncListColumns are the focused columns rendered for the job list.
var realmSyncListColumns = []string{"id", "realm", "schedule", "enabled", "scope", "comment"}

func newJobsRealmSyncListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List realm-sync jobs",
		Long: "List every realm-sync job with its realm, schedule, enabled state, sync scope, " +
			"and comment.",
		Example: `  pmx pve cluster jobs realm-sync list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListJobsRealmSync(cmd.Context())
			if err != nil {
				return fmt.Errorf("list realm-sync jobs: %w", err)
			}
			res, err := rawFixedColumnsResult(derefRawList(resp), realmSyncListColumns)
			if err != nil {
				return fmt.Errorf("list realm-sync jobs: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newJobsRealmSyncGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <id>",
		Short:   "Show a single realm-sync job",
		Long:    "Show a single realm-sync job's full configuration by job ID.",
		Example: `  pmx pve cluster jobs realm-sync get ldap-daily`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			resp, err := deps.API.Cluster.GetJobsRealmSync(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("get realm-sync job %q: %w", id, err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("get realm-sync job %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

func newJobsRealmSyncCreateCmd() *cobra.Command {
	var (
		schedule       string
		realm          string
		comment        string
		scope          string
		removeVanished string
		enabled        bool
		enableNew      bool
	)
	cmd := &cobra.Command{
		Use:   "create <id>",
		Short: "Create a realm-sync job",
		Long: "Create a realm-sync job. --schedule is a systemd calendar event " +
			"(for example 'daily' or '*/15'); --realm selects the authentication realm to sync.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			params := &pvecluster.CreateJobsRealmSyncParams{Schedule: schedule}
			fl := cmd.Flags()
			if fl.Changed("realm") {
				params.Realm = &realm
			}
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("scope") {
				params.Scope = &scope
			}
			if fl.Changed("remove-vanished") {
				params.RemoveVanished = &removeVanished
			}
			if fl.Changed("enabled") {
				params.Enabled = &enabled
			}
			if fl.Changed("enable-new") {
				params.EnableNew = &enableNew
			}
			if err := deps.API.Cluster.CreateJobsRealmSync(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("create realm-sync job %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Realm-sync job %s created.", id)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&schedule, "schedule", "", "sync schedule (systemd calendar subset), for example 'daily' (required)")
	f.StringVar(&realm, "realm", "", "authentication realm to synchronize")
	f.StringVar(&comment, "comment", "", "description of the job")
	f.StringVar(&scope, "scope", "", "what to sync: 'users', 'groups', or 'both'")
	f.StringVar(&removeVanished, "remove-vanished", "",
		"semicolon-separated list of items to remove when vanished: entry, properties, acl, or none")
	f.BoolVar(&enabled, "enabled", false, "enable the job")
	f.BoolVar(&enableNew, "enable-new", false, "enable newly synced users immediately")
	cli.MustMarkRequired(cmd, "schedule")
	return cmd
}

func newJobsRealmSyncSetCmd() *cobra.Command {
	var (
		schedule       string
		comment        string
		scope          string
		removeVanished string
		enabled        bool
		enableNew      bool
		del            string
	)
	cmd := &cobra.Command{
		Use:   "set <id>",
		Short: "Update a realm-sync job",
		Long: "Update a realm-sync job. --schedule is required because the API rewrites " +
			"the full schedule; other flags are changed only when passed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			params := &pvecluster.UpdateJobsRealmSyncParams{Schedule: schedule}
			fl := cmd.Flags()
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("scope") {
				params.Scope = &scope
			}
			if fl.Changed("remove-vanished") {
				params.RemoveVanished = &removeVanished
			}
			if fl.Changed("enabled") {
				params.Enabled = &enabled
			}
			if fl.Changed("enable-new") {
				params.EnableNew = &enableNew
			}
			if fl.Changed("delete") {
				params.Delete = &del
			}
			if err := deps.API.Cluster.UpdateJobsRealmSync(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("update realm-sync job %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Realm-sync job %s updated.", id)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&schedule, "schedule", "", "sync schedule (systemd calendar subset), for example 'daily' (required)")
	f.StringVar(&comment, "comment", "", "description of the job")
	f.StringVar(&scope, "scope", "", "what to sync: 'users', 'groups', or 'both'")
	f.StringVar(&removeVanished, "remove-vanished", "",
		"semicolon-separated list of items to remove when vanished: entry, properties, acl, or none")
	f.BoolVar(&enabled, "enabled", false, "enable the job")
	f.BoolVar(&enableNew, "enable-new", false, "enable newly synced users immediately")
	f.StringVar(&del, "delete", "", "comma-separated list of settings to reset to default")
	cli.MustMarkRequired(cmd, "schedule")
	return cmd
}

func newJobsRealmSyncDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete a realm-sync job",
		Long:    "Delete a realm-sync job by ID. Refuses to run without --yes.",
		Example: `  pmx pve cluster jobs realm-sync delete ldap-daily --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete realm-sync job %q without confirmation: pass --yes/-y", id)
			}
			if err := deps.API.Cluster.DeleteJobsRealmSync(cmd.Context(), id); err != nil {
				return fmt.Errorf("delete realm-sync job %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Realm-sync job %s deleted.", id)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// newJobsScheduleAnalyzeCmd builds `pmx pve cluster jobs schedule-analyze --schedule <cron>`.
// It calls GET /cluster/jobs/schedule-analyze and returns the next N runtimes for a
// given systemd calendar expression. Useful for validating job schedules before
// committing them to a realm-sync or backup job.
func newJobsScheduleAnalyzeCmd() *cobra.Command {
	var (
		schedule   string
		iterations int64
		starttime  int64
	)
	cmd := &cobra.Command{
		Use:   "schedule-analyze",
		Short: "Preview the next runtimes for a schedule expression",
		Long: "Calculate and list the next runtimes for a systemd calendar expression. " +
			"Use this to validate a schedule before applying it to a job. " +
			"--schedule is required; --iterations defaults to the server default (10).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			params := &pvecluster.ListJobsScheduleAnalyzeParams{Schedule: schedule}
			if fl.Changed("iterations") {
				params.Iterations = &iterations
			}
			if fl.Changed("starttime") {
				params.Starttime = &starttime
			}
			resp, err := deps.API.Cluster.ListJobsScheduleAnalyze(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("analyze schedule %q: %w", schedule, err)
			}
			entries := make([]map[string]any, 0)
			if resp != nil {
				for _, raw := range *resp {
					var m map[string]any
					if err := json.Unmarshal(raw, &m); err != nil {
						return fmt.Errorf("decode schedule entry: %w", err)
					}
					entries = append(entries, m)
				}
			}
			headers, rows := dynamicTable(entries)
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&schedule, "schedule", "", "systemd calendar expression to analyze (required)")
	f.Int64Var(&iterations, "iterations", 0, "number of upcoming run times to return")
	f.Int64Var(&starttime, "starttime", 0, "UNIX timestamp to start the calculation from (default: now)")
	cli.MustMarkRequired(cmd, "schedule")
	return cmd
}
