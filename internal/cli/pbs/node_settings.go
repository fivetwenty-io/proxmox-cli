package pbs

import (
	"fmt"

	"github.com/spf13/cobra"

	pbsnodes "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// --- dns ----------------------------------------------------------------

// newNodeDNSCmd builds `pve pbs node dns` and its show/update verbs
// (GET/PUT /nodes/{node}/dns).
func newNodeDNSCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dns",
		Short: "Show or update the node's DNS settings",
	}
	cmd.AddCommand(newNodeDNSShowCmd(nf), newNodeDNSUpdateCmd(nf))
	return cmd
}

func newNodeDNSShowCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the node's DNS settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Nodes.ListDns(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("get dns settings for node %q: %w", nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("get dns settings for node %q: empty response from server", nf.node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode dns settings for node %q: %w", nf.node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newNodeDNSUpdateCmd(nf *nodeFlags) *cobra.Command {
	var (
		dns1, dns2, dns3, search, digest string
		del                              []string
	)

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the node's DNS settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			if !anyFlagChanged(fl) {
				return fmt.Errorf("update dns on node %q: no changes given: pass at least one flag", nf.node)
			}

			params := &pbsnodes.UpdateDnsParams{}
			if fl.Changed("dns1") {
				params.Dns1 = &dns1
			}
			if fl.Changed("dns2") {
				params.Dns2 = &dns2
			}
			if fl.Changed("dns3") {
				params.Dns3 = &dns3
			}
			if fl.Changed("search") {
				params.Search = &search
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}

			err := deps.PBS.Nodes.UpdateDns(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("update dns on node %q: %w", nf.node, err)
			}

			res := output.Result{Message: fmt.Sprintf("DNS settings for node %q updated.", nf.node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&dns1, "dns1", "", "first name server IP address")
	f.StringVar(&dns2, "dns2", "", "second name server IP address")
	f.StringVar(&dns3, "dns3", "", "third name server IP address")
	f.StringVar(&search, "search", "", "search domain for host-name lookup")
	f.StringArrayVar(&del, "delete", nil, "config key to reset to its default (repeatable)")
	f.StringVar(&digest, "digest", "", "prevent changes if the config digest differs")

	return cmd
}

// --- time -----------------------------------------------------------------

// newNodeTimeCmd builds `pve pbs node time` and its show/update verbs
// (GET/PUT /nodes/{node}/time).
func newNodeTimeCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "time",
		Short: "Show or update the node's time zone",
	}
	cmd.AddCommand(newNodeTimeShowCmd(nf), newNodeTimeUpdateCmd(nf))
	return cmd
}

func newNodeTimeShowCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the node's server time and time zone",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Nodes.ListTime(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("get time settings for node %q: %w", nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("get time settings for node %q: empty response from server", nf.node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode time settings for node %q: %w", nf.node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newNodeTimeUpdateCmd(nf *nodeFlags) *cobra.Command {
	var timezone string

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Set the node's time zone",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbsnodes.UpdateTimeParams{Timezone: timezone}

			err := deps.PBS.Nodes.UpdateTime(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("set time zone on node %q: %w", nf.node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Time zone for node %q set to %q.", nf.node, timezone)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&timezone, "timezone", "", "time zone name, e.g. UTC or America/New_York (required)")
	cli.MustMarkRequired(cmd, "timezone")

	return cmd
}

// --- config -----------------------------------------------------------------

// newNodeConfigCmd builds `pve pbs node config` and its show/update verbs
// (GET/PUT /nodes/{node}/config).
func newNodeConfigCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or update the node configuration",
	}
	cmd.AddCommand(newNodeConfigShowCmd(nf), newNodeConfigUpdateCmd(nf))
	return cmd
}

func newNodeConfigShowCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the node configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Nodes.ListConfig(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("get config for node %q: %w", nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("get config for node %q: empty response from server", nf.node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode config for node %q: %w", nf.node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// nodeConfigFlags collects the update-only flags for `node config update`,
// each mapping directly onto an UpdateConfigParams field of the same name.
type nodeConfigFlags struct {
	acme                                                            string
	acmedomain0, acmedomain1, acmedomain2, acmedomain3, acmedomain4 string
	ciphersTLS12, ciphersTLS13                                      string
	consentText                                                     string
	defaultLang                                                     string
	description                                                     string
	emailFrom                                                       string
	httpProxy                                                       string
	location                                                        string
	taskLogMaxDays                                                  int64
	del                                                             []string
	digest                                                          string
}

func newNodeConfigUpdateCmd(nf *nodeFlags) *cobra.Command {
	var cf nodeConfigFlags

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the node configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			if !anyFlagChanged(fl) {
				return fmt.Errorf("update config on node %q: no changes given: pass at least one flag", nf.node)
			}

			params := &pbsnodes.UpdateConfigParams{}
			if fl.Changed("acme") {
				params.Acme = &cf.acme
			}
			if fl.Changed("acmedomain0") {
				params.Acmedomain0 = &cf.acmedomain0
			}
			if fl.Changed("acmedomain1") {
				params.Acmedomain1 = &cf.acmedomain1
			}
			if fl.Changed("acmedomain2") {
				params.Acmedomain2 = &cf.acmedomain2
			}
			if fl.Changed("acmedomain3") {
				params.Acmedomain3 = &cf.acmedomain3
			}
			if fl.Changed("acmedomain4") {
				params.Acmedomain4 = &cf.acmedomain4
			}
			if fl.Changed("ciphers-tls-1.2") {
				params.CiphersTls12 = &cf.ciphersTLS12
			}
			if fl.Changed("ciphers-tls-1.3") {
				params.CiphersTls13 = &cf.ciphersTLS13
			}
			if fl.Changed("consent-text") {
				params.ConsentText = &cf.consentText
			}
			if fl.Changed("default-lang") {
				params.DefaultLang = &cf.defaultLang
			}
			if fl.Changed("description") {
				params.Description = &cf.description
			}
			if fl.Changed("email-from") {
				params.EmailFrom = &cf.emailFrom
			}
			if fl.Changed("http-proxy") {
				params.HttpProxy = &cf.httpProxy
			}
			if fl.Changed("location") {
				params.Location = &cf.location
			}
			if fl.Changed("task-log-max-days") {
				params.TaskLogMaxDays = &cf.taskLogMaxDays
			}
			if fl.Changed("delete") {
				params.Delete = cf.del
			}
			if fl.Changed("digest") {
				params.Digest = &cf.digest
			}

			err := deps.PBS.Nodes.UpdateConfig(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("update config on node %q: %w", nf.node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Configuration for node %q updated.", nf.node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&cf.acme, "acme", "", "the ACME account to use on this node")
	f.StringVar(&cf.acmedomain0, "acmedomain0", "", "ACME domain configuration string (slot 0)")
	f.StringVar(&cf.acmedomain1, "acmedomain1", "", "ACME domain configuration string (slot 1)")
	f.StringVar(&cf.acmedomain2, "acmedomain2", "", "ACME domain configuration string (slot 2)")
	f.StringVar(&cf.acmedomain3, "acmedomain3", "", "ACME domain configuration string (slot 3)")
	f.StringVar(&cf.acmedomain4, "acmedomain4", "", "ACME domain configuration string (slot 4)")
	f.StringVar(&cf.ciphersTLS12, "ciphers-tls-1.2", "", "OpenSSL cipher list for TLS <= 1.2")
	f.StringVar(&cf.ciphersTLS13, "ciphers-tls-1.3", "", "OpenSSL ciphersuites list for TLS 1.3")
	f.StringVar(&cf.consentText, "consent-text", "", "consent banner text")
	f.StringVar(&cf.defaultLang, "default-lang", "", "default UI language")
	f.StringVar(&cf.description, "description", "", "comment (multiple lines)")
	f.StringVar(&cf.emailFrom, "email-from", "", "e-mail address notifications are sent from")
	f.StringVar(&cf.httpProxy, "http-proxy", "", "HTTP proxy configuration [http://]<host>[:port]")
	f.StringVar(&cf.location, "location", "", "the location of the PBS instance")
	f.Int64Var(&cf.taskLogMaxDays, "task-log-max-days", 0, "maximum days to keep task logs")
	f.StringArrayVar(&cf.del, "delete", nil, "config key to reset to its default (repeatable)")
	f.StringVar(&cf.digest, "digest", "", "prevent changes if the config digest differs")

	return cmd
}

// --- subscription -------------------------------------------------------

// newNodeSubscriptionCmd builds `pve pbs node subscription` and its
// show/set/update/delete verbs (/nodes/{node}/subscription).
func newNodeSubscriptionCmd(nf *nodeFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subscription",
		Short: "Show, set, refresh, or remove the node's subscription",
	}
	cmd.AddCommand(
		newNodeSubscriptionShowCmd(nf),
		newNodeSubscriptionSetCmd(nf),
		newNodeSubscriptionUpdateCmd(nf),
		newNodeSubscriptionDeleteCmd(nf),
	)
	return cmd
}

func newNodeSubscriptionShowCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the node's subscription info",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Nodes.ListSubscription(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("get subscription for node %q: %w", nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("get subscription for node %q: empty response from server", nf.node)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode subscription for node %q: %w", nf.node, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

func newNodeSubscriptionSetCmd(nf *nodeFlags) *cobra.Command {
	var key string

	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set the node's subscription key and check it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbsnodes.UpdateSubscriptionParams{Key: key}

			err := deps.PBS.Nodes.UpdateSubscription(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("set subscription key on node %q: %w", nf.node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Subscription key for node %q set.", nf.node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "subscription key to install (required)")
	cli.MustMarkRequired(cmd, "key")

	return cmd
}

func newNodeSubscriptionUpdateCmd(nf *nodeFlags) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check and refresh the node's subscription status against the server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbsnodes.CreateSubscriptionParams{}
			if cmd.Flags().Changed("force") {
				params.Force = &force
			}

			err := deps.PBS.Nodes.CreateSubscription(cmd.Context(), nf.node, params)
			if err != nil {
				return fmt.Errorf("refresh subscription on node %q: %w", nf.node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Subscription status for node %q refreshed.", nf.node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "always contact the server, even if the cache is up to date")

	return cmd
}

func newNodeSubscriptionDeleteCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete",
		Short: "Delete the node's subscription info",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			err := deps.PBS.Nodes.DeleteSubscription(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("delete subscription on node %q: %w", nf.node, err)
			}

			res := output.Result{Message: fmt.Sprintf("Subscription info for node %q deleted.", nf.node)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// --- identity -------------------------------------------------------------

// newNodeIdentityCmd builds `pve pbs node identity` — the unique server
// identity derived from /etc/machine-id (GET /nodes/{node}/identity).
func newNodeIdentityCmd(nf *nodeFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "identity",
		Short: "Show the node's unique server identity",
		Long:  "Show the unique server identity derived from /etc/machine-id.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Nodes.ListIdentity(cmd.Context(), nf.node)
			if err != nil {
				return fmt.Errorf("get identity for node %q: %w", nf.node, err)
			}
			if resp == nil {
				return fmt.Errorf("get identity for node %q: empty response from server", nf.node)
			}

			single := map[string]string{"pbs-instance-id": resp.PbsInstanceId}
			res := output.Result{Single: single, Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}
