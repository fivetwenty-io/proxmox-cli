package pdm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	pdmpve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/pve"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newPveNodeCmd builds `pmx pdm pve node` — inspect and manage a PVE
// remote's node(s): status, config, network, RRD metrics, subscription,
// APT, firewall, and SDN VRF lookups.
func newPveNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Inspect and manage a PVE remote's node(s)",
	}
	cmd.AddCommand(
		newPveNodeLsCmd(),
		newPveNodeStatusCmd(),
		newPveNodeConfigCmd(),
		newPveNodeNetworkCmd(),
		newPveNodeRrddataCmd(),
		newPveNodeSubscriptionCmd(),
		newPveNodeAptCmd(),
		newPveNodeFirewallCmd(),
		newPveNodeSdnCmd(),
	)
	return cmd
}

// pveNodeEntry is the decoded shape of one element of GET
// /pve/remotes/{remote}/nodes (pdm-apidoc.json, verified 2026-07-08).
type pveNodeEntry struct {
	Node   string   `json:"node"`
	Status *string  `json:"status,omitempty"`
	Cpu    *float64 `json:"cpu,omitempty"`
	Maxcpu *int64   `json:"maxcpu,omitempty"`
	Mem    *int64   `json:"mem,omitempty"`
	Maxmem *int64   `json:"maxmem,omitempty"`
	Uptime *int64   `json:"uptime,omitempty"`
	Level  *string  `json:"level,omitempty"`
}

// newPveNodeLsCmd builds `pmx pdm pve node ls <remote>` — query the remote's
// version/node list (GET /pve/remotes/{remote}/nodes), sorted by node name
// like every other discrete-entity ls in this package.
func newPveNodeLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <remote>",
		Short: "List a PVE remote's nodes",
		Long:  "Query the remote's version/node list (GET /pve/remotes/{remote}/nodes).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]

			resp, err := deps.PDM.Pve.ListRemotesNodes(cmd.Context(), remote)
			if err != nil {
				return fmt.Errorf("list nodes of PVE remote %q: %w", remote, err)
			}

			items := rawItemsOf(resp)
			type nodeRow struct {
				entry pveNodeEntry
				raw   map[string]any
			}
			table := make([]nodeRow, 0, len(items))

			for _, raw := range items {
				var e pveNodeEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode node entry of PVE remote %q: %w", remote, err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode node entry of PVE remote %q: %w", remote, err)
				}

				table = append(table, nodeRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Node < table[j].entry.Node })

			headers := []string{"NODE", "STATUS", "CPU", "MAXCPU", "MEM", "MAXMEM", "UPTIME", "LEVEL"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{
					e.Node, strPtrString(e.Status), float64PtrString(e.Cpu), int64PtrString(e.Maxcpu),
					int64PtrString(e.Mem), int64PtrString(e.Maxmem), int64PtrString(e.Uptime), strPtrString(e.Level),
				})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newPveNodeStatusCmd builds `pmx pdm pve node status <remote> <node>` —
