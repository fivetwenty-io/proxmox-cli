package pbs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/optionschema"
	"github.com/fivetwenty-io/pmx-cli/internal/output"

	pbsadmin "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/admin"
	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
)

// newTrafficCmd builds `pmx pbs traffic` — manage traffic-control rules that
// rate-limit Proxmox Backup Server API traffic by source network or user
// (GET/POST/PUT/DELETE /config/traffic-control), and inspect the current
// measured rate for each rule (GET /admin/traffic-control).
func newTrafficCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "traffic",
		Short: "Manage traffic-control rate-limit rules",
		Long: "Create, inspect, update, and delete Proxmox Backup Server traffic-control " +
			"rules, which cap ingress/egress bandwidth for requests matching a source " +
			"network or authenticated user, and show the currently measured rate for " +
			"each configured rule.",
	}
	cmd.AddCommand(
		newTrafficLsCmd(),
		newTrafficShowCmd(),
		newTrafficAddCmd(),
		newTrafficUpdateCmd(),
		newTrafficDeleteCmd(),
		newTrafficCurrentCmd(),
	)
	return cmd
}

// trafficJoin renders a string slice as a comma-joined table/single cell,
// returning "" for an empty or nil slice.
func trafficJoin(vals []string) string {
	if len(vals) == 0 {
		return ""
	}

	return strings.Join(vals, ",")
}

// trafficRuleEntry is the decoded shape of one element returned by
// `traffic ls` (GET /config/traffic-control), and the shape of the single
// object returned by `traffic show` (GET /config/traffic-control/{name}).
type trafficRuleEntry struct {
	BurstIn   *string  `json:"burst-in,omitempty"`
	BurstOut  *string  `json:"burst-out,omitempty"`
	Comment   *string  `json:"comment,omitempty"`
	Name      string   `json:"name"`
	Network   []string `json:"network"`
	RateIn    *string  `json:"rate-in,omitempty"`
	RateOut   *string  `json:"rate-out,omitempty"`
	Timeframe []string `json:"timeframe,omitempty"`
	Users     []string `json:"users,omitempty"`
}

// trafficRuleSingle flattens a trafficRuleEntry into a string map for
// table/plain/text rendering.
func trafficRuleSingle(e trafficRuleEntry) map[string]string {
	single := map[string]string{
		"name":    e.Name,
		"network": trafficJoin(e.Network),
	}

	if e.RateIn != nil {
		single["rate-in"] = *e.RateIn
	}

	if e.RateOut != nil {
		single["rate-out"] = *e.RateOut
	}

	if e.BurstIn != nil {
		single["burst-in"] = *e.BurstIn
	}

	if e.BurstOut != nil {
		single["burst-out"] = *e.BurstOut
	}

	if e.Comment != nil {
		single["comment"] = *e.Comment
	}

	if len(e.Timeframe) > 0 {
		single["timeframe"] = trafficJoin(e.Timeframe)
	}

	if len(e.Users) > 0 {
		single["users"] = trafficJoin(e.Users)
	}

	return single
}

// decodeTrafficRuleEntries decodes a Config.ListTrafficControl raw-array
// response into typed entries, skipping any element that fails to decode.
func decodeTrafficRuleEntries(resp *pbsconfig.ListTrafficControlResponse) []trafficRuleEntry {
	entries := make([]trafficRuleEntry, 0)
	if resp == nil {
		return entries
	}

	for _, raw := range *resp {
		var e trafficRuleEntry

		err := json.Unmarshal(raw, &e)
		if err != nil {
			continue
		}

		entries = append(entries, e)
	}

	return entries
}

// newTrafficLsCmd builds `pmx pbs traffic ls` — list every configured
// traffic-control rule (GET /config/traffic-control).
func newTrafficLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List traffic-control rules",
		Long:  "List every configured traffic-control rule (GET /config/traffic-control).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListTrafficControl(cmd.Context())
			if err != nil {
				return fmt.Errorf("list traffic-control rules: %w", err)
			}

			entries := decodeTrafficRuleEntries(resp)
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{
				"NAME", "NETWORK", "RATE-IN", "RATE-OUT", "BURST-IN", "BURST-OUT", "TIMEFRAME", "USERS", "COMMENT",
			}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Name,
					trafficJoin(e.Network),
					pbsFormatOptionalString(e.RateIn),
					pbsFormatOptionalString(e.RateOut),
					pbsFormatOptionalString(e.BurstIn),
					pbsFormatOptionalString(e.BurstOut),
					trafficJoin(e.Timeframe),
					trafficJoin(e.Users),
					pbsFormatOptionalString(e.Comment),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newTrafficShowCmd builds `pmx pbs traffic show <name>` — show one
