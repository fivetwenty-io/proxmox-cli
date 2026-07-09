package pdm

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pdmnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/optionschema"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newNodeCmd builds `pmx pdm node` — administer the Proxmox Datacenter
// Manager's own node(s) natively (/nodes): status, power control,
// configuration, DNS, time, logs, network, APT, certificates, background
// tasks, SDN VRF lookups, and subscription. Every sub-command except `ls`
// takes the node name as its first positional argument (PDM's own node
// namespace, distinct from the remote-scoped PVE/PBS proxying groups).
//
// CreateTermproxy (POST /nodes/{node}/termproxy) and ListVncwebsocket (GET
// /nodes/{node}/vncwebsocket) are intentionally excluded: both exist solely
// to hand off an interactive shell/VNC session to PDM's own web UI and have
// no meaningful CLI representation.
func newNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Administer this Proxmox Datacenter Manager's own node(s)",
		Long: "Inspect and manage the Proxmox Datacenter Manager's own node(s) " +
			"natively: status, power control, configuration, DNS, time, logs, " +
			"network interfaces, APT packages and repositories, TLS certificates, " +
			"background tasks, SDN VRF lookups, and subscription. Distinct from the " +
			"remote-scoped PVE/PBS command groups, which proxy to managed remotes.",
	}
	cmd.AddCommand(
		newNodeLsCmd(),
		newNodeStatusCmd(),
		newNodePowerCmd("reboot", "Reboot the node"),
		newNodePowerCmd("shutdown", "Shut down the node"),
		newNodeConfigCmd(),
		newNodeDNSCmd(),
		newNodeTimeCmd(),
		newNodeJournalCmd(),
		newNodeSyslogCmd(),
		newNodeReportCmd(),
		newNodeRrddataCmd(),
		newNodeNetworkCmd(),
		newNodeAptCmd(),
		newNodeCertificateCmd(),
		newNodeTaskCmd(),
		newNodeSdnCmd(),
		newNodeSubscriptionCmd(),
	)
	return cmd
}

// nodeListEntry is the decoded shape of one element of GET /nodes, PDM's
// compatibility cluster-node listing.
type nodeListEntry struct {
	Node string `json:"node"`
}

// newNodeLsCmd builds `pmx pdm node ls` — list the node entries visible at
// the compatibility cluster-node listing (GET /nodes), sorted by node name.
//
// Like every other discrete-entity ls in this package (remote.go, realm_ad.go,
// etc.), entries are sorted by their identifying name with the Raw/Rows
// pair kept together through the sort (remote.go's inline `type xRow
// struct{entry, raw}` convention) — unlike task-list or time-series
// listings (remote_task.go, node_tasks.go, node_logs.go's rrddata), a named
// list of nodes has no other meaningful order.
func newNodeLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List node entries visible to this Proxmox Datacenter Manager",
		Long:  "List the node entries returned by the compatibility cluster-node listing (GET /nodes).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Nodes.ListNodes(cmd.Context())
			if err != nil {
				return fmt.Errorf("list nodes: %w", err)
			}

			items := rawItemsOf(resp)

			type nodeListRow struct {
				entry nodeListEntry
				raw   map[string]any
			}
			table := make([]nodeListRow, 0, len(items))

			for _, raw := range items {
				var e nodeListEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode node entry: %w", err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode node entry: %w", err)
				}

				table = append(table, nodeListRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Node < table[j].entry.Node })

			headers := []string{"NODE"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				rows = append(rows, []string{t.entry.Node})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// nodeDecodeArray unmarshals each element of items into T, returning a hard
// error on the first malformed element rather than silently dropping it — a
// partially-decoded list must never be mistaken for a complete one.
func nodeDecodeArray[T any](items []json.RawMessage) ([]T, error) {
	out := make([]T, 0, len(items))

	for i, raw := range items {
		var v T

		err := json.Unmarshal(raw, &v)
		if err != nil {
			return nil, fmt.Errorf("decode entry %d: %w", i, err)
		}

		out = append(out, v)
	}

	return out, nil
}

// nodeDecodeText unmarshals a json.RawMessage that carries a plain JSON
// string, the shape PDM uses for free-text endpoints such as GET
// /nodes/{node}/report and GET /nodes/{node}/apt/changelog.
func nodeDecodeText(raw json.RawMessage) (string, error) {
	var text string

	err := json.Unmarshal(raw, &text)
	if err != nil {
		return "", fmt.Errorf("unexpected non-string response: %w", err)
	}

	return text, nil
}

// newNodeStatusCmd builds `pmx pdm node status <node>` — read node memory,
// CPU, and root-disk usage (GET /nodes/{node}/status).
func newNodeStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <node>",
		Short: "Show node memory, CPU, and root-disk usage",
		Long:  "Show node memory, CPU, load average, kernel, and root-filesystem usage (GET /nodes/{node}/status).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			resp, err := deps.PDM.Nodes.ListStatus(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("get status for node %q: %w", node, err)
			}
			if resp == nil {
				return fmt.Errorf("get status for node %q: empty response from server", node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode status for node %q: %w", node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newNodePowerCmd builds a node power-control command (reboot or shutdown)
// that wraps POST /nodes/{node}/status with the matching command. Both
// actions are disruptive, so each is gated behind --yes/-y.
//
// CreateStatus runs synchronously: its returns.type in the PDM API schema is
// "null" (pdm-apidoc.json, verified 2026-07-08) and nodes_gen.go emits
// `CreateStatus(ctx, node string, params *CreateStatusParams) error` — no
// response type at all (nodes_gen.go:137-139,1717-1745, v3.6.0), unlike
// endpoints whose returns.pattern is the UPID regex. There is no task to
// wait on or hand back with --async, matching the PBS analog (also `error`
// only, internal/cli/pbs/node.go:152-179): power commands fire the OS-level
// action directly rather than queuing a worker task.
func newNodePowerCmd(verb, short string) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   verb + " <node>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			if !yes {
				return fmt.Errorf("refusing to %s node %q without confirmation: pass --yes/-y", verb, node)
			}

			params := &pdmnodes.CreateStatusParams{Command: verb}

			err := deps.PDM.Nodes.CreateStatus(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("%s node %q: %w", verb, node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Node %q %s initiated.", node, verb)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the disruptive operation without prompting")

	return cmd
}

// --- config -----------------------------------------------------------------

// newNodeConfigCmd builds `pmx pdm node config` and its show/update verbs
// (GET/PUT /nodes/{node}/config).
func newNodeConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or update the node configuration",
	}
	cmd.AddCommand(newNodeConfigShowCmd(), newNodeConfigUpdateCmd())
	return cmd
}

