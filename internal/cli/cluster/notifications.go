package cluster

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newNotificationsCmd builds the `pve cluster notifications` sub-tree for managing
// the notification system: target endpoints (Gotify, Sendmail, SMTP, Webhook) and
// the matchers that route notifications to those targets.
func newNotificationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "notifications",
		Aliases: []string{"notify"},
		Short:   "Manage notification endpoints and matchers",
		Long: "List notification targets and matchers, and manage Gotify, Sendmail, SMTP, " +
			"and Webhook endpoints plus the matchers that route notifications to them.",
	}
	cmd.AddCommand(
		newNotificationsTargetsCmd(),
		newNotificationsTargetsTestCmd(),
		newNotificationsEndpointsCmd(),
		newNotificationsMatcherFieldsCmd(),
		newNotificationsMatcherFieldValuesCmd(),
		newGotifyCmd(),
		newSendmailCmd(),
		newSMTPCmd(),
		newWebhookCmd(),
		newMatcherCmd(),
	)
	return cmd
}

// --- read-only overviews -----------------------------------------------------

func newNotificationsTargetsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "targets",
		Short: "List all notification targets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
			resp, err := deps.API.Cluster.ListNotificationsTargets(cmd.Context())
			if err != nil {
				return fmt.Errorf("list notification targets: %w", err)
			}
			return renderRawList(cmd, deps, derefRawList(resp))
		},
	}
}

func newNotificationsEndpointsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "endpoints",
		Short: "List all notification endpoints",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
			resp, err := deps.API.Cluster.ListNotificationsEndpoints(cmd.Context())
			if err != nil {
				return fmt.Errorf("list notification endpoints: %w", err)
			}
			return renderRawList(cmd, deps, derefRawList(resp))
		},
	}
}

// newNotificationsTargetsTestCmd builds `pve cluster notifications targets test <name>`.
// It sends a test notification through the named target so operators can verify the
// endpoint configuration is functional without waiting for a real alert.
func newNotificationsTargetsTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "targets-test <name>",
		Short: "Send a test notification through a target",
		Long: "Send a test notification through the named notification target. " +
			"Use this to verify endpoint configuration is functional.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			name := args[0]
			if err := deps.API.Cluster.CreateNotificationsTargetsTest(cmd.Context(), name); err != nil {
				return fmt.Errorf("test notification target %q: %w", name, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Test notification sent to target %q.", name)}, deps.Format)
		},
	}
}

// newNotificationsMatcherFieldsCmd builds `pve cluster notifications matcher-fields`.
// It lists the known metadata field names that can be used in matcher rules.
func newNotificationsMatcherFieldsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "matcher-fields",
		Short: "List known matcher metadata field names",
		Long: "List the known metadata field names that can be used when authoring " +
			"notification matcher rules.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
			resp, err := deps.API.Cluster.ListNotificationsMatcherFields(cmd.Context())
			if err != nil {
				return fmt.Errorf("list notification matcher fields: %w", err)
			}
			return renderRawList(cmd, deps, derefRawList(resp))
		},
	}
}

// newNotificationsMatcherFieldValuesCmd builds `pve cluster notifications matcher-field-values`.
// It lists each known metadata field together with its valid values.
func newNotificationsMatcherFieldValuesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "matcher-field-values",
		Short: "List matcher field names and their known values",
		Long: "List each known notification matcher metadata field together with the " +
			"values it can take. Useful when authoring matcher rules.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
			resp, err := deps.API.Cluster.ListNotificationsMatcherFieldValues(cmd.Context())
			if err != nil {
				return fmt.Errorf("list notification matcher field values: %w", err)
			}
			return renderRawList(cmd, deps, derefRawList(resp))
		},
	}
}

// derefRawList converts any named []json.RawMessage response pointer to a plain
// slice, tolerating a nil pointer.
func derefRawList[T ~[]json.RawMessage](resp *T) []json.RawMessage {
	if resp == nil {
		return nil
	}
	return []json.RawMessage(*resp)
}

