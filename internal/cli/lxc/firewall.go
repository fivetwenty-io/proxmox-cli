package lxc

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newFirewallCmd builds the `pve lxc firewall` sub-tree: per-container rules, IP
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
	)
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
		Args:  cobra.ExactArgs(1),
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
		Args:  cobra.ExactArgs(2),
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
		comment  string
		enable   int64
		moveto   int64
		del      string
	)
	cmd := &cobra.Command{
		Use:   "update <vmid|name> <pos>",
		Short: "Modify a firewall rule by position",
		Args:  cobra.ExactArgs(2),
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
		Args:  cobra.ExactArgs(2),
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
	}
	cmd.AddCommand(
		newFirewallIpsetListCmd(),
		newFirewallIpsetCreateCmd(),
		newFirewallIpsetDeleteCmd(),
		newFirewallIpsetAddCmd(),
		newFirewallIpsetRemoveCmd(),
	)
	return cmd
}

func newFirewallIpsetListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <vmid|name> [name]",
		Short: "List IP sets, or the members of one IP set",
		Args:  cobra.RangeArgs(1, 2),
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
		Args:  cobra.ExactArgs(2),
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
		Args:  cobra.ExactArgs(2),
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
		Args:  cobra.ExactArgs(3),
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
		Use:   "remove <vmid|name> <name> <cidr>",
		Short: "Remove a CIDR entry from an IP set",
		Args:  cobra.ExactArgs(3),
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
	}
	cmd.AddCommand(
		newFirewallAliasListCmd(),
		newFirewallAliasCreateCmd(),
		newFirewallAliasUpdateCmd(),
		newFirewallAliasDeleteCmd(),
	)
	return cmd
}

func newFirewallAliasListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <vmid|name>",
		Short: "List a container's firewall aliases",
		Args:  cobra.ExactArgs(1),
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
		Args:  cobra.ExactArgs(3),
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
		Args:  cobra.ExactArgs(3),
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
		Use:   "delete <vmid|name> <name>",
		Short: "Delete a firewall address alias",
		Args:  cobra.ExactArgs(2),
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
	}
	cmd.AddCommand(
		newFirewallOptionsGetCmd(),
		newFirewallOptionsSetCmd(),
	)
	return cmd
}

func newFirewallOptionsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <vmid|name>",
		Short: "Show a container's firewall options",
		Args:  cobra.ExactArgs(1),
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
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: resp}, deps.Format)
		},
	}
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
	cmd.Flags().StringVar(&policyIn, "policy-in", "", "input policy: ACCEPT, REJECT, or DROP")
	cmd.Flags().StringVar(&policyOut, "policy-out", "", "output policy: ACCEPT, REJECT, or DROP")
	cmd.Flags().StringVar(&logLevelIn, "log-level-in", "", "log level for incoming traffic")
	cmd.Flags().StringVar(&logLevelOut, "log-level-out", "", "log level for outgoing traffic")
	cmd.Flags().StringVar(&del, "delete", "", "comma-separated list of options to reset to default")
	return cmd
}