// get status for the node (GET /pve/remotes/{remote}/nodes/{node}/status).
func newPveNodeStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <remote> <node>",
		Short: "Show a PVE remote node's status",
		Long:  "Get status for the node (GET /pve/remotes/{remote}/nodes/{node}/status).",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]

			resp, err := deps.PDM.Pve.ListRemotesNodesStatus(cmd.Context(), remote, node)
			if err != nil {
				return fmt.Errorf("get status of node %q on PVE remote %q: %w", node, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get status of node %q on PVE remote %q: empty response from server", node, remote)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode status of node %q on PVE remote %q: %w", node, remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newPveNodeConfigCmd builds `pmx pdm pve node config <remote> <node>` —
// get config for the node (GET /pve/remotes/{remote}/nodes/{node}/config).
// There is no corresponding update method in the generated Service
// interface, so this command is read-only.
func newPveNodeConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config <remote> <node>",
		Short: "Show a PVE remote node's configuration",
		Long:  "Get config for the node (GET /pve/remotes/{remote}/nodes/{node}/config).",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]

			resp, err := deps.PDM.Pve.ListRemotesNodesConfig(cmd.Context(), remote, node)
			if err != nil {
				return fmt.Errorf("get config of node %q on PVE remote %q: %w", node, remote, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode config of node %q on PVE remote %q: %w", node, remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newPveNodeNetworkCmd builds `pmx pdm pve node network <remote> <node>` —
// get network interfaces from a PVE node (GET
// /pve/remotes/{remote}/nodes/{node}/network).
func newPveNodeNetworkCmd() *cobra.Command {
	var interfaceType string
	cmd := &cobra.Command{
		Use:   "network <remote> <node>",
		Short: "List a PVE remote node's network interfaces",
		Long:  "Get network interfaces from PVE node (GET /pve/remotes/{remote}/nodes/{node}/network).",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]

			params := &pdmpve.ListRemotesNodesNetworkParams{}
			if cmd.Flags().Changed("interface-type") {
				params.InterfaceType = &interfaceType
			}

			resp, err := deps.PDM.Pve.ListRemotesNodesNetwork(cmd.Context(), remote, node, params)
			if err != nil {
				return fmt.Errorf("list network interfaces of node %q on PVE remote %q: %w", node, remote, err)
			}

			items := decodeRawList(rawItemsOf(resp))

			headers := []string{"IFACE", "TYPE", "METHOD", "ACTIVE", "AUTOSTART", "ADDRESS", "GATEWAY"}
			rows := make([][]string, 0, len(items))
			for _, m := range items {
				rows = append(rows, []string{
					scalarString(m["iface"]), scalarString(m["type"]), scalarString(m["method"]),
					scalarString(m["active"]), scalarString(m["autostart"]), scalarString(m["address"]),
					scalarString(m["gateway"]),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: items}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&interfaceType, "interface-type", "", "only list this interface type")
	return cmd
}

// pveNodeRrdEntry is a table-relevant subset of the JSON object returned by
// each element of GET /pve/remotes/{remote}/nodes/{node}/rrddata (16 fields
// total per the PDM API schema, verified 2026-07-08); every field is still
// preserved losslessly in Raw via decodeRawList. Mirrors pbsNodeRrdEntry's
// identical subset of the structurally analogous PBS endpoint.
type pveNodeRrdEntry struct {
	Time       int64    `json:"time"`
	CpuCurrent *float64 `json:"cpu-current,omitempty"`
	MemUsed    *float64 `json:"mem-used,omitempty"`
	MemTotal   *float64 `json:"mem-total,omitempty"`
	DiskUsed   *float64 `json:"disk-used,omitempty"`
	NetIn      *float64 `json:"net-in,omitempty"`
	NetOut     *float64 `json:"net-out,omitempty"`
}

// newPveNodeRrddataCmd builds `pmx pdm pve node rrddata <remote> <node>` —
// read RRD node stats for a PVE remote's node (GET
// /pve/remotes/{remote}/nodes/{node}/rrddata). Time-series data: rendered in
// server order, not sorted, matching every other RRD listing in this package.
func newPveNodeRrddataCmd() *cobra.Command {
	var (
		timeframe string
		cf        string
	)
	cmd := &cobra.Command{
		Use:   "rrddata <remote> <node>",
		Short: "Read a PVE remote node's RRD metrics",
		Long: "Read RRD (round-robin database) node stats for a PVE remote's node over the " +
			"given time frame and consolidation function (GET /pve/remotes/{remote}/nodes/{node}/rrddata).",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]

			if !stringInSlice(timeframe, validRemoteRrdTimeframes) {
				return fmt.Errorf("get rrddata for node %q on PVE remote %q: --timeframe must be one of %s (got %q)",
					node, remote, strings.Join(validRemoteRrdTimeframes, ", "), timeframe)
			}
			if !stringInSlice(cf, validRemoteRrdConsolidations) {
				return fmt.Errorf("get rrddata for node %q on PVE remote %q: --cf must be one of %s (got %q)",
					node, remote, strings.Join(validRemoteRrdConsolidations, ", "), cf)
			}

			params := &pdmpve.ListRemotesNodesRrddataParams{Cf: cf, Timeframe: timeframe}

			resp, err := deps.PDM.Pve.ListRemotesNodesRrddata(cmd.Context(), remote, node, params)
			if err != nil {
				return fmt.Errorf("get rrddata for node %q on PVE remote %q: %w", node, remote, err)
			}

			items := rawItemsOf(resp)
			entries, err := nodeDecodeArray[pveNodeRrdEntry](items)
			if err != nil {
				return fmt.Errorf("decode rrddata for node %q on PVE remote %q: %w", node, remote, err)
			}

			headers := []string{"TIME", "CPU-CURRENT", "MEM-USED", "MEM-TOTAL", "DISK-USED", "NET-IN", "NET-OUT"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					int64PtrString(&e.Time), float64PtrString(e.CpuCurrent), float64PtrString(e.MemUsed),
					float64PtrString(e.MemTotal), float64PtrString(e.DiskUsed), float64PtrString(e.NetIn),
					float64PtrString(e.NetOut),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&timeframe, "timeframe", "", "RRD time frame: hour|day|week|month|year|decade (required)")
	f.StringVar(&cf, "cf", "AVERAGE", "RRD consolidation function: MAX|AVERAGE")
	cli.MustMarkRequired(cmd, "timeframe")
	return cmd
}

// newPveNodeSubscriptionCmd builds `pmx pdm pve node subscription <remote>
// <node>` — get subscription for the node (GET
// /pve/remotes/{remote}/nodes/{node}/subscription).
func newPveNodeSubscriptionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subscription <remote> <node>",
		Short: "Show a PVE remote node's subscription info",
		Long:  "Get subscription for the node (GET /pve/remotes/{remote}/nodes/{node}/subscription).",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]

			resp, err := deps.PDM.Pve.ListRemotesNodesSubscription(cmd.Context(), remote, node)
			if err != nil {
				return fmt.Errorf("get subscription of node %q on PVE remote %q: %w", node, remote, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode subscription of node %q on PVE remote %q: %w", node, remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// --- apt ---------------------------------------------------------------

// newPveNodeAptCmd builds `pmx pdm pve node apt` — updates/update-database/
// repositories/changelog verbs (/pve/remotes/{remote}/nodes/{node}/apt...).
func newPveNodeAptCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apt",
		Short: "Inspect and manage APT packages and repositories on a PVE remote's node",
	}
	cmd.AddCommand(
		newPveNodeAptUpdatesCmd(),
		newPveNodeAptUpdateDatabaseCmd(),
		newPveNodeAptRepositoriesCmd(),
		newPveNodeAptChangelogCmd(),
	)
	return cmd
}

// pveNodeAptPackageEntry mirrors one element of the JSON array returned by
// GET /pve/remotes/{remote}/nodes/{node}/apt/update. Field names are
// capitalized because PDM proxies the PVE host's own APT-update response
// verbatim here, the identical convention pbsNodeAptPackageEntry documents
// for the structurally identical PBS endpoint (pdm-apidoc.json, verified
// 2026-07-08).
type pveNodeAptPackageEntry struct {
	Package    string `json:"Package"`
	OldVersion string `json:"OldVersion,omitempty"`
	Version    string `json:"Version"`
	Priority   string `json:"Priority"`
	Section    string `json:"Section"`
	Origin     string `json:"Origin"`
	Arch       string `json:"Arch,omitempty"`
	ExtraInfo  string `json:"ExtraInfo,omitempty"`
}

// newPveNodeAptUpdatesCmd builds `pmx pdm pve node apt updates <remote>
// <node>` — list available package updates for a remote PVE node (GET
// /pve/remotes/{remote}/nodes/{node}/apt/update).
func newPveNodeAptUpdatesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "updates <remote> <node>",
		Short: "List available APT package updates on a PVE remote's node",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]

			resp, err := deps.PDM.Pve.ListRemotesNodesAptUpdate(cmd.Context(), remote, node)
			if err != nil {
				return fmt.Errorf("list apt updates on node %q of PVE remote %q: %w", node, remote, err)
			}

			entries, err := nodeDecodeArray[pveNodeAptPackageEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode apt updates on node %q of PVE remote %q: %w", node, remote, err)
			}

			headers := []string{"PACKAGE", "OLD-VERSION", "NEW-VERSION", "PRIORITY", "SECTION"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{e.Package, e.OldVersion, e.Version, e.Priority, e.Section})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newPveNodeAptUpdateDatabaseCmd builds `pmx pdm pve node apt
// update-database <remote> <node>` — update the APT database of a remote
// node (POST /pve/remotes/{remote}/nodes/{node}/apt/update).
//
// CreateRemotesNodesAptUpdate returns CreateRemotesNodesAptUpdateResponse =
// json.RawMessage (pve_gen.go:3775-3807, v3.6.0), carrying the task's UPID
// string — a data-bearing response, not a discarded one. This task runs on
// the PVE remote itself, not on PDM's own node, so its completion is polled
// via finishPveRemoteAsync (which polls the pve group's
// ListRemotesTasksStatus) rather than finishAsync (which polls PDM's local
// node-task endpoint and would 404 for a remote-hosted UPID).
func newPveNodeAptUpdateDatabaseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update-database <remote> <node>",
		Short: "Refresh the APT package database on a PVE remote's node",
		Long: "Refresh the local APT package database from the configured repositories " +
			"on a PVE remote's node (POST /pve/remotes/{remote}/nodes/{node}/apt/update). " +
			"Runs as an asynchronous task on the remote; the command blocks until it " +
			"finishes unless --async (persistent flag) is set.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]

			resp, err := deps.PDM.Pve.CreateRemotesNodesAptUpdate(cmd.Context(), remote, node)
			if err != nil {
				return fmt.Errorf("refresh apt index on node %q of PVE remote %q: %w", node, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("refresh apt index on node %q of PVE remote %q: empty response from server", node, remote)
			}

			msg := fmt.Sprintf("APT package index on node %q of PVE remote %q refreshed.", node, remote)
			return finishPveRemoteAsync(cmd, deps, remote, *resp, msg)
		},
	}
}

