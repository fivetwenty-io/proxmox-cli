package sdn

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// ipamEntry is the subset of a /cluster/sdn/ipams element rendered in the list
// table; the full element is preserved in Raw.
type ipamEntry struct {
	Ipam string `json:"ipam"`
	Type string `json:"type"`
}

// ipamFlagNames lists the editable IPAM attribute flags, used by `set` to
// detect a no-op update. The --token value is a provider API token (a secret)
// forwarded to the API but never echoed back.
var ipamFlagNames = []string{"section", "token", "url", "fingerprint"}

// newIpamCmd builds `pmx sdn ipam` and its sub-commands.
func newIpamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ipam",
		Short: "Manage SDN IP address management (IPAM) backends",
		Long: "List, create, inspect, update, and delete SDN IPAM backends, and show " +
			"their address allocations. Changes are staged until committed with `pmx pve sdn apply`.",
	}
	cmd.AddCommand(
		newIpamListCmd(),
		newIpamCreateCmd(),
		newIpamGetCmd(),
		newIpamSetCmd(),
		newIpamDeleteCmd(),
		newIpamStatusCmd(),
	)
	return cmd
}

func newIpamListCmd() *cobra.Command {
	var typ string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List SDN IPAM backends",
		Long: "List SDN IPAM backends (pve, netbox, or phpipam) with their type. Pass --type " +
			"to filter by backend type.",
		Example: `  pmx pve sdn ipam list
  pmx pve sdn ipam list --type netbox`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &cluster.ListSdnIpamsParams{}
			if cmd.Flags().Changed("type") {
				params.Type = strPtr(typ)
			}
			resp, err := deps.API.Cluster.ListSdnIpams(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list SDN IPAMs: %w", err)
			}
			raws := []json.RawMessage(*resp)
			res := output.Result{Headers: []string{"IPAM", "TYPE"}, Raw: raws}
			for _, raw := range raws {
				var e ipamEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode IPAM entry: %w", err)
				}
				res.Rows = append(res.Rows, []string{e.Ipam, e.Type})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&typ, "type", "", "only list IPAMs of this type")
	return cmd
}

func newIpamCreateCmd() *cobra.Command {
	var (
		typ         string
		section     int64
		token       string
		url         string
		fingerprint string
		lockToken   string
	)
	cmd := &cobra.Command{
		Use:   "create <ipam> --type <type>",
		Short: "Create an SDN IPAM backend",
		Long: "Create an SDN IPAM backend (pve, netbox, or phpipam). The change is staged " +
			"until `pmx pve sdn apply`. The --token value is a provider API token and is never echoed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			ipam := args[0]
			params := &cluster.CreateSdnIpamsParams{Ipam: ipam, Type: typ}
			fl := cmd.Flags()
			if fl.Changed("section") {
				params.Section = int64Ptr(section)
			}
			if fl.Changed("token") {
				params.Token = strPtr(token)
			}
			if fl.Changed("url") {
				params.Url = strPtr(url)
			}
			if fl.Changed("fingerprint") {
				params.Fingerprint = strPtr(fingerprint)
			}
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if err := deps.API.Cluster.CreateSdnIpams(cmd.Context(), params); err != nil {
				return fmt.Errorf("create SDN IPAM %q: %w", ipam, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"SDN IPAM %q created (run `pmx pve sdn apply` to commit).", ipam)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&typ, "type", "", "IPAM type: pve, netbox, or phpipam (required)")
	f.Int64Var(&section, "section", 0, "phpipam section ID")
	f.StringVar(&token, "token", "", "provider API token (netbox/phpipam); never echoed")
	f.StringVar(&url, "url", "", "provider API URL")
	f.StringVar(&fingerprint, "fingerprint", "", "certificate SHA-256 fingerprint")
	f.StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	cli.MustMarkRequired(cmd, "type")
	return cmd
}

func newIpamGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <ipam>",
		Short: "Show an SDN IPAM backend's configuration",
		Long: "Show the configuration of one SDN IPAM backend. The provider API token is " +
			"scrubbed from the output before rendering; it cannot be retrieved this way.",
		Example: `  pmx pve sdn ipam get netbox1`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			ipam := args[0]
			resp, err := deps.API.Cluster.GetSdnIpams(cmd.Context(), ipam)
			if err != nil {
				return fmt.Errorf("get SDN IPAM %q: %w", ipam, err)
			}
			// Scrub the provider API token: the response is opaque, so the
			// secret is stripped in the CLI rather than trusting the API.
			return renderObjectScrubbed(cmd, deps, resp, "token")
		},
	}
}

