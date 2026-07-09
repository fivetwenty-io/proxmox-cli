package pdm

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pveclient "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pdmnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// nodeNetworkListEntry mirrors one element of the JSON array PDM returns
// from GET /nodes/{node}/network, a subset of the fields GetNetworkResponse
// documents for a single interface.
type nodeNetworkListEntry struct {
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	Active    pveclient.PVEBool `json:"active"`
	Autostart pveclient.PVEBool `json:"autostart"`
	Method    *string           `json:"method,omitempty"`
	Cidr      *string           `json:"cidr,omitempty"`
	Gateway   *string           `json:"gateway,omitempty"`
}

// newNodeNetworkCmd builds `pmx pdm node network` and its
// ls/show/create/update/delete/revert/apply verbs (/nodes/{node}/network...).
func newNodeNetworkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "network",
		Short: "Inspect and manage network interfaces on the node",
	}
	cmd.AddCommand(
		newNodeNetworkLsCmd(),
		newNodeNetworkShowCmd(),
		newNodeNetworkCreateCmd(),
		newNodeNetworkUpdateCmd(),
		newNodeNetworkDeleteCmd(),
		newNodeNetworkRevertCmd(),
		newNodeNetworkApplyCmd(),
	)
	return cmd
}

// newNodeNetworkLsCmd builds `pmx pdm node network ls <node>` — list
// network interfaces, sorted by interface name (GET /nodes/{node}/network).
func newNodeNetworkLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <node>",
		Short: "List network interfaces on the node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			resp, err := deps.PDM.Nodes.ListNetwork(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("list network interfaces on node %q: %w", node, err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[nodeNetworkListEntry](items, "network interface")
			if err != nil {
				return err
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Name < table[j].Entry.Name })

			headers := []string{"IFACE", "TYPE", "ACTIVE", "AUTOSTART", "METHOD", "CIDR", "GATEWAY"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{
					e.Name, e.Type, boolCellPVE(e.Active), boolCellPVE(e.Autostart),
					strPtrString(e.Method), strPtrString(e.Cidr), strPtrString(e.Gateway),
				})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// boolCellPVE renders a pveclient.PVEBool for a table cell.
func boolCellPVE(b pveclient.PVEBool) string {
	if b.Bool() {
		return "true"
	}
	return "false"
}

