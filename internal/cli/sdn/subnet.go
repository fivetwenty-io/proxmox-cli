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

// subnetSetFlagNames lists the editable subnet attribute flags used by `set` to
// detect a no-op update.
var subnetSetFlagNames = []string{
	"dhcp-dns-server", "dhcp-range", "dnszoneprefix", "gateway", "lock-token", "snat",
}

// newSubnetCmd builds `pve sdn subnet` and its sub-commands.
func newSubnetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subnet",
		Short: "Manage SDN subnets",
	}
	cmd.AddCommand(newSubnetListCmd(), newSubnetCreateCmd(), newSubnetSetCmd(), newSubnetDeleteCmd())
	return cmd
}

// newSubnetSetCmd builds `pve sdn subnet set <vnet> <subnet>`.
func newSubnetSetCmd() *cobra.Command {
	var (
		del           string
		dhcpDnsServer string
		dhcpRange     []string
		digest        string
		dnszoneprefix string
		gateway       string
		lockToken     string
		snat          bool
	)
	cmd := &cobra.Command{
		Use:   "set <vnet> <subnet>",
		Short: "Update a subnet on a vnet",
		Long:  "Update a subnet on a vnet. The change is staged until `pve sdn apply`.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet, subnet := args[0], args[1]
			fl := cmd.Flags()
			if !anyFlagChanged(fl, append(subnetSetFlagNames, "delete")...) {
				return fmt.Errorf("no changes to set: pass at least one field flag")
			}
			params := &cluster.UpdateSdnVnetsSubnetsParams{}
			if fl.Changed("delete") {
				params.Delete = strPtr(del)
			}
			if fl.Changed("dhcp-dns-server") {
				params.DhcpDnsServer = strPtr(dhcpDnsServer)
			}
			if fl.Changed("dhcp-range") {
				params.DhcpRange = dhcpRange
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}
			if fl.Changed("dnszoneprefix") {
				params.Dnszoneprefix = strPtr(dnszoneprefix)
			}
			if fl.Changed("gateway") {
				params.Gateway = strPtr(gateway)
			}
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if fl.Changed("snat") {
				params.Snat = boolPtr(snat)
			}
			if err := deps.API.Cluster.UpdateSdnVnetsSubnets(cmd.Context(), vnet, subnet, params); err != nil {
				return fmt.Errorf("update subnet %q on vnet %q: %w", subnet, vnet, err)
			}
			res := output.Result{
				Message: fmt.Sprintf("Subnet %q on vnet %q updated (run `pve sdn apply` to commit).", subnet, vnet),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&del, "delete", "", "comma-separated list of settings to delete")
	f.StringVar(&dhcpDnsServer, "dhcp-dns-server", "", "IP address for the DHCP DNS server")
	f.StringArrayVar(&dhcpRange, "dhcp-range", nil, "DHCP range for this subnet (repeatable)")
	f.StringVar(&digest, "digest", "", "digest guarding against concurrent modification")
	f.StringVar(&dnszoneprefix, "dnszoneprefix", "", "DNS domain zone prefix, e.g. adm")
	f.StringVar(&gateway, "gateway", "", "gateway IP for the subnet")
	f.StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	f.BoolVar(&snat, "snat", false, "enable source NAT (masquerade) for the subnet")
	return cmd
}

// newSubnetListCmd builds `pve sdn subnet list <vnet>`.
func newSubnetListCmd() *cobra.Command {
	var (
		pending bool
		running bool
	)
	cmd := &cobra.Command{
		Use:   "list <vnet>",
		Short: "List subnets of a vnet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			params := &cluster.ListSdnVnetsSubnetsParams{}
			fl := cmd.Flags()
			if fl.Changed("pending") {
				params.Pending = boolPtr(pending)
			}
			if fl.Changed("running") {
				params.Running = boolPtr(running)
			}
			resp, err := deps.API.Cluster.ListSdnVnetsSubnets(cmd.Context(), vnet, params)
			if err != nil {
				return fmt.Errorf("list subnets of vnet %q: %w", vnet, err)
			}
			var entries []subnetEntry
			if resp != nil {
				entries = make([]subnetEntry, 0, len(*resp))
				for _, raw := range *resp {
					var e subnetEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode subnet entry: %w", err)
					}
					entries = append(entries, e)
				}
			}
			res := output.Result{Headers: []string{"SUBNET", "CIDR", "GATEWAY", "ZONE"}, Raw: entries}
			for _, e := range entries {
				res.Rows = append(res.Rows, []string{e.Subnet, e.Cidr, e.Gateway, e.Zone})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&pending, "pending", false, "display the pending configuration")
	f.BoolVar(&running, "running", false, "display the running configuration")
	return cmd
}

// newSubnetCreateCmd builds `pve sdn subnet create <vnet> <cidr>`.
func newSubnetCreateCmd() *cobra.Command {
	var (
		gateway       string
		snat          bool
		dhcpDnsServer string
		dhcpRange     []string
		dnszoneprefix string
		lockToken     string
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
			if fl.Changed("dhcp-dns-server") {
				params.DhcpDnsServer = strPtr(dhcpDnsServer)
			}
			if fl.Changed("dhcp-range") {
				params.DhcpRange = dhcpRange
			}
			if fl.Changed("dnszoneprefix") {
				params.Dnszoneprefix = strPtr(dnszoneprefix)
			}
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}

			if err := deps.API.Cluster.CreateSdnVnetsSubnets(cmd.Context(), vnet, params); err != nil {
				return fmt.Errorf("create subnet %q on vnet %q: %w", cidr, vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf("Subnet %q created on vnet %q (run `pve sdn apply` to commit).", cidr, vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&gateway, "gateway", "", "gateway IP for the subnet, e.g. 10.241.0.1")
	f.BoolVar(&snat, "snat", false, "enable source NAT (masquerade) for the subnet")
	f.StringVar(&dhcpDnsServer, "dhcp-dns-server", "", "IP address for the DHCP DNS server")
	f.StringArrayVar(&dhcpRange, "dhcp-range", nil, "DHCP range for this subnet (repeatable)")
	f.StringVar(&dnszoneprefix, "dnszoneprefix", "", "DNS domain zone prefix, e.g. adm")
	f.StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}

// newSubnetDeleteCmd builds `pve sdn subnet delete <vnet> <subnet>`.
func newSubnetDeleteCmd() *cobra.Command {
	var (
		yes       bool
		lockToken string
	)
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
			params := &cluster.DeleteSdnVnetsSubnetsParams{}
			if cmd.Flags().Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			err := deps.API.Cluster.DeleteSdnVnetsSubnets(cmd.Context(), vnet, subnet, params)
			if err != nil {
				return fmt.Errorf("delete subnet %q on vnet %q: %w", subnet, vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf("Subnet %q deleted from vnet %q (run `pve sdn apply` to commit).", subnet, vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	cmd.Flags().StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}
