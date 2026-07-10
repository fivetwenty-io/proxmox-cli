package sdn

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/optionschema"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
)

// vnetFwRuleEntry is the minimal decoded shape of one vnet firewall rule list
// entry; the full element is preserved in Raw for lossless output.
type vnetFwRuleEntry struct {
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

// vnetRuleFlags collects the shared flag values for vnet firewall rule
// create/set so the two commands stay in sync. Values are forwarded only when
// their flag is changed.
type vnetRuleFlags struct {
	action   string
	ruleType string
	source   string
	dest     string
	proto    string
	dport    string
	sport    string
	iface    string
	macro    string
	log      string
	comment  string
	icmpType string
	enable   int64
	pos      int64
	moveto   int64
	del      string
	digest   string
}

// vnetRuleSetFlagNames lists every editable rule attribute flag, used by `set`
// to detect a no-op update.
var vnetRuleSetFlagNames = []string{
	"action", "type", "source", "dest", "proto", "dport", "sport", "iface",
	"macro", "log", "comment", "icmp-type", "enable", "moveto", "delete",
}

// register binds the shared rule attribute flags onto cmd.
func (f *vnetRuleFlags) register(cmd *cobra.Command, withPos, withMoveto, withDelete bool) {
	fl := cmd.Flags()
	fl.StringVar(&f.ruleType, "type", "", "rule direction: in, out, or forward")
	fl.StringVar(&f.action, "action", "", "ACCEPT, DROP, REJECT, or a security group name")
	fl.StringVar(&f.source, "source", "", "restrict source address, IP set (+name), or alias")
	fl.StringVar(&f.dest, "dest", "", "restrict destination address, IP set (+name), or alias")
	fl.StringVar(&f.proto, "proto", "", "IP protocol, for example tcp or udp")
	fl.StringVar(&f.dport, "dport", "", "destination port or range, for example 80 or 80:85")
	fl.StringVar(&f.sport, "sport", "", "source port or range")
	fl.StringVar(&f.iface, "iface", "", "network interface, for example net0")
	fl.StringVar(&f.macro, "macro", "", "predefined standard macro")
	fl.StringVar(&f.log, "log", "", "log level: emerg, alert, crit, err, warning, notice, info, debug, or nolog")
	fl.StringVar(&f.comment, "comment", "", "descriptive comment")
	fl.StringVar(&f.icmpType, "icmp-type", "", "icmp-type (only valid if proto is icmp or icmpv6)")
	fl.Int64Var(&f.enable, "enable", 1, "1 to enable the rule, 0 to disable it")
	if withPos {
		fl.Int64Var(&f.pos, "pos", 0, "insert the rule at this position")
	}
	if withMoveto {
		fl.Int64Var(&f.moveto, "moveto", 0, "move the rule to this position (other arguments ignored)")
	}
	if withDelete {
		fl.StringVar(&f.del, "delete", "", "comma-separated list of rule settings to clear")
	}
	fl.StringVar(&f.digest, "digest", "", "digest guarding against concurrent modification")
}

// applyCreate forwards changed flags onto a create params struct.
func (f *vnetRuleFlags) applyCreate(fl *cobra.Command, p *cluster.CreateSdnVnetsFirewallRulesParams) {
	c := fl.Flags()
	if c.Changed("source") {
		p.Source = strPtr(f.source)
	}
	if c.Changed("dest") {
		p.Dest = strPtr(f.dest)
	}
	if c.Changed("proto") {
		p.Proto = strPtr(f.proto)
	}
	if c.Changed("dport") {
		p.Dport = strPtr(f.dport)
	}
	if c.Changed("sport") {
		p.Sport = strPtr(f.sport)
	}
	if c.Changed("iface") {
		p.Iface = strPtr(f.iface)
	}
	if c.Changed("macro") {
		p.Macro = strPtr(f.macro)
	}
	if c.Changed("log") {
		p.Log = strPtr(f.log)
	}
	if c.Changed("comment") {
		p.Comment = strPtr(f.comment)
	}
	if c.Changed("icmp-type") {
		p.IcmpType = strPtr(f.icmpType)
	}
	if c.Changed("enable") {
		p.Enable = int64Ptr(f.enable)
	}
	if c.Changed("pos") {
		p.Pos = int64Ptr(f.pos)
	}
	if c.Changed("digest") {
		p.Digest = strPtr(f.digest)
	}
}

// applyUpdate forwards changed flags onto an update params struct.
func (f *vnetRuleFlags) applyUpdate(fl *cobra.Command, p *cluster.UpdateSdnVnetsFirewallRulesParams) {
	c := fl.Flags()
	if c.Changed("type") {
		p.Type = strPtr(f.ruleType)
	}
	if c.Changed("action") {
		p.Action = strPtr(f.action)
	}
	if c.Changed("source") {
		p.Source = strPtr(f.source)
	}
	if c.Changed("dest") {
		p.Dest = strPtr(f.dest)
	}
	if c.Changed("proto") {
		p.Proto = strPtr(f.proto)
	}
	if c.Changed("dport") {
		p.Dport = strPtr(f.dport)
	}
	if c.Changed("sport") {
		p.Sport = strPtr(f.sport)
	}
	if c.Changed("iface") {
		p.Iface = strPtr(f.iface)
	}
	if c.Changed("macro") {
		p.Macro = strPtr(f.macro)
	}
	if c.Changed("log") {
		p.Log = strPtr(f.log)
	}
	if c.Changed("comment") {
		p.Comment = strPtr(f.comment)
	}
	if c.Changed("icmp-type") {
		p.IcmpType = strPtr(f.icmpType)
	}
	if c.Changed("enable") {
		p.Enable = int64Ptr(f.enable)
	}
	if c.Changed("moveto") {
		p.Moveto = int64Ptr(f.moveto)
	}
	if c.Changed("delete") {
		p.Delete = strPtr(f.del)
	}
	if c.Changed("digest") {
		p.Digest = strPtr(f.digest)
	}
}

// newVnetFirewallCmd builds `pmx sdn vnet firewall` and its sub-commands: the
// per-vnet firewall rules and forward-policy options. Like all SDN edits, rule
// changes are staged until `pmx sdn apply`.
func newVnetFirewallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "firewall",
		Short: "Manage a vnet's firewall (rules and forward options)",
		Long: "Inspect and edit the firewall attached to an SDN vnet: its rule set " +
			"and the forward-policy options. Rule changes are staged until `pmx pve sdn apply`.",
	}
	cmd.AddCommand(newVnetFirewallRulesCmd(), newVnetFirewallOptionsCmd())
	return cmd
}

