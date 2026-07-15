package pdm

import (
	"fmt"

	"github.com/spf13/cobra"

	pdmnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newNodeSdnCmd builds `pmx pdm node sdn` and its vnet/zone VRF lookup
// verbs (/nodes/{node}/sdn/...).
//
// GET /nodes/{node}/sdn, GET /nodes/{node}/sdn/vnets/{vnet}, and GET
// /nodes/{node}/sdn/zones/{zone} are directory indexes with no data behind
// them (ListSdn, GetSdnVnets, GetSdnZones each return only `error`,
// nodes_gen.go:1484-1593, v3.6.0), so there is no `ls`/`show` verb for
// them — only the two documented VRF sub-resources are exposed.
func newNodeSdnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sdn",
		Short: "Read SDN VRF information for the node",
		Long: "Read SDN VRF (virtual routing and forwarding) information for the node: EVPN " +
			"vnet MAC-VRF and zone IP-VRF lookups.",
	}
	cmd.AddCommand(newNodeSdnVnetCmd(), newNodeSdnZoneCmd())
	return cmd
}

// --- vnet -------------------------------------------------------------------

func newNodeSdnVnetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vnet",
		Short: "Read EVPN vnet MAC-VRF information",
		Long:  "Read EVPN vnet MAC-VRF (MAC address to next-hop mapping) information for the node.",
	}
	cmd.AddCommand(newNodeSdnVnetMacVrfCmd())
	return cmd
}

// newNodeSdnVnetMacVrfCmd builds `pmx pdm node sdn vnet mac-vrf <node>
// <vnet>` — get the MAC-VRF for an EVPN vnet for a node on a given remote
// (GET /nodes/{node}/sdn/vnets/{vnet}/mac-vrf). --remote is required: the
// binding's ListSdnVnetsMacVrfParams.Remote is a non-pointer required string
// field (nodes_gen.go:1520-1524, v3.6.0).
func newNodeSdnVnetMacVrfCmd() *cobra.Command {
	var remote string

	cmd := &cobra.Command{
		Use:   "mac-vrf <node> <vnet>",
		Short: "Show the MAC-VRF for an EVPN vnet",
		Long:  "Get the MAC-VRF (MAC address to next-hop mapping) for an EVPN vnet, for a node on a given remote.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, vnet := args[0], args[1]

			params := &pdmnodes.ListSdnVnetsMacVrfParams{Remote: remote}

			resp, err := deps.PDM.Nodes.ListSdnVnetsMacVrf(cmd.Context(), node, vnet, params)
			if err != nil {
				return fmt.Errorf("get mac-vrf for vnet %q on node %q: %w", vnet, node, err)
			}
			if resp == nil {
				return fmt.Errorf("get mac-vrf for vnet %q on node %q: empty response from server", vnet, node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode mac-vrf for vnet %q on node %q: %w", vnet, node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "remote ID (required)")
	cli.MustMarkRequired(cmd, "remote")

	return cmd
}

// --- zone -------------------------------------------------------------------

func newNodeSdnZoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "zone",
		Short: "Read EVPN zone IP-VRF information",
		Long:  "Read EVPN zone IP-VRF (route table) information for the node.",
	}
	cmd.AddCommand(newNodeSdnZoneIPVrfCmd())
	return cmd
}

// newNodeSdnZoneIPVrfCmd builds `pmx pdm node sdn zone ip-vrf <node>
// <zone>` — get the IP-VRF for an EVPN zone for a node on a given remote
// (GET /nodes/{node}/sdn/zones/{zone}/ip-vrf). --remote is required, same
// binding shape as the vnet mac-vrf lookup (nodes_gen.go:1595-1599, v3.6.0).
func newNodeSdnZoneIPVrfCmd() *cobra.Command {
	var remote string

	cmd := &cobra.Command{
		Use:   "ip-vrf <node> <zone>",
		Short: "Show the IP-VRF for an EVPN zone",
		Long:  "Get the IP-VRF (route table) for an EVPN zone, for a node on a given remote.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, zone := args[0], args[1]

			params := &pdmnodes.ListSdnZonesIpVrfParams{Remote: remote}

			resp, err := deps.PDM.Nodes.ListSdnZonesIpVrf(cmd.Context(), node, zone, params)
			if err != nil {
				return fmt.Errorf("get ip-vrf for zone %q on node %q: %w", zone, node, err)
			}
			if resp == nil {
				return fmt.Errorf("get ip-vrf for zone %q on node %q: empty response from server", zone, node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode ip-vrf for zone %q on node %q: %w", zone, node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "remote ID (required)")
	cli.MustMarkRequired(cmd, "remote")

	return cmd
}