// renderRawList renders a raw object list as a union-of-keys table.
func renderRawList(cmd *cobra.Command, deps *cli.Deps, raws []json.RawMessage) error {
	res, err := rawUnionResult(raws)
	if err != nil {
		return err
	}
	return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
}

// renderEndpointGet renders a typed endpoint config object as a single record.
// The PVE Get responses never include real secret values: gotify/SMTP omit the
// token/password entirely, and webhook returns only masked secret-name entries
// (never the value), so rendering the config is safe.
func renderEndpointGet(cmd *cobra.Command, deps *cli.Deps, v any, label, name string) error {
	single, raw, err := objectToSingle(v)
	if err != nil {
		return fmt.Errorf("get %s endpoint %q: %w", label, name, err)
	}
	return deps.Out.Render(cmd.OutOrStdout(),
		output.Result{Single: single, Raw: raw}, deps.Format)
}

// requireDeleteYes guards a destructive delete behind --yes.
func requireDeleteYes(yes bool, what, name string) error {
	if !yes {
		return fmt.Errorf("refusing to delete %s %q without confirmation: pass --yes/-y", what, name)
	}
	return nil
}

// --- gotify ------------------------------------------------------------------

func newGotifyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "gotify", Short: "Manage Gotify notification endpoints"}
	cmd.AddCommand(
		simpleRawList("list", "List Gotify endpoints", func(cmd *cobra.Command, deps *cli.Deps) ([]json.RawMessage, error) {
			resp, err := deps.API.Cluster.ListNotificationsEndpointsGotify(cmd.Context())
			return derefRawList(resp), err
		}),
		newGotifyGetCmd(), newGotifyCreateCmd(), newGotifySetCmd(),
		newDeleteEndpointCmd("Gotify endpoint", func(cmd *cobra.Command, deps *cli.Deps, name string) error {
			return deps.API.Cluster.DeleteNotificationsEndpointsGotify(cmd.Context(), name)
		}),
	)
	return cmd
}

func newGotifyGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Show a Gotify endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			resp, err := deps.API.Cluster.GetNotificationsEndpointsGotify(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get Gotify endpoint %q: %w", args[0], err)
			}
			return renderEndpointGet(cmd, deps, resp, "Gotify", args[0])
		},
	}
}

func newGotifyCreateCmd() *cobra.Command {
	var (
		server, token, comment string
		disable                bool
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a Gotify endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			fl := cmd.Flags()
			params := &pvecluster.CreateNotificationsEndpointsGotifyParams{
				Name: args[0], Server: server, Token: token,
			}
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("disable") {
				params.Disable = &disable
			}
			if err := deps.API.Cluster.CreateNotificationsEndpointsGotify(cmd.Context(), params); err != nil {
				return fmt.Errorf("create Gotify endpoint %q: %w", args[0], err)
			}
			return endpointMsg(cmd, deps, "Gotify", args[0], "created")
		},
	}
	f := cmd.Flags()
	f.StringVar(&server, "server", "", "Gotify server URL (required)")
	f.StringVar(&token, "token", "", "Gotify application token (required)")
	f.StringVar(&comment, "comment", "", "comment")
	f.BoolVar(&disable, "disable", false, "create the endpoint disabled")
	_ = cmd.MarkFlagRequired("server")
	_ = cmd.MarkFlagRequired("token")
	return cmd
}

