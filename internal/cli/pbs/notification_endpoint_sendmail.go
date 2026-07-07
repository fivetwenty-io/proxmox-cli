package pbs

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/optionschema"
	"github.com/fivetwenty-io/pmx-cli/internal/output"

	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
)

// newNotifEndpointSendmailCmd builds `pmx pbs notification endpoint
// sendmail` — manage sendmail notification endpoints
// (/config/notifications/endpoints/sendmail CRUD).
func newNotifEndpointSendmailCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sendmail",
		Short: "Manage sendmail notification endpoints",
		Long:  "List, inspect, create, update, and delete sendmail notification endpoints.",
	}
	cmd.AddCommand(
		newNotifEndpointSendmailLsCmd(),
		newNotifEndpointSendmailShowCmd(),
		newNotifEndpointSendmailAddCmd(),
		newNotifEndpointSendmailUpdateCmd(),
		newNotifEndpointSendmailDeleteCmd(),
	)
	return cmd
}

// notifSendmailEntry is the decoded shape of one element of
// GET /config/notifications/endpoints/sendmail, and the shape `show` renders.
type notifSendmailEntry struct {
	Author      *string  `json:"author,omitempty"`
	Comment     *string  `json:"comment,omitempty"`
	Disable     *bool    `json:"disable,omitempty"`
	Filter      *string  `json:"filter,omitempty"`
	FromAddress *string  `json:"from-address,omitempty"`
	Mailto      []string `json:"mailto,omitempty"`
	MailtoUser  []string `json:"mailto-user,omitempty"`
	Name        string   `json:"name"`
	Origin      *string  `json:"origin,omitempty"`
}

