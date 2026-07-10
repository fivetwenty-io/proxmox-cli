package pbs

import (
	"fmt"

	"github.com/spf13/cobra"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pbsnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// nodeNetworkListEntry mirrors one element of the JSON array PBS returns
// from GET /nodes/{node}/network, a subset of the fields GetNetworkResponse
// documents for a single interface.
type nodeNetworkListEntry struct {
	Name      string      `json:"name"`
	Type      string      `json:"type"`
	Active    pve.PVEBool `json:"active"`
	Autostart pve.PVEBool `json:"autostart"`
	Method    *string     `json:"method,omitempty"`
	Cidr      *string     `json:"cidr,omitempty"`
	Gateway   *string     `json:"gateway,omitempty"`
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
func (nf *networkFlags) applyCreate(cmd *cobra.Command, p *pbsnodes.CreateNetworkParams) {
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
func (nf *networkFlags) applyUpdate(cmd *cobra.Command, p *pbsnodes.UpdateNetwork2Params) {
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

// newNodeNetworkCmd builds `pmx pbs node network` and its
// ls/show/create/update/delete/revert/apply verbs (/nodes/{node}/network...).
func newNodeNetworkCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "network",
		Short: "Inspect and manage network interfaces on the node",
		Long: "Inspect and manage network interface configuration on the node. Create, update, " +
			"and delete changes are staged in /etc/network/interfaces.new until `network " +
			"apply` activates them, or `network revert` discards them.",
	}
	cmd.AddCommand(
		newNodeNetworkLsCmd(nf),
		newNodeNetworkShowCmd(nf),
		newNodeNetworkCreateCmd(nf),
		newNodeNetworkUpdateCmd(nf),
		newNodeNetworkDeleteCmd(nf),
		newNodeNetworkRevertCmd(nf),
		newNodeNetworkApplyCmd(nf),
	)
	return cmd
}

// newNodeNetworkLsCmd builds `pmx pbs node network ls` — list network
// interfaces (GET /nodes/{node}/network).
func newNodeNetworkLsCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List network interfaces on the node",
		Long: "List the node's network interfaces with type, active/autostart state, IPv4 " +
			"configuration method, CIDR address, and gateway.",
		Example: "  pmx pbs node network ls",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Nodes.ListNetwork(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("list network interfaces on node %q: %w", nf.node, err)
			}

			entries, err := nodeDecodeArray[nodeNetworkListEntry](rawItemsOf(resp))
			if err != nil {
				return fmt.Errorf("decode network interfaces on node %q: %w", nf.node, err)
			}

			headers := []string{"IFACE", "TYPE", "ACTIVE", "AUTOSTART", "METHOD", "CIDR", "GATEWAY"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					e.Name, e.Type, boolCellPVE(e.Active), boolCellPVE(e.Autostart),
					pbsFormatOptionalString(e.Method), pbsFormatOptionalString(e.Cidr), pbsFormatOptionalString(e.Gateway),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// boolCellPVE renders a pve.PVEBool for a table cell.
func boolCellPVE(b pve.PVEBool) string {
	if b.Bool() {
		return "true"
	}
	return "false"
}

// newNodeNetworkShowCmd builds `pmx pbs node network show <iface>` — read
// one interface's configuration (GET /nodes/{node}/network/{iface}).
func newNodeNetworkShowCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "show <iface>",
		Short:   "Show a single network interface's configuration",
		Long:    "Show the full configuration of one network interface, including any staged, not-yet-applied changes.",
		Example: "  pmx pbs node network show vmbr0",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			iface := args[0]

			resp, err := deps.PBS.Nodes.GetNetwork(cmd.Context(), nf.node, iface)
			if err != nil {
				return fmt.Errorf("get network interface %q on node %q: %w", iface, nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("get network interface %q on node %q: empty response from server", iface, nf.node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode network interface %q on node %q: %w", iface, nf.node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newNodeNetworkCreateCmd builds `pmx pbs node network create <iface>` —
// create a network interface configuration (POST /nodes/{node}/network).
func newNodeNetworkCreateCmd(nf *nodeFlags) *cobra.Command {
	var nwf networkFlags

	cmd := &cobra.Command{
		Use:   "create <iface>",
		Short: "Create a network interface configuration",
		Long: "Create a network interface configuration. Changes are staged in " +
			"/etc/network/interfaces.new; run `network apply` to activate them.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			iface := args[0]

			params := &pbsnodes.CreateNetworkParams{Iface: iface}
			nwf.applyCreate(cmd, params)

			err := deps.PBS.Nodes.CreateNetwork(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("create network interface %q on node %q: %w", iface, nf.node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Network interface %q created on node %q.", iface, nf.node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	nwf.registerCommon(cmd)

	return cmd
}

// newNodeNetworkUpdateCmd builds `pmx pbs node network update <iface>` —
// update a network interface configuration (PUT /nodes/{node}/network/{iface}).
func newNodeNetworkUpdateCmd(nf *nodeFlags) *cobra.Command {
	var nwf networkFlags

	cmd := &cobra.Command{
		Use:   "update <iface>",
		Short: "Update a network interface configuration",
		Long: "Update a network interface configuration. Changes are staged in " +
			"/etc/network/interfaces.new; run `network apply` to activate them.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			iface := args[0]

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update network interface %q on node %q: no changes requested: pass at least one flag",
					iface, nf.node)
			}

			params := &pbsnodes.UpdateNetwork2Params{}
			nwf.applyUpdate(cmd, params)

			err := deps.PBS.Nodes.UpdateNetwork2(cmd.Context(), nf.node, iface, params)
			if err != nil {
				return fmt.Errorf("update network interface %q on node %q: %w", iface, nf.node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Network interface %q on node %q updated.", iface, nf.node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	nwf.registerCommon(cmd)
	nwf.registerUpdateOnly(cmd)

	return cmd
}

// newNodeNetworkDeleteCmd builds `pmx pbs node network delete <iface>` —
// remove a single interface's configuration (DELETE /nodes/{node}/network/{iface}).
func newNodeNetworkDeleteCmd(nf *nodeFlags) *cobra.Command {
	var (
		digest string
		yes    bool
	)

	cmd := &cobra.Command{
		Use:   "delete <iface>",
		Short: "Remove a network interface configuration",
		Long: "Remove a single network interface configuration. Changes are staged in " +
			"/etc/network/interfaces.new; run `network apply` to activate them. This is " +
			"destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			iface := args[0]
			if !yes {
				return fmt.Errorf("refusing to remove network interface %q on node %q without confirmation: pass --yes/-y",
					iface, nf.node)
			}

			params := &pbsnodes.DeleteNetwork2Params{}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}

			err := deps.PBS.Nodes.DeleteNetwork2(cmd.Context(), nf.node, iface, params)
			if err != nil {
				return fmt.Errorf("delete network interface %q on node %q: %w", iface, nf.node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Network interface %q on node %q deleted.", iface, nf.node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "prevent changes if the config digest differs")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")

	return cmd
}

// newNodeNetworkRevertCmd builds `pmx pbs node network revert` — discard
// staged network changes (DELETE /nodes/{node}/network, removing
// /etc/network/interfaces.new).
func newNodeNetworkRevertCmd(nf *nodeFlags) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "revert",
		Short: "Discard staged network configuration changes",
		Long: "Discard every staged network configuration change by removing " +
			"/etc/network/interfaces.new. This is destructive: pass --yes/-y to confirm.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if !yes {
				return fmt.Errorf("refusing to revert staged network configuration on node %q without confirmation: pass --yes/-y",
					nf.node)
			}

			err := deps.PBS.Nodes.DeleteNetwork(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("revert network configuration on node %q: %w", nf.node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Staged network configuration on node %q reverted.", nf.node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// newNodeNetworkApplyCmd builds `pmx pbs node network apply` — activate
// staged network changes (PUT /nodes/{node}/network, requires ifupdown2).
func newNodeNetworkApplyCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Reload the network configuration, activating staged changes",
		Long:  "Reload the network configuration (requires ifupdown2), activating every staged change.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			err := deps.PBS.Nodes.UpdateNetwork(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("reload network configuration on node %q: %w", nf.node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Network configuration on node %q reloaded.", nf.node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}
