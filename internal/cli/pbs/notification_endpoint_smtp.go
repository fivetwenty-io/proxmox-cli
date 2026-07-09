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

// newNotifEndpointSmtpCmd builds `pmx pbs notification endpoint smtp` —
// manage smtp notification endpoints
// (/config/notifications/endpoints/smtp CRUD).
func newNotifEndpointSmtpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "smtp",
		Short: "Manage smtp notification endpoints",
		Long:  "List, inspect, create, update, and delete smtp notification endpoints.",
	}
	cmd.AddCommand(
		newNotifEndpointSmtpLsCmd(),
		newNotifEndpointSmtpShowCmd(),
		newNotifEndpointSmtpAddCmd(),
		newNotifEndpointSmtpUpdateCmd(),
		newNotifEndpointSmtpDeleteCmd(),
	)
	return cmd
}

// notifSmtpEntry is the decoded shape of one element of
// GET /config/notifications/endpoints/smtp, and the shape `show` renders.
type notifSmtpEntry struct {
	Author      *string  `json:"author,omitempty"`
	Comment     *string  `json:"comment,omitempty"`
	Disable     *bool    `json:"disable,omitempty"`
	FromAddress string   `json:"from-address"`
	Mailto      []string `json:"mailto,omitempty"`
	MailtoUser  []string `json:"mailto-user,omitempty"`
	Mode        *string  `json:"mode,omitempty"`
	Name        string   `json:"name"`
	Origin      *string  `json:"origin,omitempty"`
	Port        *int64   `json:"port,omitempty"`
	Server      string   `json:"server"`
	Username    *string  `json:"username,omitempty"`
}

