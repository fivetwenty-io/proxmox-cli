package pbs

import (
	"fmt"

	"github.com/spf13/cobra"

	pbstape "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/tape"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

// newTapeRestoreCmd builds `pve pbs tape restore` — restore data from a
// media set into one or more datastores (POST /tape/restore). Namespaces
// are automatically created if necessary. --drive, --media-set, and --store
// are required. Runs as an asynchronous task; the command blocks until it
// finishes unless --async is set.
func newTapeRestoreCmd() *cobra.Command {
	var (
		drive            string
		mediaSet         string
		store            string
		namespaces       []string
		notificationMode string
		notifyUser       string
		owner            string
		snapshots        []string
	)
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore data from a tape media set",
		Long: "Restore data from a media set into one or more datastores (POST " +
			"/tape/restore). Namespaces are automatically created if necessary. --drive, " +
			"--media-set, and --store are required. Runs as an asynchronous task; the " +
			"command blocks until it finishes unless --async is set.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			if drive == "" {
				return fmt.Errorf("--drive is required")
			}
			if mediaSet == "" {
				return fmt.Errorf("--media-set is required")
			}
			if store == "" {
				return fmt.Errorf("--store is required")
			}

			params := &pbstape.CreateRestoreParams{Drive: drive, MediaSet: mediaSet, Store: store}
			if fl.Changed("namespaces") {
				params.Namespaces = namespaces
			}
			if fl.Changed("notification-mode") {
				params.NotificationMode = strPtr(notificationMode)
			}
			if fl.Changed("notify-user") {
				params.NotifyUser = strPtr(notifyUser)
			}
			if fl.Changed("owner") {
				params.Owner = strPtr(owner)
			}
			if fl.Changed("snapshots") {
				params.Snapshots = snapshots
			}

			resp, err := deps.PBS.Tape.CreateRestore(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("tape restore of media set %q to datastore %q: %w", mediaSet, store, err)
			}
			if resp == nil {
				return fmt.Errorf("tape restore of media set %q to datastore %q: empty response from server",
					mediaSet, store)
			}

			msg := fmt.Sprintf("Tape restore of media set %q to datastore %q finished.", mediaSet, store)
			return finishAsync(cmd, deps, *resp, msg)
		},
	}
	f := cmd.Flags()
	f.StringVar(&drive, "drive", "", "drive identifier (required)")
	f.StringVar(&mediaSet, "media-set", "", "media set UUID (required)")
	f.StringVar(&store, "store", "", "target datastore mapping(s), comma separated, e.g. 'a=b,e' (required)")
	f.StringArrayVar(&namespaces, "namespaces", nil, "namespace to restore (repeatable)")
	f.StringVar(&notificationMode, "notification-mode", "",
		"notification delivery mode: legacy-sendmail or notification-system")
	f.StringVar(&notifyUser, "notify-user", "", "user ID to notify")
	f.StringVar(&owner, "owner", "", "authentication ID to own restored snapshots")
	f.StringArrayVar(&snapshots, "snapshots", nil, "snapshot to restore (repeatable)")
	cli.MustMarkRequired(cmd, "drive")
	cli.MustMarkRequired(cmd, "media-set")
	cli.MustMarkRequired(cmd, "store")
	return cmd
}