func newGotifySetCmd() *cobra.Command {
	var (
		server, token, comment string
		disable                bool
		del                    []string
		digest                 string
	)
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Update a Gotify endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			fl := cmd.Flags()
			if !anyFlagChanged(fl, "server", "token", "comment", "disable", "delete", "digest") {
				return fmt.Errorf("no changes to set: pass at least one flag")
			}
			params := &pvecluster.UpdateNotificationsEndpointsGotifyParams{}
			if fl.Changed("server") {
				params.Server = &server
			}
			if fl.Changed("token") {
				params.Token = &token
			}
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("disable") {
				params.Disable = &disable
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if err := deps.API.Cluster.UpdateNotificationsEndpointsGotify(cmd.Context(), args[0], params); err != nil {
				return fmt.Errorf("update Gotify endpoint %q: %w", args[0], err)
			}
			return endpointMsg(cmd, deps, "Gotify", args[0], "updated")
		},
	}
	f := cmd.Flags()
	f.StringVar(&server, "server", "", "Gotify server URL")
	f.StringVar(&token, "token", "", "Gotify application token")
	f.StringVar(&comment, "comment", "", "comment")
	f.BoolVar(&disable, "disable", false, "disable the endpoint")
	f.StringArrayVar(&del, "delete", nil, "settings to reset to default (repeatable)")
	f.StringVar(&digest, "digest", "", "prevent changes if the config digest differs")
	return cmd
}

// --- sendmail ----------------------------------------------------------------

func newSendmailCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "sendmail", Short: "Manage Sendmail notification endpoints"}
	cmd.AddCommand(
		simpleRawList("list", "List Sendmail endpoints", func(cmd *cobra.Command, deps *cli.Deps) ([]json.RawMessage, error) {
			resp, err := deps.API.Cluster.ListNotificationsEndpointsSendmail(cmd.Context())
			return derefRawList(resp), err
		}),
		newSendmailGetCmd(), newSendmailCreateCmd(), newSendmailSetCmd(),
		newDeleteEndpointCmd("Sendmail endpoint", func(cmd *cobra.Command, deps *cli.Deps, name string) error {
			return deps.API.Cluster.DeleteNotificationsEndpointsSendmail(cmd.Context(), name)
		}),
	)
	return cmd
}

func newSendmailGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Show a Sendmail endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			resp, err := deps.API.Cluster.GetNotificationsEndpointsSendmail(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get Sendmail endpoint %q: %w", args[0], err)
			}
			return renderEndpointGet(cmd, deps, resp, "Sendmail", args[0])
		},
	}
}

func newSendmailCreateCmd() *cobra.Command {
	var (
		mailto, mailtoUser           []string
		fromAddress, author, comment string
		disable                      bool
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a Sendmail endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			fl := cmd.Flags()
			params := &pvecluster.CreateNotificationsEndpointsSendmailParams{Name: args[0]}
			if fl.Changed("mailto") {
				params.Mailto = mailto
			}
			if fl.Changed("mailto-user") {
				params.MailtoUser = mailtoUser
			}
			if fl.Changed("from-address") {
				params.FromAddress = &fromAddress
			}
			if fl.Changed("author") {
				params.Author = &author
			}
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("disable") {
				params.Disable = &disable
			}
			if err := deps.API.Cluster.CreateNotificationsEndpointsSendmail(cmd.Context(), params); err != nil {
				return fmt.Errorf("create Sendmail endpoint %q: %w", args[0], err)
			}
			return endpointMsg(cmd, deps, "Sendmail", args[0], "created")
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&mailto, "mailto", nil, "email recipient (repeatable)")
	f.StringArrayVar(&mailtoUser, "mailto-user", nil, "PVE user recipient (repeatable)")
	f.StringVar(&fromAddress, "from-address", "", "From address for the mail")
	f.StringVar(&author, "author", "", "author of the mail")
	f.StringVar(&comment, "comment", "", "comment")
	f.BoolVar(&disable, "disable", false, "create the endpoint disabled")
	return cmd
}

