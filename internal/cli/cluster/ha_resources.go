package cluster

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newHaCmd builds the `pmx cluster ha` sub-tree: high-availability management.
// It exposes HA resources, groups, rules, and the cluster-wide manager status.
func newHaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ha",
		Short: "Manage cluster high availability",
		Long: "Manage high availability: HA-managed resources (with manual migrate/relocate), HA " +
			"groups, HA rules, and the cluster-wide HA manager status (arm/disarm).",
	}
	cmd.AddCommand(
		newHaResourceCmd(),
		newHaGroupCmd(),
		newHaRuleCmd(),
		newHaStatusCmd(),
	)
	return cmd
}

// newHaResourceCmd builds the `pmx cluster ha resource` sub-tree.
func newHaResourceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "resource",
		Aliases: []string{"resources"},
		Short:   "Manage HA-managed resources",
		Long: "List, create, inspect, update, and delete HA resources, and request a manual " +
			"migrate (live, keeping state) or relocate (stop and restart on the target).",
	}
	cmd.AddCommand(
		newHaResourceListCmd(),
		newHaResourceGetCmd(),
		newHaResourceCreateCmd(),
		newHaResourceSetCmd(),
		newHaResourceDeleteCmd(),
		newHaResourceMigrateCmd(),
		newHaResourceRelocateCmd(),
	)
	return cmd
}

// newHaResourceListCmd builds `pmx cluster ha resource list`.
func newHaResourceListCmd() *cobra.Command {
	var typ string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List HA resources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pvecluster.ListHaResourcesParams{}
			if cmd.Flags().Changed("type") {
				params.Type = &typ
			}

			resp, err := deps.API.Cluster.ListHaResources(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list HA resources: %w", err)
			}

			headers := []string{"SID", "STATE", "GROUP", "MAX-RESTART", "MAX-RELOCATE", "COMMENT"}
			rows := make([][]string, 0)
			raw := make([]map[string]any, 0)
			if resp != nil {
				for _, rawRes := range *resp {
					var m map[string]any
					if err := json.Unmarshal(rawRes, &m); err != nil {
						return fmt.Errorf("decode HA resource: %w", err)
					}
					raw = append(raw, m)
					rows = append(rows, []string{
						anyCell(m["sid"]),
						anyCell(m["state"]),
						anyCell(m["group"]),
						anyCell(m["max_restart"]),
						anyCell(m["max_relocate"]),
						anyCell(m["comment"]),
					})
				}
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: raw}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&typ, "type", "", "only list resources of this type: vm|ct")
	return cmd
}

// newHaResourceGetCmd builds `pmx cluster ha resource get <sid>`.
func newHaResourceGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <sid>",
		Short: "Show a single HA resource",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			sid := args[0]

			resp, err := deps.API.Cluster.GetHaResources(cmd.Context(), sid)
			if err != nil {
				return fmt.Errorf("get HA resource %q: %w", sid, err)
			}

			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("decode HA resource %q: %w", sid, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

// haResourceFlags collects the mutable HA-resource attributes shared by create
// and set. The state, group, comment, and retry knobs apply to both verbs.
type haResourceFlags struct {
	state         string
	group         string
	comment       string
	maxRelocate   int64
	maxRestart    int64
	failback      bool
	autoRebalance bool
}

// register binds the shared HA-resource attribute flags onto cmd.
func (hf *haResourceFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&hf.state, "state", "", "requested resource state: started|stopped|disabled|ignored")
	fl.StringVar(&hf.group, "group", "", "HA group identifier")
	fl.StringVar(&hf.comment, "comment", "", "description")
	fl.Int64Var(&hf.maxRelocate, "max-relocate", 0, "maximal number of relocate tries when a resource fails to start")
	fl.Int64Var(&hf.maxRestart, "max-restart", 0, "maximal number of restart tries on a node before relocating")
	fl.BoolVar(&hf.failback, "failback", false, "migrate back to the highest-priority node when it comes online")
	fl.BoolVar(&hf.autoRebalance, "auto-rebalance", false, "allow migration during automatic rebalancing")
}

// newHaResourceCreateCmd builds `pmx cluster ha resource create <sid>`.
func newHaResourceCreateCmd() *cobra.Command {
	var (
		hf  haResourceFlags
		typ string
	)
	cmd := &cobra.Command{
		Use:   "create <sid>",
		Short: "Create an HA resource",
		Long: "Place a guest under HA management. The SID is a resource type and name separated by a " +
			"colon (vm:100 or ct:100); a bare guest ID (100) is also accepted.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			sid := args[0]

			params := &pvecluster.CreateHaResourcesParams{Sid: sid}
			fl := cmd.Flags()
			if fl.Changed("type") {
				params.Type = &typ
			}
			if fl.Changed("state") {
				params.State = &hf.state
			}
			if fl.Changed("group") {
				params.Group = &hf.group
			}
			if fl.Changed("comment") {
				params.Comment = &hf.comment
			}
			if fl.Changed("max-relocate") {
				params.MaxRelocate = &hf.maxRelocate
			}
			if fl.Changed("max-restart") {
				params.MaxRestart = &hf.maxRestart
			}
			if fl.Changed("failback") {
				params.Failback = &hf.failback
			}
			if fl.Changed("auto-rebalance") {
				params.AutoRebalance = &hf.autoRebalance
			}

			if err := deps.API.Cluster.CreateHaResources(cmd.Context(), params); err != nil {
				return fmt.Errorf("create HA resource %q: %w", sid, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("HA resource %q created.", sid)}, deps.Format)
		},
	}
	hf.register(cmd)
	cmd.Flags().StringVar(&typ, "type", "", "resource type: vm|ct")
	return cmd
}