// traffic-control rule's full configuration (GET /config/traffic-control/{name}).
func newTrafficShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show one traffic-control rule's configuration",
		Long: "Show the full configuration of one traffic-control rule (GET " +
			"/config/traffic-control/{name}). The PBS API omits options left at " +
			"their built-in defaults; pass --defaults to also list those, with the " +
			"value they effectively have.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("rule name must not be empty")
			}

			resp, err := deps.PBS.Config.GetTrafficControl(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("show traffic-control rule %q: %w", name, err)
			}

			if resp == nil {
				return fmt.Errorf("show traffic-control rule %q: nil response from PBS", name)
			}

			entry := trafficRuleEntry{
				BurstIn:   resp.BurstIn,
				BurstOut:  resp.BurstOut,
				Comment:   resp.Comment,
				Name:      resp.Name,
				Network:   resp.Network,
				RateIn:    resp.RateIn,
				RateOut:   resp.RateOut,
				Timeframe: resp.Timeframe,
				Users:     resp.Users,
			}
			if entry.Name == "" {
				entry.Name = name
			}

			single := trafficRuleSingle(entry)
			var raw any = resp
			if withDefaults {
				single, raw = optionschema.MergeDefaults(trafficOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// trafficRuleArgs holds the flag values shared by `traffic add` and
// `traffic update`, covering every field of CreateTrafficControlParams and
// UpdateTrafficControlParams.
type trafficRuleArgs struct {
	rateIn, rateOut, burstIn, burstOut, comment string
	network, timeframe, users                   []string
}

// registerTrafficRuleArgs binds the traffic-control rule flags shared by add
// and update onto cmd.
func registerTrafficRuleArgs(cmd *cobra.Command, a *trafficRuleArgs) {
	f := cmd.Flags()
	f.StringVar(&a.rateIn, "rate-in", "", "ingress rate limit, e.g. '10MB' (byte size with optional unit)")
	f.StringVar(&a.rateOut, "rate-out", "", "egress rate limit, e.g. '10MB' (byte size with optional unit)")
	f.StringVar(&a.burstIn, "burst-in", "", "ingress burst limit, e.g. '20MB' (byte size with optional unit)")
	f.StringVar(&a.burstOut, "burst-out", "", "egress burst limit, e.g. '20MB' (byte size with optional unit)")
	f.StringArrayVar(&a.network, "network", nil, "source network in CIDR notation the rule applies to (repeatable)")
	f.StringArrayVar(&a.timeframe, "timeframe", nil, "calendar-event window the rule is active during (repeatable)")
	f.StringArrayVar(&a.users, "users", nil, "authenticated user the rule applies to, overriding IP-only rules (repeatable)")
	f.StringVar(&a.comment, "comment", "", "rule comment")
}

// newTrafficAddCmd builds `pmx pbs traffic add <name>` — create a
// traffic-control rule (POST /config/traffic-control).
func newTrafficAddCmd() *cobra.Command {
	var a trafficRuleArgs
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a traffic-control rule",
		Long: "Create a new traffic-control rule (POST /config/traffic-control). " +
			"--network is required and repeatable; every other flag is optional and " +
			"only forwarded when explicitly set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("rule name must not be empty")
			}

			if len(a.network) == 0 {
				return fmt.Errorf("--network is required: at least one source network must be given")
			}

			params := &pbsconfig.CreateTrafficControlParams{
				Name:    name,
				Network: a.network,
			}

			fl := cmd.Flags()
			if fl.Changed("rate-in") {
				params.RateIn = strPtr(a.rateIn)
			}

			if fl.Changed("rate-out") {
				params.RateOut = strPtr(a.rateOut)
			}

			if fl.Changed("burst-in") {
				params.BurstIn = strPtr(a.burstIn)
			}

			if fl.Changed("burst-out") {
				params.BurstOut = strPtr(a.burstOut)
			}

			if fl.Changed("timeframe") {
				params.Timeframe = a.timeframe
			}

			if fl.Changed("users") {
				params.Users = a.users
			}

			if fl.Changed("comment") {
				params.Comment = strPtr(a.comment)
			}

			err := deps.PBS.Config.CreateTrafficControl(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create traffic-control rule %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Traffic-control rule %q created.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	registerTrafficRuleArgs(cmd, &a)
	cli.MustMarkRequired(cmd, "network")
	return cmd
}

// newTrafficUpdateCmd builds `pmx pbs traffic update <name>` — update a
// traffic-control rule (PUT /config/traffic-control/{name}).
func newTrafficUpdateCmd() *cobra.Command {
	var (
		a      trafficRuleArgs
		digest string
		del    []string
	)
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update a traffic-control rule",
		Long: "Update an existing traffic-control rule (PUT /config/traffic-control/{name}). " +
			"Only flags explicitly set are sent; use --delete to reset properties to their " +
			"default instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("rule name must not be empty")
			}

			fl := cmd.Flags()
			params := &pbsconfig.UpdateTrafficControlParams{}

			if fl.Changed("rate-in") {
				params.RateIn = strPtr(a.rateIn)
			}

			if fl.Changed("rate-out") {
				params.RateOut = strPtr(a.rateOut)
			}

			if fl.Changed("burst-in") {
				params.BurstIn = strPtr(a.burstIn)
			}

			if fl.Changed("burst-out") {
				params.BurstOut = strPtr(a.burstOut)
			}

			if fl.Changed("network") {
				params.Network = a.network
			}

			if fl.Changed("timeframe") {
				params.Timeframe = a.timeframe
			}

			if fl.Changed("users") {
				params.Users = a.users
			}

			if fl.Changed("comment") {
				params.Comment = strPtr(a.comment)
			}

			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}

			if fl.Changed("delete") {
				for _, propName := range del {
					if propName == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}

				params.Delete = del
			}

			err := deps.PBS.Config.UpdateTrafficControl(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("update traffic-control rule %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Traffic-control rule %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	registerTrafficRuleArgs(cmd, &a)
	cmd.Flags().StringVar(&digest, "digest", "", "only update if the current config digest matches")
	cmd.Flags().StringSliceVar(&del, "delete", nil, "property names to reset to default")
	return cmd
}

// newTrafficDeleteCmd builds `pmx pbs traffic delete <name>` — remove a
// traffic-control rule (DELETE /config/traffic-control/{name}).
func newTrafficDeleteCmd() *cobra.Command {
	var digest string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a traffic-control rule",
		Long: "Remove a traffic-control rule (DELETE /config/traffic-control/{name}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("rule name must not be empty")
			}
			if !yes {
				return fmt.Errorf("refusing to delete traffic-control rule %q without confirmation: pass --yes/-y", name)
			}

			params := &pbsconfig.DeleteTrafficControlParams{}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Config.DeleteTrafficControl(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("delete traffic-control rule %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Traffic-control rule %q deleted.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&digest, "digest", "", "only delete if the current config digest matches")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}

// trafficCurrentEntry is the decoded shape of one element returned by
// `traffic current` (GET /admin/traffic-control): a traffic-control rule's
// static configuration plus its currently measured ingress/egress rate.
type trafficCurrentEntry struct {
	BurstIn    *string  `json:"burst-in,omitempty"`
	BurstOut   *string  `json:"burst-out,omitempty"`
	Comment    *string  `json:"comment,omitempty"`
	CurRateIn  int64    `json:"cur-rate-in"`
	CurRateOut int64    `json:"cur-rate-out"`
	Name       string   `json:"name"`
	Network    []string `json:"network,omitempty"`
	RateIn     *string  `json:"rate-in,omitempty"`
	RateOut    *string  `json:"rate-out,omitempty"`
	Timeframe  []string `json:"timeframe,omitempty"`
	Users      []string `json:"users,omitempty"`
}

// decodeTrafficCurrentEntries decodes an Admin.ListTrafficControl raw-array
// response into typed entries, skipping any element that fails to decode.
func decodeTrafficCurrentEntries(resp *pbsadmin.ListTrafficControlResponse) []trafficCurrentEntry {
	entries := make([]trafficCurrentEntry, 0)
	if resp == nil {
		return entries
	}

	for _, raw := range *resp {
		var e trafficCurrentEntry

		err := json.Unmarshal(raw, &e)
		if err != nil {
			continue
		}

		entries = append(entries, e)
	}

	return entries
}

// newTrafficCurrentCmd builds `pmx pbs traffic current` — show every
// traffic-control rule alongside its currently measured rate
// (GET /admin/traffic-control).
func newTrafficCurrentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "current",
		Short: "Show configured rules with their current measured rate",
		Long: "Show every traffic-control rule's configuration together with its " +
			"currently measured ingress/egress rate in bytes/second (GET /admin/traffic-control).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Admin.ListTrafficControl(cmd.Context())
			if err != nil {
				return fmt.Errorf("list current traffic-control rates: %w", err)
			}

			entries := decodeTrafficCurrentEntries(resp)
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{
				"NAME", "RATE-IN", "RATE-OUT", "CUR-RATE-IN", "CUR-RATE-OUT", "BURST-IN", "BURST-OUT", "NETWORK",
			}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Name,
					pbsFormatOptionalString(e.RateIn),
					pbsFormatOptionalString(e.RateOut),
					strconv.FormatInt(e.CurRateIn, 10),
					strconv.FormatInt(e.CurRateOut, 10),
					pbsFormatOptionalString(e.BurstIn),
					pbsFormatOptionalString(e.BurstOut),
					trafficJoin(e.Network),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
