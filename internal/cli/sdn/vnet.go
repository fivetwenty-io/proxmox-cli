package sdn

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// vnetEntry is the subset of a /cluster/sdn/vnets element rendered in the list.
type vnetEntry struct {
	Vnet  string `json:"vnet"`
	Zone  string `json:"zone"`
	Tag   int64  `json:"tag"`
	Alias string `json:"alias"`
}

// newVnetCmd builds `pve sdn vnet` and its sub-commands.
func newVnetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vnet",
		Short: "Manage SDN vnets",
	}
	cmd.AddCommand(
		newVnetListCmd(),
		newVnetShowCmd(),
		newVnetCreateCmd(),
		newVnetSetCmd(),
		newVnetDeleteCmd(),
		newVnetFirewallCmd(),
		newVnetIpsCmd(),
		newVnetPermissionsCmd(),
	)
	return cmd
}

// newVnetShowCmd builds `pve sdn vnet show <vnet>`.
func newVnetShowCmd() *cobra.Command {
	var (
		pending bool
		running bool
	)
	cmd := &cobra.Command{
		Use:   "show <vnet>",
		Short: "Show an SDN vnet's configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			params := &cluster.GetSdnVnetsParams{}
			fl := cmd.Flags()
			if fl.Changed("pending") {
				params.Pending = boolPtr(pending)
			}
			if fl.Changed("running") {
				params.Running = boolPtr(running)
			}
			resp, err := deps.API.Cluster.GetSdnVnets(cmd.Context(), vnet, params)
			if err != nil {
				return fmt.Errorf("get SDN vnet %q: %w", vnet, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&pending, "pending", false, "display the pending configuration")
	f.BoolVar(&running, "running", false, "display the running configuration")
	return cmd
}

// newVnetIpsCmd builds `pve sdn vnet ips` and its sub-commands.
func newVnetIpsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ips",
		Short: "Manage IP mappings in an SDN vnet (IPAM)",
	}
	cmd.AddCommand(newVnetIpsCreateCmd(), newVnetIpsSetCmd(), newVnetIpsDeleteCmd())
	return cmd
}

