package cluster

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newReplicationCmd builds the `pmx cluster replication` sub-tree for managing
// storage replication jobs, which periodically replicate a guest's local-storage
// volumes to another node so the guest can be recovered or migrated quickly.
func newReplicationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "replication",
		Short: "Manage cluster storage replication jobs",
		Long: "List, create, inspect, update, and delete storage replication jobs. A job " +
			"replicates a guest's local volumes to a target node on a schedule.",
	}
	cmd.AddCommand(
		newReplicationListCmd(),
		newReplicationCreateCmd(),
		newReplicationGetCmd(),
		newReplicationSetCmd(),
		newReplicationDeleteCmd(),
	)
	return cmd
}

// replicationListColumns are the fixed columns rendered for the job list. The
// job schema is known, so a focused table reads better than the union of keys.
var replicationListColumns = []string{"id", "guest", "type", "target", "schedule", "rate", "disable", "comment"}

func newReplicationListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List storage replication jobs",
		Long: "List storage replication jobs with their guest, type, target node, schedule, " +
			"rate limit, disabled state, and comment.",
		Example: `  pmx pve cluster replication list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListReplication(cmd.Context())
			if err != nil {
				return fmt.Errorf("list replication jobs: %w", err)
			}
			entries := make([]map[string]any, 0)
			if resp != nil {
				for _, raw := range *resp {
					var m map[string]any
					if err := json.Unmarshal(raw, &m); err != nil {
						return fmt.Errorf("decode replication job: %w", err)
					}
					entries = append(entries, m)
				}
			}

			headers := make([]string, len(replicationListColumns))
			for i, k := range replicationListColumns {
				headers[i] = upperHeader(k)
			}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				row := make([]string, len(replicationListColumns))
				for i, k := range replicationListColumns {
					row[i] = anyCell(e[k])
				}
				rows = append(rows, row)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
}

func newReplicationCreateCmd() *cobra.Command {
	var (
		id        string
		target    string
		jobType   string
		schedule  string
		rate      float64
		comment   string
		disable   bool
		removeJob string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a storage replication job",
		Long: "Create a replication job. The job ID is '<GUEST>-<JOBNUM>' (for example " +
			"'101-0'); --target-node is the node to replicate to.",
		Example: `  pmx pve cluster replication create --id 101-0 --target-node pve2
  pmx pve cluster replication create --id 101-0 --target-node pve2 --schedule "*/15" --rate 50`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &pvecluster.CreateReplicationParams{
				Id:     id,
				Target: target,
				Type:   jobType,
			}
			fl := cmd.Flags()
			if fl.Changed("schedule") {
				params.Schedule = &schedule
			}
			if fl.Changed("rate") {
				params.Rate = &rate
			}
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("disable") {
				params.Disable = &disable
			}
			if fl.Changed("remove-job") {
				params.RemoveJob = &removeJob
			}
			if err := deps.API.Cluster.CreateReplication(cmd.Context(), params); err != nil {
				return fmt.Errorf("create replication job %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Replication job %s created.", id)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&id, "id", "", "replication job ID '<GUEST>-<JOBNUM>', for example 101-0 (required)")
	f.StringVar(&target, "target-node", "", "target node to replicate to (required)")
	f.StringVar(&jobType, "type", "local", "replication type")
	f.StringVar(&schedule, "schedule", "", "replication schedule (systemd calendar subset), for example */15")
	f.Float64Var(&rate, "rate", 0, "rate limit in MB/s")
	f.StringVar(&comment, "comment", "", "description")
	f.BoolVar(&disable, "disable", false, "create the job disabled")
	f.StringVar(&removeJob, "remove-job", "",
		"mark the job for removal: 'local' clears local snapshots, 'full' also removes target volumes")
	cli.MustMarkRequired(cmd, "id")
	cli.MustMarkRequired(cmd, "target-node")
	return cmd
}

func newReplicationGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show a single replication job",
		Long: "Show a single replication job's full configuration by job ID (the " +
			"'<GUEST>-<JOBNUM>' form, for example 101-0).",
		Example: `  pmx pve cluster replication get 101-0`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			resp, err := deps.API.Cluster.GetReplication(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("get replication job %q: %w", id, err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("get replication job %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

func newReplicationSetCmd() *cobra.Command {
	var (
		schedule  string
		rate      float64
		comment   string
		disable   bool
		removeJob string
		del       string
	)
	cmd := &cobra.Command{
		Use:   "set <id>",
		Short: "Update a replication job",
		Long: "Update a replication job. Only the flags you pass are changed; pass --delete " +
			"with a comma-separated list of setting names to reset them to their defaults.",
		Example: `  pmx pve cluster replication set 101-0 --schedule "*/30"
  pmx pve cluster replication set 101-0 --disable`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			fl := cmd.Flags()
			if !anyFlagChanged(fl, "schedule", "rate", "comment", "disable", "remove-job", "delete") {
				return fmt.Errorf("no changes requested: pass at least one flag")
			}
			params := &pvecluster.UpdateReplicationParams{}
			if fl.Changed("schedule") {
				params.Schedule = &schedule
			}
			if fl.Changed("rate") {
				params.Rate = &rate
			}
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("disable") {
				params.Disable = &disable
			}
			if fl.Changed("remove-job") {
				params.RemoveJob = &removeJob
			}
			if fl.Changed("delete") {
				params.Delete = &del
			}
			if err := deps.API.Cluster.UpdateReplication(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("update replication job %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Replication job %s updated.", id)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&schedule, "schedule", "", "replication schedule (systemd calendar subset)")
	f.Float64Var(&rate, "rate", 0, "rate limit in MB/s")
	f.StringVar(&comment, "comment", "", "description")
	f.BoolVar(&disable, "disable", false, "disable the job")
	f.StringVar(&removeJob, "remove-job", "",
		"mark the job for removal: 'local' clears local snapshots, 'full' also removes target volumes")
	f.StringVar(&del, "delete", "", "comma-separated list of settings to reset to default")
	return cmd
}

func newReplicationDeleteCmd() *cobra.Command {
	var (
		yes   bool
		force bool
		keep  bool
	)
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a replication job",
		Long: "Delete a replication job by ID. Pass --keep to leave replicated data on the target " +
			"node, or --force to remove the job configuration without cleaning up replicated data. " +
			"Refuses to run without --yes.",
		Example: `  pmx pve cluster replication delete 101-0 --yes
  pmx pve cluster replication delete 101-0 --keep --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			id := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete replication job %q without confirmation: pass --yes/-y", id)
			}
			params := &pvecluster.DeleteReplicationParams{}
			fl := cmd.Flags()
			if fl.Changed("force") {
				params.Force = &force
			}
			if fl.Changed("keep") {
				params.Keep = &keep
			}
			if err := deps.API.Cluster.DeleteReplication(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("delete replication job %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Replication job %s deleted.", id)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	f.BoolVar(&force, "force", false, "remove the job config without cleaning up replicated data")
	f.BoolVar(&keep, "keep", false, "keep replicated data on the target node")
	return cmd
}
