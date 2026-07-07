package sdn

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

// resolveNode returns deps.Node or an error with clear remediation steps.
func resolveNode(deps *cli.Deps) (string, error) {
	if deps.Node == "" {
		return "", fmt.Errorf("no node specified: use --node, set PVE_NODE, or configure default-node")
	}
	return deps.Node, nil
}

// newStatusCmd builds `pve sdn status` and its sub-commands.
// All commands are read-only and node-scoped; --node (or PVE_NODE) is required.
// The group itself only shows help: GET /nodes/{node}/sdn is a directory index,
// not a status view.
func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show live SDN status on a node",
		Long: "Read live SDN state from a specific cluster node. All commands here are " +
			"read-only. A node must be provided via --node or PVE_NODE.",
	}
	cmd.AddCommand(
		newStatusZonesCmd(),
		newStatusVnetsCmd(),
		newStatusFabricsCmd(),
	)
	return cmd
}

// newStatusZonesCmd builds `pve sdn status zones` and its sub-commands.
func newStatusZonesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "zones",
		Short: "List SDN zone status on a node",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListSdnZones(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("list SDN zones on node %q: %w", node, err)
			}
			return renderRawList(cmd, deps, []json.RawMessage(*resp))
		},
	}
	cmd.AddCommand(
		newStatusZonesGetCmd(),
		newStatusZonesBridgesCmd(),
		newStatusZonesContentCmd(),
		newStatusZonesIpVrfCmd(),
	)
	return cmd
}

// newStatusZonesGetCmd builds `pve sdn status zones get`.
//
// GET /nodes/{node}/sdn/zones/{zone} is only a directory index (content,
// bridges, ip-vrf); the zone's status is its row in the zones status list,
// so filter that client-side.
func newStatusZonesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <zone>",
		Short: "Show a zone's live status on a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListSdnZones(cmd.Context(), node)
			if err != nil {
				return fmt.Errorf("get SDN zone %q status on node %q: %w", args[0], node, err)
			}
			if resp != nil {
				for _, raw := range *resp {
					var e struct {
						Zone string `json:"zone"`
					}
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode SDN zone status entry: %w", err)
					}
					if e.Zone == args[0] {
						return renderRawList(cmd, deps, []json.RawMessage{raw})
					}
				}
			}
			return fmt.Errorf("SDN zone %q not found on node %q", args[0], node)
		},
	}
}

func newStatusZonesBridgesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bridges <zone>",
		Short: "List bridges for a zone on a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListSdnZonesBridges(cmd.Context(), node, args[0])
			if err != nil {
				return fmt.Errorf("list bridges for SDN zone %q on node %q: %w", args[0], node, err)
			}
			return renderRawList(cmd, deps, []json.RawMessage(*resp))
		},
	}
}

func newStatusZonesContentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "content <zone>",
		Short: "List content for a zone on a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListSdnZonesContent(cmd.Context(), node, args[0])
			if err != nil {
				return fmt.Errorf("list content for SDN zone %q on node %q: %w", args[0], node, err)
			}
			return renderRawList(cmd, deps, []json.RawMessage(*resp))
		},
	}
}

func newStatusZonesIpVrfCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ip-vrf <zone>",
		Short: "List IP-VRF entries for a zone on a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListSdnZonesIpVrf(cmd.Context(), node, args[0])
			if err != nil {
				return fmt.Errorf("list IP-VRF for SDN zone %q on node %q: %w", args[0], node, err)
			}
			return renderRawList(cmd, deps, []json.RawMessage(*resp))
		},
	}
}

// newStatusVnetsCmd builds `pve sdn status vnets` and its sub-commands.
// There is no per-vnet status endpoint (GET /nodes/{node}/sdn/vnets/{vnet} is
// only a directory index); mac-vrf is the vnet-level live view.
func newStatusVnetsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vnets",
		Short: "Show SDN vnet status on a node",
	}
	cmd.AddCommand(
		newStatusVnetsMacVrfCmd(),
	)
	return cmd
}

func newStatusVnetsMacVrfCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mac-vrf <vnet>",
		Short: "List MAC-VRF entries for a vnet on a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListSdnVnetsMacVrf(cmd.Context(), node, args[0])
			if err != nil {
				return fmt.Errorf("list MAC-VRF for SDN vnet %q on node %q: %w", args[0], node, err)
			}
			return renderRawList(cmd, deps, []json.RawMessage(*resp))
		},
	}
}

// newStatusFabricsCmd builds `pve sdn status fabrics` and its sub-commands.
// There is no per-fabric status summary endpoint (GET
// /nodes/{node}/sdn/fabrics/{fabric} is only a directory index); routes,
// neighbors, and interfaces are the fabric-level live views.
func newStatusFabricsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fabrics",
		Short: "Show SDN fabric status on a node",
	}
	cmd.AddCommand(
		newStatusFabricsInterfacesCmd(),
		newStatusFabricsNeighborsCmd(),
		newStatusFabricsRoutesCmd(),
	)
	return cmd
}

func newStatusFabricsInterfacesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "interfaces <fabric>",
		Short: "List interfaces for a fabric on a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListSdnFabricsInterfaces(cmd.Context(), node, args[0])
			if err != nil {
				return fmt.Errorf("list interfaces for SDN fabric %q on node %q: %w", args[0], node, err)
			}
			return renderRawList(cmd, deps, []json.RawMessage(*resp))
		},
	}
}

func newStatusFabricsNeighborsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "neighbors <fabric>",
		Short: "List neighbors for a fabric on a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListSdnFabricsNeighbors(cmd.Context(), node, args[0])
			if err != nil {
				return fmt.Errorf("list neighbors for SDN fabric %q on node %q: %w", args[0], node, err)
			}
			return renderRawList(cmd, deps, []json.RawMessage(*resp))
		},
	}
}

func newStatusFabricsRoutesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "routes <fabric>",
		Short: "List routes for a fabric on a node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			node, err := resolveNode(deps)
			if err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListSdnFabricsRoutes(cmd.Context(), node, args[0])
			if err != nil {
				return fmt.Errorf("list routes for SDN fabric %q on node %q: %w", args[0], node, err)
			}
			return renderRawList(cmd, deps, []json.RawMessage(*resp))
		},
	}
}