func newSendmailSetCmd() *cobra.Command {
	var (
		mailto, mailtoUser           []string
		fromAddress, author, comment string
		disable                      bool
		del                          []string
		digest                       string
	)
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Update a Sendmail endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			fl := cmd.Flags()
			if !anyFlagChanged(fl, "mailto", "mailto-user", "from-address", "author", "comment", "disable", "delete", "digest") {
				return fmt.Errorf("no changes to set: pass at least one flag")
			}
			params := &pvecluster.UpdateNotificationsEndpointsSendmailParams{}
			if fl.Changed("mailto") {
				params.Mailto = mailto
			}
			if fl.Changed("mailto-user") {
				params.MailtoUser = mailtoUser
			}
			if fl.Changed("from-address") {
				params.FromAddress = &fromAddress
			}
			if fl.Changed("author") {
				params.Author = &author
			}
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("disable") {
				params.Disable = &disable
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if err := deps.API.Cluster.UpdateNotificationsEndpointsSendmail(cmd.Context(), args[0], params); err != nil {
				return fmt.Errorf("update Sendmail endpoint %q: %w", args[0], err)
			}
			return endpointMsg(cmd, deps, "Sendmail", args[0], "updated")
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&mailto, "mailto", nil, "email recipient (repeatable)")
	f.StringArrayVar(&mailtoUser, "mailto-user", nil, "PVE user recipient (repeatable)")
	f.StringVar(&fromAddress, "from-address", "", "From address for the mail")
	f.StringVar(&author, "author", "", "author of the mail")
	f.StringVar(&comment, "comment", "", "comment")
	f.BoolVar(&disable, "disable", false, "disable the endpoint")
	f.StringArrayVar(&del, "delete", nil, "settings to reset to default (repeatable)")
	f.StringVar(&digest, "digest", "", "prevent changes if the config digest differs")
	return cmd
}

// --- smtp --------------------------------------------------------------------

func newSMTPCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "smtp", Short: "Manage SMTP notification endpoints"}
	cmd.AddCommand(
		simpleRawList("list", "List SMTP endpoints", func(cmd *cobra.Command, deps *cli.Deps) ([]json.RawMessage, error) {
			resp, err := deps.API.Cluster.ListNotificationsEndpointsSmtp(cmd.Context())
			return derefRawList(resp), err
		}),
		newSMTPGetCmd(), newSMTPCreateCmd(), newSMTPSetCmd(),
		newDeleteEndpointCmd("SMTP endpoint", func(cmd *cobra.Command, deps *cli.Deps, name string) error {
			return deps.API.Cluster.DeleteNotificationsEndpointsSmtp(cmd.Context(), name)
		}),
	)
	return cmd
}

func newSMTPGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Show an SMTP endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			resp, err := deps.API.Cluster.GetNotificationsEndpointsSmtp(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get SMTP endpoint %q: %w", args[0], err)
			}
			return renderEndpointGet(cmd, deps, resp, "SMTP", args[0])
		},
	}
}

func newSMTPCreateCmd() *cobra.Command {
	var (
		server, fromAddress      string
		mailto, mailtoUser       []string
		username, password, mode string
		author, comment          string
		port                     int64
		disable                  bool
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an SMTP endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			fl := cmd.Flags()
			params := &pvecluster.CreateNotificationsEndpointsSmtpParams{
				Name: args[0], Server: server, FromAddress: fromAddress,
			}
			if fl.Changed("mailto") {
				params.Mailto = mailto
			}
			if fl.Changed("mailto-user") {
				params.MailtoUser = mailtoUser
			}
			if fl.Changed("username") {
				params.Username = &username
			}
			if fl.Changed("password") {
				params.Password = &password
			}
			if fl.Changed("mode") {
				params.Mode = &mode
			}
			if fl.Changed("port") {
				params.Port = &port
			}
			if fl.Changed("author") {
				params.Author = &author
			}
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("disable") {
				params.Disable = &disable
			}
			if err := deps.API.Cluster.CreateNotificationsEndpointsSmtp(cmd.Context(), params); err != nil {
				return fmt.Errorf("create SMTP endpoint %q: %w", args[0], err)
			}
			return endpointMsg(cmd, deps, "SMTP", args[0], "created")
		},
	}
	f := cmd.Flags()
	f.StringVar(&server, "server", "", "SMTP server address (required)")
	f.StringVar(&fromAddress, "from-address", "", "From address for the mail (required)")
	f.StringArrayVar(&mailto, "mailto", nil, "email recipient (repeatable)")
	f.StringArrayVar(&mailtoUser, "mailto-user", nil, "PVE user recipient (repeatable)")
	f.StringVar(&username, "username", "", "username for SMTP authentication")
	f.StringVar(&password, "password", "", "password for SMTP authentication")
	f.StringVar(&mode, "mode", "", "encryption mode: insecure, starttls, or tls")
	f.Int64Var(&port, "port", 0, "SMTP server port")
	f.StringVar(&author, "author", "", "author of the mail")
	f.StringVar(&comment, "comment", "", "comment")
	f.BoolVar(&disable, "disable", false, "create the endpoint disabled")
	_ = cmd.MarkFlagRequired("server")
	_ = cmd.MarkFlagRequired("from-address")
	return cmd
}

