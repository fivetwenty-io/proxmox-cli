package pbs

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/optionschema"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"

	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
)

// newNotifEndpointWebhookCmd builds `pmx pbs notification endpoint
// webhook` — manage webhook notification endpoints
// (/config/notifications/endpoints/webhook CRUD).
func newNotifEndpointWebhookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhook",
		Short: "Manage webhook notification endpoints",
		Long:  "List, inspect, create, update, and delete webhook notification endpoints.",
	}
	cmd.AddCommand(
		newNotifEndpointWebhookLsCmd(),
		newNotifEndpointWebhookShowCmd(),
		newNotifEndpointWebhookAddCmd(),
		newNotifEndpointWebhookUpdateCmd(),
		newNotifEndpointWebhookDeleteCmd(),
	)
	return cmd
}

// notifWebhookEntry is the decoded shape of one element of
// GET /config/notifications/endpoints/webhook, and the shape `show` renders.
type notifWebhookEntry struct {
	Body    *string  `json:"body,omitempty"`
	Comment *string  `json:"comment,omitempty"`
	Disable *bool    `json:"disable,omitempty"`
	Header  []string `json:"header,omitempty"`
	Method  string   `json:"method"`
	Name    string   `json:"name"`
	Origin  *string  `json:"origin,omitempty"`
	Secret  []string `json:"secret,omitempty"`
	Url     string   `json:"url"`
}