// newNotifEndpointSendmailLsCmd builds `pmx pbs notification endpoint
// sendmail ls` — list every configured sendmail endpoint
// (GET /config/notifications/endpoints/sendmail).
func newNotifEndpointSendmailLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List sendmail notification endpoints",
		Long:  "List every configured sendmail notification endpoint (GET /config/notifications/endpoints/sendmail).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListNotificationsEndpointsSendmail(cmd.Context())
			if err != nil {
				return fmt.Errorf("list sendmail endpoints: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]notifSendmailEntry, 0, len(items))

			for _, raw := range items {
				var e notifSendmailEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode sendmail endpoint entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{"NAME", "MAILTO", "MAILTO-USER", "FROM-ADDRESS", "DISABLE", "COMMENT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Name, trafficJoin(e.Mailto), trafficJoin(e.MailtoUser),
					pbsFormatOptionalString(e.FromAddress), pbsFormatOptionalBool(e.Disable),
					pbsFormatOptionalString(e.Comment),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newNotifEndpointSendmailShowCmd builds `pmx pbs notification endpoint
// sendmail show <name>` — show a single sendmail endpoint's configuration
// (GET /config/notifications/endpoints/sendmail/{name}).
func newNotifEndpointSendmailShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a single sendmail endpoint's configuration",
		Long: "Show every populated field of a single sendmail endpoint (GET " +
			"/config/notifications/endpoints/sendmail/{name}). The API omits options " +
			"left at their built-in defaults; pass --defaults to also list those, " +
			"with the value they effectively have.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("endpoint name must not be empty")
			}

			resp, err := deps.PBS.Config.GetNotificationsEndpointsSendmail(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("show sendmail endpoint %q: %w", name, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode sendmail endpoint %q: %w", name, err)
			}

			single := stringMap(fields)
			var raw any = fields
			if withDefaults {
				single, raw = optionschema.MergeDefaults(notifSendmailOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// notifSendmailFlags collects the sendmail endpoint attribute flags shared
// by `add` and `update`. Each field maps directly onto a
// CreateNotificationsEndpointsSendmailParams / UpdateNotificationsEndpointsSendmailParams
// field of the same name.
type notifSendmailFlags struct {
	author      string
	comment     string
	disable     bool
	filter      string // create-only
	fromAddress string
	mailto      []string
	mailtoUser  []string
	origin      string // create-only

	// update-only
	del    []string
	digest string
}

// registerNotifSendmailAddFlags binds the attribute flags accepted by `add`.
func registerNotifSendmailAddFlags(cmd *cobra.Command, sf *notifSendmailFlags) {
	f := cmd.Flags()
	f.StringVar(&sf.author, "author", "", "author of the mail (defaults to 'Proxmox Backup Server ($hostname)')")
	f.StringVar(&sf.comment, "comment", "", "comment")
	f.BoolVar(&sf.disable, "disable", false, "create the endpoint disabled")
	f.StringVar(&sf.filter, "filter", "", "deprecated filter expression")
	f.StringVar(&sf.fromAddress, "from-address", "", "'From' address for sent e-mails")
	f.StringArrayVar(&sf.mailto, "mailto", nil, "mail address to send to (repeatable)")
	f.StringArrayVar(&sf.mailtoUser, "mailto-user", nil, "user ID to look up an e-mail address for (repeatable)")
	f.StringVar(&sf.origin, "origin", "", "origin of the notification configuration entry")
}

// registerNotifSendmailUpdateFlags binds every flag `update` accepts. Unlike
// `add`, the update params struct has no filter or origin field, so those
// flags are not registered here.
func registerNotifSendmailUpdateFlags(cmd *cobra.Command, sf *notifSendmailFlags) {
	f := cmd.Flags()
	f.StringVar(&sf.author, "author", "", "author of the mail")
	f.StringVar(&sf.comment, "comment", "", "comment")
	f.BoolVar(&sf.disable, "disable", false, "disable the endpoint")
	f.StringVar(&sf.fromAddress, "from-address", "", "'From' address for sent e-mails")
	f.StringArrayVar(&sf.mailto, "mailto", nil, "mail address to send to (repeatable)")
	f.StringArrayVar(&sf.mailtoUser, "mailto-user", nil, "user ID to look up an e-mail address for (repeatable)")
	f.StringArrayVar(&sf.del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&sf.digest, "digest", "", "only update if the current config digest matches")
}

// newNotifEndpointSendmailAddCmd builds `pmx pbs notification endpoint
// sendmail add <name>` — create a sendmail endpoint (POST
// /config/notifications/endpoints/sendmail). Every option is optional and
// only forwarded when explicitly set. The binding is error-only (the API
// returns null on success), so this is a synchronous verb.
func newNotifEndpointSendmailAddCmd() *cobra.Command {
	var sf notifSendmailFlags
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a sendmail notification endpoint",
		Long: "Create a new sendmail notification endpoint (POST " +
			"/config/notifications/endpoints/sendmail). Every option is optional " +
			"and only forwarded when explicitly set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("endpoint name must not be empty")
			}

			params := &pbsconfig.CreateNotificationsEndpointsSendmailParams{Name: name}

			fl := cmd.Flags()
			if fl.Changed("author") {
				params.Author = strPtr(sf.author)
			}

			if fl.Changed("comment") {
				params.Comment = strPtr(sf.comment)
			}

			if fl.Changed("disable") {
				params.Disable = boolPtr(sf.disable)
			}

			if fl.Changed("filter") {
				params.Filter = strPtr(sf.filter)
			}

			if fl.Changed("from-address") {
				params.FromAddress = strPtr(sf.fromAddress)
			}

			if fl.Changed("mailto") {
				params.Mailto = sf.mailto
			}

			if fl.Changed("mailto-user") {
				params.MailtoUser = sf.mailtoUser
			}

			if fl.Changed("origin") {
				params.Origin = strPtr(sf.origin)
			}

			err := deps.PBS.Config.CreateNotificationsEndpointsSendmail(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create sendmail endpoint %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Sendmail endpoint %q created.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	registerNotifSendmailAddFlags(cmd, &sf)
	return cmd
}

// newNotifEndpointSendmailUpdateCmd builds `pmx pbs notification endpoint
// sendmail update <name>` — update a sendmail endpoint (PUT
// /config/notifications/endpoints/sendmail/{name}). Only flags explicitly
// set are sent; use --delete to reset properties to their default.
func newNotifEndpointSendmailUpdateCmd() *cobra.Command {
	var sf notifSendmailFlags
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update a sendmail notification endpoint",
		Long: "Update an existing sendmail notification endpoint (PUT " +
			"/config/notifications/endpoints/sendmail/{name}). Only flags explicitly " +
			"set are sent; use --delete to reset properties to their default instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("endpoint name must not be empty")
			}

			fl := cmd.Flags()
			if !anyFlagChanged(fl) {
				return fmt.Errorf("update sendmail endpoint %q: no changes given: pass at least one flag", name)
			}

			if fl.Changed("delete") {
				for _, key := range sf.del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateNotificationsEndpointsSendmailParams{}

			if fl.Changed("author") {
				params.Author = strPtr(sf.author)
			}

			if fl.Changed("comment") {
				params.Comment = strPtr(sf.comment)
			}

			if fl.Changed("disable") {
				params.Disable = boolPtr(sf.disable)
			}

			if fl.Changed("from-address") {
				params.FromAddress = strPtr(sf.fromAddress)
			}

			if fl.Changed("mailto") {
				params.Mailto = sf.mailto
			}

			if fl.Changed("mailto-user") {
				params.MailtoUser = sf.mailtoUser
			}

			if fl.Changed("delete") {
				params.Delete = sf.del
			}

			if fl.Changed("digest") {
				params.Digest = strPtr(sf.digest)
			}

			err := deps.PBS.Config.UpdateNotificationsEndpointsSendmail(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("update sendmail endpoint %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Sendmail endpoint %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	registerNotifSendmailUpdateFlags(cmd, &sf)
	return cmd
}

// newNotifEndpointSendmailDeleteCmd builds `pmx pbs notification endpoint
// sendmail delete <name>` — remove a sendmail endpoint (DELETE
// /config/notifications/endpoints/sendmail/{name}). The binding takes no
// digest parameter — PBS does not support conditional deletes for
// notification endpoints.
func newNotifEndpointSendmailDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a sendmail notification endpoint",
		Long: "Remove a sendmail notification endpoint (DELETE /config/notifications/endpoints/sendmail/{name}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("endpoint name must not be empty")
			}
			if !yes {
				return fmt.Errorf("refusing to delete sendmail endpoint %q without confirmation: pass --yes/-y", name)
			}

			err := deps.PBS.Config.DeleteNotificationsEndpointsSendmail(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("delete sendmail endpoint %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Sendmail endpoint %q deleted.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