// newPveNodeAptRepositoriesCmd builds `pmx pdm pve node apt repositories
// <remote> <node>` — get configured APT repositories on a remote node (GET
// /pve/remotes/{remote}/nodes/{node}/apt/repositories). The response is
// Raw-heavy (parsed source files, standard-repo status, warnings), so Single
// carries only summary counts while Raw renders the full structure
// losslessly, matching pbs_proxy_node.go's `pbs node apt repositories`.
func newPveNodeAptRepositoriesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "repositories <remote> <node>",
		Short: "Show parsed APT repository information on a PVE remote's node",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]

			out, err := deps.PDM.Pve.ListRemotesNodesAptRepositories(cmd.Context(), remote, node)
			if err != nil {
				return fmt.Errorf("get apt repositories on node %q of PVE remote %q: %w", node, remote, err)
			}
			if out == nil {
				return fmt.Errorf("get apt repositories on node %q of PVE remote %q: empty response from server", node, remote)
			}

			single := map[string]string{
				"digest":         out.Digest,
				"files":          fmt.Sprintf("%d", len(out.Files)),
				"standard-repos": fmt.Sprintf("%d", len(out.StandardRepos)),
				"errors":         fmt.Sprintf("%d", len(out.Errors)),
				"infos":          fmt.Sprintf("%d", len(out.Infos)),
			}

			res := output.Result{Single: single, Raw: out}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newPveNodeAptChangelogCmd builds `pmx pdm pve node apt changelog <remote>
// <node> <package>` — retrieve the changelog of a package on a remote node
// (GET /pve/remotes/{remote}/nodes/{node}/apt/changelog).
func newPveNodeAptChangelogCmd() *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "changelog <remote> <node> <package>",
		Short: "Show the changelog of an APT package on a PVE remote's node",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node, pkg := args[0], args[1], args[2]

			params := &pdmpve.ListRemotesNodesAptChangelogParams{Name: pkg}
			if cmd.Flags().Changed("version") {
				params.Version = &version
			}

			resp, err := deps.PDM.Pve.ListRemotesNodesAptChangelog(cmd.Context(), remote, node, params)
			if err != nil {
				return fmt.Errorf("get changelog for package %q on node %q of PVE remote %q: %w", pkg, node, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get changelog for package %q on node %q of PVE remote %q: empty response from server",
					pkg, node, remote)
			}

			text, err := nodeDecodeText(*resp)
			if err != nil {
				return fmt.Errorf("decode changelog for package %q on node %q of PVE remote %q: %w", pkg, node, remote, err)
			}

			res := output.Result{Message: text, Raw: text}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&version, "version", "", "package version (defaults to the candidate version)")
	return cmd
}