// ---- vnet firewall rules ---------------------------------------------------

func newVnetFirewallRulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Manage a vnet's firewall rules",
		Long: "List, show, append, update, and delete the ordered firewall rules attached " +
			"to an SDN vnet. Rules are matched top-to-bottom by position. Changes are " +
			"staged until committed with `pmx pve sdn apply`.",
	}
	cmd.AddCommand(
		newVnetFirewallRulesListCmd(),
		newVnetFirewallRulesGetCmd(),
		newVnetFirewallRulesCreateCmd(),
		newVnetFirewallRulesSetCmd(),
		newVnetFirewallRulesDeleteCmd(),
	)
	return cmd
}

func newVnetFirewallRulesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <vnet>",
		Short: "List a vnet's firewall rules",
		Long: "List every firewall rule attached to a vnet, in position order, with type, " +
			"action, and match criteria.",
		Example: `  pmx pve sdn vnet firewall rules list vnet1`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			resp, err := deps.API.Cluster.ListSdnVnetsFirewallRules(cmd.Context(), vnet)
			if err != nil {
				return fmt.Errorf("list firewall rules for vnet %q: %w", vnet, err)
			}
			var raws []json.RawMessage
			if resp != nil {
				raws = *resp
			}
			headers := []string{"POS", "TYPE", "ACTION", "PROTO", "SOURCE", "DEST", "DPORT", "ENABLE", "COMMENT"}
			entries := make([]vnetFwRuleEntry, 0, len(raws))
			rows := make([][]string, 0, len(raws))
			for _, raw := range raws {
				var e vnetFwRuleEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode firewall rule entry: %w", err)
				}
				entries = append(entries, e)
				rows = append(rows, []string{
					strconv.FormatInt(e.Pos, 10), e.Type, e.Action, e.Proto,
					e.Source, e.Dest, e.Dport, strconv.FormatInt(e.Enable, 10), e.Comment,
				})
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
}

func newVnetFirewallRulesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <vnet> <pos>",
		Short:   "Show a single vnet firewall rule by position",
		Long:    "Show one firewall rule of a vnet, identified by its position index.",
		Example: `  pmx pve sdn vnet firewall rules get vnet1 0`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet, pos := args[0], args[1]
			// The typed client method cannot decode this endpoint: PVE returns
			// `pos` as a string here while the generated struct expects int64.
			// Fetch the raw object instead and render it generically.
			path := fmt.Sprintf("/cluster/sdn/vnets/%s/firewall/rules/%s", vnet, pos)
			data, err := deps.API.Raw.GetCtx(cmd.Context(), path, nil)
			if err != nil {
				return fmt.Errorf("get firewall rule %s for vnet %q: %w", pos, vnet, err)
			}
			single, raw, err := objectToSingle(data)
			if err != nil {
				return fmt.Errorf("get firewall rule %s for vnet %q: %w", pos, vnet, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

func newVnetFirewallRulesCreateCmd() *cobra.Command {
	var f vnetRuleFlags
	cmd := &cobra.Command{
		Use:   "create <vnet>",
		Short: "Append a rule to a vnet's firewall",
		Long: "Create a new vnet firewall rule. --type (in|out|forward) and --action " +
			"(ACCEPT|DROP|REJECT or a security group name) are required. The change " +
			"is staged until `pmx pve sdn apply`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			params := &cluster.CreateSdnVnetsFirewallRulesParams{Action: f.action, Type: f.ruleType}
			f.applyCreate(cmd, params)
			if err := deps.API.Cluster.CreateSdnVnetsFirewallRules(cmd.Context(), vnet, params); err != nil {
				return fmt.Errorf("create firewall rule for vnet %q: %w", vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"Firewall rule added to vnet %q (run `pmx sdn apply` to commit).", vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f.register(cmd, true, false, false)
	cli.MustMarkRequired(cmd, "type")
	cli.MustMarkRequired(cmd, "action")
	return cmd
}

func newVnetFirewallRulesSetCmd() *cobra.Command {
	var f vnetRuleFlags
	cmd := &cobra.Command{
		Use:   "set <vnet> <pos>",
		Short: "Modify a vnet firewall rule by position",
		Long:  "Update a vnet firewall rule. Only the flags you pass are changed. The change is staged until `pmx pve sdn apply`.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet, pos := args[0], args[1]
			if !anyFlagChanged(cmd.Flags(), append(vnetRuleSetFlagNames, "digest")...) {
				return fmt.Errorf("no changes requested: pass at least one field flag")
			}
			params := &cluster.UpdateSdnVnetsFirewallRulesParams{}
			f.applyUpdate(cmd, params)
			if err := deps.API.Cluster.UpdateSdnVnetsFirewallRules(cmd.Context(), vnet, pos, params); err != nil {
				return fmt.Errorf("update firewall rule %s for vnet %q: %w", pos, vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"Firewall rule %s in vnet %q updated (run `pmx sdn apply` to commit).", pos, vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f.register(cmd, false, true, true)
	return cmd
}

func newVnetFirewallRulesDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <vnet> <pos>",
		Short: "Delete a vnet firewall rule by position",
		Long: "Delete one firewall rule of a vnet, identified by its position index. Refuses " +
			"to run without --yes. The change is staged until `pmx pve sdn apply` commits it.",
		Example: `  pmx pve sdn vnet firewall rules delete vnet1 0 --yes`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet, pos := args[0], args[1]
			if !yes {
				return fmt.Errorf("refusing to delete firewall rule %s in vnet %q without confirmation: pass --yes", pos, vnet)
			}
			err := deps.API.Cluster.DeleteSdnVnetsFirewallRules(cmd.Context(), vnet, pos,
				&cluster.DeleteSdnVnetsFirewallRulesParams{})
			if err != nil {
				return fmt.Errorf("delete firewall rule %s for vnet %q: %w", pos, vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"Firewall rule %s in vnet %q deleted (run `pmx sdn apply` to commit).", pos, vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// ---- vnet firewall options -------------------------------------------------

func newVnetFirewallOptionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "options",
		Short: "Manage a vnet's firewall options",
		Long: "Show, update, and describe the forward-policy options of a vnet's firewall " +
			"(distinct from its rule set). Changes are staged until committed with " +
			"`pmx pve sdn apply`.",
	}
	cmd.AddCommand(
		newVnetFirewallOptionsGetCmd(),
		newVnetFirewallOptionsSetCmd(),
		newVnetFirewallOptionsDescribeCmd(),
	)
	return cmd
}

// newVnetFirewallOptionsDescribeCmd builds `pmx sdn vnet firewall options
// describe`, an offline catalog of every settable vnet firewall option from
// the PVE API schema (see vnet_firewall_options_schema_gen.go).
func newVnetFirewallOptionsDescribeCmd() *cobra.Command {
	return optionschema.NewDescribeCmd(optionschema.DescribeConfig{
		Schemas: vnetFirewallOptionSchemas,
		Short:   "Describe all settable vnet firewall options and their defaults",
		Long: "List every settable vnet firewall option from the PVE API schema: type, " +
			"built-in default, and allowed values. Runs offline.",
		CommandHint:         "pmx sdn vnet firewall options describe",
		SubKeyRowsInCatalog: true,
	})
}

func newVnetFirewallOptionsGetCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "get <vnet>",
		Short: "Show a vnet's firewall options",
		Long: "Show a vnet's firewall options currently set. The PVE API omits options " +
			"left at their built-in defaults; pass --defaults to also list those with " +
			"the value they effectively have.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			resp, err := deps.API.Cluster.ListSdnVnetsFirewallOptions(cmd.Context(), vnet)
			if err != nil {
				return fmt.Errorf("get firewall options for vnet %q: %w", vnet, err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("get firewall options for vnet %q: %w", vnet, err)
			}
			if withDefaults {
				single, raw = optionschema.MergeDefaults(vnetFirewallOptionSchemas, single, raw, optionschema.MergeOpts{})
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"also list unset options with their built-in default values")
	return cmd
}

func newVnetFirewallOptionsSetCmd() *cobra.Command {
	var (
		enable          bool
		logLevelForward string
		policyForward   string
		del             string
		digest          string
	)
	cmd := &cobra.Command{
		Use:   "set <vnet>",
		Short: "Set a vnet's firewall options",
		Long: "Update a vnet's firewall options: enable/disable rule enforcement and " +
			"the forward policy. Only the flags you pass are changed. The change is " +
			"staged until `pmx pve sdn apply`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			fl := cmd.Flags()
			if !anyFlagChanged(fl, "enable", "log-level-forward", "policy-forward", "delete") {
				return fmt.Errorf("no options to set: pass at least one option flag")
			}
			params := &cluster.UpdateSdnVnetsFirewallOptionsParams{}
			if fl.Changed("enable") {
				params.Enable = boolPtr(enable)
			}
			if fl.Changed("log-level-forward") {
				params.LogLevelForward = strPtr(logLevelForward)
			}
			if fl.Changed("policy-forward") {
				params.PolicyForward = strPtr(policyForward)
			}
			if fl.Changed("delete") {
				params.Delete = strPtr(del)
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}
			if err := deps.API.Cluster.UpdateSdnVnetsFirewallOptions(cmd.Context(), vnet, params); err != nil {
				return fmt.Errorf("set firewall options for vnet %q: %w", vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"Firewall options for vnet %q updated (run `pmx sdn apply` to commit).", vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&enable, "enable", false, "enable or disable firewall rule enforcement on this vnet")
	f.StringVar(&logLevelForward, "log-level-forward", "", "log level for forwarded traffic")
	f.StringVar(&policyForward, "policy-forward", "", "forward policy")
	f.StringVar(&del, "delete", "", "comma-separated list of settings to delete")
	f.StringVar(&digest, "digest", "", "digest guarding against concurrent modification")

	// Append generated schema detail (allowed values, defaults) to each option
	// flag's help text; see vnet_firewall_options_schema_gen.go.
	optionschema.EnrichFlags(f, vnetFirewallOptionSchemas)
	return cmd
}
