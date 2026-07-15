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

// newNotifEndpointGotifyCmd builds `pmx pbs notification endpoint gotify` —
// manage gotify notification endpoints (/config/notifications/endpoints/gotify CRUD).
func newNotifEndpointGotifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gotify",
		Short: "Manage gotify notification endpoints",
		Long:  "List, inspect, create, update, and delete gotify notification endpoints.",
	}
	cmd.AddCommand(
		newNotifEndpointGotifyLsCmd(),
		newNotifEndpointGotifyShowCmd(),
		newNotifEndpointGotifyAddCmd(),
		newNotifEndpointGotifyUpdateCmd(),
		newNotifEndpointGotifyDeleteCmd(),
	)
	return cmd
}

// notifGotifyEntry is the decoded shape of one element of
// GET /config/notifications/endpoints/gotify, and the shape `show` renders.
type notifGotifyEntry struct {
	Comment *string `json:"comment,omitempty"`
	Disable *bool   `json:"disable,omitempty"`
	Filter  *string `json:"filter,omitempty"`
	Name    string  `json:"name"`
	Origin  *string `json:"origin,omitempty"`
	Server  string  `json:"server"`
}

// newNotifEndpointGotifyLsCmd builds `pmx pbs notification endpoint gotify
// ls` — list every configured gotify endpoint
// (GET /config/notifications/endpoints/gotify).
func newNotifEndpointGotifyLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List gotify notification endpoints",
		Long:  "List every configured gotify notification endpoint (GET /config/notifications/endpoints/gotify).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListNotificationsEndpointsGotify(cmd.Context())
			if err != nil {
				return fmt.Errorf("list gotify endpoints: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]notifGotifyEntry, 0, len(items))

			for _, raw := range items {
				var e notifGotifyEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode gotify endpoint entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{"NAME", "SERVER", "DISABLE", "ORIGIN", "COMMENT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Name, e.Server, pbsFormatOptionalBool(e.Disable),
					pbsFormatOptionalString(e.Origin), pbsFormatOptionalString(e.Comment),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newNotifEndpointGotifyShowCmd builds `pmx pbs notification endpoint
// gotify show <name>` — show a single gotify endpoint's configuration
// (GET /config/notifications/endpoints/gotify/{name}). The API never
// returns the token back, so it is never rendered.
func newNotifEndpointGotifyShowCmd() *cobra.Command {
	var withDefaults bool
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a single gotify endpoint's configuration",
		Long: "Show every populated field of a single gotify endpoint (GET " +
			"/config/notifications/endpoints/gotify/{name}). The authentication token " +
			"is write-only and is never returned by the API. The API also omits " +
			"options left at their built-in defaults; pass --defaults to also list " +
			"those, with the value they effectively have.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("endpoint name must not be empty")
			}

			resp, err := deps.PBS.Config.GetNotificationsEndpointsGotify(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("show gotify endpoint %q: %w", name, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode gotify endpoint %q: %w", name, err)
			}

			single := stringMap(fields)
			var raw any = fields
			if withDefaults {
				single, raw = optionschema.MergeDefaults(notifGotifyOptionSchemas, single, raw, optionschema.MergeOpts{})
			}

			res := output.Result{Single: single, Raw: raw}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&withDefaults, "defaults", false,
		"include the unset options with their built-in default values")
	return cmd
}

// notifGotifyFlags collects the gotify endpoint attribute flags shared by
// `add` and `update`. Each field maps directly onto a
// CreateNotificationsEndpointsGotifyParams / UpdateNotificationsEndpointsGotifyParams
// field of the same name.
type notifGotifyFlags struct {
	comment string
	disable bool
	filter  string // create-only (deprecated upstream, kept for parity with the params struct)
	origin  string // create-only
	server  string
	token   string

	// update-only
	del    []string
	digest string
}

// registerNotifGotifyCommon binds the attribute flags accepted by `add`.
func registerNotifGotifyAddFlags(cmd *cobra.Command, gf *notifGotifyFlags) {
	f := cmd.Flags()
	f.StringVar(&gf.server, "server", "", "gotify server URL (required)")
	f.StringVar(&gf.token, "token", "", "gotify authentication token (required)")
	f.StringVar(&gf.comment, "comment", "", "comment")
	f.BoolVar(&gf.disable, "disable", false, "create the endpoint disabled")
	f.StringVar(&gf.filter, "filter", "", "deprecated filter expression")
	f.StringVar(&gf.origin, "origin", "", "origin of the notification configuration entry")
}

// registerNotifGotifyUpdateFlags binds every flag `update` accepts. Unlike
// `add`, the update params struct has no filter or origin field, so those
// flags are not registered here.
func registerNotifGotifyUpdateFlags(cmd *cobra.Command, gf *notifGotifyFlags) {
	f := cmd.Flags()
	f.StringVar(&gf.server, "server", "", "gotify server URL")
	f.StringVar(&gf.token, "token", "", "gotify authentication token")
	f.StringVar(&gf.comment, "comment", "", "comment")
	f.BoolVar(&gf.disable, "disable", false, "disable the endpoint")
	f.StringArrayVar(&gf.del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&gf.digest, "digest", "", "only update if the current config digest matches")
}

// newNotifEndpointGotifyAddCmd builds `pmx pbs notification endpoint gotify
// add <name>` — create a gotify endpoint (POST
// /config/notifications/endpoints/gotify). --server and --token are
// required; every other option is optional and only forwarded when
// explicitly set. The binding is error-only (the API returns null on
// success), so this is a synchronous verb.
func newNotifEndpointGotifyAddCmd() *cobra.Command {
	var gf notifGotifyFlags
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a gotify notification endpoint",
		Long: "Create a new gotify notification endpoint (POST " +
			"/config/notifications/endpoints/gotify). --server and --token are " +
			"required; every other option is optional and only forwarded when " +
			"explicitly set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("endpoint name must not be empty")
			}

			if gf.server == "" {
				return fmt.Errorf("--server is required")
			}

			if gf.token == "" {
				return fmt.Errorf("--token is required")
			}

			params := &pbsconfig.CreateNotificationsEndpointsGotifyParams{
				Name:   name,
				Server: gf.server,
				Token:  gf.token,
			}

			fl := cmd.Flags()
			if fl.Changed("comment") {
				params.Comment = strPtr(gf.comment)
			}

			if fl.Changed("disable") {
				params.Disable = boolPtr(gf.disable)
			}

			if fl.Changed("filter") {
				params.Filter = strPtr(gf.filter)
			}

			if fl.Changed("origin") {
				params.Origin = strPtr(gf.origin)
			}

			err := deps.PBS.Config.CreateNotificationsEndpointsGotify(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create gotify endpoint %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Gotify endpoint %q created.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	registerNotifGotifyAddFlags(cmd, &gf)
	cli.MustMarkRequired(cmd, "server")
	cli.MustMarkRequired(cmd, "token")
	return cmd
}

// newNotifEndpointGotifyUpdateCmd builds `pmx pbs notification endpoint
// gotify update <name>` — update a gotify endpoint (PUT
// /config/notifications/endpoints/gotify/{name}). Only flags explicitly set
// are sent; use --delete to reset properties to their default.
func newNotifEndpointGotifyUpdateCmd() *cobra.Command {
	var gf notifGotifyFlags
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update a gotify notification endpoint",
		Long: "Update an existing gotify notification endpoint (PUT " +
			"/config/notifications/endpoints/gotify/{name}). Only flags explicitly " +
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
				return fmt.Errorf("update gotify endpoint %q: no changes requested: pass at least one flag", name)
			}

			if fl.Changed("delete") {
				for _, key := range gf.del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateNotificationsEndpointsGotifyParams{}

			if fl.Changed("server") {
				params.Server = strPtr(gf.server)
			}

			if fl.Changed("token") {
				params.Token = strPtr(gf.token)
			}

			if fl.Changed("comment") {
				params.Comment = strPtr(gf.comment)
			}

			if fl.Changed("disable") {
				params.Disable = boolPtr(gf.disable)
			}

			if fl.Changed("delete") {
				params.Delete = gf.del
			}

			if fl.Changed("digest") {
				params.Digest = strPtr(gf.digest)
			}

			err := deps.PBS.Config.UpdateNotificationsEndpointsGotify(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("update gotify endpoint %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Gotify endpoint %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	registerNotifGotifyUpdateFlags(cmd, &gf)
	return cmd
}

// newNotifEndpointGotifyDeleteCmd builds `pmx pbs notification endpoint
// gotify delete <name>` — remove a gotify endpoint (DELETE
// /config/notifications/endpoints/gotify/{name}). The binding takes no
// digest parameter — PBS does not support conditional deletes for
// notification endpoints.
func newNotifEndpointGotifyDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a gotify notification endpoint",
		Long: "Remove a gotify notification endpoint (DELETE /config/notifications/endpoints/gotify/{name}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("endpoint name must not be empty")
			}
			if !yes {
				return fmt.Errorf("refusing to delete gotify endpoint %q without confirmation: pass --yes/-y", name)
			}

			err := deps.PBS.Config.DeleteNotificationsEndpointsGotify(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("delete gotify endpoint %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Gotify endpoint %q deleted.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
