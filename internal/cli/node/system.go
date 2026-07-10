package node

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// requireSystemYes gates an irreversible or service-affecting host change behind
// an explicit confirmation flag.
func requireSystemYes(node string, yes bool, action string) error {
	if !yes {
		return fmt.Errorf("refusing to %s on node %q without confirmation: pass --yes/-y", action, node)
	}
	return nil
}

// renderObject fetches nothing — it renders an already-decoded typed response as
// a key/value Single, preserving every field in Raw.
func renderObject(cmd *cobra.Command, deps *cli.Deps, v any) error {
	single, raw, err := objectToSingle(v)
	if err != nil {
		return err
	}
	return deps.Out.Render(cmd.OutOrStdout(),
		output.Result{Single: single, Raw: raw}, deps.Format)
}

// ---- dns -------------------------------------------------------------------

func newDnsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dns",
		Short: "Inspect and configure the node's DNS settings",
		Long:  "Show or update the search domain and name-server addresses configured on the resolved node.",
	}
	cmd.AddCommand(newDnsGetCmd(), newDnsSetCmd())
	return cmd
}

func newDnsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the node's DNS configuration",
		Long:  "Show the DNS search domain and configured name-server addresses on the resolved node.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListDns(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("get DNS configuration on node %q: %w", deps.Node, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
}

func newDnsSetCmd() *cobra.Command {
	var (
		search           string
		dns1, dns2, dns3 string
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Update the node's DNS configuration",
		Long: "Set the DNS search domain and, optionally, up to three name-server addresses " +
			"on the resolved node. Re-applying the current values is idempotent.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			fl := cmd.Flags()
			params := &nodes.UpdateDnsParams{Search: search}
			if fl.Changed("dns1") {
				params.Dns1 = &dns1
			}
			if fl.Changed("dns2") {
				params.Dns2 = &dns2
			}
			if fl.Changed("dns3") {
				params.Dns3 = &dns3
			}
			if err := deps.API.Nodes.UpdateDns(cmd.Context(), deps.Node, params); err != nil {
				return fmt.Errorf("update DNS configuration on node %q: %w", deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("DNS configuration updated on node %q.", deps.Node)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&search, "search", "", "DNS search domain (required)")
	f.StringVar(&dns1, "dns1", "", "first name-server IP address")
	f.StringVar(&dns2, "dns2", "", "second name-server IP address")
	f.StringVar(&dns3, "dns3", "", "third name-server IP address")
	cli.MustMarkRequired(cmd, "search")
	return cmd
}

// ---- hosts -----------------------------------------------------------------

func newHostsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hosts",
		Short: "Inspect and replace the node's /etc/hosts file",
		Long:  "Show the current /etc/hosts content, or replace it wholesale on the resolved node.",
	}
	cmd.AddCommand(newHostsGetCmd(), newHostsSetCmd())
	return cmd
}

func newHostsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the node's /etc/hosts content",
		Long:  "Show the current /etc/hosts file content on the resolved node.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListHosts(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("get /etc/hosts on node %q: %w", deps.Node, err)
			}
			data := ""
			if resp != nil {
				data = resp.Data
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: data, Raw: resp}, deps.Format)
		},
	}
}

func newHostsSetCmd() *cobra.Command {
	var (
		data   string
		digest string
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Replace the node's /etc/hosts content",
		Long: "Replace the entire /etc/hosts file on the resolved node with the supplied content. " +
			"This is a wholesale replacement, not a merge, so pass the complete file. Supply --digest " +
			"to guard against a concurrent modification. Requires --yes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, "replace /etc/hosts"); err != nil {
				return err
			}
			params := &nodes.CreateHostsParams{Data: data}
			if cmd.Flags().Changed("digest") {
				params.Digest = &digest
			}
			if err := deps.API.Nodes.CreateHosts(cmd.Context(), deps.Node, params); err != nil {
				return fmt.Errorf("replace /etc/hosts on node %q: %w", deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("/etc/hosts replaced on node %q.", deps.Node)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&data, "data", "", "the complete target content of /etc/hosts (required)")
	f.StringVar(&digest, "digest", "", "expected configuration digest to guard against concurrent changes")
	f.BoolVarP(&yes, "yes", "y", false, "confirm replacing /etc/hosts")
	cli.MustMarkRequired(cmd, "data")
	return cmd
}

// ---- time ------------------------------------------------------------------

func newTimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "time",
		Short: "Inspect and configure the node's time zone",
		Long:  "Show the current time and time zone, or set the time zone, on the resolved node.",
	}
	cmd.AddCommand(newTimeGetCmd(), newTimeSetCmd())
	return cmd
}

func newTimeGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the node's time and time zone",
		Long:  "Show the current local time, UTC time, and configured time zone on the resolved node.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListTime(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("get time configuration on node %q: %w", deps.Node, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
}

func newTimeSetCmd() *cobra.Command {
	var timezone string
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set the node's time zone",
		Long: "Set the node's time zone to a name from /usr/share/zoneinfo/zone.tab (for example " +
			"\"UTC\" or \"Europe/Vienna\"). Re-applying the current zone is idempotent.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			params := &nodes.UpdateTimeParams{Timezone: timezone}
			if err := deps.API.Nodes.UpdateTime(cmd.Context(), deps.Node, params); err != nil {
				return fmt.Errorf("set time zone on node %q: %w", deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Time zone set to %q on node %q.", timezone, deps.Node)}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&timezone, "timezone", "", "time zone name, e.g. UTC or Europe/Vienna (required)")
	cli.MustMarkRequired(cmd, "timezone")
	return cmd
}

// ---- syslog ----------------------------------------------------------------

func newSyslogCmd() *cobra.Command {
	var (
		service      string
		since, until string
		limit, start int64
	)
	cmd := &cobra.Command{
		Use:   "syslog",
		Short: "Read the node's system log",
		Long:  "Read entries from the node's system log, optionally filtered by service or time range.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			fl := cmd.Flags()
			params := &nodes.ListSyslogParams{}
			if fl.Changed("service") {
				params.Service = &service
			}
			if fl.Changed("since") {
				params.Since = &since
			}
			if fl.Changed("until") {
				params.Until = &until
			}
			if fl.Changed("limit") {
				params.Limit = &limit
			}
			if fl.Changed("start") {
				params.Start = &start
			}
			resp, err := deps.API.Nodes.ListSyslog(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("read system log on node %q: %w", deps.Node, err)
			}
			return renderScan(cmd, deps, derefRaws(resp), resp)
		},
	}
	f := cmd.Flags()
	f.StringVar(&service, "service", "", "limit to a single service (systemd unit)")
	f.StringVar(&since, "since", "", "show entries since this date-time string")
	f.StringVar(&until, "until", "", "show entries until this date-time string")
	f.Int64Var(&limit, "limit", 0, "maximum number of entries to return")
	f.Int64Var(&start, "start", 0, "offset of the first entry to return")
	return cmd
}

// ---- journal ---------------------------------------------------------------

func newJournalCmd() *cobra.Command {
	var (
		lastentries  int64
		since, until int64
		startcursor  string
		endcursor    string
	)
	cmd := &cobra.Command{
		Use:   "journal",
		Short: "Read the node's systemd journal",
		Long: "Read raw lines from the node's systemd journal, optionally limited to the last N " +
			"entries or bounded by a time range or cursor.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			fl := cmd.Flags()
			params := &nodes.ListJournalParams{}
			if fl.Changed("lastentries") {
				params.Lastentries = &lastentries
			}
			if fl.Changed("since") {
				params.Since = &since
			}
			if fl.Changed("until") {
				params.Until = &until
			}
			if fl.Changed("startcursor") {
				params.Startcursor = &startcursor
			}
			if fl.Changed("endcursor") {
				params.Endcursor = &endcursor
			}
			resp, err := deps.API.Nodes.ListJournal(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("read journal on node %q: %w", deps.Node, err)
			}
			lines := []string{}
			if resp != nil {
				lines = []string(*resp)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: strings.Join(lines, "\n"), Raw: lines}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&lastentries, "lastentries", 0, "limit to the last N journal lines")
	f.Int64Var(&since, "since", 0, "show entries since this UNIX epoch")
	f.Int64Var(&until, "until", 0, "show entries until this UNIX epoch")
	f.StringVar(&startcursor, "startcursor", "", "start after the given journal cursor")
	f.StringVar(&endcursor, "endcursor", "", "end before the given journal cursor")
	return cmd
}

// ---- report ----------------------------------------------------------------

func newReportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "report",
		Short: "Generate the node's system report",
		Long:  "Generate the node's full system report — a single text document summarizing host configuration and state.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListReport(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("generate system report on node %q: %w", deps.Node, err)
			}
			// The report endpoint returns a bare JSON string; fall back to the
			// raw bytes if it is not a string for some reason.
			text := ""
			if resp != nil {
				if err := json.Unmarshal(*resp, &text); err != nil {
					text = string(*resp)
				}
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: text, Raw: text}, deps.Format)
		},
	}
}

