package cluster

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newFirewallCmd builds the `pve cluster firewall` sub-tree: cluster-wide rules,
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
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
			"(ACCEPT|DROP|REJECT or a security group name) are required.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
		newClusterFirewallGroupCreateCmd(),
		newClusterFirewallGroupDeleteCmd(),
		newClusterFirewallGroupRulesCmd(),
		newClusterFirewallGroupRuleAddCmd(),
		newClusterFirewallGroupRuleUpdateCmd(),
		newClusterFirewallGroupRuleDeleteCmd(),
	)
	return cmd
}

func newClusterFirewallGroupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List security groups",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
		Use:   "delete <group>",
		Short: "Delete a security group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
			"(ACCEPT|DROP|REJECT or a security group name) are required.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
	}
	cmd.AddCommand(
		newClusterFirewallIpsetListCmd(),
		newClusterFirewallIpsetCreateCmd(),
		newClusterFirewallIpsetDeleteCmd(),
		newClusterFirewallIpsetAddCmd(),
		newClusterFirewallIpsetRemoveCmd(),
	)
	return cmd
}

func newClusterFirewallIpsetListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [name]",
		Short: "List IP sets, or the members of one IP set",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)

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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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

func newClusterFirewallIpsetRemoveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <name> <cidr>",
		Short: "Remove a CIDR entry from an IP set",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
	}
	cmd.AddCommand(
		newClusterFirewallAliasListCmd(),
		newClusterFirewallAliasCreateCmd(),
		newClusterFirewallAliasUpdateCmd(),
		newClusterFirewallAliasDeleteCmd(),
	)
	return cmd
}

func newClusterFirewallAliasListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the cluster firewall aliases",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
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
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
		Use:   "delete <name>",
		Short: "Delete a firewall address alias",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
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
	}
	cmd.AddCommand(
		newClusterFirewallOptionsGetCmd(),
		newClusterFirewallOptionsSetCmd(),
	)
	return cmd
}

func newClusterFirewallOptionsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the datacenter firewall options",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
			resp, err := deps.API.Cluster.ListFirewallOptions(cmd.Context())
			if err != nil {
				return fmt.Errorf("get cluster firewall options: %w", err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("get cluster firewall options: %w", err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

func newClusterFirewallOptionsSetCmd() *cobra.Command {
	var (
		enable        int64
		ebtables      bool
		policyIn      string
		policyOut     string
		policyForward string
		logRatelimit  string
		del           string
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set the datacenter firewall options",
		Long:  "Update the cluster-wide firewall options. Only the flags you pass are changed.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
			fl := cmd.Flags()
			if !anyFlagChanged(fl, "enable", "ebtables", "policy-in", "policy-out",
				"policy-forward", "log-ratelimit", "delete") {
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
	cmd.Flags().StringVar(&policyIn, "policy-in", "", "input policy: ACCEPT, REJECT, or DROP")
	cmd.Flags().StringVar(&policyOut, "policy-out", "", "output policy: ACCEPT, REJECT, or DROP")
	cmd.Flags().StringVar(&policyForward, "policy-forward", "", "forward policy: ACCEPT or DROP")
	cmd.Flags().StringVar(&logRatelimit, "log-ratelimit", "", "log rate-limiting settings, for example enable=1,rate=1/second")
	cmd.Flags().StringVar(&del, "delete", "", "comma-separated list of options to reset to default")
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
