package sdn

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"
)

// dnsEntry is the subset of a /cluster/sdn/dns element rendered in the list
// table; the full element is preserved in Raw.
type dnsEntry struct {
	Dns  string `json:"dns"`
	Type string `json:"type"`
	Url  string `json:"url"`
}

// dnsSetFlagNames lists the editable DNS attribute flags, used by `set` to
// detect a no-op update. The --key value is a provider API key (a secret)
// forwarded to the API but never echoed back.
var dnsSetFlagNames = []string{"url", "key", "fingerprint", "reversemaskv6", "ttl"}

// newDnsCmd builds `pmx sdn dns` and its sub-commands.
func newDnsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dns",
		Short: "Manage SDN DNS providers",
		Long: "List, create, inspect, update, and delete SDN DNS providers (e.g. PowerDNS). " +
			"Changes are staged until committed with `pmx sdn apply`.",
	}
	cmd.AddCommand(
		newDnsListCmd(),
		newDnsCreateCmd(),
		newDnsGetCmd(),
		newDnsSetCmd(),
		newDnsDeleteCmd(),
	)
	return cmd
}

func newDnsListCmd() *cobra.Command {
	var typ string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List SDN DNS providers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			params := &cluster.ListSdnDnsParams{}
			if cmd.Flags().Changed("type") {
				params.Type = strPtr(typ)
			}
			resp, err := deps.API.Cluster.ListSdnDns(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list SDN DNS providers: %w", err)
			}
			raws := []json.RawMessage(*resp)
			res := output.Result{Headers: []string{"DNS", "TYPE", "URL"}, Raw: raws}
			for _, raw := range raws {
				var e dnsEntry
				if err := json.Unmarshal(raw, &e); err != nil {
					return fmt.Errorf("decode DNS entry: %w", err)
				}
				res.Rows = append(res.Rows, []string{e.Dns, e.Type, e.Url})
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&typ, "type", "", "only list DNS providers of this type")
	return cmd
}

func newDnsCreateCmd() *cobra.Command {
	var (
		typ           string
		url           string
		key           string
		fingerprint   string
		reversemaskv6 int64
		reversev6mask int64
		ttl           int64
		lockToken     string
	)
	cmd := &cobra.Command{
		Use:   "create <dns> --type <type> --url <url> --key <key>",
		Short: "Create an SDN DNS provider",
		Long: "Create an SDN DNS provider (e.g. PowerDNS). The change is staged until " +
			"`pmx sdn apply`. The --key value is a provider API key and is never echoed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			dns := args[0]
			params := &cluster.CreateSdnDnsParams{Dns: dns, Type: typ, Url: url, Key: key}
			fl := cmd.Flags()
			if fl.Changed("fingerprint") {
				params.Fingerprint = strPtr(fingerprint)
			}
			if fl.Changed("reversemaskv6") {
				params.Reversemaskv6 = int64Ptr(reversemaskv6)
			}
			if fl.Changed("reversev6mask") {
				params.Reversev6mask = int64Ptr(reversev6mask)
			}
			if fl.Changed("ttl") {
				params.Ttl = int64Ptr(ttl)
			}
			if fl.Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if err := deps.API.Cluster.CreateSdnDns(cmd.Context(), params); err != nil {
				return fmt.Errorf("create SDN DNS provider %q: %w", dns, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"SDN DNS provider %q created (run `pmx sdn apply` to commit).", dns)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&typ, "type", "", "DNS plugin type, e.g. powerdns (required)")
	f.StringVar(&url, "url", "", "provider API URL (required)")
	f.StringVar(&key, "key", "", "provider API key (required); never echoed")
	f.StringVar(&fingerprint, "fingerprint", "", "certificate SHA-256 fingerprint")
	f.Int64Var(&reversemaskv6, "reversemaskv6", 0, "IPv6 reverse DNS mask")
	f.Int64Var(&reversev6mask, "reversev6mask", 0, "IPv6 reverse DNS mask (legacy alias)")
	f.Int64Var(&ttl, "ttl", 0, "default TTL for records")
	f.StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	cli.MustMarkRequired(cmd, "type")
	cli.MustMarkRequired(cmd, "url")
	cli.MustMarkRequired(cmd, "key")
	return cmd
}

func newDnsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <dns>",
		Short: "Show an SDN DNS provider's configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			dns := args[0]
			resp, err := deps.API.Cluster.GetSdnDns(cmd.Context(), dns)
			if err != nil {
				return fmt.Errorf("get SDN DNS provider %q: %w", dns, err)
			}
			// Scrub the provider API key: the response is opaque, so the
			// secret is stripped in the CLI rather than trusting the API.
			return renderObjectScrubbed(cmd, deps, resp, "key")
		},
	}
}

