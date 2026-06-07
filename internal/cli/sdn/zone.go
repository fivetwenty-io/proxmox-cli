package sdn

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// zoneEntry is the subset of a /cluster/sdn/zones element rendered in the list.
type zoneEntry struct {
	Zone  string `json:"zone"`
	Type  string `json:"type"`
	Nodes string `json:"nodes"`
	Ipam  string `json:"ipam"`
}

// zoneSetFlagNames lists the editable zone attribute flags used by `set` to
// detect a no-op update.
var zoneSetFlagNames = []string{
	"advertise-subnets", "bridge", "bridge-disable-mac-learning", "controller",
	"dhcp", "disable-arp-nd-suppression", "dns", "dnszone", "dp-id",
	"exitnodes", "exitnodes-local-routing", "exitnodes-primary", "fabric",
	"ipam", "lock-token", "mac", "mtu", "nodes", "peers", "reversedns",
	"rt-import", "secondary-controller", "tag", "vlan-protocol", "vrf-vxlan",
	"vxlan-port",
}

// newZoneCmd builds `pve sdn zone` and its sub-commands.
func newZoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "zone",
		Short: "Manage SDN zones",
	}
	cmd.AddCommand(newZoneListCmd(), newZoneCreateCmd(), newZoneSetCmd(), newZoneDeleteCmd())
	return cmd
}

