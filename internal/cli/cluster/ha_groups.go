package cluster

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newHaGroupCmd builds the `pmx cluster ha group` sub-tree: HA group management.
// A group pins HA resources to a preferred set of nodes with optional priorities.
func newHaGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "group",
		Aliases: []string{"groups"},
		Short:   "Manage HA groups",
		Long: "List, create, inspect, update, and delete HA groups. A group binds HA resources to a " +
			"preferred set of nodes (with optional priorities) and controls failback behavior.",
	}
	cmd.AddCommand(
		newHaGroupListCmd(),
		newHaGroupGetCmd(),
		newHaGroupCreateCmd(),
		newHaGroupSetCmd(),
		newHaGroupDeleteCmd(),
	)
	return cmd
}

// newHaGroupListCmd builds `pmx cluster ha group list`.
func newHaGroupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List HA groups",
		Long: "List every HA group with its member nodes, restricted and nofailback flags, " +
			"type, and comment.",
		Example: `  pmx pve cluster ha group list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.API.Cluster.ListHaGroups(cmd.Context())
			if err != nil {
				return fmt.Errorf("list HA groups: %w", err)
			}

			headers := []string{"GROUP", "NODES", "RESTRICTED", "NOFAILBACK", "TYPE", "COMMENT"}
			rows := make([][]string, 0)
			raw := make([]map[string]any, 0)
			if resp != nil {
				for _, rawGrp := range *resp {
					var m map[string]any
					if err := json.Unmarshal(rawGrp, &m); err != nil {
						return fmt.Errorf("decode HA group: %w", err)
					}
					raw = append(raw, m)
					rows = append(rows, []string{
						anyCell(m["group"]),
						anyCell(m["nodes"]),
						anyCell(m["restricted"]),
						anyCell(m["nofailback"]),
						anyCell(m["type"]),
						anyCell(m["comment"]),
					})
				}
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: raw}, deps.Format)
		},
	}
}

// newHaGroupGetCmd builds `pmx cluster ha group get <group>`.
func newHaGroupGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <group>",
		Short: "Show a single HA group",
		Long: "Show one HA group's full configuration: its member nodes and priorities, " +
			"restricted and nofailback flags, and comment.",
		Example: `  pmx pve cluster ha group get preferred-nodes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			group := args[0]

			resp, err := deps.API.Cluster.GetHaGroups(cmd.Context(), group)
			if err != nil {
				return fmt.Errorf("get HA group %q: %w", group, err)
			}

			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("decode HA group %q: %w", group, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

// haGroupFlags collects the mutable HA-group attributes shared by create and set.
type haGroupFlags struct {
	nodes      string
	comment    string
	nofailback bool
	restricted bool
}

// register binds the shared HA-group attribute flags onto cmd.
func (gf *haGroupFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&gf.nodes, "nodes", "",
		"comma-separated node list with optional priorities, e.g. node1:2,node2:1")
	fl.StringVar(&gf.comment, "comment", "", "description")
	fl.BoolVar(&gf.nofailback, "nofailback", false,
		"do not migrate back to the highest-priority node when it comes online")
	fl.BoolVar(&gf.restricted, "restricted", false,
		"resources may only run on the nodes defined by this group")
}