// --- firewall ------------------------------------------------------------

// newPveNodeFirewallCmd builds `pmx pdm pve node firewall` — options/rules/
// status verbs for a PVE remote node
// (/pve/remotes/{remote}/nodes/{node}/firewall/...).
func newPveNodeFirewallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "firewall",
		Short: "Inspect and manage a PVE remote node's firewall",
	}
	cmd.AddCommand(newPveNodeFirewallOptionsCmd(), newPveNodeFirewallRulesCmd(), newPveNodeFirewallStatusCmd())
	return cmd
}

// newPveNodeFirewallOptionsCmd builds `pmx pdm pve node firewall options`
// and its show/update verbs (GET/PUT
// /pve/remotes/{remote}/nodes/{node}/firewall/options).
func newPveNodeFirewallOptionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "options",
		Short: "Show or update a PVE remote node's firewall options",
	}
	cmd.AddCommand(newPveNodeFirewallOptionsShowCmd(), newPveNodeFirewallOptionsUpdateCmd())
	return cmd
}

// newPveNodeFirewallOptionsShowCmd builds `pmx pdm pve node firewall
// options show <remote> <node>` — get node firewall options (GET
// /pve/remotes/{remote}/nodes/{node}/firewall/options).
func newPveNodeFirewallOptionsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <remote> <node>",
		Short: "Show a PVE remote node's firewall options",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]

			resp, err := deps.PDM.Pve.ListRemotesNodesFirewallOptions(cmd.Context(), remote, node)
			if err != nil {
				return fmt.Errorf("get firewall options of node %q on PVE remote %q: %w", node, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get firewall options of node %q on PVE remote %q: empty response from server", node, remote)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode firewall options of node %q on PVE remote %q: %w", node, remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// pveNodeFirewallOptionsFlags collects the update flags for `node firewall
// options update`, each mapping directly onto an
// UpdateRemotesNodesFirewallOptionsParams field of the same name — a much
// larger, unrelated option set from the cluster-level equivalent
// (pveFirewallOptionsFlags, pve_proxy_firewall.go).
type pveNodeFirewallOptionsFlags struct {
	del                                                                            []string
	digest                                                                         string
	logLevelForward, logLevelIn, logLevelOut                                       string
	nfConntrackHelpers                                                             string
	smurfLogLevel, tcpFlagsLogLevel                                                string
	enable, logNfConntrack, ndp, nfConntrackAllowInvalid                           bool
	nftables, nosmurfs, protectionSynflood, tcpflags                               bool
	nfConntrackMax, nfConntrackTCPTimeoutEstablished, nfConntrackTCPTimeoutSynRecv int64
	protectionSynfloodBurst, protectionSynfloodRate                                int64
}

// newPveNodeFirewallOptionsUpdateCmd builds `pmx pdm pve node firewall
// options update <remote> <node>` — update a node's firewall configuration
// (PUT /pve/remotes/{remote}/nodes/{node}/firewall/options). This is a
// configuration update, not a destructive action, so it is guarded by
// anyFlagChanged rather than --yes/-y, matching every other config-update
// command in this package.
func newPveNodeFirewallOptionsUpdateCmd() *cobra.Command {
	var ff pveNodeFirewallOptionsFlags
	cmd := &cobra.Command{
		Use:   "update <remote> <node>",
		Short: "Update a PVE remote node's firewall options",
		Long: "Update a node's firewall configuration (PUT " +
			"/pve/remotes/{remote}/nodes/{node}/firewall/options). Only flags explicitly " +
			"set are sent; use --delete to reset properties to their default instead.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]
			fl := cmd.Flags()

			if !anyFlagChanged(fl) {
				return fmt.Errorf("update firewall options on node %q of PVE remote %q: no changes requested: pass at least one flag",
					node, remote)
			}

			params := &pdmpve.UpdateRemotesNodesFirewallOptionsParams{}
			if fl.Changed("delete") {
				params.Delete = ff.del
			}
			if fl.Changed("digest") {
				params.Digest = &ff.digest
			}
			if fl.Changed("enable") {
				params.Enable = &ff.enable
			}
			if fl.Changed("log-level-forward") {
				params.LogLevelForward = &ff.logLevelForward
			}
			if fl.Changed("log-level-in") {
				params.LogLevelIn = &ff.logLevelIn
			}
			if fl.Changed("log-level-out") {
				params.LogLevelOut = &ff.logLevelOut
			}
			if fl.Changed("log-nf-conntrack") {
				params.LogNfConntrack = &ff.logNfConntrack
			}
			if fl.Changed("ndp") {
				params.Ndp = &ff.ndp
			}
			if fl.Changed("nf-conntrack-allow-invalid") {
				params.NfConntrackAllowInvalid = &ff.nfConntrackAllowInvalid
			}
			if fl.Changed("nf-conntrack-helpers") {
				params.NfConntrackHelpers = &ff.nfConntrackHelpers
			}
			if fl.Changed("nf-conntrack-max") {
				params.NfConntrackMax = &ff.nfConntrackMax
			}
			if fl.Changed("nf-conntrack-tcp-timeout-established") {
				params.NfConntrackTcpTimeoutEstablished = &ff.nfConntrackTCPTimeoutEstablished
			}
			if fl.Changed("nf-conntrack-tcp-timeout-syn-recv") {
				params.NfConntrackTcpTimeoutSynRecv = &ff.nfConntrackTCPTimeoutSynRecv
			}
			if fl.Changed("nftables") {
				params.Nftables = &ff.nftables
			}
			if fl.Changed("nosmurfs") {
				params.Nosmurfs = &ff.nosmurfs
			}
			if fl.Changed("protection-synflood") {
				params.ProtectionSynflood = &ff.protectionSynflood
			}
			if fl.Changed("protection-synflood-burst") {
				params.ProtectionSynfloodBurst = &ff.protectionSynfloodBurst
			}
			if fl.Changed("protection-synflood-rate") {
				params.ProtectionSynfloodRate = &ff.protectionSynfloodRate
			}
			if fl.Changed("smurf-log-level") {
				params.SmurfLogLevel = &ff.smurfLogLevel
			}
			if fl.Changed("tcp-flags-log-level") {
				params.TcpFlagsLogLevel = &ff.tcpFlagsLogLevel
			}
			if fl.Changed("tcpflags") {
				params.Tcpflags = &ff.tcpflags
			}

			err := deps.PDM.Pve.UpdateRemotesNodesFirewallOptions(cmd.Context(), remote, node, params)
			if err != nil {
				return fmt.Errorf("update firewall options on node %q of PVE remote %q: %w", node, remote, err)
			}

			res := output.Result{
				Message: fmt.Sprintf("Firewall options on node %q of PVE remote %q updated.", node, remote),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&ff.del, "delete", nil, "setting to reset to its default (repeatable)")
	f.StringVar(&ff.digest, "digest", "", "prevent changes if the config digest differs")
	f.BoolVar(&ff.enable, "enable", false, "enable host firewall rules")
	f.StringVar(&ff.logLevelForward, "log-level-forward", "", "firewall log level for the forward chain")
	f.StringVar(&ff.logLevelIn, "log-level-in", "", "firewall log level for incoming traffic")
	f.StringVar(&ff.logLevelOut, "log-level-out", "", "firewall log level for outgoing traffic")
	f.BoolVar(&ff.logNfConntrack, "log-nf-conntrack", false, "enable logging of conntrack information")
	f.BoolVar(&ff.ndp, "ndp", false, "enable NDP (Neighbor Discovery Protocol)")
	f.BoolVar(&ff.nfConntrackAllowInvalid, "nf-conntrack-allow-invalid", false, "allow invalid packets on connection tracking")
	f.StringVar(&ff.nfConntrackHelpers, "nf-conntrack-helpers", "",
		"enable conntrack helpers for specific protocols (comma-separated)")
	f.Int64Var(&ff.nfConntrackMax, "nf-conntrack-max", 0, "maximum number of tracked connections")
	f.Int64Var(&ff.nfConntrackTCPTimeoutEstablished, "nf-conntrack-tcp-timeout-established", 0,
		"conntrack established timeout")
	f.Int64Var(&ff.nfConntrackTCPTimeoutSynRecv, "nf-conntrack-tcp-timeout-syn-recv", 0, "conntrack syn recv timeout")
	f.BoolVar(&ff.nftables, "nftables", false, "enable nftables based firewall (tech preview)")
	f.BoolVar(&ff.nosmurfs, "nosmurfs", false, "enable SMURFS filter")
	f.BoolVar(&ff.protectionSynflood, "protection-synflood", false, "enable synflood protection")
	f.Int64Var(&ff.protectionSynfloodBurst, "protection-synflood-burst", 0, "synflood protection rate burst by ip src")
	f.Int64Var(&ff.protectionSynfloodRate, "protection-synflood-rate", 0, "synflood protection rate syn/sec by ip src")
	f.StringVar(&ff.smurfLogLevel, "smurf-log-level", "", "firewall log level for smurf filtering")
	f.StringVar(&ff.tcpFlagsLogLevel, "tcp-flags-log-level", "", "firewall log level for illegal TCP flag combinations")
	f.BoolVar(&ff.tcpflags, "tcpflags", false, "filter illegal combinations of TCP flags")
	return cmd
}

// newPveNodeFirewallRulesCmd builds `pmx pdm pve node firewall rules
// <remote> <node>` — get node firewall rules (GET
// /pve/remotes/{remote}/nodes/{node}/firewall/rules). Same
// pveFirewallRuleEntry shape and server-order rendering as `pve firewall
// rules` (pve_proxy_firewall.go) — node firewall rules are position-ordered
// too.
func newPveNodeFirewallRulesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rules <remote> <node>",
		Short: "Show a PVE remote node's firewall rules",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]

			resp, err := deps.PDM.Pve.ListRemotesNodesFirewallRules(cmd.Context(), remote, node)
			if err != nil {
				return fmt.Errorf("list firewall rules of node %q on PVE remote %q: %w", node, remote, err)
			}

			entries, err := nodeDecodeArray[pveFirewallRuleEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode firewall rules of node %q on PVE remote %q: %w", node, remote, err)
			}

			res := renderFirewallRules(entries)
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newPveNodeFirewallStatusCmd builds `pmx pdm pve node firewall status
// <remote> <node>` — get firewall status of a specific node (GET
// /pve/remotes/{remote}/nodes/{node}/firewall/status).
func newPveNodeFirewallStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <remote> <node>",
		Short: "Show a PVE remote node's firewall status",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]

			resp, err := deps.PDM.Pve.ListRemotesNodesFirewallStatus(cmd.Context(), remote, node)
			if err != nil {
				return fmt.Errorf("get firewall status of node %q on PVE remote %q: %w", node, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get firewall status of node %q on PVE remote %q: empty response from server", node, remote)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode firewall status of node %q on PVE remote %q: %w", node, remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// --- sdn -------------------------------------------------------------------

// newPveNodeSdnCmd builds `pmx pdm pve node sdn` and its vnet/zone VRF
// lookup verbs (/pve/remotes/{remote}/nodes/{node}/sdn/...).
//
// GET .../sdn, GET .../sdn/vnets/{vnet}, and GET .../sdn/zones/{zone} are
// directory indexes with no data behind them (ListRemotesNodesSdn,
// GetRemotesNodesSdnVnets, GetRemotesNodesSdnZones each return only `error`,
// pve_gen.go:4167-4260, v3.6.0), so there is no `ls`/`show` verb for them —
// only the two documented VRF sub-resources are exposed, matching
// node_sdn.go's identical exclusion for PDM's own nodes.
func newPveNodeSdnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sdn",
		Short: "Read SDN VRF information for a PVE remote's node",
	}
	cmd.AddCommand(newPveNodeSdnVnetCmd(), newPveNodeSdnZoneCmd())
	return cmd
}

func newPveNodeSdnVnetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vnet",
		Short: "Read EVPN vnet MAC-VRF information",
	}
	cmd.AddCommand(newPveNodeSdnVnetMacVrfCmd())
	return cmd
}