// newNotifEndpointWebhookLsCmd builds `pmx pbs notification endpoint
// webhook ls` — list every configured webhook endpoint
// (GET /config/notifications/endpoints/webhook).
func newNotifEndpointWebhookLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Short:   "List webhook notification endpoints",
		Long:    "List every configured webhook notification endpoint (GET /config/notifications/endpoints/webhook).",
		Example: "  pmx pbs notification endpoint webhook ls",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListNotificationsEndpointsWebhook(cmd.Context())
			if err != nil {
				return fmt.Errorf("list webhook endpoints: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]notifWebhookEntry, 0, len(items))

			for _, raw := range items {
				var e notifWebhookEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode webhook endpoint entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{"NAME", "METHOD", "URL", "DISABLE", "COMMENT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Name, e.Method, e.Url, pbsFormatOptionalBool(e.Disable), pbsFormatOptionalString(e.Comment),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newNotifEndpointWebhookShowCmd builds `pmx pbs notification endpoint
// webhook show <name>` — show a single webhook endpoint's configuration
// (GET /config/notifications/endpoints/webhook/{name}). Secret values are
// write-only; only secret names are ever returned by the API.
func newNotifEndpointWebhookShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a single webhook endpoint's configuration",
		Long: "Show every populated field of a single webhook endpoint (GET " +
			"/config/notifications/endpoints/webhook/{name}). Secret values are " +
			"write-only; only secret names are ever returned by the API. The API " +
			"also omits options left at their built-in defaults; pass --defaults to " +
			"also list those, with the value they effectively have.",
		Example: "  pmx pbs notification endpoint webhook show webhook-main",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("endpoint name must not be empty")
			}

			resp, err := deps.PBS.Config.GetNotificationsEndpointsWebhook(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("show webhook endpoint %q: %w", name, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode webhook endpoint %q: %w", name, err)
			}

			single := stringMap(fields)
			var raw any = fields
			if withDefaults {
				single, raw = optionschema.MergeDefaults(notifWebhookOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// notifWebhookFlags collects the webhook endpoint attribute flags shared by
// `add` and `update`. Each field maps directly onto a
// CreateNotificationsEndpointsWebhookParams / UpdateNotificationsEndpointsWebhookParams
// field of the same name. --header and --secret take PBS's
// "name=<string>[,value=<string>]" property-string form; the value is
// forwarded verbatim without further encoding.
type notifWebhookFlags struct {
	body    string
	comment string
	disable bool
	header  []string
	method  string
	origin  string // create-only
	secret  []string
	url     string

	// update-only
	del    []string
	digest string
}

// registerNotifWebhookAddFlags binds the attribute flags accepted by `add`.
func registerNotifWebhookAddFlags(cmd *cobra.Command, wf *notifWebhookFlags) {
	f := cmd.Flags()
	f.StringVar(&wf.method, "method", "", "HTTP method to use: post|put|get (required)")
	f.StringVar(&wf.url, "url", "", "HTTP(s) URL with optional port (required)")
	f.StringVar(&wf.body, "body", "", "HTTP body to send (supports templating)")
	f.StringVar(&wf.comment, "comment", "", "comment")
	f.BoolVar(&wf.disable, "disable", false, "create the endpoint disabled")
	f.StringArrayVar(&wf.header, "header", nil,
		"HTTP header as 'name=<string>[,value=<base64>]' (repeatable)")
	f.StringVar(&wf.origin, "origin", "", "origin of the notification configuration entry")
	f.StringArrayVar(&wf.secret, "secret", nil,
		"secret as 'name=<string>[,value=<base64>]' (repeatable)")
}

// registerNotifWebhookUpdateFlags binds every flag `update` accepts. Unlike
// `add`, the update params struct has no origin field, so that flag is not
// registered here.
func registerNotifWebhookUpdateFlags(cmd *cobra.Command, wf *notifWebhookFlags) {
	f := cmd.Flags()
	f.StringVar(&wf.method, "method", "", "HTTP method to use: post|put|get")
	f.StringVar(&wf.url, "url", "", "HTTP(s) URL with optional port")
	f.StringVar(&wf.body, "body", "", "HTTP body to send (supports templating)")
	f.StringVar(&wf.comment, "comment", "", "comment")
	f.BoolVar(&wf.disable, "disable", false, "disable the endpoint")
	f.StringArrayVar(&wf.header, "header", nil,
		"HTTP header as 'name=<string>[,value=<base64>]' (repeatable)")
	f.StringArrayVar(&wf.secret, "secret", nil,
		"secret as 'name=<string>[,value=<base64>]' (repeatable)")
	f.StringArrayVar(&wf.del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&wf.digest, "digest", "", "only update if the current config digest matches")
}

// newNotifEndpointWebhookAddCmd builds `pmx pbs notification endpoint
// webhook add <name>` — create a webhook endpoint (POST
// /config/notifications/endpoints/webhook). --method and --url are
// required; every other option is optional and only forwarded when
// explicitly set. The binding is error-only (the API returns null on
// success), so this is a synchronous verb.
func newNotifEndpointWebhookAddCmd() *cobra.Command {
	var wf notifWebhookFlags
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a webhook notification endpoint",
		Long: "Create a new webhook notification endpoint (POST " +
			"/config/notifications/endpoints/webhook). --method and --url are " +
			"required; every other option is optional and only forwarded when " +
			"explicitly set.",
		Example: `  pmx pbs notification endpoint webhook add webhook-main --method post \
  --url https://hooks.example.com/backup`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("endpoint name must not be empty")
			}

			if wf.method == "" {
				return fmt.Errorf("--method is required")
			}

			if wf.url == "" {
				return fmt.Errorf("--url is required")
			}

			params := &pbsconfig.CreateNotificationsEndpointsWebhookParams{
				Name:   name,
				Method: wf.method,
				Url:    wf.url,
			}

			fl := cmd.Flags()
			if fl.Changed("body") {
				params.Body = strPtr(wf.body)
			}

			if fl.Changed("comment") {
				params.Comment = strPtr(wf.comment)
			}

			if fl.Changed("disable") {
				params.Disable = boolPtr(wf.disable)
			}

			if fl.Changed("header") {
				params.Header = wf.header
			}

			if fl.Changed("origin") {
				params.Origin = strPtr(wf.origin)
			}

			if fl.Changed("secret") {
				params.Secret = wf.secret
			}

			err := deps.PBS.Config.CreateNotificationsEndpointsWebhook(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create webhook endpoint %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Webhook endpoint %q created.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	registerNotifWebhookAddFlags(cmd, &wf)
	cli.MustMarkRequired(cmd, "method")
	cli.MustMarkRequired(cmd, "url")
	return cmd
}

// newNotifEndpointWebhookUpdateCmd builds `pmx pbs notification endpoint
// webhook update <name>` — update a webhook endpoint (PUT
// /config/notifications/endpoints/webhook/{name}). Only flags explicitly
// set are sent; use --delete to reset properties to their default.
func newNotifEndpointWebhookUpdateCmd() *cobra.Command {
	var wf notifWebhookFlags
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update a webhook notification endpoint",
		Long: "Update an existing webhook notification endpoint (PUT " +
			"/config/notifications/endpoints/webhook/{name}). Only flags explicitly " +
			"set are sent; use --delete to reset properties to their default instead.",
		Example: "  pmx pbs notification endpoint webhook update webhook-main --url https://hooks.example.com/backup",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("endpoint name must not be empty")
			}

			fl := cmd.Flags()
			if !anyFlagChanged(fl) {
				return fmt.Errorf("update webhook endpoint %q: no changes requested: pass at least one flag", name)
			}

			if fl.Changed("delete") {
				for _, key := range wf.del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateNotificationsEndpointsWebhookParams{}

			if fl.Changed("method") {
				params.Method = strPtr(wf.method)
			}

			if fl.Changed("url") {
				params.Url = strPtr(wf.url)
			}

			if fl.Changed("body") {
				params.Body = strPtr(wf.body)
			}

			if fl.Changed("comment") {
				params.Comment = strPtr(wf.comment)
			}

			if fl.Changed("disable") {
				params.Disable = boolPtr(wf.disable)
			}

			if fl.Changed("header") {
				params.Header = wf.header
			}

			if fl.Changed("secret") {
				params.Secret = wf.secret
			}

			if fl.Changed("delete") {
				params.Delete = wf.del
			}

			if fl.Changed("digest") {
				params.Digest = strPtr(wf.digest)
			}

			err := deps.PBS.Config.UpdateNotificationsEndpointsWebhook(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("update webhook endpoint %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Webhook endpoint %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	registerNotifWebhookUpdateFlags(cmd, &wf)
	return cmd
}

// newNotifEndpointWebhookDeleteCmd builds `pmx pbs notification endpoint
// webhook delete <name>` — remove a webhook endpoint (DELETE
// /config/notifications/endpoints/webhook/{name}). The binding takes no
// digest parameter — PBS does not support conditional deletes for
// notification endpoints.
func newNotifEndpointWebhookDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a webhook notification endpoint",
		Long: "Remove a webhook notification endpoint (DELETE /config/notifications/endpoints/webhook/{name}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Example: "  pmx pbs notification endpoint webhook delete webhook-main --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("endpoint name must not be empty")
			}
			if !yes {
				return fmt.Errorf("refusing to delete webhook endpoint %q without confirmation: pass --yes/-y", name)
			}

			err := deps.PBS.Config.DeleteNotificationsEndpointsWebhook(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("delete webhook endpoint %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Webhook endpoint %q deleted.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