// newHaResourceSetCmd builds `pmx cluster ha resource set <sid>`.
func newHaResourceSetCmd() *cobra.Command {
	var (
		hf     haResourceFlags
		delete string
		digest string
	)
	cmd := &cobra.Command{
		Use:   "set <sid>",
		Short: "Update an HA resource",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			sid := args[0]

			params := &pvecluster.UpdateHaResourcesParams{}
			fl := cmd.Flags()
			if fl.Changed("state") {
				params.State = &hf.state
			}
			if fl.Changed("group") {
				params.Group = &hf.group
			}
			if fl.Changed("comment") {
				params.Comment = &hf.comment
			}
			if fl.Changed("max-relocate") {
				params.MaxRelocate = &hf.maxRelocate
			}
			if fl.Changed("max-restart") {
				params.MaxRestart = &hf.maxRestart
			}
			if fl.Changed("failback") {
				params.Failback = &hf.failback
			}
			if fl.Changed("auto-rebalance") {
				params.AutoRebalance = &hf.autoRebalance
			}
			if fl.Changed("delete") {
				params.Delete = &delete
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}

			if err := deps.API.Cluster.UpdateHaResources(cmd.Context(), sid, params); err != nil {
				return fmt.Errorf("update HA resource %q: %w", sid, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("HA resource %q updated.", sid)}, deps.Format)
		},
	}
	hf.register(cmd)
	cmd.Flags().StringVar(&delete, "delete", "", "comma-separated list of settings to reset to their default")
	cmd.Flags().StringVar(&digest, "digest", "", "prevent changes if the configuration digest differs")
	return cmd
}

// newHaResourceDeleteCmd builds `pmx cluster ha resource delete <sid>`.
func newHaResourceDeleteCmd() *cobra.Command {
	var (
		yes   bool
		purge bool
	)
	cmd := &cobra.Command{
		Use:   "delete <sid>",
		Short: "Remove a resource from HA management",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			sid := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete HA resource %q without --yes", sid)
			}

			params := &pvecluster.DeleteHaResourcesParams{}
			if cmd.Flags().Changed("purge") {
				params.Purge = &purge
			}

			if err := deps.API.Cluster.DeleteHaResources(cmd.Context(), sid, params); err != nil {
				return fmt.Errorf("delete HA resource %q: %w", sid, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("HA resource %q deleted.", sid)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove the resource from any HA rules that reference it")
	return cmd
}

// newHaResourceMigrateCmd builds `pmx cluster ha resource migrate <sid>`.
func newHaResourceMigrateCmd() *cobra.Command {
	var node string
	cmd := &cobra.Command{
		Use:   "migrate <sid>",
		Short: "Request a manual migration of an HA resource",
		Long: "Ask the HA manager to live-migrate the resource to --target-node, preserving its " +
			"running state. The command reports the requested node and any resources that block or " +
			"co-migrate with it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			sid := args[0]
			if !cmd.Flags().Changed("target-node") {
				return fmt.Errorf("--target-node is required: provide the destination node name")
			}

			resp, err := deps.API.Cluster.CreateHaResourcesMigrate(cmd.Context(), sid,
				&pvecluster.CreateHaResourcesMigrateParams{Node: node})
			if err != nil {
				return fmt.Errorf("migrate HA resource %q: %w", sid, err)
			}

			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("decode migrate response for %q: %w", sid, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&node, "target-node", "", "destination node name (required)")
	return cmd
}

// newHaResourceRelocateCmd builds `pmx cluster ha resource relocate <sid>`.
func newHaResourceRelocateCmd() *cobra.Command {
	var node string
	cmd := &cobra.Command{
		Use:   "relocate <sid>",
		Short: "Request a manual relocation of an HA resource",
		Long: "Ask the HA manager to relocate the resource to --target-node by stopping it and " +
			"starting it on the target. The command reports the requested node and any resources " +
			"that block or co-relocate with it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			sid := args[0]
			if !cmd.Flags().Changed("target-node") {
				return fmt.Errorf("--target-node is required: provide the destination node name")
			}

			resp, err := deps.API.Cluster.CreateHaResourcesRelocate(cmd.Context(), sid,
				&pvecluster.CreateHaResourcesRelocateParams{Node: node})
			if err != nil {
				return fmt.Errorf("relocate HA resource %q: %w", sid, err)
			}

			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("decode relocate response for %q: %w", sid, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&node, "target-node", "", "destination node name (required)")
	return cmd
}