// newPveNodeSdnVnetMacVrfCmd builds `pmx pdm pve node sdn vnet mac-vrf
// <remote> <node> <vnet>` — get the MAC-VRF for an EVPN vnet for a node on a
// given remote (GET
// /pve/remotes/{remote}/nodes/{node}/sdn/vnets/{vnet}/mac-vrf).
func newPveNodeSdnVnetMacVrfCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mac-vrf <remote> <node> <vnet>",
		Short: "Show the MAC-VRF for an EVPN vnet",
		Long:  "Get the MAC-VRF (MAC address to next-hop mapping) for an EVPN vnet, for a node on a given remote.",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node, vnet := args[0], args[1], args[2]

			resp, err := deps.PDM.Pve.ListRemotesNodesSdnVnetsMacVrf(cmd.Context(), remote, node, vnet)
			if err != nil {
				return fmt.Errorf("get mac-vrf for vnet %q on node %q of PVE remote %q: %w", vnet, node, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get mac-vrf for vnet %q on node %q of PVE remote %q: empty response from server",
					vnet, node, remote)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode mac-vrf for vnet %q on node %q of PVE remote %q: %w", vnet, node, remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newPveNodeSdnZoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "zone",
		Short: "Read EVPN zone IP-VRF information",
	}
	cmd.AddCommand(newPveNodeSdnZoneIPVrfCmd())
	return cmd
}

// newPveNodeSdnZoneIPVrfCmd builds `pmx pdm pve node sdn zone ip-vrf
// <remote> <node> <zone>` — get the IP-VRF for an EVPN zone for a node on a
// given remote (GET
// /pve/remotes/{remote}/nodes/{node}/sdn/zones/{zone}/ip-vrf).
func newPveNodeSdnZoneIPVrfCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ip-vrf <remote> <node> <zone>",
		Short: "Show the IP-VRF for an EVPN zone",
		Long:  "Get the IP-VRF (route table) for an EVPN zone, for a node on a given remote.",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node, zone := args[0], args[1], args[2]

			resp, err := deps.PDM.Pve.ListRemotesNodesSdnZonesIpVrf(cmd.Context(), remote, node, zone)
			if err != nil {
				return fmt.Errorf("get ip-vrf for zone %q on node %q of PVE remote %q: %w", zone, node, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get ip-vrf for zone %q on node %q of PVE remote %q: empty response from server",
					zone, node, remote)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode ip-vrf for zone %q on node %q of PVE remote %q: %w", zone, node, remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}
