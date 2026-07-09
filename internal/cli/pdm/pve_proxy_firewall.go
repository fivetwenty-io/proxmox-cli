package pdm

import (
	"fmt"

	"github.com/spf13/cobra"

	pdmpve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/pve"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newPveFirewallCmd builds `pmx pdm pve firewall` — cluster-level firewall
// status, options, and rules across a PVE remote's cluster
// (/pve/firewall/status, /pve/remotes/{remote}/firewall/...).
func newPveFirewallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "firewall",
		Short: "Inspect and manage a PVE remote's cluster-level firewall",
	}
	cmd.AddCommand(newPveFirewallStatusCmd(), newPveFirewallShowCmd(), newPveFirewallOptionsCmd(), newPveFirewallRulesCmd())
	return cmd
}

// newPveFirewallStatusCmd builds `pmx pdm pve firewall status` — get
// firewall status of every managed PVE remote (GET /pve/firewall/status).
// Each element has the same {remote, status, nodes} shape as
// ListRemotesFirewallStatus (`pve firewall show`), so rows use the same
// column set; a per-remote listing is rendered in server order like every
// other "status of all remotes" listing in this package.
func newPveFirewallStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show firewall status of every managed PVE remote",
		Long:  "Get firewall status of every managed PVE remote (GET /pve/firewall/status).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Pve.ListFirewallStatus(cmd.Context())
			if err != nil {
				return fmt.Errorf("list firewall status of every PVE remote: %w", err)
			}

			items := decodeRawList(rawItemsOf(resp))

			headers := []string{"REMOTE", "NODES", "STATUS"}
			rows := make([][]string, 0, len(items))
			for _, m := range items {
				nodes, _ := m["nodes"].([]any)
				rows = append(rows, []string{scalarString(m["remote"]), fmt.Sprintf("%d", len(nodes)), scalarString(m["status"])})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: items}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newPveFirewallShowCmd builds `pmx pdm pve firewall show <remote>` — get
// firewall status of a specific PVE remote (GET
// /pve/remotes/{remote}/firewall/status).
func newPveFirewallShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <remote>",
		Short: "Show firewall status of a PVE remote",
		Long:  "Get firewall status of a specific PVE remote (GET /pve/remotes/{remote}/firewall/status).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]

			resp, err := deps.PDM.Pve.ListRemotesFirewallStatus(cmd.Context(), remote)
			if err != nil {
				return fmt.Errorf("get firewall status of PVE remote %q: %w", remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get firewall status of PVE remote %q: empty response from server", remote)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode firewall status of PVE remote %q: %w", remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newPveFirewallOptionsCmd builds `pmx pdm pve firewall options` and its
// show/update verbs (GET/PUT /pve/remotes/{remote}/firewall/options).
func newPveFirewallOptionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "options",
		Short: "Show or update a PVE remote's cluster firewall options",
	}
	cmd.AddCommand(newPveFirewallOptionsShowCmd(), newPveFirewallOptionsUpdateCmd())
	return cmd
}

