package cluster

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/optionschema"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newFirewallCmd builds the `pmx cluster firewall` sub-tree: cluster-wide rules,
// security groups, IP sets, address aliases, and the datacenter firewall
// options. Every operation is synchronous (no task UPID).
func newFirewallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "firewall",
		Short: "Manage the cluster-wide firewall",
		Long: "Inspect and edit the datacenter firewall: cluster rules, security " +
			"groups, IP sets, address aliases, and the global firewall options that " +
			"govern policy and logging.",
	}
	cmd.AddCommand(
		newClusterFirewallRulesCmd(),
		newClusterFirewallGroupCmd(),
		newClusterFirewallIpsetCmd(),
		newClusterFirewallAliasCmd(),
		newClusterFirewallOptionsCmd(),
		newClusterFirewallMacrosCmd(),
		newClusterFirewallRefsCmd(),
	)
	return cmd
}

// fwRuleEntry is the minimal decoded shape of one firewall rule list entry.
type fwRuleEntry struct {
	Pos     int64  `json:"pos"`
	Type    string `json:"type"`
	Action  string `json:"action"`
	Proto   string `json:"proto"`
	Source  string `json:"source"`
	Dest    string `json:"dest"`
	Dport   string `json:"dport"`
	Enable  int64  `json:"enable"`
	Comment string `json:"comment"`
}

func fwRuleHeaders() []string {
	return []string{"POS", "TYPE", "ACTION", "PROTO", "SOURCE", "DEST", "DPORT", "ENABLE", "COMMENT"}
}

func fwRuleRow(e fwRuleEntry) []string {
	return []string{
		strconv.FormatInt(e.Pos, 10),
		e.Type, e.Action, e.Proto, e.Source, e.Dest, e.Dport,
		strconv.FormatInt(e.Enable, 10), e.Comment,
	}
}

// decodeRuleList renders a slice of raw rule objects as a table.
func decodeRuleList(raws []json.RawMessage) ([]string, [][]string, []fwRuleEntry, error) {
	entries := make([]fwRuleEntry, 0, len(raws))
	rows := make([][]string, 0, len(raws))
	for _, raw := range raws {
		var e fwRuleEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, nil, nil, fmt.Errorf("decode firewall rule entry: %w", err)
		}
		entries = append(entries, e)
		rows = append(rows, fwRuleRow(e))
	}
	return fwRuleHeaders(), rows, entries, nil
}

// clusterRuleFlags collects the shared flag values for rule create/update so the
// two commands stay in sync.
type clusterRuleFlags struct {
	action, ruleType, source, dest, proto, dport, sport, iface, macro, logLevel, comment string
	icmpType, digest                                                                     string
	enable, pos, moveto                                                                  int64
	del                                                                                  string
}

func (f *clusterRuleFlags) register(cmd *cobra.Command, withPos, withMoveto, withDelete bool) {
	cmd.Flags().StringVar(&f.ruleType, "type", "", "rule direction: in, out, or group")
	cmd.Flags().StringVar(&f.action, "action", "", "ACCEPT, DROP, REJECT, or a security group name")
	cmd.Flags().StringVar(&f.source, "source", "", "restrict source address, IP set (+name), or alias")
	cmd.Flags().StringVar(&f.dest, "dest", "", "restrict destination address, IP set (+name), or alias")
	cmd.Flags().StringVar(&f.proto, "proto", "", "IP protocol, for example tcp or udp")
	cmd.Flags().StringVar(&f.dport, "dport", "", "destination port or range, for example 80 or 80:85")
	cmd.Flags().StringVar(&f.sport, "sport", "", "source port or range")
	cmd.Flags().StringVar(&f.iface, "iface", "", "network interface, for example net0")
	cmd.Flags().StringVar(&f.macro, "macro", "", "predefined standard macro")
	cmd.Flags().StringVar(&f.logLevel, "log", "", "log level: emerg, alert, crit, err, warning, notice, info, debug, or nolog")
	cmd.Flags().StringVar(&f.comment, "comment", "", "descriptive comment")
	cmd.Flags().StringVar(&f.icmpType, "icmp-type", "",
		"ICMP type, valid only when --proto is icmp or icmpv6/ipv6-icmp")
	cmd.Flags().StringVar(&f.digest, "digest", "",
		"reject the change unless the current config matches this SHA-1 digest")
	cmd.Flags().Int64Var(&f.enable, "enable", 1, "1 to enable the rule, 0 to disable it")
	if withPos {
		cmd.Flags().Int64Var(&f.pos, "pos", 0, "insert the rule at this position")
	}
	if withMoveto {
		cmd.Flags().Int64Var(&f.moveto, "moveto", 0, "move the rule to this position (other arguments ignored)")
	}
	if withDelete {
		cmd.Flags().StringVar(&f.del, "delete", "", "comma-separated list of rule settings to clear")
	}
}

// ---- cluster rules ---------------------------------------------------------

func newClusterFirewallRulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Manage cluster-wide firewall rules",
		Long: "Manage the ordered list of cluster-wide firewall rules that apply across " +
			"the whole datacenter. Rules are addressed by their integer position and " +
			"evaluated from top to bottom.",
	}
	cmd.AddCommand(
		newClusterFirewallRulesListCmd(),
		newClusterFirewallRulesGetCmd(),
		newClusterFirewallRulesCreateCmd(),
		newClusterFirewallRulesUpdateCmd(),
		newClusterFirewallRulesDeleteCmd(),
	)
	return cmd
}

func newClusterFirewallRulesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the cluster firewall rules",
		Long: "List the cluster-wide firewall rules in evaluation order, showing each " +
			"rule's position, direction, action, protocol, source, destination, " +
			"destination port, enabled flag, and comment.",
		Example: `  pmx pve cluster firewall rules list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListFirewallRules(cmd.Context())
			if err != nil {
				return fmt.Errorf("list cluster firewall rules: %w", err)
			}
			var raws []json.RawMessage
			if resp != nil {
				raws = *resp
			}
			headers, rows, entries, err := decodeRuleList(raws)
			if err != nil {
				return err
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
}

func newClusterFirewallRulesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <pos>",
		Short: "Show a single cluster firewall rule by position",
		Long: "Show every configured field of a single cluster firewall rule, identified " +
			"by its integer position in the rule list.",
		Example: `  pmx pve cluster firewall rules get 0`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			pos := args[0]
			// The typed client method cannot decode this endpoint: PVE returns
			// `pos` as a string here while the generated struct expects int64.
			// Fetch the raw object instead and render it generically.
			path := fmt.Sprintf("/cluster/firewall/rules/%s", pos)
			data, err := deps.API.Raw.GetCtx(cmd.Context(), path, nil)
			if err != nil {
				return fmt.Errorf("get cluster firewall rule %s: %w", pos, err)
			}
			single, raw, err := objectToSingle(data)
			if err != nil {
				return fmt.Errorf("get cluster firewall rule %s: %w", pos, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

func newClusterFirewallRulesCreateCmd() *cobra.Command {
	var f clusterRuleFlags
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Append a rule to the cluster firewall",
		Long: "Create a new cluster firewall rule. --type (in|out|group) and --action " +
			"(ACCEPT|DROP|REJECT or a security group name) are required. Use --pos to " +
			"insert the rule at a specific position instead of appending it.",
		Example: `  pmx pve cluster firewall rules create --type in --action ACCEPT --proto tcp --dport 22
  pmx pve cluster firewall rules create --type in --action DROP --source 10.0.0.0/8`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !cmd.Flags().Changed("type") {
				return fmt.Errorf("--type is required: one of in, out, or group")
			}
			if !cmd.Flags().Changed("action") {
				return fmt.Errorf("--action is required: ACCEPT, DROP, REJECT, or a security group name")
			}

			params := &pvecluster.CreateFirewallRulesParams{Action: f.action, Type: f.ruleType}
			applyRuleCreateFlags(cmd, &f, params)

			if err := deps.API.Cluster.CreateFirewallRules(cmd.Context(), params); err != nil {
				return fmt.Errorf("create cluster firewall rule: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: "Cluster firewall rule added."}, deps.Format)
		},
	}
	f.register(cmd, true, false, false)
	return cmd
}

func applyRuleCreateFlags(cmd *cobra.Command, f *clusterRuleFlags, params *pvecluster.CreateFirewallRulesParams) {
	fl := cmd.Flags()
	if fl.Changed("source") {
		params.Source = &f.source
	}
	if fl.Changed("dest") {
		params.Dest = &f.dest
	}
	if fl.Changed("proto") {
		params.Proto = &f.proto
	}
	if fl.Changed("dport") {
		params.Dport = &f.dport
	}
	if fl.Changed("sport") {
		params.Sport = &f.sport
	}
	if fl.Changed("iface") {
		params.Iface = &f.iface
	}
	if fl.Changed("macro") {
		params.Macro = &f.macro
	}
	if fl.Changed("log") {
		params.Log = &f.logLevel
	}
	if fl.Changed("comment") {
		params.Comment = &f.comment
	}
	if fl.Changed("icmp-type") {
		params.IcmpType = &f.icmpType
	}
	if fl.Changed("digest") {
		params.Digest = &f.digest
	}
	if fl.Changed("enable") {
		params.Enable = &f.enable
	}
	if fl.Changed("pos") {
		params.Pos = &f.pos
	}
}

func newClusterFirewallRulesUpdateCmd() *cobra.Command {
	var f clusterRuleFlags
	cmd := &cobra.Command{
		Use:   "update <pos>",
		Short: "Modify a cluster firewall rule by position",
		Long: "Modify an existing cluster firewall rule identified by its integer " +
			"position. Only the flags you pass are changed. Use --moveto to reposition " +
			"the rule and --delete to clear a comma-separated list of settings.",
		Example: `  pmx pve cluster firewall rules update 0 --action DROP
  pmx pve cluster firewall rules update 0 --moveto 2`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			pos := args[0]

			params := &pvecluster.UpdateFirewallRulesParams{}
			applyRuleUpdateFlags(cmd, &f, params)

			if err := deps.API.Cluster.UpdateFirewallRules(cmd.Context(), pos, params); err != nil {
				return fmt.Errorf("update cluster firewall rule %s: %w", pos, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Cluster firewall rule %s updated.", pos)}, deps.Format)
		},
	}
	f.register(cmd, false, true, true)
	return cmd
}

func applyRuleUpdateFlags(cmd *cobra.Command, f *clusterRuleFlags, params *pvecluster.UpdateFirewallRulesParams) {
	fl := cmd.Flags()
	if fl.Changed("type") {
		params.Type = &f.ruleType
	}
	if fl.Changed("action") {
		params.Action = &f.action
	}
	if fl.Changed("source") {
		params.Source = &f.source
	}
	if fl.Changed("dest") {
		params.Dest = &f.dest
	}
	if fl.Changed("proto") {
		params.Proto = &f.proto
	}
	if fl.Changed("dport") {
		params.Dport = &f.dport
	}
	if fl.Changed("sport") {
		params.Sport = &f.sport
	}
	if fl.Changed("iface") {
		params.Iface = &f.iface
	}
	if fl.Changed("macro") {
		params.Macro = &f.macro
	}
	if fl.Changed("log") {
		params.Log = &f.logLevel
	}
	if fl.Changed("comment") {
		params.Comment = &f.comment
	}
	if fl.Changed("icmp-type") {
		params.IcmpType = &f.icmpType
	}
	if fl.Changed("digest") {
		params.Digest = &f.digest
	}
	if fl.Changed("enable") {
		params.Enable = &f.enable
	}
	if fl.Changed("moveto") {
		params.Moveto = &f.moveto
	}
	if fl.Changed("delete") {
		params.Delete = &f.del
	}
}

func newClusterFirewallRulesDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <pos>",
		Short: "Delete a cluster firewall rule by position",
		Long: "Delete a cluster firewall rule identified by its integer position. " +
			"Refuses to run without --yes/-y.",
		Example: `  pmx pve cluster firewall rules delete 0 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			pos := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete cluster firewall rule %s without confirmation: pass --yes/-y", pos)
			}
			if err := deps.API.Cluster.DeleteFirewallRules(cmd.Context(), pos, &pvecluster.DeleteFirewallRulesParams{}); err != nil {
				return fmt.Errorf("delete cluster firewall rule %s: %w", pos, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Cluster firewall rule %s deleted.", pos)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// ---- security groups -------------------------------------------------------

// fwGroupEntry is the minimal decoded shape of one security-group list entry.
type fwGroupEntry struct {
	Group   string `json:"group"`
	Comment string `json:"comment"`
	Digest  string `json:"digest"`
}

func newClusterFirewallGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "group",
		Short: "Manage cluster firewall security groups",
		Long: "Manage reusable security groups and the rules they contain. A group " +
			"can be referenced from a rule's --action to apply its rule set.",
	}
	cmd.AddCommand(
		newClusterFirewallGroupListCmd(),
		newClusterFirewallGroupGetCmd(),
		newClusterFirewallGroupCreateCmd(),
		newClusterFirewallGroupDeleteCmd(),
		newClusterFirewallGroupRulesCmd(),
		newClusterFirewallGroupRuleAddCmd(),
		newClusterFirewallGroupRuleUpdateCmd(),
		newClusterFirewallGroupRuleDeleteCmd(),
	)
	return cmd
}

func newClusterFirewallGroupGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <group> <pos>",
		Short: "Show a single rule from a security group by position",
		Long: "Show every configured field of a single rule within a security group, " +
			"identified by the group name and the rule's integer position.",
		Example: `  pmx pve cluster firewall group get webservers 0`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			group, pos := args[0], args[1]
			resp, err := deps.API.Cluster.GetFirewallGroups2(cmd.Context(), group, pos)
			if err != nil {
				return fmt.Errorf("get rule %s in security group %q: %w", pos, group, err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("decode rule %s in security group %q: %w", pos, group, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

func newClusterFirewallGroupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List security groups",
		Long: "List the defined firewall security groups, showing each group's name, " +
			"comment, and configuration digest.",
		Example: `  pmx pve cluster firewall group list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListFirewallGroups(cmd.Context())
			if err != nil {
				return fmt.Errorf("list cluster firewall security groups: %w", err)
			}
			headers := []string{"GROUP", "COMMENT", "DIGEST"}
			entries := make([]fwGroupEntry, 0)
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e fwGroupEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode security group entry: %w", err)
					}
					entries = append(entries, e)
					rows = append(rows, []string{e.Group, e.Comment, e.Digest})
				}
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
}

func newClusterFirewallGroupCreateCmd() *cobra.Command {
	var (
		comment string
		rename  string
	)
	cmd := &cobra.Command{
		Use:   "create <group>",
		Short: "Create or rename a security group",
		Long: "Create a new security group, or rename an existing one with --rename. " +
			"Pass --comment to set a description; set --rename equal to the current " +
			"name to update only the comment.",
		Example: `  pmx pve cluster firewall group create webservers --comment "Web tier"
  pmx pve cluster firewall group create webservers --rename web`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			group := args[0]
			params := &pvecluster.CreateFirewallGroupsParams{Group: group}
			if cmd.Flags().Changed("comment") {
				params.Comment = &comment
			}
			if cmd.Flags().Changed("rename") {
				params.Rename = &rename
			}
			if err := deps.API.Cluster.CreateFirewallGroups(cmd.Context(), params); err != nil {
				return fmt.Errorf("create security group %q: %w", group, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Security group %s created.", group)}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "descriptive comment")
	cmd.Flags().StringVar(&rename, "rename", "", "rename an existing group (set equal to <group> to only update the comment)")
	return cmd
}

func newClusterFirewallGroupDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <group>",
		Short:   "Delete a security group",
		Long:    "Delete a security group by name. Refuses to run without --yes/-y.",
		Example: `  pmx pve cluster firewall group delete webservers --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			group := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete security group %q without confirmation: pass --yes/-y", group)
			}
			if err := deps.API.Cluster.DeleteFirewallGroups(cmd.Context(), group); err != nil {
				return fmt.Errorf("delete security group %q: %w", group, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Security group %s deleted.", group)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

func newClusterFirewallGroupRulesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rules <group>",
		Short: "List the rules in a security group",
		Long: "List the rules contained in a security group in evaluation order, showing " +
			"each rule's position, direction, action, protocol, source, destination, " +
			"destination port, enabled flag, and comment.",
		Example: `  pmx pve cluster firewall group rules webservers`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			group := args[0]
			resp, err := deps.API.Cluster.GetFirewallGroups(cmd.Context(), group)
			if err != nil {
				return fmt.Errorf("list rules in security group %q: %w", group, err)
			}
			var raws []json.RawMessage
			if resp != nil {
				raws = *resp
			}
			headers, rows, entries, err := decodeRuleList(raws)
			if err != nil {
				return err
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
}

func newClusterFirewallGroupRuleAddCmd() *cobra.Command {
	var f clusterRuleFlags
	cmd := &cobra.Command{
		Use:   "rule-add <group>",
		Short: "Append a rule to a security group",
		Long: "Add a rule to a security group. --type (in|out|group) and --action " +
			"(ACCEPT|DROP|REJECT or a security group name) are required. Use --pos to " +
			"insert the rule at a specific position instead of appending it.",
		Example: `  pmx pve cluster firewall group rule-add webservers --type in --action ACCEPT --proto tcp --dport 443`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			group := args[0]
			if !cmd.Flags().Changed("type") {
				return fmt.Errorf("--type is required: one of in, out, or group")
			}
			if !cmd.Flags().Changed("action") {
				return fmt.Errorf("--action is required: ACCEPT, DROP, REJECT, or a security group name")
			}

			params := &pvecluster.CreateFirewallGroups2Params{Action: f.action, Type: f.ruleType}
			applyGroupRuleAddFlags(cmd, &f, params)

			if err := deps.API.Cluster.CreateFirewallGroups2(cmd.Context(), group, params); err != nil {
				return fmt.Errorf("add rule to security group %q: %w", group, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Rule added to security group %s.", group)}, deps.Format)
		},
	}
	f.register(cmd, true, false, false)
	return cmd
}

func applyGroupRuleAddFlags(cmd *cobra.Command, f *clusterRuleFlags, params *pvecluster.CreateFirewallGroups2Params) {
	fl := cmd.Flags()
	if fl.Changed("source") {
		params.Source = &f.source
	}
	if fl.Changed("dest") {
		params.Dest = &f.dest
	}
	if fl.Changed("proto") {
		params.Proto = &f.proto
	}
	if fl.Changed("dport") {
		params.Dport = &f.dport
	}
	if fl.Changed("sport") {
		params.Sport = &f.sport
	}
	if fl.Changed("iface") {
		params.Iface = &f.iface
	}
	if fl.Changed("macro") {
		params.Macro = &f.macro
	}
	if fl.Changed("log") {
		params.Log = &f.logLevel
	}
	if fl.Changed("comment") {
		params.Comment = &f.comment
	}
	if fl.Changed("icmp-type") {
		params.IcmpType = &f.icmpType
	}
	if fl.Changed("digest") {
		params.Digest = &f.digest
	}
	if fl.Changed("enable") {
		params.Enable = &f.enable
	}
	if fl.Changed("pos") {
		params.Pos = &f.pos
	}
}

func newClusterFirewallGroupRuleUpdateCmd() *cobra.Command {
	var f clusterRuleFlags
	cmd := &cobra.Command{
		Use:   "rule-update <group> <pos>",
		Short: "Modify a rule in a security group by position",
		Long: "Modify a rule within a security group, identified by the group name and " +
			"the rule's integer position. Only the flags you pass are changed. Use " +
			"--moveto to reposition the rule and --delete to clear a comma-separated " +
			"list of settings.",
		Example: `  pmx pve cluster firewall group rule-update webservers 0 --action DROP`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			group, pos := args[0], args[1]

			params := &pvecluster.UpdateFirewallGroupsParams{}
			applyGroupRuleUpdateFlags(cmd, &f, params)

			if err := deps.API.Cluster.UpdateFirewallGroups(cmd.Context(), group, pos, params); err != nil {
				return fmt.Errorf("update rule %s in security group %q: %w", pos, group, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Rule %s in security group %s updated.", pos, group)}, deps.Format)
		},
	}
	f.register(cmd, false, true, true)
	return cmd
}

func applyGroupRuleUpdateFlags(cmd *cobra.Command, f *clusterRuleFlags, params *pvecluster.UpdateFirewallGroupsParams) {
	fl := cmd.Flags()
	if fl.Changed("type") {
		params.Type = &f.ruleType
	}
	if fl.Changed("action") {
		params.Action = &f.action
	}
	if fl.Changed("source") {
		params.Source = &f.source
	}
	if fl.Changed("dest") {
		params.Dest = &f.dest
	}
	if fl.Changed("proto") {
		params.Proto = &f.proto
	}
	if fl.Changed("dport") {
		params.Dport = &f.dport
	}
	if fl.Changed("sport") {
		params.Sport = &f.sport
	}
	if fl.Changed("iface") {
		params.Iface = &f.iface
	}
	if fl.Changed("macro") {
		params.Macro = &f.macro
	}
	if fl.Changed("log") {
		params.Log = &f.logLevel
	}
	if fl.Changed("comment") {
		params.Comment = &f.comment
	}
	if fl.Changed("icmp-type") {
		params.IcmpType = &f.icmpType
	}
	if fl.Changed("digest") {
		params.Digest = &f.digest
	}
	if fl.Changed("enable") {
		params.Enable = &f.enable
	}
	if fl.Changed("moveto") {
		params.Moveto = &f.moveto
	}
	if fl.Changed("delete") {
		params.Delete = &f.del
	}
}

func newClusterFirewallGroupRuleDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rule-delete <group> <pos>",
		Short: "Delete a rule from a security group by position",
		Long: "Delete a rule from a security group, identified by the group name and the " +
			"rule's integer position. Refuses to run without --yes/-y.",
		Example: `  pmx pve cluster firewall group rule-delete webservers 0 --yes`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			group, pos := args[0], args[1]
			if !yes {
				return fmt.Errorf("refusing to delete rule %s from security group %q without confirmation: pass --yes/-y", pos, group)
			}
			if err := deps.API.Cluster.DeleteFirewallGroups2(cmd.Context(), group, pos, &pvecluster.DeleteFirewallGroups2Params{}); err != nil {
				return fmt.Errorf("delete rule %s from security group %q: %w", pos, group, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Rule %s deleted from security group %s.", pos, group)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// ---- ipset -----------------------------------------------------------------

// fwIpsetEntry is the minimal decoded shape of one IP set list/member entry.
type fwIpsetEntry struct {
	Name    string `json:"name"`
	Cidr    string `json:"cidr"`
	Nomatch bool   `json:"nomatch"`
	Comment string `json:"comment"`
	Digest  string `json:"digest"`
}

func newClusterFirewallIpsetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ipset",
		Short: "Manage cluster firewall IP sets",
		Long: "Manage named IP sets: reusable collections of CIDR entries that firewall " +
			"rules can reference as a source or destination through a +name reference.",
	}
	cmd.AddCommand(
		newClusterFirewallIpsetListCmd(),
		newClusterFirewallIpsetGetCmd(),
		newClusterFirewallIpsetCreateCmd(),
		newClusterFirewallIpsetDeleteCmd(),
		newClusterFirewallIpsetAddCmd(),
		newClusterFirewallIpsetUpdateCmd(),
		newClusterFirewallIpsetRemoveCmd(),
	)
	return cmd
}

func newClusterFirewallIpsetGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name> <cidr>",
		Short: "Show a single CIDR entry from an IP set",
		Long: "Show a single CIDR entry from an IP set, identified by the set name and " +
			"the CIDR value, including its nomatch flag and comment.",
		Example: `  pmx pve cluster firewall ipset get blocklist 10.0.0.0/8`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name, cidr := args[0], args[1]
			resp, err := deps.API.Cluster.GetFirewallIpset2(cmd.Context(), name, cidr)
			if err != nil {
				return fmt.Errorf("get %s from IP set %q: %w", cidr, name, err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("decode %s from IP set %q: %w", cidr, name, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

func newClusterFirewallIpsetListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [name]",
		Short: "List IP sets, or the members of one IP set",
		Long: "Without an argument, list the defined IP sets and their comments. With a " +
			"set name, list that set's CIDR members, showing each entry's CIDR, nomatch " +
			"flag, and comment.",
		Example: `  pmx pve cluster firewall ipset list
  pmx pve cluster firewall ipset list blocklist`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			var raws []json.RawMessage
			var headers []string
			member := len(args) == 1
			if member {
				resp, err := deps.API.Cluster.GetFirewallIpset(cmd.Context(), args[0])
				if err != nil {
					return fmt.Errorf("list IP set %q members: %w", args[0], err)
				}
				if resp != nil {
					raws = *resp
				}
				headers = []string{"CIDR", "NOMATCH", "COMMENT"}
			} else {
				resp, err := deps.API.Cluster.ListFirewallIpset(cmd.Context())
				if err != nil {
					return fmt.Errorf("list IP sets: %w", err)
				}
				if resp != nil {
					raws = *resp
				}
				headers = []string{"NAME", "COMMENT"}
			}

			entries := make([]fwIpsetEntry, 0, len(raws))
			rows := make([][]string, 0, len(raws))
			for _, raw := range raws {
				var e fwIpsetEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode IP set entry: %w", err)
				}
				entries = append(entries, e)
				if member {
					rows = append(rows, []string{e.Cidr, strconv.FormatBool(e.Nomatch), e.Comment})
				} else {
					rows = append(rows, []string{e.Name, e.Comment})
				}
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
}

func newClusterFirewallIpsetCreateCmd() *cobra.Command {
	var (
		comment string
		rename  string
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create or rename a firewall IP set",
		Long: "Create a new IP set, or rename an existing one with --rename. Pass " +
			"--comment to set a description; set --rename equal to the current name to " +
			"update only the comment.",
		Example: `  pmx pve cluster firewall ipset create blocklist --comment "Blocked networks"
  pmx pve cluster firewall ipset create blocklist --rename denylist`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			params := &pvecluster.CreateFirewallIpsetParams{Name: name}
			if cmd.Flags().Changed("comment") {
				params.Comment = &comment
			}
			if cmd.Flags().Changed("rename") {
				params.Rename = &rename
			}
			if err := deps.API.Cluster.CreateFirewallIpset(cmd.Context(), params); err != nil {
				return fmt.Errorf("create IP set %q: %w", name, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("IP set %s created.", name)}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "descriptive comment")
	cmd.Flags().StringVar(&rename, "rename", "", "rename an existing IP set (set equal to <name> to only update the comment)")
	return cmd
}

func newClusterFirewallIpsetDeleteCmd() *cobra.Command {
	var (
		yes   bool
		force bool
	)
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a firewall IP set",
		Long: "Delete an IP set by name. Refuses to run without --yes/-y; pass --force " +
			"to delete the set even if it still has members.",
		Example: `  pmx pve cluster firewall ipset delete blocklist --yes
  pmx pve cluster firewall ipset delete blocklist --yes --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete IP set %q without confirmation: pass --yes/-y", name)
			}
			params := &pvecluster.DeleteFirewallIpsetParams{}
			if cmd.Flags().Changed("force") {
				params.Force = &force
			}
			if err := deps.API.Cluster.DeleteFirewallIpset(cmd.Context(), name, params); err != nil {
				return fmt.Errorf("delete IP set %q: %w", name, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("IP set %s deleted.", name)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	cmd.Flags().BoolVar(&force, "force", false, "delete all members of the IP set if any remain")
	return cmd
}

func newClusterFirewallIpsetAddCmd() *cobra.Command {
	var (
		comment string
		nomatch bool
	)
	cmd := &cobra.Command{
		Use:   "add <name> <cidr>",
		Short: "Add a CIDR entry to an IP set",
		Long: "Add a CIDR entry to an IP set. Pass --nomatch to make the entry an " +
			"exclusion and --comment to describe it.",
		Example: `  pmx pve cluster firewall ipset add blocklist 10.0.0.0/8
  pmx pve cluster firewall ipset add blocklist 10.1.0.0/16 --nomatch`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name, cidr := args[0], args[1]
			params := &pvecluster.CreateFirewallIpset2Params{Cidr: cidr}
			if cmd.Flags().Changed("comment") {
				params.Comment = &comment
			}
			if cmd.Flags().Changed("nomatch") {
				params.Nomatch = &nomatch
			}
			if err := deps.API.Cluster.CreateFirewallIpset2(cmd.Context(), name, params); err != nil {
				return fmt.Errorf("add %s to IP set %q: %w", cidr, name, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("%s added to IP set %s.", cidr, name)}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "descriptive comment")
	cmd.Flags().BoolVar(&nomatch, "nomatch", false, "treat this entry as an exclusion")
	return cmd
}

func newClusterFirewallIpsetUpdateCmd() *cobra.Command {
	var (
		comment string
		nomatch bool
		digest  string
	)
	cmd := &cobra.Command{
		Use:   "update <name> <cidr>",
		Short: "Update a CIDR entry in an IP set",
		Long: "Update the comment or nomatch flag of an existing CIDR entry in an IP set, " +
			"without deleting and re-adding it. Pass at least one of --comment, " +
			"--nomatch, or --digest.",
		Example: `  pmx pve cluster firewall ipset update blocklist 10.0.0.0/8 --comment "Corp range"
  pmx pve cluster firewall ipset update blocklist 10.0.0.0/8 --nomatch`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name, cidr := args[0], args[1]
			fl := cmd.Flags()
			if !anyFlagChanged(fl, "comment", "nomatch", "digest") {
				return fmt.Errorf("no changes requested: pass --comment, --nomatch, or --digest")
			}
			params := &pvecluster.UpdateFirewallIpsetParams{}
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("nomatch") {
				params.Nomatch = &nomatch
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if err := deps.API.Cluster.UpdateFirewallIpset(cmd.Context(), name, cidr, params); err != nil {
				return fmt.Errorf("update %s in IP set %q: %w", cidr, name, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("%s updated in IP set %s.", cidr, name)}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "descriptive comment")
	cmd.Flags().BoolVar(&nomatch, "nomatch", false, "treat this entry as an exclusion")
	cmd.Flags().StringVar(&digest, "digest", "",
		"reject the change unless the current config matches this SHA-1 digest")
	return cmd
}

func newClusterFirewallIpsetRemoveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <name> <cidr>",
		Short: "Remove a CIDR entry from an IP set",
		Long: "Remove a CIDR entry from an IP set, identified by the set name and the " +
			"CIDR value. Refuses to run without --yes/-y.",
		Example: `  pmx pve cluster firewall ipset remove blocklist 10.0.0.0/8 --yes`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name, cidr := args[0], args[1]
			if !yes {
				return fmt.Errorf("refusing to remove %s from IP set %q without confirmation: pass --yes/-y", cidr, name)
			}
			if err := deps.API.Cluster.DeleteFirewallIpset2(cmd.Context(), name, cidr, &pvecluster.DeleteFirewallIpset2Params{}); err != nil {
				return fmt.Errorf("remove %s from IP set %q: %w", cidr, name, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("%s removed from IP set %s.", cidr, name)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm removal without prompting")
	return cmd
}

// ---- alias -----------------------------------------------------------------

// fwAliasEntry is the minimal decoded shape of one alias list entry.
type fwAliasEntry struct {
	Name    string `json:"name"`
	Cidr    string `json:"cidr"`
	Comment string `json:"comment"`
}

func newClusterFirewallAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Manage cluster firewall address aliases",
		Long: "Manage firewall address aliases: named shortcuts for an IP address or " +
			"CIDR that firewall rules can reference as a source or destination.",
	}
	cmd.AddCommand(
		newClusterFirewallAliasListCmd(),
		newClusterFirewallAliasGetCmd(),
		newClusterFirewallAliasCreateCmd(),
		newClusterFirewallAliasUpdateCmd(),
		newClusterFirewallAliasDeleteCmd(),
	)
	return cmd
}

func newClusterFirewallAliasGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Show a single firewall address alias",
		Long: "Show a single firewall address alias by name, including its CIDR value " +
			"and comment.",
		Example: `  pmx pve cluster firewall alias get dns-server`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			resp, err := deps.API.Cluster.GetFirewallAliases(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("get firewall alias %q: %w", name, err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("decode firewall alias %q: %w", name, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

func newClusterFirewallAliasListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the cluster firewall aliases",
		Long: "List the defined firewall address aliases, showing each alias's name, " +
			"CIDR value, and comment.",
		Example: `  pmx pve cluster firewall alias list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListFirewallAliases(cmd.Context())
			if err != nil {
				return fmt.Errorf("list cluster firewall aliases: %w", err)
			}
			headers := []string{"NAME", "CIDR", "COMMENT"}
			entries := make([]fwAliasEntry, 0)
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e fwAliasEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode alias entry: %w", err)
					}
					entries = append(entries, e)
					rows = append(rows, []string{e.Name, e.Cidr, e.Comment})
				}
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
}

func newClusterFirewallAliasCreateCmd() *cobra.Command {
	var comment string
	cmd := &cobra.Command{
		Use:   "create <name> <cidr>",
		Short: "Create a firewall address alias",
		Long: "Create a firewall address alias that maps a name to an IP address or " +
			"CIDR. Pass --comment to describe it.",
		Example: `  pmx pve cluster firewall alias create dns-server 10.0.0.53`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name, cidr := args[0], args[1]
			params := &pvecluster.CreateFirewallAliasesParams{Name: name, Cidr: cidr}
			if cmd.Flags().Changed("comment") {
				params.Comment = &comment
			}
			if err := deps.API.Cluster.CreateFirewallAliases(cmd.Context(), params); err != nil {
				return fmt.Errorf("create alias %q: %w", name, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Alias %s created.", name)}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "descriptive comment")
	return cmd
}

func newClusterFirewallAliasUpdateCmd() *cobra.Command {
	var (
		comment string
		rename  string
	)
	cmd := &cobra.Command{
		Use:   "update <name> <cidr>",
		Short: "Update a firewall address alias",
		Long: "Update a firewall address alias, setting its CIDR value to <cidr>. Pass " +
			"--rename to give the alias a new name and --comment to change its " +
			"description.",
		Example: `  pmx pve cluster firewall alias update dns-server 10.0.0.54
  pmx pve cluster firewall alias update dns-server 10.0.0.54 --rename dns`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name, cidr := args[0], args[1]
			params := &pvecluster.UpdateFirewallAliasesParams{Cidr: cidr}
			if cmd.Flags().Changed("comment") {
				params.Comment = &comment
			}
			if cmd.Flags().Changed("rename") {
				params.Rename = &rename
			}
			if err := deps.API.Cluster.UpdateFirewallAliases(cmd.Context(), name, params); err != nil {
				return fmt.Errorf("update alias %q: %w", name, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Alias %s updated.", name)}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "descriptive comment")
	cmd.Flags().StringVar(&rename, "rename", "", "rename the alias to this name")
	return cmd
}

func newClusterFirewallAliasDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <name>",
		Short:   "Delete a firewall address alias",
		Long:    "Delete a firewall address alias by name. Refuses to run without --yes/-y.",
		Example: `  pmx pve cluster firewall alias delete dns-server --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete alias %q without confirmation: pass --yes/-y", name)
			}
			if err := deps.API.Cluster.DeleteFirewallAliases(cmd.Context(), name, &pvecluster.DeleteFirewallAliasesParams{}); err != nil {
				return fmt.Errorf("delete alias %q: %w", name, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Alias %s deleted.", name)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// ---- options ---------------------------------------------------------------

func newClusterFirewallOptionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "options",
		Short: "Inspect and set the datacenter firewall options",
		Long: "Inspect and change the datacenter-wide firewall options that govern the " +
			"default input, output, and forward policies, logging, and whether the " +
			"cluster firewall and ebtables are enabled.",
	}
	cmd.AddCommand(
		newClusterFirewallOptionsGetCmd(),
		newClusterFirewallOptionsSetCmd(),
		newClusterFirewallOptionsDescribeCmd(),
	)
	return cmd
}

// newClusterFirewallOptionsDescribeCmd builds `pmx cluster firewall options
// describe`, an offline catalog of every settable datacenter firewall option
// from the PVE API schema (see firewall_options_schema_gen.go).
func newClusterFirewallOptionsDescribeCmd() *cobra.Command {
	return optionschema.NewDescribeCmd(optionschema.DescribeConfig{
		Schemas: firewallOptionSchemas,
		Short:   "Describe all settable datacenter firewall options and their defaults",
		Long: "List every settable datacenter firewall option from the PVE API schema: " +
			"type, built-in default, allowed values, and the sub-keys of dict-encoded " +
			"options. Runs offline. Pass an option name to show only that option with " +
			"full descriptions.",
		CommandHint:         "pmx pve cluster firewall options describe",
		SubKeyRowsInCatalog: true,
	})
}

func newClusterFirewallOptionsGetCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Show the datacenter firewall options",
		Long: "Show the datacenter firewall options currently set. The PVE API omits " +
			"options left at their built-in defaults; pass --defaults to also list " +
			"those with the value they effectively have.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListFirewallOptions(cmd.Context())
			if err != nil {
				return fmt.Errorf("get cluster firewall options: %w", err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("get cluster firewall options: %w", err)
			}
			if withDefaults {
				single, raw = optionschema.MergeDefaults(firewallOptionSchemas, single, raw, optionschema.MergeOpts{})
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"also list unset options with their built-in default values")
	return cmd
}

func newClusterFirewallOptionsSetCmd() *cobra.Command {
	var (
		enable        int64
		ebtables      bool
		policyIn      string
		policyOut     string
		policyForward string
		logRatelimit  string
		digest        string
		del           string
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set the datacenter firewall options",
		Long:  "Update the cluster-wide firewall options. Only the flags you pass are changed.",
		Example: `  pmx pve cluster firewall options set --enable 1
  pmx pve cluster firewall options set --policy-in DROP --policy-out ACCEPT`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			if !anyFlagChanged(fl, "enable", "ebtables", "policy-in", "policy-out",
				"policy-forward", "log-ratelimit", "digest", "delete") {
				return fmt.Errorf("no options to set: pass at least one option flag")
			}

			params := &pvecluster.UpdateFirewallOptionsParams{}
			if fl.Changed("enable") {
				params.Enable = &enable
			}
			if fl.Changed("ebtables") {
				params.Ebtables = &ebtables
			}
			if fl.Changed("policy-in") {
				params.PolicyIn = &policyIn
			}
			if fl.Changed("policy-out") {
				params.PolicyOut = &policyOut
			}
			if fl.Changed("policy-forward") {
				params.PolicyForward = &policyForward
			}
			if fl.Changed("log-ratelimit") {
				params.LogRatelimit = &logRatelimit
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if fl.Changed("delete") {
				params.Delete = &del
			}

			if err := deps.API.Cluster.UpdateFirewallOptions(cmd.Context(), params); err != nil {
				return fmt.Errorf("set cluster firewall options: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: "Cluster firewall options updated."}, deps.Format)
		},
	}
	cmd.Flags().Int64Var(&enable, "enable", 1, "1 to enable the cluster firewall, 0 to disable it")
	cmd.Flags().BoolVar(&ebtables, "ebtables", false, "enable ebtables rules cluster wide")
	cmd.Flags().StringVar(&policyIn, "policy-in", "", "input policy")
	cmd.Flags().StringVar(&policyOut, "policy-out", "", "output policy")
	cmd.Flags().StringVar(&policyForward, "policy-forward", "", "forward policy")
	cmd.Flags().StringVar(&logRatelimit, "log-ratelimit", "", "log rate-limiting settings, for example enable=1,rate=1/second")
	cmd.Flags().StringVar(&digest, "digest", "",
		"reject the change unless the current config matches this SHA-1 digest")
	cmd.Flags().StringVar(&del, "delete", "", "comma-separated list of options to reset to default")

	// Append generated schema detail (allowed values, defaults, sub-keys) to
	// each option flag's help text; see firewall_options_schema_gen.go.
	optionschema.EnrichFlags(cmd.Flags(), firewallOptionSchemas)
	return cmd
}

// newClusterFirewallMacrosCmd builds `pmx cluster firewall macros list`.
// It reads the static list of built-in firewall macros from the server. The list
// is read-only and useful when authoring rules that reference a macro name.
func newClusterFirewallMacrosCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "macros",
		Short: "List built-in firewall macros",
		Long: "List the built-in firewall macros provided by the PVE server, which can be " +
			"referenced by name from a rule's --macro flag when authoring rules.",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all built-in firewall macros",
		Long: "List all built-in firewall macros available for use in firewall rules. " +
			"The list is static and provided by the PVE server.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListFirewallMacros(cmd.Context())
			if err != nil {
				return fmt.Errorf("list firewall macros: %w", err)
			}
			entries := make([]map[string]any, 0)
			if resp != nil {
				for _, raw := range *resp {
					var m map[string]any
					if err := json.Unmarshal(raw, &m); err != nil {
						return fmt.Errorf("decode firewall macro entry: %w", err)
					}
					entries = append(entries, m)
				}
			}
			headers, rows := dynamicTable(entries)
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	})
	return cmd
}

// newClusterFirewallRefsCmd builds `pmx cluster firewall refs list`.
// It returns the valid IPSet and alias references that can be used as source or
// destination in firewall rules. The optional --type flag limits results to
// either "ipset" or "alias".
func newClusterFirewallRefsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "refs",
		Short: "List valid IP set and alias references for firewall rules",
		Long: "List the IP set and alias references that can be used as a rule's source " +
			"or destination.",
	}
	var refType string
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List IP set and alias references",
		Long: "List the IP set and alias references that can be used as source or " +
			"destination in firewall rules. Pass --type ipset or --type alias to filter.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()
			params := &pvecluster.ListFirewallRefsParams{}
			if fl.Changed("type") {
				params.Type = &refType
			}
			resp, err := deps.API.Cluster.ListFirewallRefs(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list firewall refs: %w", err)
			}
			entries := make([]map[string]any, 0)
			if resp != nil {
				for _, raw := range *resp {
					var m map[string]any
					if err := json.Unmarshal(raw, &m); err != nil {
						return fmt.Errorf("decode firewall ref entry: %w", err)
					}
					entries = append(entries, m)
				}
			}
			headers, rows := dynamicTable(entries)
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
	listCmd.Flags().StringVar(&refType, "type", "", "filter by reference type: ipset or alias")
	cmd.AddCommand(listCmd)
	return cmd
}

// anyFlagChanged reports whether at least one of the named flags was set.
func anyFlagChanged(fl interface{ Changed(string) bool }, names ...string) bool {
	for _, n := range names {
		if fl.Changed(n) {
			return true
		}
	}
	return false
}
