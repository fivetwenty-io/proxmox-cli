package lxc

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/optionschema"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newFirewallCmd builds the `pmx lxc firewall` sub-tree: per-container rules, IP
// sets, aliases, and options. Every operation is synchronous (no task UPID).
func newFirewallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "firewall",
		Short: "Manage an LXC container's firewall",
		Long: "Inspect and edit the per-container firewall: rules, IP sets, address aliases, " +
			"and the firewall options that govern policy and logging.",
	}
	cmd.AddCommand(
		newFirewallRulesCmd(),
		newFirewallIpsetCmd(),
		newFirewallAliasCmd(),
		newFirewallOptionsCmd(),
		newFirewallLogCmd(),
		newFirewallRefsCmd(),
	)
	return cmd
}

// fwLogEntry is the decoded shape of one firewall log line: a line number and
// the log text.
type fwLogEntry struct {
	N int64  `json:"n"`
	T string `json:"t"`
}

// newFirewallLogCmd builds `pmx lxc firewall log <vmid|name>` — the per-container
// firewall log (GET /nodes/{node}/lxc/{vmid}/firewall/log).
func newFirewallLogCmd() *cobra.Command {
	var (
		limit int64
		since int64
		start int64
		until int64
	)
	cmd := &cobra.Command{
		Use:   "log <vmid|name>",
		Short: "Read a container's firewall log",
		Long:  "Read the firewall log of a container. Use --start and --limit to page through entries.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			fl := cmd.Flags()
			params := &nodes.ListLxcFirewallLogParams{}
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
			resp, err := deps.API.Nodes.ListLxcFirewallLog(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("read firewall log for container %s on node %q: %w", vmid, node, err)
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

// fwRefEntry is the decoded shape of one firewall reference: an IP set or alias
// that rules may reference by name.
type fwRefEntry struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Ref     string `json:"ref"`
	Comment string `json:"comment"`
}

// newFirewallRefsCmd builds `pmx lxc firewall refs <vmid|name>` — the IP sets and
// aliases a rule may reference (GET /nodes/{node}/lxc/{vmid}/firewall/refs).
func newFirewallRefsCmd() *cobra.Command {
	var refType string
	cmd := &cobra.Command{
		Use:   "refs <vmid|name>",
		Short: "List IP sets and aliases rules can reference",
		Long: "List the IP sets and address aliases defined on a container that firewall " +
			"rules may reference by name. Pass --type to restrict the listing to one kind.",
		Example: `  pmx pve lxc firewall refs 200
  pmx pve lxc firewall refs web1 --type ipset`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			params := &nodes.ListLxcFirewallRefsParams{}
			if cmd.Flags().Changed("type") {
				params.Type = &refType
			}
			resp, err := deps.API.Nodes.ListLxcFirewallRefs(cmd.Context(), node, vmid, params)
			if err != nil {
				return fmt.Errorf("list firewall references for container %s on node %q: %w", vmid, node, err)
			}
			entries := make([]fwRefEntry, 0)
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e fwRefEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode firewall reference entry: %w", err)
					}
					entries = append(entries, e)
					rows = append(rows, []string{e.Type, e.Name, e.Ref, e.Comment})
				}
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: []string{"TYPE", "NAME", "REF", "COMMENT"}, Rows: rows, Raw: entries}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&refType, "type", "", "only list references of this type: alias or ipset")
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

// ---- rules -----------------------------------------------------------------

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

func newFirewallRulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Manage per-container firewall rules",
		Long:  "List, inspect, create, update, and delete the firewall rules of a container.",
	}
	cmd.AddCommand(
		newFirewallRulesListCmd(),
		newFirewallRulesGetCmd(),
		newFirewallRulesCreateCmd(),
		newFirewallRulesUpdateCmd(),
		newFirewallRulesDeleteCmd(),
	)
	return cmd
}

func newFirewallRulesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <vmid|name>",
		Short: "List a container's firewall rules",
		Long:  "List every firewall rule configured on a container, in rule-evaluation order.",
		Example: `  pmx pve lxc firewall rules list 200
  pmx pve lxc firewall rules list web1`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.ListLxcFirewallRules(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("list firewall rules for container %s on node %q: %w", vmid, node, err)
			}

			headers := []string{"POS", "TYPE", "ACTION", "PROTO", "SOURCE", "DEST", "DPORT", "ENABLE", "COMMENT"}
			entries := make([]fwRuleEntry, 0)
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e fwRuleEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode firewall rule entry: %w", err)
					}
					entries = append(entries, e)
					rows = append(rows, []string{
						strconv.FormatInt(e.Pos, 10),
						e.Type, e.Action, e.Proto, e.Source, e.Dest, e.Dport,
						strconv.FormatInt(e.Enable, 10), e.Comment,
					})
				}
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
}

func newFirewallRulesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <vmid|name> <pos>",
		Short: "Show a single firewall rule by position",
		Long:  "Show a single firewall rule of a container by its position in the rule list.",
		Example: `  pmx pve lxc firewall rules get 200 0
  pmx pve lxc firewall rules get web1 0`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			pos := args[1]

			// The typed client method cannot decode this endpoint: PVE returns
			// `pos` as a string here while the generated struct expects int64.
			// Fetch the raw object instead and render it generically.
			path := fmt.Sprintf("/nodes/%s/lxc/%s/firewall/rules/%s",
				url.PathEscape(node), url.PathEscape(vmid), url.PathEscape(pos))
			data, err := deps.API.Raw.GetCtx(cmd.Context(), path, nil)
			if err != nil {
				return fmt.Errorf("get firewall rule %s for container %s on node %q: %w", pos, vmid, node, err)
			}
			single, err := structToStringMap(data)
			if err != nil {
				return err
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: data}, deps.Format)
		},
	}
}

