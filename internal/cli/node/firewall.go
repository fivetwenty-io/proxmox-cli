package node

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/optionschema"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newFirewallCmd builds the `pmx pve node firewall` sub-tree: the host firewall
// rules and the per-node firewall options. Unlike the cluster and per-guest
// firewalls, a node firewall has no IP sets, aliases, or security groups — only
// rules and options. Every operation is synchronous (no task UPID).
func newFirewallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "firewall",
		Short: "Manage the host firewall of a node",
		Long: "Inspect and edit the firewall of the resolved node: the host rules " +
			"and the node firewall options that govern host protection and logging.",
	}
	cmd.AddCommand(
		newNodeFirewallRulesCmd(),
		newNodeFirewallOptionsCmd(),
		newNodeFirewallLogCmd(),
	)
	return cmd
}

// fwLogEntry is the decoded shape of one firewall log list entry: a line number
// and the log text.
type fwLogEntry struct {
	N int64  `json:"n"`
	T string `json:"t"`
}

func newNodeFirewallLogCmd() *cobra.Command {
	var (
		limit int64
		since int64
		start int64
		until int64
	)
	cmd := &cobra.Command{
		Use:     "log",
		Short:   "Read the host firewall log",
		Long:    "Read the firewall log of the resolved node. Use --start and --limit to page through entries.",
		Example: `  pmx pve node firewall log --limit 50`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			fl := cmd.Flags()
			params := &nodes.ListFirewallLogParams{}
			if fl.Changed("limit") {
				params.Limit = &limit
			}
			if fl.Changed("since") {
				params.Since = &since
			}
			if fl.Changed("start") {
				params.Start = &start
			}
			if fl.Changed("until") {
				params.Until = &until
			}
			resp, err := deps.API.Nodes.ListFirewallLog(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("read firewall log on node %q: %w", deps.Node, err)
			}
			entries := make([]fwLogEntry, 0)
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e fwLogEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode firewall log entry: %w", err)
					}
					entries = append(entries, e)
					rows = append(rows, []string{strconv.FormatInt(e.N, 10), e.T})
				}
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: []string{"N", "LINE"}, Rows: rows, Raw: entries}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&limit, "limit", 0, "maximum number of log lines to return")
	f.Int64Var(&since, "since", 0, "only return entries newer than this UNIX epoch timestamp")
	f.Int64Var(&start, "start", 0, "line number to start reading from (for paging)")
	f.Int64Var(&until, "until", 0, "only return entries older than this UNIX epoch timestamp")
	return cmd
}

// requireNode returns a clear error when no node could be resolved from the
// --node flag, PMX_NODE, or the configured default.
func requireNode(deps *cli.Deps) error {
	if deps.Node == "" {
		return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
	}
	return nil
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

// nodeRuleFlags collects the shared flag values for rule create/update so the
// two commands stay in sync.
type nodeRuleFlags struct {
	action, ruleType, source, dest, proto, dport, sport string
	iface, macro, logLevel, icmpType, comment           string
	digest                                              string
	enable, pos, moveto                                 int64
	del                                                 string
}

func (f *nodeRuleFlags) register(cmd *cobra.Command, withPos, withMoveto, withDelete bool) {
	cmd.Flags().StringVar(&f.ruleType, "type", "", "rule direction: in, out, or group")
	cmd.Flags().StringVar(&f.action, "action", "", "ACCEPT, DROP, REJECT, or a security group name")
	cmd.Flags().StringVar(&f.source, "source", "", "restrict source address, IP set (+name), or alias")
	cmd.Flags().StringVar(&f.dest, "dest", "", "restrict destination address, IP set (+name), or alias")
	cmd.Flags().StringVar(&f.proto, "proto", "", "IP protocol, for example tcp or udp")
	cmd.Flags().StringVar(&f.dport, "dport", "", "destination port or range, for example 80 or 80:85")
	cmd.Flags().StringVar(&f.sport, "sport", "", "source port or range")
	cmd.Flags().StringVar(&f.iface, "iface", "", "network interface, for example vmbr0")
	cmd.Flags().StringVar(&f.macro, "macro", "", "predefined standard macro")
	cmd.Flags().StringVar(&f.icmpType, "icmp-type", "", "icmp type, only valid when --proto is icmp or icmpv6")
	cmd.Flags().StringVar(&f.logLevel, "log", "", "log level: emerg, alert, crit, err, warning, notice, info, debug, or nolog")
	cmd.Flags().StringVar(&f.comment, "comment", "", "descriptive comment")
	cmd.Flags().Int64Var(&f.enable, "enable", 1, "1 to enable the rule, 0 to disable it")
	cmd.Flags().StringVar(&f.digest, "digest", "",
		"SHA1 digest of the current rules to guard against concurrent edits")
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

// ---- node rules ------------------------------------------------------------

func newNodeFirewallRulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Manage the host firewall rules",
		Long:  "List, inspect, create, update, and delete the firewall rules of the resolved node.",
	}
	cmd.AddCommand(
		newNodeFirewallRulesListCmd(),
		newNodeFirewallRulesGetCmd(),
		newNodeFirewallRulesCreateCmd(),
		newNodeFirewallRulesUpdateCmd(),
		newNodeFirewallRulesDeleteCmd(),
	)
	return cmd
}

func newNodeFirewallRulesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List the host firewall rules",
		Long:    "List every firewall rule configured on the resolved node, in rule-evaluation order.",
		Example: `  pmx pve node firewall rules list`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListFirewallRules(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("list firewall rules on node %q: %w", deps.Node, err)
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

func newNodeFirewallRulesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <pos>",
		Short:   "Show a single host firewall rule by position",
		Long:    "Show a single firewall rule of the resolved node by its position in the rule list.",
		Example: `  pmx pve node firewall rules get 0`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			pos := args[0]
			// The typed client method cannot decode this endpoint: PVE returns
			// `pos` as a string here while the generated struct expects int64.
			// Fetch the raw object instead and render it generically.
			path := fmt.Sprintf("/nodes/%s/firewall/rules/%s", deps.Node, pos)
			data, err := deps.API.Raw.GetCtx(cmd.Context(), path, nil)
			if err != nil {
				return fmt.Errorf("get firewall rule %s on node %q: %w", pos, deps.Node, err)
			}
			single, raw, err := objectToSingle(data)
			if err != nil {
				return fmt.Errorf("get firewall rule %s on node %q: %w", pos, deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

func newNodeFirewallRulesCreateCmd() *cobra.Command {
	var f nodeRuleFlags
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Append a rule to the host firewall",
		Long: "Create a new host firewall rule. --type (in|out|group) and --action " +
			"(ACCEPT|DROP|REJECT or a security group name) are required.",
		Example: `  pmx pve node firewall rules create --type in --action ACCEPT --source 10.0.0.0/24 --dport 22`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if !cmd.Flags().Changed("type") {
				return fmt.Errorf("--type is required: one of in, out, or group")
			}
			if !cmd.Flags().Changed("action") {
				return fmt.Errorf("--action is required: ACCEPT, DROP, REJECT, or a security group name")
			}

			params := &nodes.CreateFirewallRulesParams{Action: f.action, Type: f.ruleType}
			applyNodeRuleCreateFlags(cmd, &f, params)

			if err := deps.API.Nodes.CreateFirewallRules(cmd.Context(), deps.Node, params); err != nil {
				return fmt.Errorf("create firewall rule on node %q: %w", deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Firewall rule added on node %q.", deps.Node)}, deps.Format)
		},
	}
	f.register(cmd, true, false, false)
	return cmd
}

func applyNodeRuleCreateFlags(cmd *cobra.Command, f *nodeRuleFlags, params *nodes.CreateFirewallRulesParams) {
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
	if fl.Changed("icmp-type") {
		params.IcmpType = &f.icmpType
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
	if fl.Changed("digest") {
		params.Digest = &f.digest
	}
	if fl.Changed("pos") {
		params.Pos = &f.pos
	}
}

func newNodeFirewallRulesUpdateCmd() *cobra.Command {
	var f nodeRuleFlags
	cmd := &cobra.Command{
		Use:   "update <pos>",
		Short: "Modify a host firewall rule by position",
		Long: "Update the host firewall rule at the given position. Only the flags you pass " +
			"are changed; pass --moveto to relocate the rule to a different position instead " +
			"(other flags are ignored when moving), or --delete to clear specific settings.",
		Example: `  pmx pve node firewall rules update 0 --comment "allow ssh"
  pmx pve node firewall rules update 0 --moveto 2`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			pos := args[0]

			params := &nodes.UpdateFirewallRulesParams{}
			applyNodeRuleUpdateFlags(cmd, &f, params)

			if err := deps.API.Nodes.UpdateFirewallRules(cmd.Context(), deps.Node, pos, params); err != nil {
				return fmt.Errorf("update firewall rule %s on node %q: %w", pos, deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Firewall rule %s updated on node %q.", pos, deps.Node)}, deps.Format)
		},
	}
	f.register(cmd, false, true, true)
	return cmd
}

func applyNodeRuleUpdateFlags(cmd *cobra.Command, f *nodeRuleFlags, params *nodes.UpdateFirewallRulesParams) {
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
	if fl.Changed("icmp-type") {
		params.IcmpType = &f.icmpType
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
	if fl.Changed("digest") {
		params.Digest = &f.digest
	}
	if fl.Changed("moveto") {
		params.Moveto = &f.moveto
	}
	if fl.Changed("delete") {
		params.Delete = &f.del
	}
}

func newNodeFirewallRulesDeleteCmd() *cobra.Command {
	var (
		yes    bool
		digest string
	)
	cmd := &cobra.Command{
		Use:   "delete <pos>",
		Short: "Delete a host firewall rule by position",
		Long: "Permanently delete the host firewall rule at the given position. Refuses to " +
			"run without --yes/-y.",
		Example: `  pmx pve node firewall rules delete 0 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			pos := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete firewall rule %s without confirmation: pass --yes/-y", pos)
			}
			params := &nodes.DeleteFirewallRulesParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}
			if err := deps.API.Nodes.DeleteFirewallRules(cmd.Context(), deps.Node, pos, params); err != nil {
				return fmt.Errorf("delete firewall rule %s on node %q: %w", pos, deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Firewall rule %s deleted on node %q.", pos, deps.Node)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	cmd.Flags().StringVar(&digest, "digest", "",
		"SHA1 digest of the current rules to guard against concurrent edits")
	return cmd
}

// ---- node options ----------------------------------------------------------

func newNodeFirewallOptionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "options",
		Short: "Inspect and set the host firewall options",
		Long: "Show or update the firewall options that govern the resolved node's host " +
			"protection and logging, and browse the offline catalog of every settable " +
			"option with 'describe'.",
	}
	cmd.AddCommand(
		newNodeFirewallOptionsGetCmd(),
		newNodeFirewallOptionsSetCmd(),
		newNodeFirewallOptionsDescribeCmd(),
	)
	return cmd
}

// newNodeFirewallOptionsDescribeCmd builds `pmx pve node firewall options
// describe`, an offline catalog of every settable host firewall option from
// the PVE API schema (see firewall_options_schema_gen.go).
func newNodeFirewallOptionsDescribeCmd() *cobra.Command {
	return optionschema.NewDescribeCmd(optionschema.DescribeConfig{
		Schemas: firewallOptionSchemas,
		Short:   "Describe all settable node firewall options and their defaults",
		Long: "List every settable host firewall option from the PVE API schema: " +
			"type, built-in default, allowed values, and the sub-keys of dict-encoded " +
			"options. Runs offline. Pass an option name to show only that option with " +
			"full descriptions.",
		CommandHint:         "pmx pve node firewall options describe",
		SubKeyRowsInCatalog: true,
	})
}

func newNodeFirewallOptionsGetCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Show the host firewall options",
		Long: "Show the host firewall options currently set on the resolved node. The PVE " +
			"API omits options left at their built-in defaults; pass --defaults to also " +
			"list those with the value they effectively have.",
		Example: `  pmx pve node firewall options get`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListFirewallOptions(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("get firewall options on node %q: %w", deps.Node, err)
			}
			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("get firewall options on node %q: %w", deps.Node, err)
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

func newNodeFirewallOptionsSetCmd() *cobra.Command {
	var (
		enable                  bool
		ndp                     bool
		nftables                bool
		nosmurfs                bool
		tcpflags                bool
		logNfConntrack          bool
		nfConntrackAllowInvalid bool
		protectionSynflood      bool
		logLevelIn              string
		logLevelOut             string
		logLevelForward         string
		nfConntrackHelpers      string
		smurfLogLevel           string
		tcpFlagsLogLevel        string
		nfConntrackMax          int64
		nfConntrackEstablished  int64
		nfConntrackSynRecv      int64
		synfloodBurst           int64
		synfloodRate            int64
		digest                  string
		del                     string
	)
	optionFlags := []string{
		"enable", "ndp", "nftables", "nosmurfs", "tcpflags", "log-nf-conntrack",
		"nf-conntrack-allow-invalid", "protection-synflood", "log-level-in",
		"log-level-out", "log-level-forward", "nf-conntrack-helpers",
		"smurf-log-level", "tcp-flags-log-level", "nf-conntrack-max",
		"nf-conntrack-tcp-timeout-established", "nf-conntrack-tcp-timeout-syn-recv",
		"protection-synflood-burst", "protection-synflood-rate", "digest", "delete",
	}
	cmd := &cobra.Command{
		Use:     "set",
		Short:   "Set the host firewall options",
		Long:    "Update the host firewall options on the resolved node. Only the flags you pass are changed.",
		Example: `  pmx pve node firewall options set --enable`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			fl := cmd.Flags()
			if !anyFlagChanged(fl, optionFlags...) {
				return fmt.Errorf("no options to set: pass at least one option flag")
			}

			params := &nodes.UpdateFirewallOptionsParams{}
			if fl.Changed("enable") {
				params.Enable = &enable
			}
			if fl.Changed("ndp") {
				params.Ndp = &ndp
			}
			if fl.Changed("nftables") {
				params.Nftables = &nftables
			}
			if fl.Changed("nosmurfs") {
				params.Nosmurfs = &nosmurfs
			}
			if fl.Changed("tcpflags") {
				params.Tcpflags = &tcpflags
			}
			if fl.Changed("log-nf-conntrack") {
				params.LogNfConntrack = &logNfConntrack
			}
			if fl.Changed("nf-conntrack-allow-invalid") {
				params.NfConntrackAllowInvalid = &nfConntrackAllowInvalid
			}
			if fl.Changed("protection-synflood") {
				params.ProtectionSynflood = &protectionSynflood
			}
			if fl.Changed("log-level-in") {
				params.LogLevelIn = &logLevelIn
			}
			if fl.Changed("log-level-out") {
				params.LogLevelOut = &logLevelOut
			}
			if fl.Changed("log-level-forward") {
				params.LogLevelForward = &logLevelForward
			}
			if fl.Changed("nf-conntrack-helpers") {
				params.NfConntrackHelpers = &nfConntrackHelpers
			}
			if fl.Changed("smurf-log-level") {
				params.SmurfLogLevel = &smurfLogLevel
			}
			if fl.Changed("tcp-flags-log-level") {
				params.TcpFlagsLogLevel = &tcpFlagsLogLevel
			}
			if fl.Changed("nf-conntrack-max") {
				params.NfConntrackMax = &nfConntrackMax
			}
			if fl.Changed("nf-conntrack-tcp-timeout-established") {
				params.NfConntrackTcpTimeoutEstablished = &nfConntrackEstablished
			}
			if fl.Changed("nf-conntrack-tcp-timeout-syn-recv") {
				params.NfConntrackTcpTimeoutSynRecv = &nfConntrackSynRecv
			}
			if fl.Changed("protection-synflood-burst") {
				params.ProtectionSynfloodBurst = &synfloodBurst
			}
			if fl.Changed("protection-synflood-rate") {
				params.ProtectionSynfloodRate = &synfloodRate
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if fl.Changed("delete") {
				params.Delete = &del
			}

			if fl.Changed("enable") && enable {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"WARNING: enabling the node firewall applies the cluster input policy (default DROP) on this node; "+
						"ensure a management allow rule (SSH 22, GUI 8006) exists first")
				// The host firewall evaluates both the node and datacenter rule
				// sets, so an enabled allow in either one is enough to stay quiet.
				scanned := false
				state := cli.InboundAllowMissing
				if resp, rerr := deps.API.Nodes.ListFirewallRules(cmd.Context(), deps.Node); rerr == nil && resp != nil {
					scanned = true
					state = cli.ScanInboundAllow(*resp)
				}
				if state != cli.InboundAllowFound {
					if resp, rerr := deps.API.Cluster.ListFirewallRules(cmd.Context()); rerr == nil && resp != nil {
						scanned = true
						if s := cli.ScanInboundAllow(*resp); s < state {
							state = s
						}
					}
				}
				if scanned {
					cli.WarnInboundAllow(cmd.ErrOrStderr(), state, "node and datacenter")
				}
			}
			if err := deps.API.Nodes.UpdateFirewallOptions(cmd.Context(), deps.Node, params); err != nil {
				return fmt.Errorf("set firewall options on node %q: %w", deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Firewall options updated on node %q.", deps.Node)}, deps.Format)
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&enable, "enable", false, "enable the host firewall rules")
	fl.BoolVar(&ndp, "ndp", false, "enable NDP (Neighbor Discovery Protocol)")
	fl.BoolVar(&nftables, "nftables", false, "enable the nftables-based firewall (tech preview)")
	fl.BoolVar(&nosmurfs, "nosmurfs", false, "enable the SMURFS filter")
	fl.BoolVar(&tcpflags, "tcpflags", false, "filter illegal combinations of TCP flags")
	fl.BoolVar(&logNfConntrack, "log-nf-conntrack", false, "log conntrack information")
	fl.BoolVar(&nfConntrackAllowInvalid, "nf-conntrack-allow-invalid", false, "allow invalid packets on connection tracking")
	fl.BoolVar(&protectionSynflood, "protection-synflood", false, "enable synflood protection")
	fl.StringVar(&logLevelIn, "log-level-in", "", "log level for incoming traffic")
	fl.StringVar(&logLevelOut, "log-level-out", "", "log level for outgoing traffic")
	fl.StringVar(&logLevelForward, "log-level-forward", "", "log level for forwarded traffic")
	fl.StringVar(&nfConntrackHelpers, "nf-conntrack-helpers", "", "conntrack helpers to enable, for example ftp,sip")
	fl.StringVar(&smurfLogLevel, "smurf-log-level", "", "log level for the SMURFS filter")
	fl.StringVar(&tcpFlagsLogLevel, "tcp-flags-log-level", "", "log level for the illegal-TCP-flags filter")
	fl.Int64Var(&nfConntrackMax, "nf-conntrack-max", 0, "maximum number of tracked connections")
	fl.Int64Var(&nfConntrackEstablished, "nf-conntrack-tcp-timeout-established", 0, "conntrack established timeout in seconds")
	fl.Int64Var(&nfConntrackSynRecv, "nf-conntrack-tcp-timeout-syn-recv", 0, "conntrack syn-recv timeout in seconds")
	fl.Int64Var(&synfloodBurst, "protection-synflood-burst", 0, "synflood protection rate burst by source IP")
	fl.Int64Var(&synfloodRate, "protection-synflood-rate", 0, "synflood protection rate (syn/sec) by source IP")
	fl.StringVar(&digest, "digest", "",
		"SHA1 digest of the current options to guard against concurrent edits")
	fl.StringVar(&del, "delete", "", "comma-separated list of options to reset to default")

	// Append generated schema detail (allowed values, defaults, ranges) to
	// each option flag's help text; see firewall_options_schema_gen.go.
	optionschema.EnrichFlags(fl, firewallOptionSchemas)
	return cmd
}

// ---- helpers ---------------------------------------------------------------

// objectToSingle marshals a typed response object (or a generic decoded value)
// into a key/value Single map and a generic Raw object, so structured output
// preserves every field.
func objectToSingle(v any) (map[string]string, any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, nil, err
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, nil, err
	}
	single := make(map[string]string, len(obj))
	for k, val := range obj {
		single[k] = anyCell(val)
	}
	return single, obj, nil
}

// anyCell renders an arbitrary JSON value as a single table/Single cell.
func anyCell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "yes"
		}
		return "no"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
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
