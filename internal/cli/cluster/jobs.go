package cluster

import (
	"fmt"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newJobsCmd builds the `pve cluster jobs` sub-tree for managing scheduled
// cluster jobs. Currently this exposes realm-sync jobs, which periodically
// synchronize users and groups from an authentication realm (LDAP/AD).
func newJobsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "Manage scheduled cluster jobs",
		Long:  "Manage scheduled cluster jobs, such as periodic realm (LDAP/AD) user synchronization.",
	}
	cmd.AddCommand(newJobsRealmSyncCmd())
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
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
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
		Use:   "get <id>",
		Short: "Show a single realm-sync job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
			deps := resolveDeps(cmd)
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
	_ = cmd.MarkFlagRequired("schedule")
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
			deps := resolveDeps(cmd)
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
	_ = cmd.MarkFlagRequired("schedule")
	return cmd
}

func newJobsRealmSyncDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a realm-sync job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