// newNotifEndpointSmtpLsCmd builds `pmx pbs notification endpoint smtp ls`
// — list every configured smtp endpoint
// (GET /config/notifications/endpoints/smtp).
func newNotifEndpointSmtpLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List smtp notification endpoints",
		Long:  "List every configured smtp notification endpoint (GET /config/notifications/endpoints/smtp).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListNotificationsEndpointsSmtp(cmd.Context())
			if err != nil {
				return fmt.Errorf("list smtp endpoints: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]notifSmtpEntry, 0, len(items))

			for _, raw := range items {
				var e notifSmtpEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode smtp endpoint entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{"NAME", "SERVER", "PORT", "MODE", "FROM-ADDRESS", "MAILTO", "DISABLE", "COMMENT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Name, e.Server, pbsFormatOptionalInt64(e.Port), pbsFormatOptionalString(e.Mode),
					e.FromAddress, trafficJoin(e.Mailto), pbsFormatOptionalBool(e.Disable),
					pbsFormatOptionalString(e.Comment),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newNotifEndpointSmtpShowCmd builds `pmx pbs notification endpoint smtp
// show <name>` — show a single smtp endpoint's configuration
// (GET /config/notifications/endpoints/smtp/{name}). The password is
// write-only and is never returned by the API.
func newNotifEndpointSmtpShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a single smtp endpoint's configuration",
		Long: "Show every populated field of a single smtp endpoint (GET " +
			"/config/notifications/endpoints/smtp/{name}). The password is " +
			"write-only and is never returned by the API. The API also omits " +
			"options left at their built-in defaults; pass --defaults to also list " +
			"those, with the value they effectively have.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("endpoint name must not be empty")
			}

			resp, err := deps.PBS.Config.GetNotificationsEndpointsSmtp(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("show smtp endpoint %q: %w", name, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode smtp endpoint %q: %w", name, err)
			}

			single := stringMap(fields)
			var raw any = fields
			if withDefaults {
				single, raw = optionschema.MergeDefaults(notifSmtpOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// notifSmtpFlags collects the smtp endpoint attribute flags shared by `add`
// and `update`. Each field maps directly onto a
// CreateNotificationsEndpointsSmtpParams / UpdateNotificationsEndpointsSmtpParams
// field of the same name.
type notifSmtpFlags struct {
	author      string
	comment     string
	disable     bool
	fromAddress string
	mailto      []string
	mailtoUser  []string
	mode        string
	origin      string // create-only
	password    string
	port        int64
	server      string
	username    string

	// update-only
	del    []string
	digest string
}

// registerNotifSmtpAddFlags binds the attribute flags accepted by `add`.
func registerNotifSmtpAddFlags(cmd *cobra.Command, sf *notifSmtpFlags) {
	f := cmd.Flags()
	f.StringVar(&sf.server, "server", "", "host name or IP of the SMTP relay (required)")
	f.StringVar(&sf.fromAddress, "from-address", "", "'From' address for the mail (required)")
	f.StringVar(&sf.author, "author", "", "author of the mail (defaults to 'Proxmox Backup Server ($hostname)')")
	f.StringVar(&sf.comment, "comment", "", "comment")
	f.BoolVar(&sf.disable, "disable", false, "create the endpoint disabled")
	f.StringArrayVar(&sf.mailto, "mailto", nil, "mail address to send to (repeatable)")
	f.StringArrayVar(&sf.mailtoUser, "mailto-user", nil, "user ID to look up an e-mail address for (repeatable)")
	f.StringVar(&sf.mode, "mode", "", "connection security: insecure|starttls|tls (default: tls)")
	f.StringVar(&sf.origin, "origin", "", "origin of the notification configuration entry")
	f.StringVar(&sf.password, "password", "", "SMTP authentication password")
	f.Int64Var(&sf.port, "port", 0, "port to connect to (default depends on --mode)")
	f.StringVar(&sf.username, "username", "", "SMTP authentication username")
}

// registerNotifSmtpUpdateFlags binds every flag `update` accepts. Unlike
// `add`, the update params struct has no origin field, so that flag is not
// registered here.
func registerNotifSmtpUpdateFlags(cmd *cobra.Command, sf *notifSmtpFlags) {
	f := cmd.Flags()
	f.StringVar(&sf.server, "server", "", "host name or IP of the SMTP relay")
	f.StringVar(&sf.fromAddress, "from-address", "", "'From' address for the mail")
	f.StringVar(&sf.author, "author", "", "author of the mail")
	f.StringVar(&sf.comment, "comment", "", "comment")
	f.BoolVar(&sf.disable, "disable", false, "disable the endpoint")
	f.StringArrayVar(&sf.mailto, "mailto", nil, "mail address to send to (repeatable)")
	f.StringArrayVar(&sf.mailtoUser, "mailto-user", nil, "user ID to look up an e-mail address for (repeatable)")
	f.StringVar(&sf.mode, "mode", "", "connection security: insecure|starttls|tls")
	f.StringVar(&sf.password, "password", "", "SMTP authentication password")
	f.Int64Var(&sf.port, "port", 0, "port to connect to")
	f.StringVar(&sf.username, "username", "", "SMTP authentication username")
	f.StringArrayVar(&sf.del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&sf.digest, "digest", "", "only update if the current config digest matches")
}

// newNotifEndpointSmtpAddCmd builds `pmx pbs notification endpoint smtp add
// <name>` — create an smtp endpoint (POST
// /config/notifications/endpoints/smtp). --server and --from-address are
// required; every other option is optional and only forwarded when
// explicitly set. The binding is error-only (the API returns null on
// success), so this is a synchronous verb.
func newNotifEndpointSmtpAddCmd() *cobra.Command {
	var sf notifSmtpFlags
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create an smtp notification endpoint",
		Long: "Create a new smtp notification endpoint (POST " +
			"/config/notifications/endpoints/smtp). --server and --from-address " +
			"are required; every other option is optional and only forwarded " +
			"when explicitly set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("endpoint name must not be empty")
			}

			if sf.server == "" {
				return fmt.Errorf("--server is required")
			}

			if sf.fromAddress == "" {
				return fmt.Errorf("--from-address is required")
			}

			params := &pbsconfig.CreateNotificationsEndpointsSmtpParams{
				Name:        name,
				Server:      sf.server,
				FromAddress: sf.fromAddress,
			}

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

			if fl.Changed("mailto") {
				params.Mailto = sf.mailto
			}

			if fl.Changed("mailto-user") {
				params.MailtoUser = sf.mailtoUser
			}

			if fl.Changed("mode") {
				params.Mode = strPtr(sf.mode)
			}

			if fl.Changed("origin") {
				params.Origin = strPtr(sf.origin)
			}

			if fl.Changed("password") {
				params.Password = strPtr(sf.password)
			}

			if fl.Changed("port") {
				params.Port = int64Ptr(sf.port)
			}

			if fl.Changed("username") {
				params.Username = strPtr(sf.username)
			}

			err := deps.PBS.Config.CreateNotificationsEndpointsSmtp(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create smtp endpoint %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("SMTP endpoint %q created.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	registerNotifSmtpAddFlags(cmd, &sf)
	cli.MustMarkRequired(cmd, "server")
	cli.MustMarkRequired(cmd, "from-address")
	return cmd
}

// newNotifEndpointSmtpUpdateCmd builds `pmx pbs notification endpoint smtp
// update <name>` — update an smtp endpoint (PUT
// /config/notifications/endpoints/smtp/{name}). Only flags explicitly set
// are sent; use --delete to reset properties to their default.
func newNotifEndpointSmtpUpdateCmd() *cobra.Command {
	var sf notifSmtpFlags
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update an smtp notification endpoint",
		Long: "Update an existing smtp notification endpoint (PUT " +
			"/config/notifications/endpoints/smtp/{name}). Only flags explicitly " +
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
				return fmt.Errorf("update smtp endpoint %q: no changes requested: pass at least one flag", name)
			}

			if fl.Changed("delete") {
				for _, key := range sf.del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateNotificationsEndpointsSmtpParams{}

			if fl.Changed("server") {
				params.Server = strPtr(sf.server)
			}

			if fl.Changed("from-address") {
				params.FromAddress = strPtr(sf.fromAddress)
			}

			if fl.Changed("author") {
				params.Author = strPtr(sf.author)
			}

			if fl.Changed("comment") {
				params.Comment = strPtr(sf.comment)
			}

			if fl.Changed("disable") {
				params.Disable = boolPtr(sf.disable)
			}

			if fl.Changed("mailto") {
				params.Mailto = sf.mailto
			}

			if fl.Changed("mailto-user") {
				params.MailtoUser = sf.mailtoUser
			}

			if fl.Changed("mode") {
				params.Mode = strPtr(sf.mode)
			}

			if fl.Changed("password") {
				params.Password = strPtr(sf.password)
			}

			if fl.Changed("port") {
				params.Port = int64Ptr(sf.port)
			}

			if fl.Changed("username") {
				params.Username = strPtr(sf.username)
			}

			if fl.Changed("delete") {
				params.Delete = sf.del
			}

			if fl.Changed("digest") {
				params.Digest = strPtr(sf.digest)
			}

			err := deps.PBS.Config.UpdateNotificationsEndpointsSmtp(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("update smtp endpoint %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("SMTP endpoint %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	registerNotifSmtpUpdateFlags(cmd, &sf)
	return cmd
}

// newNotifEndpointSmtpDeleteCmd builds `pmx pbs notification endpoint smtp
// delete <name>` — remove an smtp endpoint (DELETE
// /config/notifications/endpoints/smtp/{name}). The binding takes no digest
// parameter — PBS does not support conditional deletes for notification
// endpoints.
func newNotifEndpointSmtpDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an smtp notification endpoint",
		Long: "Remove an smtp notification endpoint (DELETE /config/notifications/endpoints/smtp/{name}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("endpoint name must not be empty")
			}
			if !yes {
				return fmt.Errorf("refusing to delete smtp endpoint %q without confirmation: pass --yes/-y", name)
			}

			err := deps.PBS.Config.DeleteNotificationsEndpointsSmtp(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("delete smtp endpoint %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("SMTP endpoint %q deleted.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