func newIpamSetCmd() *cobra.Command {
	var (
		section     int64
		token       string
		url         string
		fingerprint string
		del         string
		digest      string
		lockToken   string
	)
	cmd := &cobra.Command{
		Use:   "set <ipam>",
		Short: "Update an SDN IPAM backend",
		Long: "Update an SDN IPAM backend. The change is staged until `pmx pve sdn apply`. " +
			"The --token value is a provider API token and is never echoed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			ipam := args[0]
			fl := cmd.Flags()
			if !anyFlagChanged(fl, append(ipamFlagNames, "delete")...) {
				return fmt.Errorf("no changes requested: pass at least one field flag")
			}
			params := &cluster.UpdateSdnIpamsParams{}
			if fl.Changed("section") {
				params.Section = int64Ptr(section)
			}
			if fl.Changed("token") {
				params.Token = strPtr(token)
			}
			if fl.Changed("url") {
				params.Url = strPtr(url)
			}
			if fl.Changed("fingerprint") {
				params.Fingerprint = strPtr(fingerprint)
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
			if err := deps.API.Cluster.UpdateSdnIpams(cmd.Context(), ipam, params); err != nil {
				return fmt.Errorf("update SDN IPAM %q: %w", ipam, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"SDN IPAM %q updated (run `pmx pve sdn apply` to commit).", ipam)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&section, "section", 0, "phpipam section ID")
	f.StringVar(&token, "token", "", "provider API token (netbox/phpipam); never echoed")
	f.StringVar(&url, "url", "", "provider API URL")
	f.StringVar(&fingerprint, "fingerprint", "", "certificate SHA-256 fingerprint")
	f.StringVar(&del, "delete", "", "comma-separated list of settings to delete")
	f.StringVar(&digest, "digest", "", "digest guarding against concurrent modification")
	f.StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}

func newIpamDeleteCmd() *cobra.Command {
	var (
		yes       bool
		lockToken string
	)
	cmd := &cobra.Command{
		Use:   "delete <ipam>",
		Short: "Delete an SDN IPAM backend",
		Long: "Delete an SDN IPAM backend. Refuses to run without --yes. The change is staged " +
			"until `pmx pve sdn apply` commits it.",
		Example: `  pmx pve sdn ipam delete netbox1 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			ipam := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete SDN IPAM %q without confirmation: pass --yes", ipam)
			}
			params := &cluster.DeleteSdnIpamsParams{}
			if cmd.Flags().Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if err := deps.API.Cluster.DeleteSdnIpams(cmd.Context(), ipam, params); err != nil {
				return fmt.Errorf("delete SDN IPAM %q: %w", ipam, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"SDN IPAM %q deleted (run `pmx pve sdn apply` to commit).", ipam)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	cmd.Flags().StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}

func newIpamStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <ipam>",
		Short: "Show address allocations recorded by an IPAM backend",
		Long: "List the IP address allocations an IPAM backend currently tracks, including " +
			"the owning subnet, VM/CT, and hostname where recorded.",
		Example: `  pmx pve sdn ipam status pve`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			ipam := args[0]
			resp, err := deps.API.Cluster.ListSdnIpamsStatus(cmd.Context(), ipam)
			if err != nil {
				return fmt.Errorf("get IPAM %q status: %w", ipam, err)
			}
			return renderRawList(cmd, deps, []json.RawMessage(*resp))
		},
	}
}