// newPveFirewallOptionsShowCmd builds `pmx pdm pve firewall options show
// <remote>` — get cluster firewall options (GET
// /pve/remotes/{remote}/firewall/options).
func newPveFirewallOptionsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <remote>",
		Short: "Show a PVE remote's cluster firewall options",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]

			resp, err := deps.PDM.Pve.ListRemotesFirewallOptions(cmd.Context(), remote)
			if err != nil {
				return fmt.Errorf("get cluster firewall options of PVE remote %q: %w", remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get cluster firewall options of PVE remote %q: empty response from server", remote)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode cluster firewall options of PVE remote %q: %w", remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// pveFirewallOptionsFlags collects the update flags for `firewall options
// update` (cluster-level), each mapping directly onto an
// UpdateRemotesFirewallOptionsParams field of the same name. See
// pveNodeFirewallOptionsFlags (pve_proxy_node.go) for the node-level
// equivalent, which accepts a much larger, unrelated set of options.
type pveFirewallOptionsFlags struct {
	del                                []string
	digest                             string
	logRatelimit                       string
	policyForward, policyIn, policyOut string
	ebtables                           bool
	enable                             bool
}

// newPveFirewallOptionsUpdateCmd builds `pmx pdm pve firewall options update
// <remote>` — update cluster firewall configuration (PUT
// /pve/remotes/{remote}/firewall/options). This is a configuration update,
// not a destructive action, so it is guarded by anyFlagChanged rather than
// --yes/-y, matching every other config-update command in this package.
func newPveFirewallOptionsUpdateCmd() *cobra.Command {
	var ff pveFirewallOptionsFlags
	cmd := &cobra.Command{
		Use:   "update <remote>",
		Short: "Update a PVE remote's cluster firewall options",
		Long: "Update cluster firewall configuration (PUT /pve/remotes/{remote}/firewall/options). " +
			"Only flags explicitly set are sent; use --delete to reset properties to their default instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]
			fl := cmd.Flags()

			if !anyFlagChanged(fl) {
				return fmt.Errorf("update cluster firewall options on PVE remote %q: no changes given: pass at least one flag",
					remote)
			}

			params := &pdmpve.UpdateRemotesFirewallOptionsParams{}
			if fl.Changed("delete") {
				params.Delete = ff.del
			}
			if fl.Changed("digest") {
				params.Digest = &ff.digest
			}
			if fl.Changed("ebtables") {
				params.Ebtables = &ff.ebtables
			}
			if fl.Changed("enable") {
				params.Enable = int64Ptr(map[bool]int64{true: 1, false: 0}[ff.enable])
			}
			if fl.Changed("log-ratelimit") {
				params.LogRatelimit = &ff.logRatelimit
			}
			if fl.Changed("policy-forward") {
				params.PolicyForward = &ff.policyForward
			}
			if fl.Changed("policy-in") {
				params.PolicyIn = &ff.policyIn
			}
			if fl.Changed("policy-out") {
				params.PolicyOut = &ff.policyOut
			}

			err := deps.PDM.Pve.UpdateRemotesFirewallOptions(cmd.Context(), remote, params)
			if err != nil {
				return fmt.Errorf("update cluster firewall options on PVE remote %q: %w", remote, err)
			}

			res := output.Result{Message: fmt.Sprintf("Cluster firewall options on PVE remote %q updated.", remote)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&ff.del, "delete", nil, "setting to reset to its default (repeatable)")
	f.StringVar(&ff.digest, "digest", "", "prevent changes if the config digest differs")
	f.BoolVar(&ff.ebtables, "ebtables", true, "enable ebtables rules cluster wide")
	f.BoolVar(&ff.enable, "enable", false, "enable the firewall cluster wide")
	f.StringVar(&ff.logRatelimit, "log-ratelimit", "", "log ratelimiting settings")
	f.StringVar(&ff.policyForward, "policy-forward", "", "firewall IO policy for the forward chain: ACCEPT|DROP|REJECT")
	f.StringVar(&ff.policyIn, "policy-in", "", "firewall IO policy for incoming traffic: ACCEPT|DROP|REJECT")
	f.StringVar(&ff.policyOut, "policy-out", "", "firewall IO policy for outgoing traffic: ACCEPT|DROP|REJECT")
	return cmd
}

// pveFirewallRuleEntry mirrors one element of the JSON array returned by
// GET /pve/remotes/{remote}/firewall/rules and
// GET /pve/remotes/{remote}/nodes/{node}/firewall/rules (identical property
// sets: action, comment, dest, dport, enable, icmp-type, iface, ipversion,
// log, macro, pos, proto, source, sport, type — pdm-apidoc.json, verified
// 2026-07-08).
type pveFirewallRuleEntry struct {
	Pos     *int64  `json:"pos,omitempty"`
	Type    *string `json:"type,omitempty"`
	Action  *string `json:"action,omitempty"`
	Enable  *int64  `json:"enable,omitempty"`
	Macro   *string `json:"macro,omitempty"`
	Iface   *string `json:"iface,omitempty"`
	Source  *string `json:"source,omitempty"`
	Dest    *string `json:"dest,omitempty"`
	Proto   *string `json:"proto,omitempty"`
	Dport   *string `json:"dport,omitempty"`
	Sport   *string `json:"sport,omitempty"`
	Comment *string `json:"comment,omitempty"`
}

// newPveFirewallRulesCmd builds `pmx pdm pve firewall rules <remote>` — get
// cluster firewall rules (GET /pve/remotes/{remote}/firewall/rules).
// Firewall rules are position-ordered (each rule's POS is meaningful and the
// list is evaluated top-to-bottom), so rows preserve server order rather
// than being sorted, unlike every discrete-entity ls in this package.
func newPveFirewallRulesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rules <remote>",
		Short: "Show a PVE remote's cluster firewall rules",
		Long:  "Get cluster firewall rules (GET /pve/remotes/{remote}/firewall/rules).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]

			resp, err := deps.PDM.Pve.ListRemotesFirewallRules(cmd.Context(), remote)
			if err != nil {
				return fmt.Errorf("list cluster firewall rules of PVE remote %q: %w", remote, err)
			}

			entries, err := nodeDecodeArray[pveFirewallRuleEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode cluster firewall rules of PVE remote %q: %w", remote, err)
			}

			res := renderFirewallRules(entries)
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// renderFirewallRules builds the shared table/Raw shape for cluster- and
// node-level firewall rule listings, both of which decode into
// pveFirewallRuleEntry.
func renderFirewallRules(entries []pveFirewallRuleEntry) output.Result {
	headers := []string{"POS", "TYPE", "ACTION", "ENABLE", "MACRO", "SOURCE", "DEST", "PROTO", "COMMENT"}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{
			int64PtrString(e.Pos), strPtrString(e.Type), strPtrString(e.Action), int64PtrString(e.Enable),
			strPtrString(e.Macro), strPtrString(e.Source), strPtrString(e.Dest), strPtrString(e.Proto),
			strPtrString(e.Comment),
		})
	}
	return output.Result{Headers: headers, Rows: rows, Raw: entries}
}
