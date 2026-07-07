package pbs

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"

	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
)

// newNotifMatcherCmd builds `pve pbs notification matcher` — manage
// notification matchers that route notifications to endpoints
// (/config/notifications/matchers CRUD), and inspect the read-only
// metadata-field directories matchers can filter on.
func newNotifMatcherCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "matcher",
		Short: "Manage notification matchers",
		Long: "List, inspect, create, update, and delete notification matchers, and " +
			"inspect the metadata fields and field values matchers can filter on.",
	}
	cmd.AddCommand(
		newNotifMatcherLsCmd(),
		newNotifMatcherShowCmd(),
		newNotifMatcherAddCmd(),
		newNotifMatcherUpdateCmd(),
		newNotifMatcherDeleteCmd(),
		newNotifMatcherFieldsCmd(),
		newNotifMatcherFieldValuesCmd(),
	)
	return cmd
}

// notifMatcherEntry is the decoded shape of one element of
// GET /config/notifications/matchers, and the shape `show` renders.
type notifMatcherEntry struct {
	Comment       *string  `json:"comment,omitempty"`
	Disable       *bool    `json:"disable,omitempty"`
	InvertMatch   *bool    `json:"invert-match,omitempty"`
	MatchCalendar []string `json:"match-calendar,omitempty"`
	MatchField    []string `json:"match-field,omitempty"`
	MatchSeverity []string `json:"match-severity,omitempty"`
	Mode          *string  `json:"mode,omitempty"`
	Name          string   `json:"name"`
	Origin        *string  `json:"origin,omitempty"`
	Target        []string `json:"target,omitempty"`
}