// newHaGroupCreateCmd builds `pmx cluster ha group create <group>`.
func newHaGroupCreateCmd() *cobra.Command {
	var (
		gf  haGroupFlags
		typ string
	)
	cmd := &cobra.Command{
		Use:   "create <group>",
		Short: "Create an HA group",
		Long: "Create an HA group that binds resources to a preferred set of nodes. --nodes is " +
			"required and lists the member nodes, each with an optional priority (node:priority).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			group := args[0]
			if !cmd.Flags().Changed("nodes") {
				return fmt.Errorf("--nodes is required: provide the member node list, e.g. --nodes node1:2,node2")
			}

			params := &pvecluster.CreateHaGroupsParams{Group: group, Nodes: gf.nodes}
			fl := cmd.Flags()
			if fl.Changed("comment") {
				params.Comment = &gf.comment
			}
			if fl.Changed("nofailback") {
				params.Nofailback = &gf.nofailback
			}
			if fl.Changed("restricted") {
				params.Restricted = &gf.restricted
			}
			if fl.Changed("type") {
				params.Type = &typ
			}

			if err := deps.API.Cluster.CreateHaGroups(cmd.Context(), params); err != nil {
				return fmt.Errorf("create HA group %q: %w", group, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("HA group %q created.", group)}, deps.Format)
		},
	}
	gf.register(cmd)
	cmd.Flags().StringVar(&typ, "type", "", "group type")
	return cmd
}

// newHaGroupSetCmd builds `pmx cluster ha group set <group>`.
func newHaGroupSetCmd() *cobra.Command {
	var (
		gf     haGroupFlags
		delete string
		digest string
	)
	cmd := &cobra.Command{
		Use:   "set <group>",
		Short: "Update an HA group",
		Long: "Update an HA group's member nodes, comment, or failback and restricted behavior. " +
			"Only the flags you pass are changed; --delete resets named settings to their default.",
		Example: `  pmx pve cluster ha group set preferred-nodes --nodes node1:2,node2:1
  pmx pve cluster ha group set preferred-nodes --nofailback`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			group := args[0]

			params := &pvecluster.UpdateHaGroupsParams{}
			fl := cmd.Flags()
			if fl.Changed("nodes") {
				params.Nodes = &gf.nodes
			}
			if fl.Changed("comment") {
				params.Comment = &gf.comment
			}
			if fl.Changed("nofailback") {
				params.Nofailback = &gf.nofailback
			}
			if fl.Changed("restricted") {
				params.Restricted = &gf.restricted
			}
			if fl.Changed("delete") {
				params.Delete = &delete
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}

			if err := deps.API.Cluster.UpdateHaGroups(cmd.Context(), group, params); err != nil {
				return fmt.Errorf("update HA group %q: %w", group, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("HA group %q updated.", group)}, deps.Format)
		},
	}
	gf.register(cmd)
	cmd.Flags().StringVar(&delete, "delete", "", "comma-separated list of settings to reset to their default")
	cmd.Flags().StringVar(&digest, "digest", "", "prevent changes if the configuration digest differs")
	return cmd
}

