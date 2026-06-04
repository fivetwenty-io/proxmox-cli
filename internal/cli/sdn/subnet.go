package sdn

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// subnetEntry is the subset of a /cluster/sdn/vnets/{vnet}/subnets element
// rendered in the list.
type subnetEntry struct {
	Subnet  string `json:"subnet"`
	Cidr    string `json:"cidr"`
	Gateway string `json:"gateway"`
	Zone    string `json:"zone"`
}

// newSubnetCmd builds `pve sdn subnet` and its sub-commands.
func newSubnetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subnet",
		Short: "Manage SDN subnets",
	}
	cmd.AddCommand(newSubnetListCmd(), newSubnetCreateCmd(), newSubnetDeleteCmd())
	return cmd
}

// newSubnetListCmd builds `pve sdn subnet list <vnet>`.
func newSubnetListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <vnet>",
		Short: "List subnets of a vnet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			resp, err := deps.API.Cluster.ListSdnVnetsSubnets(cmd.Context(), vnet, &cluster.ListSdnVnetsSubnetsParams{})
			if err != nil {
				return fmt.Errorf("list subnets of vnet %q: %w", vnet, err)
			}
			entries := make([]subnetEntry, 0, len(*resp))
			for _, raw := range *resp {
				var e subnetEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode subnet entry: %w", err)
				}
				entries = append(entries, e)
			}
			res := output.Result{Headers: []string{"SUBNET", "CIDR", "GATEWAY", "ZONE"}, Raw: entries}
			for _, e := range entries {
				res.Rows = append(res.Rows, []string{e.Subnet, e.Cidr, e.Gateway, e.Zone})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// newSubnetCreateCmd builds `pve sdn subnet create <vnet> <cidr>`.
func newSubnetCreateCmd() *cobra.Command {
	var (
		gateway string
		snat    bool
	)
	cmd := &cobra.Command{
		Use:   "create <vnet> <cidr>",
		Short: "Create a subnet on a vnet",
		Long: "Create a subnet (given as a CIDR, e.g. 10.241.0.0/24) on a vnet. " +
			"The change is staged until `pve sdn apply`.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet, cidr := args[0], args[1]

			params := &cluster.CreateSdnVnetsSubnetsParams{Subnet: cidr, Type: "subnet"}
			fl := cmd.Flags()
			if fl.Changed("gateway") {
				params.Gateway = strPtr(gateway)
			}
			if fl.Changed("snat") {
				params.Snat = boolPtr(snat)
			}

			if err := deps.API.Cluster.CreateSdnVnetsSubnets(cmd.Context(), vnet, params); err != nil {
				return fmt.Errorf("create subnet %q on vnet %q: %w", cidr, vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf("Subnet %q created on vnet %q (run `pve sdn apply` to commit).", cidr, vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&gateway, "gateway", "", "gateway IP for the subnet, e.g. 10.241.0.1")
	cmd.Flags().BoolVar(&snat, "snat", false, "enable source NAT (masquerade) for the subnet")
	return cmd
}

// newSubnetDeleteCmd builds `pve sdn subnet delete <vnet> <subnet>`.
func newSubnetDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <vnet> <subnet>",
		Short: "Delete a subnet from a vnet",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet, subnet := args[0], args[1]
			if !yes {
				return fmt.Errorf("refusing to delete subnet %q without confirmation: pass --yes", subnet)
			}
			err := deps.API.Cluster.DeleteSdnVnetsSubnets(cmd.Context(), vnet, subnet, &cluster.DeleteSdnVnetsSubnetsParams{})
			if err != nil {
				return fmt.Errorf("delete subnet %q on vnet %q: %w", subnet, vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf("Subnet %q deleted from vnet %q (run `pve sdn apply` to commit).", subnet, vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}