func newSMTPSetCmd() *cobra.Command {
	var (
		server, fromAddress      string
		mailto, mailtoUser       []string
		username, password, mode string
		author, comment          string
		port                     int64
		disable                  bool
		del                      []string
		digest                   string
	)
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Update an SMTP endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			fl := cmd.Flags()
			if !anyFlagChanged(fl, "server", "from-address", "mailto", "mailto-user", "username",
				"password", "mode", "port", "author", "comment", "disable", "delete", "digest") {
				return fmt.Errorf("no changes to set: pass at least one flag")
			}
			params := &pvecluster.UpdateNotificationsEndpointsSmtpParams{}
			if fl.Changed("server") {
				params.Server = &server
			}
			if fl.Changed("from-address") {
				params.FromAddress = &fromAddress
			}
			if fl.Changed("mailto") {
				params.Mailto = mailto
			}
			if fl.Changed("mailto-user") {
				params.MailtoUser = mailtoUser
			}
			if fl.Changed("username") {
				params.Username = &username
			}
			if fl.Changed("password") {
				params.Password = &password
			}
			if fl.Changed("mode") {
				params.Mode = &mode
			}
			if fl.Changed("port") {
				params.Port = &port
			}
			if fl.Changed("author") {
				params.Author = &author
			}
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("disable") {
				params.Disable = &disable
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if err := deps.API.Cluster.UpdateNotificationsEndpointsSmtp(cmd.Context(), args[0], params); err != nil {
				return fmt.Errorf("update SMTP endpoint %q: %w", args[0], err)
			}
			return endpointMsg(cmd, deps, "SMTP", args[0], "updated")
		},
	}
	f := cmd.Flags()
	f.StringVar(&server, "server", "", "SMTP server address")
	f.StringVar(&fromAddress, "from-address", "", "From address for the mail")
	f.StringArrayVar(&mailto, "mailto", nil, "email recipient (repeatable)")
	f.StringArrayVar(&mailtoUser, "mailto-user", nil, "PVE user recipient (repeatable)")
	f.StringVar(&username, "username", "", "username for SMTP authentication")
	f.StringVar(&password, "password", "", "password for SMTP authentication")
	f.StringVar(&mode, "mode", "", "encryption mode: insecure, starttls, or tls")
	f.Int64Var(&port, "port", 0, "SMTP server port")
	f.StringVar(&author, "author", "", "author of the mail")
	f.StringVar(&comment, "comment", "", "comment")
	f.BoolVar(&disable, "disable", false, "disable the endpoint")
	f.StringArrayVar(&del, "delete", nil, "settings to reset to default (repeatable)")
	f.StringVar(&digest, "digest", "", "prevent changes if the config digest differs")
	return cmd
}

// --- webhook -----------------------------------------------------------------

func newWebhookCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "webhook", Short: "Manage Webhook notification endpoints"}
	cmd.AddCommand(
		simpleRawList("list", "List Webhook endpoints", func(cmd *cobra.Command, deps *cli.Deps) ([]json.RawMessage, error) {
			resp, err := deps.API.Cluster.ListNotificationsEndpointsWebhook(cmd.Context())
			return derefRawList(resp), err
		}),
		newWebhookGetCmd(), newWebhookCreateCmd(), newWebhookSetCmd(),
		newDeleteEndpointCmd("Webhook endpoint", func(cmd *cobra.Command, deps *cli.Deps, name string) error {
			return deps.API.Cluster.DeleteNotificationsEndpointsWebhook(cmd.Context(), name)
		}),
	)
	return cmd
}

func newWebhookGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Show a Webhook endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			resp, err := deps.API.Cluster.GetNotificationsEndpointsWebhook(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get Webhook endpoint %q: %w", args[0], err)
			}
			return renderEndpointGet(cmd, deps, resp, "Webhook", args[0])
		},
	}
}

func newWebhookCreateCmd() *cobra.Command {
	var (
		url, method, body, comment string
		header, secret             []string
		disable                    bool
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a Webhook endpoint",
		Long: "Create a webhook endpoint. --header and --secret take property strings of " +
			"the form name=<name>,value=<base64 of value>.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			fl := cmd.Flags()
			params := &pvecluster.CreateNotificationsEndpointsWebhookParams{
				Name: args[0], Url: url, Method: method,
			}
			if fl.Changed("header") {
				params.Header = header
			}
			if fl.Changed("secret") {
				params.Secret = secret
			}
			if fl.Changed("body") {
				params.Body = &body
			}
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("disable") {
				params.Disable = &disable
			}
			if err := deps.API.Cluster.CreateNotificationsEndpointsWebhook(cmd.Context(), params); err != nil {
				return fmt.Errorf("create Webhook endpoint %q: %w", args[0], err)
			}
			return endpointMsg(cmd, deps, "Webhook", args[0], "created")
		},
	}
	f := cmd.Flags()
	f.StringVar(&url, "url", "", "server URL (required)")
	f.StringVar(&method, "method", "", "HTTP method: post, put, or get (required)")
	f.StringArrayVar(&header, "header", nil, "HTTP header property string (repeatable)")
	f.StringArrayVar(&secret, "secret", nil, "secret property string (repeatable)")
	f.StringVar(&body, "body", "", "HTTP body, base64 encoded")
	f.StringVar(&comment, "comment", "", "comment")
	f.BoolVar(&disable, "disable", false, "create the endpoint disabled")
	_ = cmd.MarkFlagRequired("url")
	_ = cmd.MarkFlagRequired("method")
	return cmd
}

func newWebhookSetCmd() *cobra.Command {
	var (
		url, method, body, comment string
		header, secret             []string
		disable                    bool
		del                        []string
		digest                     string
	)
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Update a Webhook endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			fl := cmd.Flags()
			if !anyFlagChanged(fl, "url", "method", "header", "secret", "body", "comment", "disable", "delete", "digest") {
				return fmt.Errorf("no changes to set: pass at least one flag")
			}
			params := &pvecluster.UpdateNotificationsEndpointsWebhookParams{}
			if fl.Changed("url") {
				params.Url = &url
			}
			if fl.Changed("method") {
				params.Method = &method
			}
			if fl.Changed("header") {
				params.Header = header
			}
			if fl.Changed("secret") {
				params.Secret = secret
			}
			if fl.Changed("body") {
				params.Body = &body
			}
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("disable") {
				params.Disable = &disable
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if err := deps.API.Cluster.UpdateNotificationsEndpointsWebhook(cmd.Context(), args[0], params); err != nil {
				return fmt.Errorf("update Webhook endpoint %q: %w", args[0], err)
			}
			return endpointMsg(cmd, deps, "Webhook", args[0], "updated")
		},
	}
	f := cmd.Flags()
	f.StringVar(&url, "url", "", "server URL")
	f.StringVar(&method, "method", "", "HTTP method: post, put, or get")
	f.StringArrayVar(&header, "header", nil, "HTTP header property string (repeatable)")
	f.StringArrayVar(&secret, "secret", nil, "secret property string (repeatable)")
	f.StringVar(&body, "body", "", "HTTP body, base64 encoded")
	f.StringVar(&comment, "comment", "", "comment")
	f.BoolVar(&disable, "disable", false, "disable the endpoint")
	f.StringArrayVar(&del, "delete", nil, "settings to reset to default (repeatable)")
	f.StringVar(&digest, "digest", "", "prevent changes if the config digest differs")
	return cmd
}