// newVnetIpsCreateCmd builds `pve sdn vnet ips create <vnet> --ip <ip> --zone <zone>`.
func newVnetIpsCreateCmd() *cobra.Command {
	var (
		ip   string
		mac  string
		zone string
	)
	cmd := &cobra.Command{
		Use:   "create <vnet>",
		Short: "Add an IP mapping to a vnet",
		Long:  "Add an IP-to-MAC mapping in a vnet's IPAM zone.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			params := &cluster.CreateSdnVnetsIpsParams{Ip: ip, Zone: zone}
			fl := cmd.Flags()
			if fl.Changed("mac") {
				params.Mac = strPtr(mac)
			}
			if err := deps.API.Cluster.CreateSdnVnetsIps(cmd.Context(), vnet, params); err != nil {
				return fmt.Errorf("create IP mapping in vnet %q: %w", vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf("IP mapping %q created in vnet %q.", ip, vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&ip, "ip", "", "IP address to associate (required)")
	f.StringVar(&mac, "mac", "", "unicast MAC address")
	f.StringVar(&zone, "zone", "", "SDN zone the IP belongs to (required)")
	cli.MustMarkRequired(cmd, "ip")
	cli.MustMarkRequired(cmd, "zone")
	return cmd
}

// vnetIpsSetFlagNames lists the optional editable vnet-ips flags.
var vnetIpsSetFlagNames = []string{"mac", "vmid"}

// newVnetIpsSetCmd builds `pve sdn vnet ips set <vnet> --ip <ip> --zone <zone>`.
func newVnetIpsSetCmd() *cobra.Command {
	var (
		ip   string
		mac  string
		vmid int64
		zone string
	)
	cmd := &cobra.Command{
		Use:   "set <vnet>",
		Short: "Update an IP mapping in a vnet",
		Long:  "Update an IP-to-MAC/VMID mapping in a vnet's IPAM zone.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			fl := cmd.Flags()
			if !anyFlagChanged(fl, vnetIpsSetFlagNames...) {
				return fmt.Errorf("no changes to set: pass at least one field flag")
			}
			params := &cluster.UpdateSdnVnetsIpsParams{Ip: ip, Zone: zone}
			if fl.Changed("mac") {
				params.Mac = strPtr(mac)
			}
			if fl.Changed("vmid") {
				params.Vmid = int64Ptr(vmid)
			}
			if err := deps.API.Cluster.UpdateSdnVnetsIps(cmd.Context(), vnet, params); err != nil {
				return fmt.Errorf("update IP mapping in vnet %q: %w", vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf("IP mapping %q in vnet %q updated.", ip, vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&ip, "ip", "", "IP address to update (required)")
	f.StringVar(&mac, "mac", "", "unicast MAC address")
	f.Int64Var(&vmid, "vmid", 0, "VM ID to associate with the IP")
	f.StringVar(&zone, "zone", "", "SDN zone the IP belongs to (required)")
	cli.MustMarkRequired(cmd, "ip")
	cli.MustMarkRequired(cmd, "zone")
	return cmd
}

// newVnetIpsDeleteCmd builds `pve sdn vnet ips delete <vnet> --ip <ip> --zone <zone>`.
func newVnetIpsDeleteCmd() *cobra.Command {
	var (
		ip   string
		mac  string
		yes  bool
		zone string
	)
	cmd := &cobra.Command{
		Use:   "delete <vnet>",
		Short: "Remove an IP mapping from a vnet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete IP mapping %q without confirmation: pass --yes", ip)
			}
			params := &cluster.DeleteSdnVnetsIpsParams{Ip: ip, Zone: zone}
			fl := cmd.Flags()
			if fl.Changed("mac") {
				params.Mac = strPtr(mac)
			}
			if err := deps.API.Cluster.DeleteSdnVnetsIps(cmd.Context(), vnet, params); err != nil {
				return fmt.Errorf("delete IP mapping %q from vnet %q: %w", ip, vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf("IP mapping %q deleted from vnet %q.", ip, vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&ip, "ip", "", "IP address to delete (required)")
	f.StringVar(&mac, "mac", "", "unicast MAC address")
	f.BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	f.StringVar(&zone, "zone", "", "SDN zone the IP belongs to (required)")
	cli.MustMarkRequired(cmd, "ip")
	cli.MustMarkRequired(cmd, "zone")
	return cmd
}

// vnetSetFlagNames lists the editable vnet attribute flags, used by `set` to
// detect a no-op update.
var vnetSetFlagNames = []string{"zone", "tag", "alias", "vlanaware", "isolate-ports"}

// newVnetSetCmd builds `pve sdn vnet set <vnet>`.
func newVnetSetCmd() *cobra.Command {
	var (
		zone         string
		tag          int64
		alias        string
		vlanaware    bool
		isolatePorts bool
		del          string
		digest       string
		lockToken    string
	)
	cmd := &cobra.Command{
		Use:   "set <vnet>",
		Short: "Update an SDN vnet",
		Long:  "Update an SDN vnet. The change is staged until `pve sdn apply`.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			fl := cmd.Flags()
			if !anyFlagChanged(fl, append(vnetSetFlagNames, "delete")...) {
				return fmt.Errorf("no changes to set: pass at least one field flag")
			}
			params := &cluster.UpdateSdnVnetsParams{}
			if fl.Changed("zone") {
				params.Zone = strPtr(zone)
			}
			if fl.Changed("tag") {
				params.Tag = int64Ptr(tag)
			}
			if fl.Changed("alias") {
				params.Alias = strPtr(alias)
			}
			if fl.Changed("vlanaware") {
				params.Vlanaware = boolPtr(vlanaware)
			}
			if fl.Changed("isolate-ports") {
				params.IsolatePorts = boolPtr(isolatePorts)
			}
			if fl.Changed("delete") {
				params.Delete = strPtr(del)
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if err := deps.API.Cluster.UpdateSdnVnets(cmd.Context(), vnet, params); err != nil {
				return fmt.Errorf("update SDN vnet %q: %w", vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf("SDN vnet %q updated (run `pve sdn apply` to commit).", vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&zone, "zone", "", "zone the vnet belongs to")
	f.Int64Var(&tag, "tag", 0, "VLAN tag or VXLAN VNI")
	f.StringVar(&alias, "alias", "", "vnet alias/description")
	f.BoolVar(&vlanaware, "vlanaware", false, "allow VLANs to pass through this vnet")
	f.BoolVar(&isolatePorts, "isolate-ports", false, "isolate all interfaces on this vnet's bridge")
	f.StringVar(&del, "delete", "", "comma-separated list of settings to delete")
	f.StringVar(&digest, "digest", "", "digest guarding against concurrent modification")
	f.StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}

// newVnetListCmd builds `pve sdn vnet list`.
func newVnetListCmd() *cobra.Command {
	var (
		pending bool
		running bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List SDN vnets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &cluster.ListSdnVnetsParams{}
			fl := cmd.Flags()
			if fl.Changed("pending") {
				params.Pending = boolPtr(pending)
			}
			if fl.Changed("running") {
				params.Running = boolPtr(running)
			}
			resp, err := deps.API.Cluster.ListSdnVnets(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list SDN vnets: %w", err)
			}
			var entries []vnetEntry
			if resp != nil {
				entries = make([]vnetEntry, 0, len(*resp))
				for _, raw := range *resp {
					var e vnetEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode vnet entry: %w", err)
					}
					entries = append(entries, e)
				}
			}
			res := output.Result{Headers: []string{"VNET", "ZONE", "TAG", "ALIAS"}, Raw: entries}
			for _, e := range entries {
				tag := ""
				if e.Tag != 0 {
					tag = strconv.FormatInt(e.Tag, 10)
				}
				res.Rows = append(res.Rows, []string{e.Vnet, e.Zone, tag, e.Alias})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&pending, "pending", false, "display the pending configuration")
	f.BoolVar(&running, "running", false, "display the running configuration")
	return cmd
}

// newVnetCreateCmd builds `pve sdn vnet create <vnet> --zone <zone>`.
func newVnetCreateCmd() *cobra.Command {
	var (
		zone         string
		tag          int64
		alias        string
		vlanaware    bool
		isolatePorts bool
		vnetType     string
		lockToken    string
	)
	cmd := &cobra.Command{
		Use:   "create <vnet>",
		Short: "Create an SDN vnet",
		Long:  "Create an SDN vnet in a zone. The change is staged until `pve sdn apply`.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]

			params := &cluster.CreateSdnVnetsParams{Vnet: vnet, Zone: zone}
			fl := cmd.Flags()
			if fl.Changed("tag") {
				params.Tag = int64Ptr(tag)
			}
			if fl.Changed("alias") {
				params.Alias = strPtr(alias)
			}
			if fl.Changed("vlanaware") {
				params.Vlanaware = boolPtr(vlanaware)
			}
			if fl.Changed("isolate-ports") {
				params.IsolatePorts = boolPtr(isolatePorts)
			}
			if fl.Changed("type") {
				params.Type = strPtr(vnetType)
			}
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}

			if err := deps.API.Cluster.CreateSdnVnets(cmd.Context(), params); err != nil {
				return fmt.Errorf("create SDN vnet %q: %w", vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf("SDN vnet %q created (run `pve sdn apply` to commit).", vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&zone, "zone", "", "zone the vnet belongs to (required)")
	f.Int64Var(&tag, "tag", 0, "VLAN tag or VXLAN VNI")
	f.StringVar(&alias, "alias", "", "vnet alias/description")
	f.BoolVar(&vlanaware, "vlanaware", false, "allow VLANs to pass through this vnet")
	f.BoolVar(&isolatePorts, "isolate-ports", false, "isolate all interfaces on this vnet's bridge")
	f.StringVar(&vnetType, "type", "", "type of the vnet")
	f.StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	cli.MustMarkRequired(cmd, "zone")
	return cmd
}

// newVnetDeleteCmd builds `pve sdn vnet delete <vnet>`.
func newVnetDeleteCmd() *cobra.Command {
	var (
		yes       bool
		lockToken string
	)
	cmd := &cobra.Command{
		Use:   "delete <vnet>",
		Short: "Delete an SDN vnet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			vnet := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete SDN vnet %q without confirmation: pass --yes", vnet)
			}
			params := &cluster.DeleteSdnVnetsParams{}
			if cmd.Flags().Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if err := deps.API.Cluster.DeleteSdnVnets(cmd.Context(), vnet, params); err != nil {
				return fmt.Errorf("delete SDN vnet %q: %w", vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf("SDN vnet %q deleted (run `pve sdn apply` to commit).", vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	cmd.Flags().StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}
