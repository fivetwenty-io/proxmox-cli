package node

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newNetworkCmd builds the `pmx pve node network` sub-tree: the host network
// interfaces and bridges of the resolved node, plus the apply/revert verbs that
// commit or discard the pending /etc/network/interfaces.new changes. Edits to a
// host interface only become live once `network apply` reloads the networking
// stack; `network revert` throws the staged changes away.
func newNetworkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "network",
		Short: "Manage the host network interfaces of a node",
		Long: "List, inspect, create, update, and delete the host network interfaces " +
			"and bridges of the resolved node. Configuration changes are staged; use " +
			"`network apply` to reload them or `network revert` to discard them.",
	}
	cmd.AddCommand(
		newNetworkListCmd(),
		newNetworkGetCmd(),
		newNetworkCreateCmd(),
		newNetworkUpdateCmd(),
		newNetworkDeleteCmd(),
		newNetworkApplyCmd(),
		newNetworkRevertCmd(),
	)
	return cmd
}

// newNetstatCmd builds `pmx pve node netstat` — reads per-interface traffic counters
// from the resolved node.
func newNetstatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "netstat",
		Short: "Show network interface statistics for the node",
		Long: "Read the per-interface traffic counters (bytes, packets, errors) from the " +
			"resolved node. This is a read-only point-in-time snapshot.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListNetstat(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("get netstat on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
}

// netIfaceEntry is the minimal decoded shape of one network list entry. PVE
// returns the active/autostart flags as integers (1/0).
type netIfaceEntry struct {
	Iface     string `json:"iface"`
	Type      string `json:"type"`
	Method    string `json:"method"`
	Active    int64  `json:"active"`
	Autostart int64  `json:"autostart"`
	Cidr      string `json:"cidr"`
	Address   string `json:"address"`
	Gateway   string `json:"gateway"`
}

func netIfaceHeaders() []string {
	return []string{"IFACE", "TYPE", "METHOD", "ACTIVE", "AUTOSTART", "CIDR", "ADDRESS", "GATEWAY"}
}

func netIfaceRow(e netIfaceEntry) []string {
	return []string{
		e.Iface, e.Type, e.Method,
		strconv.FormatInt(e.Active, 10), strconv.FormatInt(e.Autostart, 10),
		e.Cidr, e.Address, e.Gateway,
	}
}

// ---- list ------------------------------------------------------------------

func newNetworkListCmd() *cobra.Command {
	var ifaceType string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the host network interfaces",
		Long: "List the host network interfaces and bridges configured on the resolved " +
			"node. Pass --type to restrict the listing to one interface type.",
		Example: `  pmx pve node network list
  pmx pve node network list --type bridge`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.ListNetworkParams{}
			if cmd.Flags().Changed("type") {
				params.Type = &ifaceType
			}
			resp, err := deps.API.Nodes.ListNetwork(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("list network interfaces on node %q: %w", deps.Node, err)
			}
			entries := make([]netIfaceEntry, 0)
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e netIfaceEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode network interface entry: %w", err)
					}
					entries = append(entries, e)
					rows = append(rows, netIfaceRow(e))
				}
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: netIfaceHeaders(), Rows: rows, Raw: entries}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&ifaceType, "type", "",
		"only list interfaces of this type, for example bridge, bond, eth, or vlan")
	return cmd
}

// ---- get -------------------------------------------------------------------

func newNetworkGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <iface>",
		Short:   "Show a single host network interface",
		Long:    "Show every configured field of a single host network interface on the resolved node.",
		Example: `  pmx pve node network get vmbr0`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			iface := args[0]
			// The typed client method decodes only `method` and `type`, discarding
			// the address, gateway, bridge, and bond fields. Fetch the raw object
			// instead and render every key generically.
			path := fmt.Sprintf("/nodes/%s/network/%s", deps.Node, iface)
			data, err := deps.API.Raw.GetCtx(cmd.Context(), path, nil)
			if err != nil {
				return fmt.Errorf("get network interface %q on node %q: %w", iface, deps.Node, err)
			}
			single, raw, err := objectToSingle(data)
			if err != nil {
				return fmt.Errorf("get network interface %q on node %q: %w", iface, deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

// netIfaceFlags collects the shared flag values for interface create/update so
// the two commands stay in sync.
type netIfaceFlags struct {
	address, netmask, cidr, gateway           string
	address6, cidr6, gateway6                 string
	comments, comments6                       string
	bondMode, bondPrimary, bondXmitHashPolicy string
	bridgePorts, bridgeVids                   string
	ovsBridge, ovsPorts, ovsBonds, ovsOptions string
	slaves, vlanRawDevice                     string
	autostart, bridgeVlanAware                bool
	mtu, netmask6, ovsTag, vlanID             int64
	del                                       string
}

func (f *netIfaceFlags) register(cmd *cobra.Command, withDelete bool) {
	fl := cmd.Flags()
	fl.StringVar(&f.address, "address", "", "IPv4 address")
	fl.StringVar(&f.netmask, "netmask", "", "IPv4 network mask")
	fl.StringVar(&f.cidr, "cidr", "", "IPv4 CIDR, for example 10.0.0.5/24")
	fl.StringVar(&f.gateway, "gateway", "", "default IPv4 gateway address")
	fl.StringVar(&f.address6, "address6", "", "IPv6 address")
	fl.Int64Var(&f.netmask6, "netmask6", 0, "IPv6 prefix length")
	fl.StringVar(&f.cidr6, "cidr6", "", "IPv6 CIDR")
	fl.StringVar(&f.gateway6, "gateway6", "", "default IPv6 gateway address")
	fl.StringVar(&f.comments, "comments", "", "comment for the IPv4 configuration")
	fl.StringVar(&f.comments6, "comments6", "", "comment for the IPv6 configuration")
	fl.BoolVar(&f.autostart, "autostart", false, "automatically start the interface on boot")
	fl.Int64Var(&f.mtu, "mtu", 0, "interface MTU")
	fl.StringVar(&f.bridgePorts, "bridge-ports", "", "interfaces to add to the bridge")
	fl.BoolVar(&f.bridgeVlanAware, "bridge-vlan-aware", false, "enable bridge VLAN support")
	fl.StringVar(&f.bridgeVids, "bridge-vids", "", "allowed VLANs, for example '2 4 100-200'")
	fl.StringVar(&f.bondMode, "bond-mode", "", "bonding mode")
	fl.StringVar(&f.bondPrimary, "bond-primary", "", "primary interface for an active-backup bond")
	fl.StringVar(&f.bondXmitHashPolicy, "bond-xmit-hash-policy", "", "transmit hash policy for balance-xor and 802.3ad")
	fl.StringVar(&f.slaves, "slaves", "", "interfaces used by the bonding device")
	fl.StringVar(&f.ovsBridge, "ovs-bridge", "", "OVS bridge associated with an OVS port")
	fl.StringVar(&f.ovsPorts, "ovs-ports", "", "interfaces to add to the OVS bridge")
	fl.StringVar(&f.ovsBonds, "ovs-bonds", "", "interfaces used by the OVS bonding device")
	fl.StringVar(&f.ovsOptions, "ovs-options", "", "OVS interface options")
	fl.Int64Var(&f.ovsTag, "ovs-tag", 0, "VLAN tag used by an OVS port, int port, or bond")
	fl.Int64Var(&f.vlanID, "vlan-id", 0, "vlan-id for a custom named vlan interface (ifupdown2 only)")
	fl.StringVar(&f.vlanRawDevice, "vlan-raw-device", "", "raw interface for the vlan interface")
	if withDelete {
		fl.StringVar(&f.del, "delete", "", "comma-separated list of interface settings to clear")
	}
}

// ---- create ----------------------------------------------------------------

func newNetworkCreateCmd() *cobra.Command {
	var (
		f         netIfaceFlags
		iface     string
		ifaceType string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a host network interface",
		Long: "Create a new host network interface or bridge. --iface (the interface name) " +
			"and --type (for example bridge, bond, eth, vlan, or OVSBridge) are required. " +
			"The change is staged; run `network apply` to make it live.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if !cmd.Flags().Changed("iface") {
				return fmt.Errorf("--iface is required: the network interface name, for example vmbr1")
			}
			if !cmd.Flags().Changed("type") {
				return fmt.Errorf("--type is required: for example bridge, bond, eth, vlan, or OVSBridge")
			}

			params := &nodes.CreateNetworkParams{Iface: iface, Type: ifaceType}
			applyNetworkCreateFlags(cmd, &f, params)

			if err := deps.API.Nodes.CreateNetwork(cmd.Context(), deps.Node, params); err != nil {
				return fmt.Errorf("create network interface %q on node %q: %w", iface, deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf(
					"Network interface %q staged on node %q. Run `pmx pve node network apply` to make it live.",
					iface, deps.Node)}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&iface, "iface", "", "network interface name, for example vmbr1")
	cmd.Flags().StringVar(&ifaceType, "type", "", "interface type: bridge, bond, eth, alias, vlan, OVSBridge, OVSBond, OVSPort, OVSIntPort, or unknown")
	f.register(cmd, false)
	return cmd
}

func applyNetworkCreateFlags(cmd *cobra.Command, f *netIfaceFlags, params *nodes.CreateNetworkParams) {
	fl := cmd.Flags()
	if fl.Changed("address") {
		params.Address = &f.address
	}
	if fl.Changed("netmask") {
		params.Netmask = &f.netmask
	}
	if fl.Changed("cidr") {
		params.Cidr = &f.cidr
	}
	if fl.Changed("gateway") {
		params.Gateway = &f.gateway
	}
	if fl.Changed("address6") {
		params.Address6 = &f.address6
	}
	if fl.Changed("netmask6") {
		params.Netmask6 = &f.netmask6
	}
	if fl.Changed("cidr6") {
		params.Cidr6 = &f.cidr6
	}
	if fl.Changed("gateway6") {
		params.Gateway6 = &f.gateway6
	}
	if fl.Changed("comments") {
		params.Comments = &f.comments
	}
	if fl.Changed("comments6") {
		params.Comments6 = &f.comments6
	}
	if fl.Changed("autostart") {
		params.Autostart = &f.autostart
	}
	if fl.Changed("mtu") {
		params.Mtu = &f.mtu
	}
	if fl.Changed("bridge-ports") {
		params.BridgePorts = &f.bridgePorts
	}
	if fl.Changed("bridge-vlan-aware") {
		params.BridgeVlanAware = &f.bridgeVlanAware
	}
	if fl.Changed("bridge-vids") {
		params.BridgeVids = &f.bridgeVids
	}
	if fl.Changed("bond-mode") {
		params.BondMode = &f.bondMode
	}
	if fl.Changed("bond-primary") {
		params.BondPrimary = &f.bondPrimary
	}
	if fl.Changed("bond-xmit-hash-policy") {
		params.BondXmitHashPolicy = &f.bondXmitHashPolicy
	}
	if fl.Changed("slaves") {
		params.Slaves = &f.slaves
	}
	if fl.Changed("ovs-bridge") {
		params.OvsBridge = &f.ovsBridge
	}
	if fl.Changed("ovs-ports") {
		params.OvsPorts = &f.ovsPorts
	}
	if fl.Changed("ovs-bonds") {
		params.OvsBonds = &f.ovsBonds
	}
	if fl.Changed("ovs-options") {
		params.OvsOptions = &f.ovsOptions
	}
	if fl.Changed("ovs-tag") {
		params.OvsTag = &f.ovsTag
	}
	if fl.Changed("vlan-id") {
		params.VlanId = &f.vlanID
	}
	if fl.Changed("vlan-raw-device") {
		params.VlanRawDevice = &f.vlanRawDevice
	}
}

// ---- update ----------------------------------------------------------------

func newNetworkUpdateCmd() *cobra.Command {
	var (
		f         netIfaceFlags
		ifaceType string
	)
	cmd := &cobra.Command{
		Use:   "set <iface>",
		Short: "Modify a host network interface",
		Long: "Update an existing host network interface. --type is required. The change " +
			"is staged; run `network apply` to make it live.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			iface := args[0]
			if !cmd.Flags().Changed("type") {
				return fmt.Errorf("--type is required: for example bridge, bond, eth, vlan, or OVSBridge")
			}

			params := &nodes.UpdateNetwork2Params{Type: ifaceType}
			applyNetworkUpdateFlags(cmd, &f, params)

			if err := deps.API.Nodes.UpdateNetwork2(cmd.Context(), deps.Node, iface, params); err != nil {
				return fmt.Errorf("update network interface %q on node %q: %w", iface, deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf(
					"Network interface %q updated on node %q. Run `pmx pve node network apply` to make it live.",
					iface, deps.Node)}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&ifaceType, "type", "", "interface type: bridge, bond, eth, alias, vlan, OVSBridge, OVSBond, OVSPort, OVSIntPort, or unknown")
	f.register(cmd, true)
	return cmd
}

func applyNetworkUpdateFlags(cmd *cobra.Command, f *netIfaceFlags, params *nodes.UpdateNetwork2Params) {
	fl := cmd.Flags()
	if fl.Changed("address") {
		params.Address = &f.address
	}
	if fl.Changed("netmask") {
		params.Netmask = &f.netmask
	}
	if fl.Changed("cidr") {
		params.Cidr = &f.cidr
	}
	if fl.Changed("gateway") {
		params.Gateway = &f.gateway
	}
	if fl.Changed("address6") {
		params.Address6 = &f.address6
	}
	if fl.Changed("netmask6") {
		params.Netmask6 = &f.netmask6
	}
	if fl.Changed("cidr6") {
		params.Cidr6 = &f.cidr6
	}
	if fl.Changed("gateway6") {
		params.Gateway6 = &f.gateway6
	}
	if fl.Changed("comments") {
		params.Comments = &f.comments
	}
	if fl.Changed("comments6") {
		params.Comments6 = &f.comments6
	}
	if fl.Changed("autostart") {
		params.Autostart = &f.autostart
	}
	if fl.Changed("mtu") {
		params.Mtu = &f.mtu
	}
	if fl.Changed("bridge-ports") {
		params.BridgePorts = &f.bridgePorts
	}
	if fl.Changed("bridge-vlan-aware") {
		params.BridgeVlanAware = &f.bridgeVlanAware
	}
	if fl.Changed("bridge-vids") {
		params.BridgeVids = &f.bridgeVids
	}
	if fl.Changed("bond-mode") {
		params.BondMode = &f.bondMode
	}
	if fl.Changed("bond-primary") {
		params.BondPrimary = &f.bondPrimary
	}
	if fl.Changed("bond-xmit-hash-policy") {
		params.BondXmitHashPolicy = &f.bondXmitHashPolicy
	}
	if fl.Changed("slaves") {
		params.Slaves = &f.slaves
	}
	if fl.Changed("ovs-bridge") {
		params.OvsBridge = &f.ovsBridge
	}
	if fl.Changed("ovs-ports") {
		params.OvsPorts = &f.ovsPorts
	}
	if fl.Changed("ovs-bonds") {
		params.OvsBonds = &f.ovsBonds
	}
	if fl.Changed("ovs-options") {
		params.OvsOptions = &f.ovsOptions
	}
	if fl.Changed("ovs-tag") {
		params.OvsTag = &f.ovsTag
	}
	if fl.Changed("vlan-id") {
		params.VlanId = &f.vlanID
	}
	if fl.Changed("vlan-raw-device") {
		params.VlanRawDevice = &f.vlanRawDevice
	}
	if fl.Changed("delete") {
		params.Delete = &f.del
	}
}

// ---- delete ----------------------------------------------------------------

func newNetworkDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <iface>",
		Short: "Delete a host network interface",
		Long: "Delete a host network interface. The change is staged; run `network apply` " +
			"to make it live.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			iface := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete network interface %q without confirmation: pass --yes/-y", iface)
			}
			if err := deps.API.Nodes.DeleteNetwork2(cmd.Context(), deps.Node, iface); err != nil {
				return fmt.Errorf("delete network interface %q on node %q: %w", iface, deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf(
					"Network interface %q deleted on node %q. Run `pmx pve node network apply` to make it live.",
					iface, deps.Node)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// ---- apply / revert --------------------------------------------------------

func newNetworkApplyCmd() *cobra.Command {
	var (
		yes           bool
		regenerateFrr bool
	)
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Reload the network configuration to apply staged changes",
		Long: "Reload the host network configuration, making every staged interface change " +
			"live. This can briefly disrupt connectivity to the node.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("refusing to reload the network on node %q without confirmation: pass --yes/-y", deps.Node)
			}
			params := &nodes.UpdateNetworkParams{}
			if cmd.Flags().Changed("regenerate-frr") {
				params.RegenerateFrr = &regenerateFrr
			}
			resp, err := deps.API.Nodes.UpdateNetwork(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("apply network configuration on node %q: %w", deps.Node, err)
			}
			return renderNetworkTask(cmd, deps, rawOrNil(resp),
				fmt.Sprintf("Network configuration applied on node %q.", deps.Node))
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the reload without prompting")
	cmd.Flags().BoolVar(&regenerateFrr, "regenerate-frr", false, "regenerate the FRR configuration during the reload")
	return cmd
}

func newNetworkRevertCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "revert",
		Short: "Discard staged network configuration changes",
		Long:  "Revert the staged host network configuration changes that have not yet been applied.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("refusing to revert staged network changes on node %q without confirmation: pass --yes/-y", deps.Node)
			}
			if err := deps.API.Nodes.DeleteNetwork(cmd.Context(), deps.Node); err != nil {
				return fmt.Errorf("revert network configuration on node %q: %w", deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Staged network changes reverted on node %q.", deps.Node)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the revert without prompting")
	return cmd
}

// renderNetworkTask renders the result of an asynchronous network reload. The
// endpoint returns a task UPID when a reload is scheduled; if the payload is not
// a UPID (for example an empty body when nothing was pending), the success
// message is rendered directly.
func renderNetworkTask(cmd *cobra.Command, deps *cli.Deps, raw json.RawMessage, doneMsg string) error {
	upid, err := apiclient.UPIDFromRaw(raw)
	if err != nil {
		return deps.Out.Render(cmd.OutOrStdout(),
			output.Result{Message: doneMsg}, deps.Format)
	}
	if deps.Async {
		return deps.Out.Render(cmd.OutOrStdout(),
			output.Result{
				Single:  map[string]string{"upid": upid},
				Raw:     map[string]string{"upid": upid},
				Message: upid,
			}, deps.Format)
	}
	if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
		return fmt.Errorf("apply network configuration on node %q: %w", deps.Node, err)
	}
	return deps.Out.Render(cmd.OutOrStdout(),
		output.Result{Message: doneMsg}, deps.Format)
}
