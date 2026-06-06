package sdn

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"
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
	cmd.AddCommand(newVnetListCmd(), newVnetCreateCmd(), newVnetSetCmd(), newVnetDeleteCmd())
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
	return cmd
}

// newVnetListCmd builds `pve sdn vnet list`.
func newVnetListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List SDN vnets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListSdnVnets(cmd.Context(), &cluster.ListSdnVnetsParams{})
			if err != nil {
				return fmt.Errorf("list SDN vnets: %w", err)
			}
			entries := make([]vnetEntry, 0, len(*resp))
			for _, raw := range *resp {
				var e vnetEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode vnet entry: %w", err)
				}
				entries = append(entries, e)
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
}

// newVnetCreateCmd builds `pve sdn vnet create <vnet> --zone <zone>`.
func newVnetCreateCmd() *cobra.Command {
	var (
		zone  string
		tag   int64
		alias string
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

			if err := deps.API.Cluster.CreateSdnVnets(cmd.Context(), params); err != nil {
				return fmt.Errorf("create SDN vnet %q: %w", vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf("SDN vnet %q created (run `pve sdn apply` to commit).", vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&zone, "zone", "", "zone the vnet belongs to (required)")
	cmd.Flags().Int64Var(&tag, "tag", 0, "VLAN tag or VXLAN VNI")
	cmd.Flags().StringVar(&alias, "alias", "", "vnet alias/description")
	_ = cmd.MarkFlagRequired("zone")
	return cmd
}

// newVnetDeleteCmd builds `pve sdn vnet delete <vnet>`.
func newVnetDeleteCmd() *cobra.Command {
	var yes bool
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
			if err := deps.API.Cluster.DeleteSdnVnets(cmd.Context(), vnet, &cluster.DeleteSdnVnetsParams{}); err != nil {
				return fmt.Errorf("delete SDN vnet %q: %w", vnet, err)
			}
			res := output.Result{Message: fmt.Sprintf("SDN vnet %q deleted (run `pve sdn apply` to commit).", vnet)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}