// newZoneSetCmd builds `pve sdn zone set <zone>`.
func newZoneSetCmd() *cobra.Command {
	var (
		advertiseSubnets         bool
		bridge                   string
		bridgeDisableMacLearning bool
		controller               string
		del                      string
		dhcp                     string
		digest                   string
		disableArpNdSuppression  bool
		dns                      string
		dnszone                  string
		dpID                     int64
		exitnodes                string
		exitnodesLocalRouting    bool
		exitnodesPrimary         string
		fabric                   string
		ipam                     string
		lockToken                string
		mac                      string
		mtu                      int64
		nodes                    string
		peers                    string
		reversedns               string
		rtImport                 string
		secondaryControllers     []string
		tag                      int64
		vlanProtocol             string
		vrfVxlan                 int64
		vxlanPort                int64
	)
	cmd := &cobra.Command{
		Use:   "set <zone>",
		Short: "Update an SDN zone",
		Long:  "Update an SDN zone. The change is staged until `pve sdn apply`.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			zone := args[0]
			fl := cmd.Flags()
			if !anyFlagChanged(fl, append(zoneSetFlagNames, "delete")...) {
				return fmt.Errorf("no changes to set: pass at least one field flag")
			}
			params := &cluster.UpdateSdnZonesParams{}
			if fl.Changed("advertise-subnets") {
				params.AdvertiseSubnets = boolPtr(advertiseSubnets)
			}
			if fl.Changed("bridge") {
				params.Bridge = strPtr(bridge)
			}
			if fl.Changed("bridge-disable-mac-learning") {
				params.BridgeDisableMacLearning = boolPtr(bridgeDisableMacLearning)
			}
			if fl.Changed("controller") {
				params.Controller = strPtr(controller)
			}
			if fl.Changed("delete") {
				params.Delete = strPtr(del)
			}
			if fl.Changed("dhcp") {
				params.Dhcp = strPtr(dhcp)
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}
			if fl.Changed("disable-arp-nd-suppression") {
				params.DisableArpNdSuppression = boolPtr(disableArpNdSuppression)
			}
			if fl.Changed("dns") {
				params.Dns = strPtr(dns)
			}
			if fl.Changed("dnszone") {
				params.Dnszone = strPtr(dnszone)
			}
			if fl.Changed("dp-id") {
				params.DpId = int64Ptr(dpID)
			}
			if fl.Changed("exitnodes") {
				params.Exitnodes = strPtr(exitnodes)
			}
			if fl.Changed("exitnodes-local-routing") {
				params.ExitnodesLocalRouting = boolPtr(exitnodesLocalRouting)
			}
			if fl.Changed("exitnodes-primary") {
				params.ExitnodesPrimary = strPtr(exitnodesPrimary)
			}
			if fl.Changed("fabric") {
				params.Fabric = strPtr(fabric)
			}
			if fl.Changed("ipam") {
				params.Ipam = strPtr(ipam)
			}
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if fl.Changed("mac") {
				params.Mac = strPtr(mac)
			}
			if fl.Changed("mtu") {
				params.Mtu = int64Ptr(mtu)
			}
			if fl.Changed("nodes") {
				params.Nodes = strPtr(nodes)
			}
			if fl.Changed("peers") {
				params.Peers = strPtr(peers)
			}
			if fl.Changed("reversedns") {
				params.Reversedns = strPtr(reversedns)
			}
			if fl.Changed("rt-import") {
				params.RtImport = strPtr(rtImport)
			}
			if fl.Changed("secondary-controller") {
				params.SecondaryControllers = secondaryControllers
			}
			if fl.Changed("tag") {
				params.Tag = int64Ptr(tag)
			}
			if fl.Changed("vlan-protocol") {
				params.VlanProtocol = strPtr(vlanProtocol)
			}
			if fl.Changed("vrf-vxlan") {
				params.VrfVxlan = int64Ptr(vrfVxlan)
			}
			if fl.Changed("vxlan-port") {
				params.VxlanPort = int64Ptr(vxlanPort)
			}
			if err := deps.API.Cluster.UpdateSdnZones(cmd.Context(), zone, params); err != nil {
				return fmt.Errorf("update SDN zone %q: %w", zone, err)
			}
			res := output.Result{Message: fmt.Sprintf("SDN zone %q updated (run `pve sdn apply` to commit).", zone)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&advertiseSubnets, "advertise-subnets", false, "advertise IP prefixes (Type-5 routes) instead of MAC/IP pairs")
	f.StringVar(&bridge, "bridge", "", "bridge for which VLANs should be managed")
	f.BoolVar(&bridgeDisableMacLearning, "bridge-disable-mac-learning", false, "disable auto MAC learning")
	f.StringVar(&controller, "controller", "", "controller for this zone")
	f.StringVar(&del, "delete", "", "comma-separated list of settings to delete")
	f.StringVar(&dhcp, "dhcp", "", "type of the DHCP backend for this zone")
	f.StringVar(&digest, "digest", "", "digest guarding against concurrent modification")
	f.BoolVar(&disableArpNdSuppression, "disable-arp-nd-suppression", false, "suppress IPv4 ARP and IPv6 ND messages")
	f.StringVar(&dns, "dns", "", "DNS API server")
	f.StringVar(&dnszone, "dnszone", "", "DNS domain zone, e.g. mydomain.com")
	f.Int64Var(&dpID, "dp-id", 0, "Faucet dataplane ID")
	f.StringVar(&exitnodes, "exitnodes", "", "comma-separated list of exit nodes")
	f.BoolVar(&exitnodesLocalRouting, "exitnodes-local-routing", false, "allow exit nodes to connect to EVPN guests")
	f.StringVar(&exitnodesPrimary, "exitnodes-primary", "", "force traffic through this exit node first")
	f.StringVar(&fabric, "fabric", "", "SDN fabric to use as underlay for this VXLAN zone")
	f.StringVar(&ipam, "ipam", "", "IPAM backend, e.g. pve")
	f.StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	f.StringVar(&mac, "mac", "", "anycast logical router MAC address")
	f.Int64Var(&mtu, "mtu", 0, "MTU of the zone")
	f.StringVar(&nodes, "nodes", "", "comma-separated list of cluster node names")
	f.StringVar(&peers, "peers", "", "comma-separated list of VXLAN peers (usually node IPs)")
	f.StringVar(&reversedns, "reversedns", "", "reverse DNS API server")
	f.StringVar(&rtImport, "rt-import", "", "list of route targets to import into the VRF of the zone")
	f.StringArrayVar(&secondaryControllers, "secondary-controller", nil, "additional controller (repeatable)")
	f.Int64Var(&tag, "tag", 0, "service-VLAN tag (outer VLAN)")
	f.StringVar(&vlanProtocol, "vlan-protocol", "", "VLAN protocol for QinQ zones")
	f.Int64Var(&vrfVxlan, "vrf-vxlan", 0, "VNI for the zone VRF")
	f.Int64Var(&vxlanPort, "vxlan-port", 0, "UDP port for the VXLAN tunnel (default 4789)")
	return cmd
}

// newZoneListCmd builds `pve sdn zone list`.
func newZoneListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List SDN zones",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListSdnZones(cmd.Context(), &cluster.ListSdnZonesParams{})
			if err != nil {
				return fmt.Errorf("list SDN zones: %w", err)
			}
			entries := make([]zoneEntry, 0, len(*resp))
			for _, raw := range *resp {
				var e zoneEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode zone entry: %w", err)
				}
				entries = append(entries, e)
			}
			res := output.Result{Headers: []string{"ZONE", "TYPE", "NODES", "IPAM"}, Raw: entries}
			for _, e := range entries {
				res.Rows = append(res.Rows, []string{e.Zone, e.Type, e.Nodes, e.Ipam})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newZoneCreateCmd builds `pve sdn zone create <zone>`.
func newZoneCreateCmd() *cobra.Command {
	var (
		zoneType string
		nodes    string
		bridge   string
		ipam     string
	)
	cmd := &cobra.Command{
		Use:   "create <zone>",
		Short: "Create an SDN zone",
		Long: "Create an SDN zone. The change is staged until `pve sdn apply`. " +
			"A simple zone needs no bridge or uplink and provides an isolated L2 segment.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			zone := args[0]

			params := &cluster.CreateSdnZonesParams{Zone: zone, Type: zoneType}
			fl := cmd.Flags()
			if fl.Changed("nodes") {
				params.Nodes = strPtr(nodes)
			}
			if fl.Changed("bridge") {
				params.Bridge = strPtr(bridge)
			}
			if fl.Changed("ipam") {
				params.Ipam = strPtr(ipam)
			}

			if err := deps.API.Cluster.CreateSdnZones(cmd.Context(), params); err != nil {
				return fmt.Errorf("create SDN zone %q: %w", zone, err)
			}
			res := output.Result{Message: fmt.Sprintf("SDN zone %q created (run `pve sdn apply` to commit).", zone)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&zoneType, "type", "simple", "zone type: simple|vlan|qinq|vxlan|evpn")
	cmd.Flags().StringVar(&nodes, "nodes", "", "comma-separated nodes the zone applies to")
	cmd.Flags().StringVar(&bridge, "bridge", "", "bridge for vlan/qinq zones")
	cmd.Flags().StringVar(&ipam, "ipam", "", "IPAM backend (e.g. pve)")
	return cmd
}

// newZoneDeleteCmd builds `pve sdn zone delete <zone>`.
func newZoneDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <zone>",
		Short: "Delete an SDN zone",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			zone := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete SDN zone %q without confirmation: pass --yes", zone)
			}
			if err := deps.API.Cluster.DeleteSdnZones(cmd.Context(), zone, &cluster.DeleteSdnZonesParams{}); err != nil {
				return fmt.Errorf("delete SDN zone %q: %w", zone, err)
			}
			res := output.Result{Message: fmt.Sprintf("SDN zone %q deleted (run `pve sdn apply` to commit).", zone)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}