func newDnsSetCmd() *cobra.Command {
	var (
		url           string
		key           string
		fingerprint   string
		reversemaskv6 int64
		ttl           int64
		del           string
		digest        string
		lockToken     string
	)
	cmd := &cobra.Command{
		Use:   "set <dns>",
		Short: "Update an SDN DNS provider",
		Long: "Update an SDN DNS provider. The change is staged until `pmx sdn apply`. " +
			"The --key value is a provider API key and is never echoed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			dns := args[0]
			fl := cmd.Flags()
			if !anyFlagChanged(fl, append(dnsSetFlagNames, "delete")...) {
				return fmt.Errorf("no changes to set: pass at least one field flag")
			}
			params := &cluster.UpdateSdnDnsParams{}
			if fl.Changed("url") {
				params.Url = strPtr(url)
			}
			if fl.Changed("key") {
				params.Key = strPtr(key)
			}
			if fl.Changed("fingerprint") {
				params.Fingerprint = strPtr(fingerprint)
			}
			if fl.Changed("reversemaskv6") {
				params.Reversemaskv6 = int64Ptr(reversemaskv6)
			}
			if fl.Changed("ttl") {
				params.Ttl = int64Ptr(ttl)
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
			if err := deps.API.Cluster.UpdateSdnDns(cmd.Context(), dns, params); err != nil {
				return fmt.Errorf("update SDN DNS provider %q: %w", dns, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"SDN DNS provider %q updated (run `pmx sdn apply` to commit).", dns)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&url, "url", "", "provider API URL")
	f.StringVar(&key, "key", "", "provider API key; never echoed")
	f.StringVar(&fingerprint, "fingerprint", "", "certificate SHA-256 fingerprint")
	f.Int64Var(&reversemaskv6, "reversemaskv6", 0, "IPv6 reverse DNS mask")
	f.Int64Var(&ttl, "ttl", 0, "default TTL for records")
	f.StringVar(&del, "delete", "", "comma-separated list of settings to delete")
	f.StringVar(&digest, "digest", "", "digest guarding against concurrent modification")
	f.StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}

func newDnsDeleteCmd() *cobra.Command {
	var (
		yes       bool
		lockToken string
	)
	cmd := &cobra.Command{
		Use:   "delete <dns>",
		Short: "Delete an SDN DNS provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			dns := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete SDN DNS provider %q without confirmation: pass --yes", dns)
			}
			params := &cluster.DeleteSdnDnsParams{}
			if cmd.Flags().Changed("lock-token") {
				params.LockToken = strPtr(lockToken)
			}
			if err := deps.API.Cluster.DeleteSdnDns(cmd.Context(), dns, params); err != nil {
				return fmt.Errorf("delete SDN DNS provider %q: %w", dns, err)
			}
			res := output.Result{Message: fmt.Sprintf(
				"SDN DNS provider %q deleted (run `pmx sdn apply` to commit).", dns)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	cmd.Flags().StringVar(&lockToken, "lock-token", "", "token for unlocking the global SDN configuration")
	return cmd
}