// newNodeNetworkShowCmd builds `pmx pdm node network show <node> <iface>` —
// read one interface's configuration (GET /nodes/{node}/network/{iface}).
func newNodeNetworkShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <node> <iface>",
		Short: "Show a single network interface's configuration",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, iface := args[0], args[1]

			resp, err := deps.PDM.Nodes.GetNetwork(cmd.Context(), node, iface)
			if err != nil {
				return fmt.Errorf("get network interface %q on node %q: %w", iface, node, err)
			}
			if resp == nil {
				return fmt.Errorf("get network interface %q on node %q: empty response from server", iface, node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode network interface %q on node %q: %w", iface, node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// networkFlags collects the network-interface attribute flags shared by
// create and update. Every field maps directly onto a CreateNetworkParams /
// UpdateNetwork2Params field of the same name.
type networkFlags struct {
	autostart          bool
	bondPrimary        string
	bondMode           string
	bondXmitHashPolicy string
	bridgePorts        string
	bridgeVlanAware    bool
	cidr               string
	cidr6              string
	comments           string
	comments6          string
	gateway            string
	gateway6           string
	method             string
	method6            string
	mtu                int64
	slaves             string
	ifaceType          string
	vlanID             int64
	vlanRawDevice      string

	// update-only
	del    []string
	digest string
}

// registerCommon binds the attribute flags accepted by both create and update.
func (nf *networkFlags) registerCommon(cmd *cobra.Command) {
	f := cmd.Flags()
	f.BoolVar(&nf.autostart, "autostart", false, "autostart this interface")
	f.StringVar(&nf.bondPrimary, "bond-primary", "", "primary interface name for active-backup bonds")
	f.StringVar(&nf.bondMode, "bond-mode", "", "Linux bonding mode")
	f.StringVar(&nf.bondXmitHashPolicy, "bond-xmit-hash-policy", "", "bond transmit hash policy for LACP (802.3ad)")
	f.StringVar(&nf.bridgePorts, "bridge-ports", "", "comma-separated list of bridge member devices")
	f.BoolVar(&nf.bridgeVlanAware, "bridge-vlan-aware", false, "enable bridge VLAN support")
	f.StringVar(&nf.cidr, "cidr", "", "IPv4 address with netmask (CIDR notation)")
	f.StringVar(&nf.cidr6, "cidr6", "", "IPv6 address with netmask (CIDR notation)")
	f.StringVar(&nf.comments, "comments", "", "comments (inet, may span multiple lines)")
	f.StringVar(&nf.comments6, "comments6", "", "comments (inet6, may span multiple lines)")
	f.StringVar(&nf.gateway, "gateway", "", "IPv4 gateway address")
	f.StringVar(&nf.gateway6, "gateway6", "", "IPv6 gateway address")
	f.StringVar(&nf.method, "method", "", "IPv4 configuration method")
	f.StringVar(&nf.method6, "method6", "", "IPv6 configuration method")
	f.Int64Var(&nf.mtu, "mtu", 0, "maximum transmission unit")
	f.StringVar(&nf.slaves, "slaves", "", "comma-separated list of bond member devices")
	f.StringVar(&nf.ifaceType, "type", "", "network interface type")
	f.Int64Var(&nf.vlanID, "vlan-id", 0, "VLAN ID")
	f.StringVar(&nf.vlanRawDevice, "vlan-raw-device", "", "VLAN raw (parent) device name")
}

// registerUpdateOnly binds the update-only delete/digest flags.
func (nf *networkFlags) registerUpdateOnly(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringArrayVar(&nf.del, "delete", nil, "config key to reset to its default (repeatable)")
	f.StringVar(&nf.digest, "digest", "", "prevent changes if the config digest differs")
}

// applyCreate builds the create payload, forwarding optional flags only when set.
func (nf *networkFlags) applyCreate(cmd *cobra.Command, p *pdmnodes.CreateNetworkParams) {
	fl := cmd.Flags()
	if fl.Changed("autostart") {
		p.Autostart = &nf.autostart
	}
	if fl.Changed("bond-primary") {
		p.BondPrimary = &nf.bondPrimary
	}
	if fl.Changed("bond-mode") {
		p.BondMode = &nf.bondMode
	}
	if fl.Changed("bond-xmit-hash-policy") {
		p.BondXmitHashPolicy = &nf.bondXmitHashPolicy
	}
	if fl.Changed("bridge-ports") {
		p.BridgePorts = &nf.bridgePorts
	}
	if fl.Changed("bridge-vlan-aware") {
		p.BridgeVlanAware = &nf.bridgeVlanAware
	}
	if fl.Changed("cidr") {
		p.Cidr = &nf.cidr
	}
	if fl.Changed("cidr6") {
		p.Cidr6 = &nf.cidr6
	}
	if fl.Changed("comments") {
		p.Comments = &nf.comments
	}
	if fl.Changed("comments6") {
		p.Comments6 = &nf.comments6
	}
	if fl.Changed("gateway") {
		p.Gateway = &nf.gateway
	}
	if fl.Changed("gateway6") {
		p.Gateway6 = &nf.gateway6
	}
	if fl.Changed("method") {
		p.Method = &nf.method
	}
	if fl.Changed("method6") {
		p.Method6 = &nf.method6
	}
	if fl.Changed("mtu") {
		p.Mtu = &nf.mtu
	}
	if fl.Changed("slaves") {
		p.Slaves = &nf.slaves
	}
	if fl.Changed("type") {
		p.Type = &nf.ifaceType
	}
	if fl.Changed("vlan-id") {
		p.VlanId = &nf.vlanID
	}
	if fl.Changed("vlan-raw-device") {
		p.VlanRawDevice = &nf.vlanRawDevice
	}
}

// applyUpdate builds the update payload, forwarding optional flags only when set.
func (nf *networkFlags) applyUpdate(cmd *cobra.Command, p *pdmnodes.UpdateNetwork2Params) {
	fl := cmd.Flags()
	if fl.Changed("autostart") {
		p.Autostart = &nf.autostart
	}
	if fl.Changed("bond-primary") {
		p.BondPrimary = &nf.bondPrimary
	}
	if fl.Changed("bond-mode") {
		p.BondMode = &nf.bondMode
	}
	if fl.Changed("bond-xmit-hash-policy") {
		p.BondXmitHashPolicy = &nf.bondXmitHashPolicy
	}
	if fl.Changed("bridge-ports") {
		p.BridgePorts = &nf.bridgePorts
	}
	if fl.Changed("bridge-vlan-aware") {
		p.BridgeVlanAware = &nf.bridgeVlanAware
	}
	if fl.Changed("cidr") {
		p.Cidr = &nf.cidr
	}
	if fl.Changed("cidr6") {
		p.Cidr6 = &nf.cidr6
	}
	if fl.Changed("comments") {
		p.Comments = &nf.comments
	}
	if fl.Changed("comments6") {
		p.Comments6 = &nf.comments6
	}
	if fl.Changed("gateway") {
		p.Gateway = &nf.gateway
	}
	if fl.Changed("gateway6") {
		p.Gateway6 = &nf.gateway6
	}
	if fl.Changed("method") {
		p.Method = &nf.method
	}
	if fl.Changed("method6") {
		p.Method6 = &nf.method6
	}
	if fl.Changed("mtu") {
		p.Mtu = &nf.mtu
	}
	if fl.Changed("slaves") {
		p.Slaves = &nf.slaves
	}
	if fl.Changed("type") {
		p.Type = &nf.ifaceType
	}
	if fl.Changed("vlan-id") {
		p.VlanId = &nf.vlanID
	}
	if fl.Changed("vlan-raw-device") {
		p.VlanRawDevice = &nf.vlanRawDevice
	}
	if fl.Changed("delete") {
		p.Delete = nf.del
	}
	if fl.Changed("digest") {
		p.Digest = &nf.digest
	}
}

// newNodeNetworkCreateCmd builds `pmx pdm node network create <node> <iface>`
// — create a network interface configuration (POST /nodes/{node}/network).
func newNodeNetworkCreateCmd() *cobra.Command {
	var nwf networkFlags

	cmd := &cobra.Command{
		Use:   "create <node> <iface>",
		Short: "Create a network interface configuration",
		Long: "Create a network interface configuration. Changes are staged in " +
			"/etc/network/interfaces.new; run `network apply` to activate them.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, iface := args[0], args[1]

			params := &pdmnodes.CreateNetworkParams{Iface: iface}
			nwf.applyCreate(cmd, params)

			err := deps.PDM.Nodes.CreateNetwork(cmd.Context(), node, params)
			if err != nil {
				return fmt.Errorf("create network interface %q on node %q: %w", iface, node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Network interface %q created on node %q.", iface, node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	nwf.registerCommon(cmd)

	return cmd
}

// newNodeNetworkUpdateCmd builds `pmx pdm node network update <node> <iface>`
// — update a network interface configuration (PUT /nodes/{node}/network/{iface}).
func newNodeNetworkUpdateCmd() *cobra.Command {
	var nwf networkFlags

	cmd := &cobra.Command{
		Use:   "update <node> <iface>",
		Short: "Update a network interface configuration",
		Long: "Update a network interface configuration. Changes are staged in " +
			"/etc/network/interfaces.new; run `network apply` to activate them.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, iface := args[0], args[1]

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update network interface %q on node %q: no changes requested: pass at least one flag",
					iface, node)
			}

			params := &pdmnodes.UpdateNetwork2Params{}
			nwf.applyUpdate(cmd, params)

			err := deps.PDM.Nodes.UpdateNetwork2(cmd.Context(), node, iface, params)
			if err != nil {
				return fmt.Errorf("update network interface %q on node %q: %w", iface, node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Network interface %q on node %q updated.", iface, node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	nwf.registerCommon(cmd)
	nwf.registerUpdateOnly(cmd)

	return cmd
}

// newNodeNetworkDeleteCmd builds `pmx pdm node network delete <node> <iface>`
// — remove a single interface's configuration (DELETE /nodes/{node}/network/{iface}).
func newNodeNetworkDeleteCmd() *cobra.Command {
	var (
		digest string
		yes    bool
	)

	cmd := &cobra.Command{
		Use:   "delete <node> <iface>",
		Short: "Remove a network interface configuration",
		Long: "Remove a single network interface configuration. Changes are staged in " +
			"/etc/network/interfaces.new; run `network apply` to activate them. This is " +
			"destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, iface := args[0], args[1]
			if !yes {
				return fmt.Errorf("refusing to remove network interface %q on node %q without confirmation: pass --yes/-y",
					iface, node)
			}

			params := &pdmnodes.DeleteNetwork2Params{}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}

			err := deps.PDM.Nodes.DeleteNetwork2(cmd.Context(), node, iface, params)
			if err != nil {
				return fmt.Errorf("delete network interface %q on node %q: %w", iface, node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Network interface %q on node %q deleted.", iface, node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "prevent changes if the config digest differs")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")

	return cmd
}

// newNodeNetworkRevertCmd builds `pmx pdm node network revert <node>` —
// discard staged network changes (DELETE /nodes/{node}/network, removing
// /etc/network/interfaces.new).
func newNodeNetworkRevertCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "revert <node>",
		Short: "Discard staged network configuration changes",
		Long: "Discard every staged network configuration change by removing " +
			"/etc/network/interfaces.new. This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]
			if !yes {
				return fmt.Errorf("refusing to revert staged network configuration on node %q without confirmation: pass --yes/-y",
					node)
			}

			err := deps.PDM.Nodes.DeleteNetwork(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("revert network configuration on node %q: %w", node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Staged network configuration on node %q reverted.", node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newNodeNetworkApplyCmd builds `pmx pdm node network apply <node>` —
// activate staged network changes (PUT /nodes/{node}/network, requires
// ifupdown2).
//
// UpdateNetwork runs as an asynchronous task: its returns.pattern in the PDM
// API schema is the UPID regex (pdm-apidoc.json, verified 2026-07-08), and
// nodes_gen.go types UpdateNetworkResponse as `= json.RawMessage`
// (nodes_gen.go:1172-1176, v3.6.0) — unlike the PBS analog, whose
// UpdateNetwork returns only `error` and is called fire-and-forget
// (internal/cli/pbs/node_network.go:456-474). PDM reloads via ifupdown2
// under a worker task, so this blocks until it finishes unless --async
// (persistent flag) is set.
func newNodeNetworkApplyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply <node>",
		Short: "Reload the network configuration, activating staged changes",
		Long: "Reload the network configuration (requires ifupdown2), activating every " +
			"staged change. Runs as an asynchronous task; the command blocks until it " +
			"finishes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node := args[0]

			resp, err := deps.PDM.Nodes.UpdateNetwork(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("reload network configuration on node %q: %w", node, err)
			}
			if resp == nil {
				return fmt.Errorf("reload network configuration on node %q: empty response from server", node)
			}

			msg := fmt.Sprintf("Network configuration on node %q reloaded.", node)
			return finishAsync(cmd, deps, *resp, msg)
		},
	}
}