// newNodeConfigShowCmd builds `pmx pdm node config show <node>` — show the
// node configuration (GET /nodes/{node}/config). Unlike PUT's
// UpdateConfigParams, GET's ListConfigResponse carries no digest field at
// all (nodes_gen.go:806-817 vs :849-864, v3.6.0; the PDM API schema's GET
// returns.properties has no "digest" key either, verified 2026-07-08): there
// is no way to read back the digest to guard a subsequent update against
// concurrent modification, unlike most other PDM config endpoints.
func newNodeConfigShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <node>",
		Short: "Show the node configuration",
		Long: "Show the node configuration (GET /nodes/{node}/config). The API omits " +
			"options left at their built-in defaults; pass --defaults to also list " +
			"those, with the value they effectively have.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			resp, err := deps.PDM.Nodes.ListConfig(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("get config for node %q: %w", node, err)
			}
			if resp == nil {
				return fmt.Errorf("get config for node %q: empty response from server", node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode config for node %q: %w", node, err)
			}

			single := stringMap(fields)
			var raw any = fields
			if withDefaults {
				single, raw = optionschema.MergeDefaults(nodeConfigOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// nodeConfigFlags collects the update-only flags for `node config update`,
// each mapping directly onto an UpdateConfigParams field of the same name.
type nodeConfigFlags struct {
	ciphersTLS12, ciphersTLS13 string
	defaultLang                string
	emailFrom                  string
	httpProxy                  string
	del                        []string
	digest                     string
}

// newNodeConfigUpdateCmd builds `pmx pdm node config update <node>` —
// update the node configuration (PUT /nodes/{node}/config).
func newNodeConfigUpdateCmd() *cobra.Command {
	var cf nodeConfigFlags

	cmd := &cobra.Command{
		Use:   "update <node>",
		Short: "Update the node configuration",
		Long: "Update the node configuration (PUT /nodes/{node}/config). Only flags " +
			"explicitly set are sent; use --delete to reset properties to their " +
			"default instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			fl := cmd.Flags()

			if !anyFlagChanged(fl) {
				return fmt.Errorf("update config on node %q: no changes requested: pass at least one flag", node)
			}

			params := &pdmnodes.UpdateConfigParams{}
			if fl.Changed("ciphers-tls-1.2") {
				params.CiphersTls12 = &cf.ciphersTLS12
			}
			if fl.Changed("ciphers-tls-1.3") {
				params.CiphersTls13 = &cf.ciphersTLS13
			}
			if fl.Changed("default-lang") {
				params.DefaultLang = &cf.defaultLang
			}
			if fl.Changed("email-from") {
				params.EmailFrom = &cf.emailFrom
			}
			if fl.Changed("http-proxy") {
				params.HttpProxy = &cf.httpProxy
			}
			if fl.Changed("delete") {
				params.Delete = cf.del
			}
			if fl.Changed("digest") {
				params.Digest = &cf.digest
			}

			err := deps.PDM.Nodes.UpdateConfig(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("update config on node %q: %w", node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Configuration for node %q updated.", node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&cf.ciphersTLS12, "ciphers-tls-1.2", "", "OpenSSL cipher list for TLS <= 1.2")
	f.StringVar(&cf.ciphersTLS13, "ciphers-tls-1.3", "", "OpenSSL ciphersuites list for TLS 1.3")
	f.StringVar(&cf.defaultLang, "default-lang", "", "default UI language")
	f.StringVar(&cf.emailFrom, "email-from", "", "e-mail address notifications are sent from")
	f.StringVar(&cf.httpProxy, "http-proxy", "", "HTTP proxy configuration [http://]<host>[:port]")
	f.StringArrayVar(&cf.del, "delete", nil, "config key to reset to its default (repeatable)")
	f.StringVar(&cf.digest, "digest", "", "prevent changes if the config digest differs")

	return cmd
}

// --- dns ----------------------------------------------------------------

// newNodeDNSCmd builds `pmx pdm node dns` and its show/update verbs
// (GET/PUT /nodes/{node}/dns).
func newNodeDNSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dns",
		Short: "Show or update the node's DNS settings",
	}
	cmd.AddCommand(newNodeDNSShowCmd(), newNodeDNSUpdateCmd())
	return cmd
}

func newNodeDNSShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <node>",
		Short: "Show the node's DNS settings",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			resp, err := deps.PDM.Nodes.ListDns(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("get dns settings for node %q: %w", node, err)
			}
			if resp == nil {
				return fmt.Errorf("get dns settings for node %q: empty response from server", node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode dns settings for node %q: %w", node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newNodeDNSUpdateCmd() *cobra.Command {
	var (
		dns1, dns2, dns3, search, digest string
		del                              []string
	)

	cmd := &cobra.Command{
		Use:   "update <node>",
		Short: "Update the node's DNS settings",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			fl := cmd.Flags()

			if !anyFlagChanged(fl) {
				return fmt.Errorf("update dns on node %q: no changes requested: pass at least one flag", node)
			}

			params := &pdmnodes.UpdateDnsParams{}
			if fl.Changed("dns1") {
				params.Dns1 = &dns1
			}
			if fl.Changed("dns2") {
				params.Dns2 = &dns2
			}
			if fl.Changed("dns3") {
				params.Dns3 = &dns3
			}
			if fl.Changed("search") {
				params.Search = &search
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}

			err := deps.PDM.Nodes.UpdateDns(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("update dns on node %q: %w", node, err)
			}

			res := output.Result{Message: fmt.Sprintf("DNS settings for node %q updated.", node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&dns1, "dns1", "", "first name server IP address")
	f.StringVar(&dns2, "dns2", "", "second name server IP address")
	f.StringVar(&dns3, "dns3", "", "third name server IP address")
	f.StringVar(&search, "search", "", "search domain for host-name lookup")
	f.StringArrayVar(&del, "delete", nil, "config key to reset to its default (repeatable)")
	f.StringVar(&digest, "digest", "", "prevent changes if the config digest differs")

	return cmd
}

// --- time -----------------------------------------------------------------

// newNodeTimeCmd builds `pmx pdm node time` and its show/update verbs
// (GET/PUT /nodes/{node}/time).
func newNodeTimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "time",
		Short: "Show or update the node's time zone",
	}
	cmd.AddCommand(newNodeTimeShowCmd(), newNodeTimeUpdateCmd())
	return cmd
}

func newNodeTimeShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <node>",
		Short: "Show the node's server time and time zone",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			resp, err := deps.PDM.Nodes.ListTime(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("get time settings for node %q: %w", node, err)
			}
			if resp == nil {
				return fmt.Errorf("get time settings for node %q: empty response from server", node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode time settings for node %q: %w", node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newNodeTimeUpdateCmd() *cobra.Command {
	var timezone string

	cmd := &cobra.Command{
		Use:   "update <node>",
		Short: "Set the node's time zone",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			params := &pdmnodes.UpdateTimeParams{Timezone: timezone}

			err := deps.PDM.Nodes.UpdateTime(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("set time zone on node %q: %w", node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Time zone for node %q set to %q.", node, timezone)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&timezone, "timezone", "", "time zone name, e.g. UTC or America/New_York (required)")
	cli.MustMarkRequired(cmd, "timezone")

	return cmd
}