// --- matcher -----------------------------------------------------------------

func newMatcherCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "matcher", Short: "Manage notification matchers"}
	cmd.AddCommand(
		simpleRawList("list", "List matchers", func(cmd *cobra.Command, deps *cli.Deps) ([]json.RawMessage, error) {
			resp, err := deps.API.Cluster.ListNotificationsMatchers(cmd.Context())
			return derefRawList(resp), err
		}),
		newMatcherGetCmd(), newMatcherCreateCmd(), newMatcherSetCmd(),
		newDeleteEndpointCmd("matcher", func(cmd *cobra.Command, deps *cli.Deps, name string) error {
			return deps.API.Cluster.DeleteNotificationsMatchers(cmd.Context(), name)
		}),
	)
	return cmd
}

func newMatcherGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Show a matcher",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			resp, err := deps.API.Cluster.GetNotificationsMatchers(cmd.Context(), args[0])
			if err != nil {
				return fmt.Errorf("get matcher %q: %w", args[0], err)
			}
			return renderEndpointGet(cmd, deps, resp, "matcher", args[0])
		},
	}
}

func newMatcherCreateCmd() *cobra.Command {
	var (
		matchField, matchSeverity, matchCalendar, target []string
		mode, comment                                    string
		invertMatch, disable                             bool
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a matcher",
		Long: "Create a matcher that routes matching notifications to targets. --match-field " +
			"takes (regex|exact):<field>=<value>.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			fl := cmd.Flags()
			params := &pvecluster.CreateNotificationsMatchersParams{Name: args[0]}
			applyMatcherCreate(fl, params, matcherVals{
				matchField, matchSeverity, matchCalendar, target, mode, comment, invertMatch, disable,
			})
			if err := deps.API.Cluster.CreateNotificationsMatchers(cmd.Context(), params); err != nil {
				return fmt.Errorf("create matcher %q: %w", args[0], err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Matcher %q created.", args[0])}, deps.Format)
		},
	}
	registerMatcherFlags(cmd, &matchField, &matchSeverity, &matchCalendar, &target, &mode, &comment, &invertMatch, &disable)
	return cmd
}

func newMatcherSetCmd() *cobra.Command {
	var (
		matchField, matchSeverity, matchCalendar, target []string
		mode, comment                                    string
		invertMatch, disable                             bool
		del                                              []string
		digest                                           string
	)
	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Update a matcher",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			fl := cmd.Flags()
			if !anyFlagChanged(fl, "match-field", "match-severity", "match-calendar", "notify-target",
				"mode", "comment", "invert-match", "disable", "delete", "digest") {
				return fmt.Errorf("no changes to set: pass at least one flag")
			}
			params := &pvecluster.UpdateNotificationsMatchersParams{}
			if fl.Changed("match-field") {
				params.MatchField = matchField
			}
			if fl.Changed("match-severity") {
				params.MatchSeverity = matchSeverity
			}
			if fl.Changed("match-calendar") {
				params.MatchCalendar = matchCalendar
			}
			if fl.Changed("notify-target") {
				params.Target = target
			}
			if fl.Changed("mode") {
				params.Mode = &mode
			}
			if fl.Changed("comment") {
				params.Comment = &comment
			}
			if fl.Changed("invert-match") {
				params.InvertMatch = &invertMatch
			}
			if fl.Changed("disable") {
				params.Disable = &disable
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = &digest
			}
			if err := deps.API.Cluster.UpdateNotificationsMatchers(cmd.Context(), args[0], params); err != nil {
				return fmt.Errorf("update matcher %q: %w", args[0], err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Matcher %q updated.", args[0])}, deps.Format)
		},
	}
	registerMatcherFlags(cmd, &matchField, &matchSeverity, &matchCalendar, &target, &mode, &comment, &invertMatch, &disable)
	f := cmd.Flags()
	f.StringArrayVar(&del, "delete", nil, "settings to reset to default (repeatable)")
	f.StringVar(&digest, "digest", "", "prevent changes if the config digest differs")
	return cmd
}

type matcherVals struct {
	matchField    []string
	matchSeverity []string
	matchCalendar []string
	target        []string
	mode          string
	comment       string
	invertMatch   bool
	disable       bool
}

func applyMatcherCreate(fl flagChecker, p *pvecluster.CreateNotificationsMatchersParams, v matcherVals) {
	if fl.Changed("match-field") {
		p.MatchField = v.matchField
	}
	if fl.Changed("match-severity") {
		p.MatchSeverity = v.matchSeverity
	}
	if fl.Changed("match-calendar") {
		p.MatchCalendar = v.matchCalendar
	}
	if fl.Changed("notify-target") {
		p.Target = v.target
	}
	if fl.Changed("mode") {
		p.Mode = &v.mode
	}
	if fl.Changed("comment") {
		p.Comment = &v.comment
	}
	if fl.Changed("invert-match") {
		p.InvertMatch = &v.invertMatch
	}
	if fl.Changed("disable") {
		p.Disable = &v.disable
	}
}

func registerMatcherFlags(cmd *cobra.Command, matchField, matchSeverity, matchCalendar, target *[]string,
	mode, comment *string, invertMatch, disable *bool) {
	f := cmd.Flags()
	f.StringArrayVar(matchField, "match-field", nil, "field match (regex|exact):<field>=<value> (repeatable)")
	f.StringArrayVar(matchSeverity, "match-severity", nil, "severity to match (repeatable)")
	f.StringArrayVar(matchCalendar, "match-calendar", nil, "timestamp match (systemd calendar) (repeatable)")
	f.StringArrayVar(target, "notify-target", nil, "target endpoint to notify on match (repeatable)")
	f.StringVar(mode, "mode", "", "combine properties with 'all' or 'any'")
	f.StringVar(comment, "comment", "", "comment")
	f.BoolVar(invertMatch, "invert-match", false, "invert the match of the whole matcher")
	f.BoolVar(disable, "disable", false, "create the matcher disabled")
}

// --- shared helpers ----------------------------------------------------------

// simpleRawList builds a read-only list command from a fetch function.
func simpleRawList(use, short string, fetch func(*cobra.Command, *cli.Deps) ([]json.RawMessage, error)) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)
			raws, err := fetch(cmd, deps)
			if err != nil {
				return fmt.Errorf("%s: %w", short, err)
			}
			return renderRawList(cmd, deps, raws)
		},
	}
}

// newDeleteEndpointCmd builds a --yes-gated delete command for a notification
// endpoint or matcher.
func newDeleteEndpointCmd(what string, del func(*cobra.Command, *cli.Deps, string) error) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: fmt.Sprintf("Delete a %s", what),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			if err := requireDeleteYes(yes, what, args[0]); err != nil {
				return err
			}
			if err := del(cmd, deps, args[0]); err != nil {
				return fmt.Errorf("delete %s %q: %w", what, args[0], err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("%s %q deleted.", what, args[0])}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// endpointMsg renders a success message for a create/update operation.
func endpointMsg(cmd *cobra.Command, deps *cli.Deps, what, name, verb string) error {
	return deps.Out.Render(cmd.OutOrStdout(),
		output.Result{Message: fmt.Sprintf("%s endpoint %q %s.", what, name, verb)}, deps.Format)
}
