package pbs

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newNotificationCmd builds `pmx pbs notification` — manage Proxmox Backup
// Server notification endpoints (gotify, sendmail, smtp, webhook), matchers
// that route notifications to those endpoints, and the read-only target
// directory that lists every endpoint usable as a matcher target
// (/config/notifications and its endpoints, matchers, and targets children).
func newNotificationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notification",
		Short: "Manage notification endpoints, matchers, and targets",
		Long: "Create, inspect, update, and delete Proxmox Backup Server notification " +
			"endpoints (gotify, sendmail, smtp, webhook) and the matchers that route " +
			"notifications to them, and inspect the combined target directory.",
	}
	cmd.AddCommand(
		newNotifEndpointCmd(),
		newNotifMatcherCmd(),
		newNotifTargetCmd(),
	)
	return cmd
}

// newNotifEndpointCmd builds `pmx pbs notification endpoint` — the parent
// grouping for the four notification endpoint types PBS supports.
//
// GET /config/notifications/endpoints (Config.ListNotificationsEndpoints) is
// not exposed as a command: per the PBS API schema it is a directory-index
// endpoint whose declared return type is "null" — it carries no data of its
// own, only routing to the per-type children below it (gotify, sendmail,
// smtp, webhook). The same applies to GET /config/notifications
// (Config.ListNotifications), the root directory index for the whole
// notification sub-tree, which is why newNotificationCmd itself has no `ls`.
func newNotifEndpointCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "endpoint",
		Short: "Manage notification endpoints",
		Long:  "Create, inspect, update, and delete gotify, sendmail, smtp, and webhook notification endpoints.",
	}
	cmd.AddCommand(
		newNotifEndpointGotifyCmd(),
		newNotifEndpointSendmailCmd(),
		newNotifEndpointSmtpCmd(),
		newNotifEndpointWebhookCmd(),
	)
	return cmd
}

// notifTargetEntry is the decoded shape of one element of
// GET /config/notifications/targets: the combined directory of every entity
// (of any endpoint type) usable as a matcher --target.
type notifTargetEntry struct {
	Comment *string `json:"comment,omitempty"`
	Disable *bool   `json:"disable,omitempty"`
	Name    string  `json:"name"`
	Origin  *string `json:"origin,omitempty"`
	Type    string  `json:"type"`
}

// newNotifTargetCmd builds `pmx pbs notification target` — list every
// notification target across all endpoint types, and send a test
// notification to one.
//
// GET /config/notifications/targets/{name} (Config.GetNotificationsTargets)
// is not exposed as a `show` verb: per the PBS API schema its declared
// return type is "null" (it is a directory index whose only real child is
// its /test action), so there is nothing to render.
func newNotifTargetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "target",
		Short: "List notification targets and send test notifications",
		Long: "List every entity usable as a notification-matcher target across all " +
			"endpoint types, and send a test notification to a named target.",
	}
	cmd.AddCommand(newNotifTargetLsCmd(), newNotifTargetTestCmd())
	return cmd
}

// newNotifTargetLsCmd builds `pmx pbs notification target ls` — list every
// notification target (GET /config/notifications/targets).
func newNotifTargetLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Short:   "List notification targets",
		Long:    "List every entity usable as a notification-matcher target (GET /config/notifications/targets).",
		Example: "  pmx pbs notification target ls",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListNotificationsTargets(cmd.Context())
			if err != nil {
				return fmt.Errorf("list notification targets: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]notifTargetEntry, 0, len(items))

			for _, raw := range items {
				var e notifTargetEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode notification target entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{"NAME", "TYPE", "DISABLE", "ORIGIN", "COMMENT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Name, e.Type, pbsFormatOptionalBool(e.Disable),
					pbsFormatOptionalString(e.Origin), pbsFormatOptionalString(e.Comment),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newNotifTargetTestCmd builds `pmx pbs notification target test <name>` —
// send a test notification to a target (POST
// /config/notifications/targets/{name}/test). The binding is error-only
// (the API returns null on success), so this is a synchronous verb that
// prints a success message rather than waiting on a task.
func newNotifTargetTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "test <name>",
		Short:   "Send a test notification to a target",
		Long:    "Send a test notification to a named target (POST /config/notifications/targets/{name}/test).",
		Example: "  pmx pbs notification target test smtp-main",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("target name must not be empty")
			}

			err := deps.PBS.Config.CreateNotificationsTargetsTest(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("test notification target %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Test notification sent to target %q.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
