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

// newZoneCmd builds `pve sdn zone` and its sub-commands.
func newZoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "zone",
		Short: "Manage SDN zones",
	}
	cmd.AddCommand(newZoneListCmd(), newZoneCreateCmd(), newZoneDeleteCmd())
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