// newNotifMatcherLsCmd builds `pve pbs notification matcher ls` — list
// every configured matcher (GET /config/notifications/matchers).
func newNotifMatcherLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List notification matchers",
		Long:  "List every configured notification matcher (GET /config/notifications/matchers).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListNotificationsMatchers(cmd.Context())
			if err != nil {
				return fmt.Errorf("list notification matchers: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]notifMatcherEntry, 0, len(items))

			for _, raw := range items {
				var e notifMatcherEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode notification matcher entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{"NAME", "MODE", "TARGET", "MATCH-SEVERITY", "MATCH-FIELD", "MATCH-CALENDAR", "INVERT-MATCH", "DISABLE"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Name, pbsFormatOptionalString(e.Mode), trafficJoin(e.Target),
					trafficJoin(e.MatchSeverity), trafficJoin(e.MatchField), trafficJoin(e.MatchCalendar),
					pbsFormatOptionalBool(e.InvertMatch), pbsFormatOptionalBool(e.Disable),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newNotifMatcherShowCmd builds `pve pbs notification matcher show <name>`
// — show a single matcher's configuration
// (GET /config/notifications/matchers/{name}).
func newNotifMatcherShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a single matcher's configuration",
		Long:  "Show every populated field of a single notification matcher (GET /config/notifications/matchers/{name}).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("matcher name must not be empty")
			}

			resp, err := deps.PBS.Config.GetNotificationsMatchers(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("show notification matcher %q: %w", name, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode notification matcher %q: %w", name, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// notifMatcherFlags collects the matcher attribute flags shared by `add` and
// `update`. Each field maps directly onto a CreateNotificationsMatchersParams
// / UpdateNotificationsMatchersParams field of the same name.
type notifMatcherFlags struct {
	comment       string
	disable       bool
	invertMatch   bool
	matchCalendar []string
	matchField    []string
	matchSeverity []string
	mode          string
	origin        string // create-only
	target        []string

	// update-only
	del    []string
	digest string
}

// registerNotifMatcherAddFlags binds the attribute flags accepted by `add`.
func registerNotifMatcherAddFlags(cmd *cobra.Command, mf *notifMatcherFlags) {
	f := cmd.Flags()
	f.StringVar(&mf.comment, "comment", "", "comment")
	f.BoolVar(&mf.disable, "disable", false, "create the matcher disabled")
	f.BoolVar(&mf.invertMatch, "invert-match", false, "invert the match of the whole filter")
	f.StringArrayVar(&mf.matchCalendar, "match-calendar", nil, "calendar-event match expression (repeatable)")
	f.StringArrayVar(&mf.matchField, "match-field", nil, "metadata-field match expression (repeatable)")
	f.StringArrayVar(&mf.matchSeverity, "match-severity", nil, "severity-level match expression (repeatable)")
	f.StringVar(&mf.mode, "mode", "", "result-combination mode: all|any (default: all)")
	f.StringVar(&mf.origin, "origin", "", "origin of the notification configuration entry")
	f.StringArrayVar(&mf.target, "target", nil, "target to notify on match (repeatable)")
}

// registerNotifMatcherUpdateFlags binds every flag `update` accepts. Unlike
// `add`, the update params struct has no origin field, so that flag is not
// registered here.
func registerNotifMatcherUpdateFlags(cmd *cobra.Command, mf *notifMatcherFlags) {
	f := cmd.Flags()
	f.StringVar(&mf.comment, "comment", "", "comment")
	f.BoolVar(&mf.disable, "disable", false, "disable the matcher")
	f.BoolVar(&mf.invertMatch, "invert-match", false, "invert the match of the whole filter")
	f.StringArrayVar(&mf.matchCalendar, "match-calendar", nil, "calendar-event match expression (repeatable)")
	f.StringArrayVar(&mf.matchField, "match-field", nil, "metadata-field match expression (repeatable)")
	f.StringArrayVar(&mf.matchSeverity, "match-severity", nil, "severity-level match expression (repeatable)")
	f.StringVar(&mf.mode, "mode", "", "result-combination mode: all|any")
	f.StringArrayVar(&mf.target, "target", nil, "target to notify on match (repeatable)")
	f.StringArrayVar(&mf.del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&mf.digest, "digest", "", "only update if the current config digest matches")
}

// newNotifMatcherAddCmd builds `pve pbs notification matcher add <name>` —
// create a notification matcher (POST /config/notifications/matchers).
// Every option is optional and only forwarded when explicitly set. The
// binding is error-only (the API returns null on success), so this is a
// synchronous verb.
func newNotifMatcherAddCmd() *cobra.Command {
	var mf notifMatcherFlags
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a notification matcher",
		Long: "Create a new notification matcher (POST /config/notifications/matchers). " +
			"Every option is optional and only forwarded when explicitly set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("matcher name must not be empty")
			}

			params := &pbsconfig.CreateNotificationsMatchersParams{Name: name}

			fl := cmd.Flags()
			if fl.Changed("comment") {
				params.Comment = strPtr(mf.comment)
			}

			if fl.Changed("disable") {
				params.Disable = boolPtr(mf.disable)
			}

			if fl.Changed("invert-match") {
				params.InvertMatch = boolPtr(mf.invertMatch)
			}

			if fl.Changed("match-calendar") {
				params.MatchCalendar = mf.matchCalendar
			}

			if fl.Changed("match-field") {
				params.MatchField = mf.matchField
			}

			if fl.Changed("match-severity") {
				params.MatchSeverity = mf.matchSeverity
			}

			if fl.Changed("mode") {
				params.Mode = strPtr(mf.mode)
			}

			if fl.Changed("origin") {
				params.Origin = strPtr(mf.origin)
			}

			if fl.Changed("target") {
				params.Target = mf.target
			}

			err := deps.PBS.Config.CreateNotificationsMatchers(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create notification matcher %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Notification matcher %q created.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	registerNotifMatcherAddFlags(cmd, &mf)
	return cmd
}

// newNotifMatcherUpdateCmd builds `pve pbs notification matcher update
// <name>` — update a notification matcher (PUT
// /config/notifications/matchers/{name}). Only flags explicitly set are
// sent; use --delete to reset properties to their default.
func newNotifMatcherUpdateCmd() *cobra.Command {
	var mf notifMatcherFlags
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update a notification matcher",
		Long: "Update an existing notification matcher (PUT " +
			"/config/notifications/matchers/{name}). Only flags explicitly set " +
			"are sent; use --delete to reset properties to their default instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("matcher name must not be empty")
			}

			fl := cmd.Flags()
			if !anyFlagChanged(fl) {
				return fmt.Errorf("update notification matcher %q: no changes given: pass at least one flag", name)
			}

			if fl.Changed("delete") {
				for _, key := range mf.del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateNotificationsMatchersParams{}

			if fl.Changed("comment") {
				params.Comment = strPtr(mf.comment)
			}

			if fl.Changed("disable") {
				params.Disable = boolPtr(mf.disable)
			}

			if fl.Changed("invert-match") {
				params.InvertMatch = boolPtr(mf.invertMatch)
			}

			if fl.Changed("match-calendar") {
				params.MatchCalendar = mf.matchCalendar
			}

			if fl.Changed("match-field") {
				params.MatchField = mf.matchField
			}

			if fl.Changed("match-severity") {
				params.MatchSeverity = mf.matchSeverity
			}

			if fl.Changed("mode") {
				params.Mode = strPtr(mf.mode)
			}

			if fl.Changed("target") {
				params.Target = mf.target
			}

			if fl.Changed("delete") {
				params.Delete = mf.del
			}

			if fl.Changed("digest") {
				params.Digest = strPtr(mf.digest)
			}

			err := deps.PBS.Config.UpdateNotificationsMatchers(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("update notification matcher %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Notification matcher %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	registerNotifMatcherUpdateFlags(cmd, &mf)
	return cmd
}

// newNotifMatcherDeleteCmd builds `pve pbs notification matcher delete
// <name>` — remove a notification matcher (DELETE
// /config/notifications/matchers/{name}). The binding takes no digest
// parameter — PBS does not support conditional deletes for notification
// matchers.
func newNotifMatcherDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a notification matcher",
		Long:  "Remove a notification matcher (DELETE /config/notifications/matchers/{name}).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("matcher name must not be empty")
			}

			err := deps.PBS.Config.DeleteNotificationsMatchers(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("delete notification matcher %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Notification matcher %q deleted.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// notifMatcherFieldEntry is the decoded shape of one element of
// GET /config/notifications/matcher-fields: a metadata field name a
// matcher's --match-field can reference.
type notifMatcherFieldEntry struct {
	Name string `json:"name"`
}

// newNotifMatcherFieldsCmd builds `pve pbs notification matcher fields` —
// inspect the read-only directory of matchable metadata field names.
func newNotifMatcherFieldsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fields",
		Short: "Inspect matchable metadata field names",
		Long:  "List every metadata field name a matcher's --match-field can reference.",
	}
	cmd.AddCommand(newNotifMatcherFieldsLsCmd())
	return cmd
}

// newNotifMatcherFieldsLsCmd builds `pve pbs notification matcher fields
// ls` — list every known matchable metadata field name
// (GET /config/notifications/matcher-fields).
func newNotifMatcherFieldsLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List matchable metadata field names",
		Long:  "List every known matchable metadata field name (GET /config/notifications/matcher-fields).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListNotificationsMatcherFields(cmd.Context())
			if err != nil {
				return fmt.Errorf("list notification matcher fields: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]notifMatcherFieldEntry, 0, len(items))

			for _, raw := range items {
				var e notifMatcherFieldEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode notification matcher field entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{"NAME"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{e.Name})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// notifMatcherFieldValueEntry is the decoded shape of one element of
// GET /config/notifications/matcher-field-values: a known value for one
// matchable metadata field.
type notifMatcherFieldValueEntry struct {
	Comment *string `json:"comment,omitempty"`
	Field   string  `json:"field"`
	Value   string  `json:"value"`
}

// newNotifMatcherFieldValuesCmd builds `pve pbs notification matcher
// field-values` — inspect the read-only directory of known metadata field
// values.
func newNotifMatcherFieldValuesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "field-values",
		Short: "Inspect known metadata field values",
		Long:  "List every known value for a matchable metadata field.",
	}
	cmd.AddCommand(newNotifMatcherFieldValuesLsCmd())
	return cmd
}

// newNotifMatcherFieldValuesLsCmd builds `pve pbs notification matcher
// field-values ls` — list every known metadata field value
// (GET /config/notifications/matcher-field-values).
func newNotifMatcherFieldValuesLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List known metadata field values",
		Long:  "List every known matchable metadata field value (GET /config/notifications/matcher-field-values).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListNotificationsMatcherFieldValues(cmd.Context())
			if err != nil {
				return fmt.Errorf("list notification matcher field values: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]notifMatcherFieldValueEntry, 0, len(items))

			for _, raw := range items {
				var e notifMatcherFieldValueEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode notification matcher field value entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool {
				if entries[i].Field != entries[j].Field {
					return entries[i].Field < entries[j].Field
				}
				return entries[i].Value < entries[j].Value
			})

			headers := []string{"FIELD", "VALUE", "COMMENT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{e.Field, e.Value, pbsFormatOptionalString(e.Comment)})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