// ---- subscription ----------------------------------------------------------

func newSubscriptionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subscription",
		Short: "Inspect and manage the node's subscription",
		Long:  "Show the node's subscription status, set or remove the subscription key, and refresh it against the server.",
	}
	cmd.AddCommand(
		newSubscriptionGetCmd(),
		newSubscriptionSetCmd(),
		newSubscriptionUpdateCmd(),
		newSubscriptionDeleteCmd(),
	)
	return cmd
}

func newSubscriptionGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the node's subscription status",
		Long:  "Show the Proxmox VE subscription status and product info of the resolved node.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListSubscription(cmd.Context(), deps.Node)
			if err != nil {
				return fmt.Errorf("get subscription status on node %q: %w", deps.Node, err)
			}
			return renderObject(cmd, deps, resp)
		},
	}
}

func newSubscriptionSetCmd() *cobra.Command {
	var (
		key string
		yes bool
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set the node's subscription key",
		Long:  "Set (or replace) the Proxmox VE subscription key on the resolved node. Requires --yes.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, "set the subscription key"); err != nil {
				return err
			}
			params := &nodes.UpdateSubscriptionParams{Key: key}
			if err := deps.API.Nodes.UpdateSubscription(cmd.Context(), deps.Node, params); err != nil {
				return fmt.Errorf("set subscription key on node %q: %w", deps.Node, err)
			}
			// The key is a secret: report success without echoing it.
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Subscription key set on node %q.", deps.Node)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&key, "key", "", "Proxmox VE subscription key (required)")
	f.BoolVarP(&yes, "yes", "y", false, "confirm setting the subscription key")
	cli.MustMarkRequired(cmd, "key")
	return cmd
}

func newSubscriptionUpdateCmd() *cobra.Command {
	var (
		force bool
		yes   bool
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Refresh the node's subscription against the server",
		Long: "Refresh the node's subscription information from the Proxmox server. Pass --force to " +
			"always contact the server even when the local cache is still valid. Requires --yes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, "refresh the subscription"); err != nil {
				return err
			}
			params := &nodes.CreateSubscriptionParams{}
			if cmd.Flags().Changed("force") {
				params.Force = &force
			}
			if err := deps.API.Nodes.CreateSubscription(cmd.Context(), deps.Node, params); err != nil {
				return fmt.Errorf("refresh subscription on node %q: %w", deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Subscription refreshed on node %q.", deps.Node)}, deps.Format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&force, "force", false, "always contact the server, even if the local cache is still valid")
	f.BoolVarP(&yes, "yes", "y", false, "confirm refreshing the subscription")
	return cmd
}

func newSubscriptionDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Remove the node's subscription key",
		Long:  "Remove the Proxmox VE subscription key from the resolved node. Requires --yes.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			if err := requireSystemYes(deps.Node, yes, "remove the subscription key"); err != nil {
				return err
			}
			if err := deps.API.Nodes.DeleteSubscription(cmd.Context(), deps.Node); err != nil {
				return fmt.Errorf("remove subscription key on node %q: %w", deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Subscription key removed on node %q.", deps.Node)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm removing the subscription key")
	return cmd
}