func newFirewallRulesCreateCmd() *cobra.Command {
	var (
		action   string
		ruleType string
		source   string
		dest     string
		proto    string
		dport    string
		sport    string
		iface    string
		macro    string
		logLevel string
		icmpType string
		comment  string
		enable   int64
		pos      int64
	)
	cmd := &cobra.Command{
		Use:   "create <vmid|name>",
		Short: "Append a firewall rule to a container",
		Long: "Create a new firewall rule. --type (in|out|group) and --action " +
			"(ACCEPT|DROP|REJECT or a security group name) are required.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("type") {
				return fmt.Errorf("--type is required: one of in, out, or group")
			}
			if !cmd.Flags().Changed("action") {
				return fmt.Errorf("--action is required: ACCEPT, DROP, REJECT, or a security group name")
			}

			params := &nodes.CreateLxcFirewallRulesParams{Action: action, Type: ruleType}
			fl := cmd.Flags()
			if fl.Changed("source") {
				params.Source = &source
			}
			if fl.Changed("dest") {
				params.Dest = &dest
			}
			if fl.Changed("proto") {
				params.Proto = &proto
			}
			if fl.Changed("dport") {
				params.Dport = &dport
			}
			if fl.Changed("sport") {
				params.Sport = &sport
			}
			if fl.Changed("iface") {
				params.Iface = &iface
			}
			if fl.Changed("macro") {
				params.Macro = &macro
			}
			if fl.Changed("log") {
				params.Log = &logLevel
			}
			if fl.Changed("icmp-type") {
				params.IcmpType = &icmpType
			}
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("enable") {
				params.Enable = &enable
			}
			if fl.Changed("pos") {
				params.Pos = &pos
			}

			if err := deps.API.Nodes.CreateLxcFirewallRules(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("create firewall rule for container %s on node %q: %w", vmid, node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Firewall rule added to container %s.", vmid)}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&ruleType, "type", "", "rule direction: in, out, or group (required)")
	cmd.Flags().StringVar(&action, "action", "", "ACCEPT, DROP, REJECT, or a security group name (required)")
	cmd.Flags().StringVar(&source, "source", "", "restrict source address, IP set (+name), or alias")
	cmd.Flags().StringVar(&dest, "dest", "", "restrict destination address, IP set (+name), or alias")
	cmd.Flags().StringVar(&proto, "proto", "", "IP protocol, for example tcp or udp")
	cmd.Flags().StringVar(&dport, "dport", "", "destination port or range, for example 80 or 80:85")
	cmd.Flags().StringVar(&sport, "sport", "", "source port or range")
	cmd.Flags().StringVar(&iface, "iface", "", "network interface, for example net0")
	cmd.Flags().StringVar(&macro, "macro", "", "predefined standard macro")
	cmd.Flags().StringVar(&logLevel, "log", "", "log level: emerg, alert, crit, err, warning, notice, info, debug, or nolog")
	cmd.Flags().StringVar(&icmpType, "icmp-type", "", "ICMP type, valid only when proto is icmp or icmpv6")
	cmd.Flags().StringVar(&comment, "comment", "", "descriptive comment")
	cmd.Flags().Int64Var(&enable, "enable", 1, "1 to enable the rule, 0 to disable it")
	cmd.Flags().Int64Var(&pos, "pos", 0, "insert the rule at this position")
	return cmd
}

func newFirewallRulesUpdateCmd() *cobra.Command {
	var (
		action   string
		ruleType string
		source   string
		dest     string
		proto    string
		dport    string
		sport    string
		iface    string
		macro    string
		logLevel string
		icmpType string
		comment  string
		enable   int64
		moveto   int64
		del      string
	)
	cmd := &cobra.Command{
		Use:   "update <vmid|name> <pos>",
		Short: "Modify a firewall rule by position",
		Long: "Update the firewall rule at the given position. Only the flags you pass are " +
			"changed; pass --moveto to relocate the rule to a different position instead " +
			"(other flags are ignored when moving), or --delete to clear specific settings.",
		Example: `  pmx pve lxc firewall rules update 200 0 --comment "allow ssh"
  pmx pve lxc firewall rules update web1 0 --moveto 2`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			pos := args[1]

			params := &nodes.UpdateLxcFirewallRulesParams{}
			fl := cmd.Flags()
			if fl.Changed("type") {
				params.Type = &ruleType
			}
			if fl.Changed("action") {
				params.Action = &action
			}
			if fl.Changed("source") {
				params.Source = &source
			}
			if fl.Changed("dest") {
				params.Dest = &dest
			}
			if fl.Changed("proto") {
				params.Proto = &proto
			}
			if fl.Changed("dport") {
				params.Dport = &dport
			}
			if fl.Changed("sport") {
				params.Sport = &sport
			}
			if fl.Changed("iface") {
				params.Iface = &iface
			}
			if fl.Changed("macro") {
				params.Macro = &macro
			}
			if fl.Changed("log") {
				params.Log = &logLevel
			}
			if fl.Changed("icmp-type") {
				params.IcmpType = &icmpType
			}
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("enable") {
				params.Enable = &enable
			}
			if fl.Changed("moveto") {
				params.Moveto = &moveto
			}
			if fl.Changed("delete") {
				params.Delete = &del
			}

			if err := deps.API.Nodes.UpdateLxcFirewallRules(cmd.Context(), node, vmid, pos, params); err != nil {
				return fmt.Errorf("update firewall rule %s for container %s on node %q: %w", pos, vmid, node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Firewall rule %s of container %s updated.", pos, vmid)}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&ruleType, "type", "", "rule direction: in, out, or group")
	cmd.Flags().StringVar(&action, "action", "", "ACCEPT, DROP, REJECT, or a security group name")
	cmd.Flags().StringVar(&source, "source", "", "restrict source address, IP set (+name), or alias")
	cmd.Flags().StringVar(&dest, "dest", "", "restrict destination address, IP set (+name), or alias")
	cmd.Flags().StringVar(&proto, "proto", "", "IP protocol, for example tcp or udp")
	cmd.Flags().StringVar(&dport, "dport", "", "destination port or range")
	cmd.Flags().StringVar(&sport, "sport", "", "source port or range")
	cmd.Flags().StringVar(&iface, "iface", "", "network interface, for example net0")
	cmd.Flags().StringVar(&macro, "macro", "", "predefined standard macro")
	cmd.Flags().StringVar(&logLevel, "log", "", "log level for the rule")
	cmd.Flags().StringVar(&icmpType, "icmp-type", "", "ICMP type, valid only when proto is icmp or icmpv6")
	cmd.Flags().StringVar(&comment, "comment", "", "descriptive comment")
	cmd.Flags().Int64Var(&enable, "enable", 1, "1 to enable the rule, 0 to disable it")
	cmd.Flags().Int64Var(&moveto, "moveto", 0, "move the rule to this position (other arguments ignored)")
	cmd.Flags().StringVar(&del, "delete", "", "comma-separated list of rule settings to clear")
	return cmd
}

func newFirewallRulesDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <vmid|name> <pos>",
		Short: "Delete a firewall rule by position",
		Long: "Permanently delete the firewall rule at the given position. Refuses to run " +
			"without --yes/-y.",
		Example: `  pmx pve lxc firewall rules delete 200 0 --yes`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			pos := args[1]
			if !yes {
				return fmt.Errorf("refusing to delete firewall rule %s without confirmation: pass --yes/-y", pos)
			}

			if err := deps.API.Nodes.DeleteLxcFirewallRules(cmd.Context(), node, vmid, pos, &nodes.DeleteLxcFirewallRulesParams{}); err != nil {
				return fmt.Errorf("delete firewall rule %s for container %s on node %q: %w", pos, vmid, node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Firewall rule %s of container %s deleted.", pos, vmid)}, deps.Format)
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

func newFirewallIpsetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ipset",
		Short: "Manage per-container firewall IP sets",
		Long: "List, create, and delete named IP sets on a container, and manage the CIDR " +
			"entries within each set. IP sets can be referenced by firewall rules via " +
			"+name in --source/--dest.",
	}
	cmd.AddCommand(
		newFirewallIpsetListCmd(),
		newFirewallIpsetCreateCmd(),
		newFirewallIpsetDeleteCmd(),
		newFirewallIpsetAddCmd(),
		newFirewallIpsetRemoveCmd(),
		newFirewallIpsetUpdateMemberCmd(),
		newFirewallIpsetGetMemberCmd(),
	)
	return cmd
}

// newFirewallIpsetGetMemberCmd builds
// `pmx lxc firewall ipset get-member <vmid|name> <name> <cidr>` — show a
// single CIDR entry of an IP set (GET .../firewall/ipset/{name}/{cidr}).
// Named get-member (not get) because `ipset list <vmid> [name]` already
// overloads the name-scoped read, and update-member set the member-verb
// naming precedent.
func newFirewallIpsetGetMemberCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get-member <vmid|name> <name> <cidr>",
		Short: "Show a single CIDR entry of an IP set",
		Long:  "Show a single CIDR entry within a named IP set on a container.",
		Example: `  pmx pve lxc firewall ipset get-member 200 blocklist 10.0.0.0/8
  pmx pve lxc firewall ipset get-member web1 blocklist 10.0.0.0/8`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			name, cidr := args[1], args[2]

			resp, err := deps.API.Nodes.GetLxcFirewallIpset2(cmd.Context(), node, vmid, name, cidr)
			if err != nil {
				return fmt.Errorf("get %s in IP set %q for container %s on node %q: %w", cidr, name, vmid, node, err)
			}
			if resp == nil {
				return fmt.Errorf("get %s in IP set %q for container %s on node %q: empty response", cidr, name, vmid, node)
			}
			single, err := structToStringMap(resp)
			if err != nil {
				return err
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}
}

// newFirewallIpsetUpdateMemberCmd builds
// `pmx lxc firewall ipset update-member <vmid|name> <name> <cidr>` — update an
// existing IP set entry (PUT .../firewall/ipset/{name}/{cidr}).
func newFirewallIpsetUpdateMemberCmd() *cobra.Command {
	var (
		comment string
		nomatch bool
		digest  string
	)
	cmd := &cobra.Command{
		Use:   "update-member <vmid|name> <name> <cidr>",
		Short: "Update a CIDR entry of an IP set",
		Long:  "Update the comment or exclusion flag of an existing CIDR entry within an IP set.",
		Example: `  pmx pve lxc firewall ipset update-member 200 blocklist 10.0.0.0/8 --comment "known bad"
  pmx pve lxc firewall ipset update-member web1 blocklist 10.0.0.0/8 --nomatch`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			name, cidr := args[1], args[2]

			params := &nodes.UpdateLxcFirewallIpsetParams{}
			fl := cmd.Flags()
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("nomatch") {
				params.Nomatch = &nomatch
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if err := deps.API.Nodes.UpdateLxcFirewallIpset(cmd.Context(), node, vmid, name, cidr, params); err != nil {
				return fmt.Errorf("update %s in IP set %q for container %s on node %q: %w", cidr, name, vmid, node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("%s in IP set %s on container %s updated.", cidr, name, vmid)}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "descriptive comment")
	cmd.Flags().BoolVar(&nomatch, "nomatch", false, "treat this entry as an exclusion")
	cmd.Flags().StringVar(&digest, "digest", "", "only apply if the current config matches this SHA1 digest")
	return cmd
}

func newFirewallIpsetListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <vmid|name> [name]",
		Short: "List IP sets, or the members of one IP set",
		Long: "Without [name], list the IP sets defined on a container. With [name], list " +
			"the CIDR entries within that IP set.",
		Example: `  pmx pve lxc firewall ipset list 200
  pmx pve lxc firewall ipset list web1 blocklist`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			var raws []json.RawMessage
			var headers []string
			if len(args) == 2 {
				resp, err := deps.API.Nodes.GetLxcFirewallIpset(cmd.Context(), node, vmid, args[1])
				if err != nil {
					return fmt.Errorf("list IP set %q members for container %s on node %q: %w", args[1], vmid, node, err)
				}
				if resp != nil {
					raws = *resp
				}
				headers = []string{"CIDR", "NOMATCH", "COMMENT"}
			} else {
				resp, err := deps.API.Nodes.ListLxcFirewallIpset(cmd.Context(), node, vmid)
				if err != nil {
					return fmt.Errorf("list IP sets for container %s on node %q: %w", vmid, node, err)
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
				if len(args) == 2 {
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

func newFirewallIpsetCreateCmd() *cobra.Command {
	var comment string
	cmd := &cobra.Command{
		Use:   "create <vmid|name> <name>",
		Short: "Create a firewall IP set",
		Long:  "Create a new, empty named IP set on a container. Add entries with 'ipset add'.",
		Example: `  pmx pve lxc firewall ipset create 200 blocklist
  pmx pve lxc firewall ipset create web1 blocklist --comment "known bad addresses"`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			name := args[1]

			params := &nodes.CreateLxcFirewallIpsetParams{Name: name}
			if cmd.Flags().Changed("comment") {
				params.Comment = &comment
			}
			if err := deps.API.Nodes.CreateLxcFirewallIpset(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("create IP set %q for container %s on node %q: %w", name, vmid, node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("IP set %s created on container %s.", name, vmid)}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "descriptive comment")
	return cmd
}

func newFirewallIpsetDeleteCmd() *cobra.Command {
	var (
		yes   bool
		force bool
	)
	cmd := &cobra.Command{
		Use:   "delete <vmid|name> <name>",
		Short: "Delete a firewall IP set",
		Long: "Permanently delete a named IP set. Refuses to run without --yes/-y; pass " +
			"--force to also delete its members if any remain.",
		Example: `  pmx pve lxc firewall ipset delete 200 blocklist --yes`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			name := args[1]
			if !yes {
				return fmt.Errorf("refusing to delete IP set %q without confirmation: pass --yes/-y", name)
			}

			params := &nodes.DeleteLxcFirewallIpsetParams{}
			if cmd.Flags().Changed("force") {
				params.Force = &force
			}
			if err := deps.API.Nodes.DeleteLxcFirewallIpset(cmd.Context(), node, vmid, name, params); err != nil {
				return fmt.Errorf("delete IP set %q for container %s on node %q: %w", name, vmid, node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("IP set %s deleted from container %s.", name, vmid)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	cmd.Flags().BoolVar(&force, "force", false, "delete all members of the IP set if any remain")
	return cmd
}

func newFirewallIpsetAddCmd() *cobra.Command {
	var (
		comment string
		nomatch bool
	)
	cmd := &cobra.Command{
		Use:   "add <vmid|name> <name> <cidr>",
		Short: "Add a CIDR entry to an IP set",
		Long:  "Add a CIDR entry to an existing named IP set on a container.",
		Example: `  pmx pve lxc firewall ipset add 200 blocklist 10.0.0.0/8
  pmx pve lxc firewall ipset add web1 blocklist 10.0.0.0/8 --comment "known bad"`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			name, cidr := args[1], args[2]

			params := &nodes.CreateLxcFirewallIpset2Params{Cidr: cidr}
			if cmd.Flags().Changed("comment") {
				params.Comment = &comment
			}
			if cmd.Flags().Changed("nomatch") {
				params.Nomatch = &nomatch
			}
			if err := deps.API.Nodes.CreateLxcFirewallIpset2(cmd.Context(), node, vmid, name, params); err != nil {
				return fmt.Errorf("add %s to IP set %q for container %s on node %q: %w", cidr, name, vmid, node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("%s added to IP set %s on container %s.", cidr, name, vmid)}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "descriptive comment")
	cmd.Flags().BoolVar(&nomatch, "nomatch", false, "treat this entry as an exclusion")
	return cmd
}

func newFirewallIpsetRemoveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "remove <vmid|name> <name> <cidr>",
		Short:   "Remove a CIDR entry from an IP set",
		Long:    "Remove a CIDR entry from a named IP set. Refuses to run without --yes/-y.",
		Example: `  pmx pve lxc firewall ipset remove 200 blocklist 10.0.0.0/8 --yes`,
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			name, cidr := args[1], args[2]
			if !yes {
				return fmt.Errorf("refusing to remove %s from IP set %q without confirmation: pass --yes/-y", cidr, name)
			}

			if err := deps.API.Nodes.DeleteLxcFirewallIpset2(cmd.Context(), node, vmid, name, cidr, &nodes.DeleteLxcFirewallIpset2Params{}); err != nil {
				return fmt.Errorf("remove %s from IP set %q for container %s on node %q: %w", cidr, name, vmid, node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("%s removed from IP set %s on container %s.", cidr, name, vmid)}, deps.Format)
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

func newFirewallAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Manage per-container firewall address aliases",
		Long: "List, create, update, and delete named address aliases on a container. " +
			"Aliases can be referenced by firewall rules via their name in --source/--dest.",
	}
	cmd.AddCommand(
		newFirewallAliasListCmd(),
		newFirewallAliasGetCmd(),
		newFirewallAliasCreateCmd(),
		newFirewallAliasUpdateCmd(),
		newFirewallAliasDeleteCmd(),
	)
	return cmd
}

// newFirewallAliasGetCmd builds `pmx lxc firewall alias get <vmid|name>
// <name>` — show a single firewall alias by name
// (GET .../firewall/aliases/{name}).
func newFirewallAliasGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <vmid|name> <name>",
		Short: "Show a single firewall alias by name",
		Long:  "Show a single address alias defined on a container by its name.",
		Example: `  pmx pve lxc firewall alias get 200 mgmt-host
  pmx pve lxc firewall alias get web1 mgmt-host`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			name := args[1]

			resp, err := deps.API.Nodes.GetLxcFirewallAliases(cmd.Context(), node, vmid, name)
			if err != nil {
				return fmt.Errorf("get alias %q for container %s on node %q: %w", name, vmid, node, err)
			}
			if resp == nil {
				return fmt.Errorf("get alias %q for container %s on node %q: empty response", name, vmid, node)
			}
			single, err := structToStringMap(resp)
			if err != nil {
				return err
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}
}

func newFirewallAliasListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <vmid|name>",
		Short: "List a container's firewall aliases",
		Long:  "List every address alias defined on a container.",
		Example: `  pmx pve lxc firewall alias list 200
  pmx pve lxc firewall alias list web1`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.ListLxcFirewallAliases(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("list firewall aliases for container %s on node %q: %w", vmid, node, err)
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

func newFirewallAliasCreateCmd() *cobra.Command {
	var comment string
	cmd := &cobra.Command{
		Use:   "create <vmid|name> <name> <cidr>",
		Short: "Create a firewall address alias",
		Long:  "Create a new named address alias on a container, bound to the given CIDR.",
		Example: `  pmx pve lxc firewall alias create 200 mgmt-host 10.0.0.5/32
  pmx pve lxc firewall alias create web1 mgmt-host 10.0.0.5/32 --comment "management jump host"`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			name, cidr := args[1], args[2]

			params := &nodes.CreateLxcFirewallAliasesParams{Name: name, Cidr: cidr}
			if cmd.Flags().Changed("comment") {
				params.Comment = &comment
			}
			if err := deps.API.Nodes.CreateLxcFirewallAliases(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("create alias %q for container %s on node %q: %w", name, vmid, node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Alias %s created on container %s.", name, vmid)}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "descriptive comment")
	return cmd
}

func newFirewallAliasUpdateCmd() *cobra.Command {
	var (
		comment string
		rename  string
	)
	cmd := &cobra.Command{
		Use:   "update <vmid|name> <name> <cidr>",
		Short: "Update a firewall address alias",
		Long: "Update the CIDR of an existing address alias. Pass --rename to also change " +
			"its name.",
		Example: `  pmx pve lxc firewall alias update 200 mgmt-host 10.0.0.6/32
  pmx pve lxc firewall alias update web1 mgmt-host 10.0.0.6/32 --rename mgmt-jump`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			name, cidr := args[1], args[2]

			params := &nodes.UpdateLxcFirewallAliasesParams{Cidr: cidr}
			if cmd.Flags().Changed("comment") {
				params.Comment = &comment
			}
			if cmd.Flags().Changed("rename") {
				params.Rename = &rename
			}
			if err := deps.API.Nodes.UpdateLxcFirewallAliases(cmd.Context(), node, vmid, name, params); err != nil {
				return fmt.Errorf("update alias %q for container %s on node %q: %w", name, vmid, node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Alias %s on container %s updated.", name, vmid)}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "descriptive comment")
	cmd.Flags().StringVar(&rename, "rename", "", "rename the alias to this name")
	return cmd
}

func newFirewallAliasDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <vmid|name> <name>",
		Short:   "Delete a firewall address alias",
		Long:    "Permanently delete a named address alias. Refuses to run without --yes/-y.",
		Example: `  pmx pve lxc firewall alias delete 200 mgmt-host --yes`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}
			name := args[1]
			if !yes {
				return fmt.Errorf("refusing to delete alias %q without confirmation: pass --yes/-y", name)
			}

			if err := deps.API.Nodes.DeleteLxcFirewallAliases(cmd.Context(), node, vmid, name, &nodes.DeleteLxcFirewallAliasesParams{}); err != nil {
				return fmt.Errorf("delete alias %q for container %s on node %q: %w", name, vmid, node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Alias %s deleted from container %s.", name, vmid)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// ---- options ---------------------------------------------------------------

func newFirewallOptionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "options",
		Short: "Inspect and set per-container firewall options",
		Long: "Show or update the firewall options that govern a container's firewall " +
			"policy and logging, and browse the offline catalog of every settable option " +
			"with 'describe'.",
	}
	cmd.AddCommand(
		newFirewallOptionsGetCmd(),
		newFirewallOptionsSetCmd(),
		newFirewallOptionsDescribeCmd(),
	)
	return cmd
}

// newFirewallOptionsDescribeCmd builds `pmx lxc firewall options describe`, an
// offline catalog of every settable container firewall option from the PVE
// API schema (see firewall_options_schema_gen.go).
func newFirewallOptionsDescribeCmd() *cobra.Command {
	return optionschema.NewDescribeCmd(optionschema.DescribeConfig{
		Schemas: firewallOptionSchemas,
		Short:   "Describe all settable container firewall options and their defaults",
		Long: "List every settable container firewall option from the PVE API schema: " +
			"type, built-in default, allowed values, and the sub-keys of dict-encoded " +
			"options. Runs offline. Pass an option name to show only that option with " +
			"full descriptions.",
		CommandHint:         "pmx pve lxc firewall options describe",
		SubKeyRowsInCatalog: true,
	})
}

func newFirewallOptionsGetCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "get <vmid|name>",
		Short: "Show a container's firewall options",
		Long: "Show the firewall options currently set on a container. The PVE API omits " +
			"options left at their built-in defaults; pass --defaults to also list those " +
			"with the value they effectively have.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			resp, err := deps.API.Nodes.ListLxcFirewallOptions(cmd.Context(), node, vmid)
			if err != nil {
				return fmt.Errorf("get firewall options for container %s on node %q: %w", vmid, node, err)
			}
			single, err := structToStringMap(resp)
			if err != nil {
				return err
			}
			var raw any = resp
			if withDefaults {
				single, raw = optionschema.MergeDefaults(firewallOptionSchemas, single, resp, optionschema.MergeOpts{})
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"also list unset options with their built-in default values")
	return cmd
}

func newFirewallOptionsSetCmd() *cobra.Command {
	var (
		enable      bool
		dhcp        bool
		macfilter   bool
		ndp         bool
		radv        bool
		ipfilter    bool
		policyIn    string
		policyOut   string
		logLevelIn  string
		logLevelOut string
		del         string
	)
	cmd := &cobra.Command{
		Use:   "set <vmid|name>",
		Short: "Set a container's firewall options",
		Long:  "Update per-container firewall options. Only the flags you pass are changed.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vmid, node, err := resolveGuest(cmd.Context(), deps, args[0])
			if err != nil {
				return err
			}

			params := &nodes.UpdateLxcFirewallOptionsParams{}
			fl := cmd.Flags()
			if fl.Changed("enable") {
				params.Enable = &enable
			}
			if fl.Changed("dhcp") {
				params.Dhcp = &dhcp
			}
			if fl.Changed("macfilter") {
				params.Macfilter = &macfilter
			}
			if fl.Changed("ndp") {
				params.Ndp = &ndp
			}
			if fl.Changed("radv") {
				params.Radv = &radv
			}
			if fl.Changed("ipfilter") {
				params.Ipfilter = &ipfilter
			}
			if fl.Changed("policy-in") {
				params.PolicyIn = &policyIn
			}
			if fl.Changed("policy-out") {
				params.PolicyOut = &policyOut
			}
			if fl.Changed("log-level-in") {
				params.LogLevelIn = &logLevelIn
			}
			if fl.Changed("log-level-out") {
				params.LogLevelOut = &logLevelOut
			}
			if fl.Changed("delete") {
				params.Delete = &del
			}

			if !anyFlagChanged(fl, "enable", "dhcp", "macfilter", "ndp", "radv", "ipfilter",
				"policy-in", "policy-out", "log-level-in", "log-level-out", "delete") {
				return fmt.Errorf("no options to set: pass at least one option flag")
			}

			if err := deps.API.Nodes.UpdateLxcFirewallOptions(cmd.Context(), node, vmid, params); err != nil {
				return fmt.Errorf("set firewall options for container %s on node %q: %w", vmid, node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Firewall options for container %s updated.", vmid)}, deps.Format)
		},
	}

	cmd.Flags().BoolVar(&enable, "enable", false, "enable or disable the container firewall")
	cmd.Flags().BoolVar(&dhcp, "dhcp", false, "enable DHCP")
	cmd.Flags().BoolVar(&macfilter, "macfilter", false, "enable MAC address filtering")
	cmd.Flags().BoolVar(&ndp, "ndp", false, "enable NDP (Neighbor Discovery Protocol)")
	cmd.Flags().BoolVar(&radv, "radv", false, "allow sending Router Advertisements")
	cmd.Flags().BoolVar(&ipfilter, "ipfilter", false, "enable default IP filters")
	cmd.Flags().StringVar(&policyIn, "policy-in", "", "input policy")
	cmd.Flags().StringVar(&policyOut, "policy-out", "", "output policy")
	cmd.Flags().StringVar(&logLevelIn, "log-level-in", "", "log level for incoming traffic")
	cmd.Flags().StringVar(&logLevelOut, "log-level-out", "", "log level for outgoing traffic")
	cmd.Flags().StringVar(&del, "delete", "", "comma-separated list of options to reset to default")

	// Append generated schema detail (allowed values, defaults) to each option
	// flag's help text; see firewall_options_schema_gen.go.
	optionschema.EnrichFlags(cmd.Flags(), firewallOptionSchemas)
	return cmd
}