// newHaGroupDeleteCmd builds `pmx cluster ha group delete <group>`.
func newHaGroupDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <group>",
		Short:   "Delete an HA group",
		Long:    "Delete an HA group. Refuses to run without --yes.",
		Example: `  pmx pve cluster ha group delete preferred-nodes --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			group := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete HA group %q without --yes", group)
			}

			if err := deps.API.Cluster.DeleteHaGroups(cmd.Context(), group); err != nil {
				return fmt.Errorf("delete HA group %q: %w", group, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("HA group %q deleted.", group)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// newHaRuleCmd builds the `pmx cluster ha rule` sub-tree: HA rule management.
// Rules express node- or resource-affinity constraints over HA resources.
func newHaRuleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rule",
		Aliases: []string{"rules"},
		Short:   "Manage HA rules",
		Long: "List, create, inspect, update, and delete HA rules. A rule constrains where HA " +
			"resources run: node-affinity rules pin resources to nodes, and resource-affinity rules " +
			"keep resources together or apart.",
	}
	cmd.AddCommand(
		newHaRuleListCmd(),
		newHaRuleGetCmd(),
		newHaRuleCreateCmd(),
		newHaRuleSetCmd(),
		newHaRuleDeleteCmd(),
	)
	return cmd
}

// newHaRuleListCmd builds `pmx cluster ha rule list`.
func newHaRuleListCmd() *cobra.Command {
	var (
		resource string
		typ      string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List HA rules",
		Long: "List HA rules with their type, affinity, resources, nodes, and enabled state. " +
			"Filter with --resource to show only rules affecting a resource, or --type to show " +
			"only node-affinity or resource-affinity rules.",
		Example: `  pmx pve cluster ha rule list
  pmx pve cluster ha rule list --type node-affinity
  pmx pve cluster ha rule list --resource vm:100`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pvecluster.ListHaRulesParams{}
			fl := cmd.Flags()
			if fl.Changed("resource") {
				params.Resource = &resource
			}
			if fl.Changed("type") {
				params.Type = &typ
			}

			resp, err := deps.API.Cluster.ListHaRules(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list HA rules: %w", err)
			}

			headers := []string{"RULE", "TYPE", "AFFINITY", "RESOURCES", "NODES", "STRICT", "DISABLE", "COMMENT"}
			rows := make([][]string, 0)
			raw := make([]map[string]any, 0)
			if resp != nil {
				for _, rawRule := range *resp {
					var m map[string]any
					if err := json.Unmarshal(rawRule, &m); err != nil {
						return fmt.Errorf("decode HA rule: %w", err)
					}
					raw = append(raw, m)
					rows = append(rows, []string{
						anyCell(m["rule"]),
						anyCell(m["type"]),
						anyCell(m["affinity"]),
						anyCell(m["resources"]),
						anyCell(m["nodes"]),
						anyCell(m["strict"]),
						anyCell(m["disable"]),
						anyCell(m["comment"]),
					})
				}
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: raw}, deps.Format)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&resource, "resource", "", "only list rules affecting this resource, e.g. vm:100")
	fl.StringVar(&typ, "type", "", "only list rules of this type: node-affinity|resource-affinity")
	return cmd
}

// newHaRuleGetCmd builds `pmx cluster ha rule get <rule>`.
func newHaRuleGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <rule>",
		Short: "Show a single HA rule",
		Long: "Show one HA rule's full configuration, including every field the rule sets " +
			"(resources, nodes, affinity, strict, and disable).",
		Example: `  pmx pve cluster ha rule get keep-web-together`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			rule := args[0]

			// The typed client method decodes only the rule id and type; fetch the
			// raw object so every configured field (resources, nodes, affinity, …)
			// is surfaced.
			path := fmt.Sprintf("/cluster/ha/rules/%s", rule)
			data, err := deps.API.Raw.GetCtx(cmd.Context(), path, nil)
			if err != nil {
				return fmt.Errorf("get HA rule %q: %w", rule, err)
			}
			single, raw, err := objectToSingle(data)
			if err != nil {
				return fmt.Errorf("decode HA rule %q: %w", rule, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

// haRuleFlags collects the mutable HA-rule attributes shared by create and set.
type haRuleFlags struct {
	affinity  string
	comment   string
	nodes     string
	resources string
	disable   bool
	strict    bool
}

// register binds the shared HA-rule attribute flags onto cmd.
func (rf *haRuleFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&rf.affinity, "affinity", "",
		"resource-affinity direction: positive (keep together) | negative (keep apart)")
	fl.StringVar(&rf.comment, "comment", "", "description")
	fl.StringVar(&rf.nodes, "nodes", "",
		"comma-separated node list with optional priorities (node-affinity rules)")
	fl.StringVar(&rf.resources, "resources", "",
		"comma-separated HA resource IDs, e.g. vm:100,ct:101")
	fl.BoolVar(&rf.disable, "disable", false, "create the rule in a disabled state")
	fl.BoolVar(&rf.strict, "strict", false, "enforce the rule strictly rather than as a preference")
}

// newHaRuleCreateCmd builds `pmx cluster ha rule create <rule>`.
func newHaRuleCreateCmd() *cobra.Command {
	var (
		rf  haRuleFlags
		typ string
	)
	cmd := &cobra.Command{
		Use:   "create <rule>",
		Short: "Create an HA rule",
		Long: "Create an HA rule. --type selects node-affinity or resource-affinity, and --resources " +
			"lists the HA resource IDs the rule applies to. Node-affinity rules also take --nodes; " +
			"resource-affinity rules take --affinity.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			rule := args[0]
			fl := cmd.Flags()
			if !fl.Changed("type") {
				return fmt.Errorf("--type is required: node-affinity|resource-affinity")
			}
			if !fl.Changed("resources") {
				return fmt.Errorf("--resources is required: provide the HA resource IDs, e.g. vm:100,ct:101")
			}

			params := &pvecluster.CreateHaRulesParams{Rule: rule, Type: typ, Resources: rf.resources}
			if fl.Changed("affinity") {
				params.Affinity = &rf.affinity
			}
			if fl.Changed("comment") {
				params.Comment = &rf.comment
			}
			if fl.Changed("nodes") {
				params.Nodes = &rf.nodes
			}
			if fl.Changed("disable") {
				params.Disable = &rf.disable
			}
			if fl.Changed("strict") {
				params.Strict = &rf.strict
			}

			if err := deps.API.Cluster.CreateHaRules(cmd.Context(), params); err != nil {
				return fmt.Errorf("create HA rule %q: %w", rule, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("HA rule %q created.", rule)}, deps.Format)
		},
	}
	rf.register(cmd)
	cmd.Flags().StringVar(&typ, "type", "", "rule type: node-affinity|resource-affinity (required)")
	return cmd
}

// newHaRuleSetCmd builds `pmx cluster ha rule set <rule>`.
func newHaRuleSetCmd() *cobra.Command {
	var (
		rf     haRuleFlags
		typ    string
		delete string
		digest string
	)
	cmd := &cobra.Command{
		Use:   "set <rule>",
		Short: "Update an HA rule",
		Long: "Update an HA rule. --type is required because it identifies the rule kind the other " +
			"attributes are validated against.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			rule := args[0]
			fl := cmd.Flags()
			if !fl.Changed("type") {
				return fmt.Errorf("--type is required: node-affinity|resource-affinity")
			}

			params := &pvecluster.UpdateHaRulesParams{Type: typ}
			if fl.Changed("affinity") {
				params.Affinity = &rf.affinity
			}
			if fl.Changed("comment") {
				params.Comment = &rf.comment
			}
			if fl.Changed("nodes") {
				params.Nodes = &rf.nodes
			}
			if fl.Changed("resources") {
				params.Resources = &rf.resources
			}
			if fl.Changed("disable") {
				params.Disable = &rf.disable
			}
			if fl.Changed("strict") {
				params.Strict = &rf.strict
			}
			if fl.Changed("delete") {
				params.Delete = &delete
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}

			if err := deps.API.Cluster.UpdateHaRules(cmd.Context(), rule, params); err != nil {
				return fmt.Errorf("update HA rule %q: %w", rule, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("HA rule %q updated.", rule)}, deps.Format)
		},
	}
	rf.register(cmd)
	cmd.Flags().StringVar(&typ, "type", "", "rule type: node-affinity|resource-affinity (required)")
	cmd.Flags().StringVar(&delete, "delete", "", "comma-separated list of settings to reset to their default")
	cmd.Flags().StringVar(&digest, "digest", "", "prevent changes if the configuration digest differs")
	return cmd
}

// newHaRuleDeleteCmd builds `pmx cluster ha rule delete <rule>`.
func newHaRuleDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <rule>",
		Short:   "Delete an HA rule",
		Long:    "Delete an HA rule. Refuses to run without --yes.",
		Example: `  pmx pve cluster ha rule delete keep-web-together --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			rule := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete HA rule %q without --yes", rule)
			}

			if err := deps.API.Cluster.DeleteHaRules(cmd.Context(), rule); err != nil {
				return fmt.Errorf("delete HA rule %q: %w", rule, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("HA rule %q deleted.", rule)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}
