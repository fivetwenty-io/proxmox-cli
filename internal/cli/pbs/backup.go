package pbs

import (
	"fmt"

	"github.com/spf13/cobra"

	pbstape "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/tape"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// newTapeBackupCmd builds `pmx pbs tape backup` — run a one-shot tape
// backup of a datastore to a media pool via a tape drive (POST /tape/backup).
// --drive, --pool, and --store are required. Runs as an asynchronous task;
// the command blocks until it finishes unless --async is set.
func newTapeBackupCmd() *cobra.Command {
	var (
		drive            string
		pool             string
		store            string
		ejectMedia       bool
		exportMediaSet   bool
		forceMediaSet    bool
		groupFilter      []string
		latestOnly       bool
		maxDepth         int64
		notificationMode string
		notifyUser       string
		ns               string
		workerThreads    int64
	)
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Back up a datastore to tape",
		Long: "Run a one-shot tape backup (POST /tape/backup): copy a datastore's backup " +
			"snapshots to a tape media pool via the given drive. --drive, --pool, and " +
			"--store are required. Runs as an asynchronous task; the command blocks until " +
			"it finishes unless --async is set.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			if drive == "" {
				return fmt.Errorf("--drive is required")
			}
			if pool == "" {
				return fmt.Errorf("--pool is required")
			}
			if store == "" {
				return fmt.Errorf("--store is required")
			}

			params := &pbstape.CreateBackupParams{Drive: drive, Pool: pool, Store: store}
			if fl.Changed("eject-media") {
				params.EjectMedia = boolPtr(ejectMedia)
			}
			if fl.Changed("export-media-set") {
				params.ExportMediaSet = boolPtr(exportMediaSet)
			}
			if fl.Changed("force-media-set") {
				params.ForceMediaSet = boolPtr(forceMediaSet)
			}
			if fl.Changed("group-filter") {
				params.GroupFilter = groupFilter
			}
			if fl.Changed("latest-only") {
				params.LatestOnly = boolPtr(latestOnly)
			}
			if fl.Changed("max-depth") {
				params.MaxDepth = int64Ptr(maxDepth)
			}
			if fl.Changed("notification-mode") {
				params.NotificationMode = strPtr(notificationMode)
			}
			if fl.Changed("notify-user") {
				params.NotifyUser = strPtr(notifyUser)
			}
			if fl.Changed("ns") {
				params.Ns = strPtr(ns)
			}
			if fl.Changed("worker-threads") {
				params.WorkerThreads = int64Ptr(workerThreads)
			}

			resp, err := deps.PBS.Tape.CreateBackup(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("tape backup of datastore %q to drive %q: %w", store, drive, err)
			}
			if resp == nil {
				return fmt.Errorf("tape backup of datastore %q to drive %q: empty response from server", store, drive)
			}

			msg := fmt.Sprintf("Tape backup of datastore %q to drive %q finished.", store, drive)
			return finishAsync(cmd, deps, *resp, msg)
		},
	}
	f := cmd.Flags()
	f.StringVar(&drive, "drive", "", "drive identifier (required)")
	f.StringVar(&pool, "pool", "", "media pool name (required)")
	f.StringVar(&store, "store", "", "datastore name (required)")
	f.BoolVar(&ejectMedia, "eject-media", false, "eject media upon job completion")
	f.BoolVar(&exportMediaSet, "export-media-set", false, "export media set upon job completion")
	f.BoolVar(&forceMediaSet, "force-media-set", false, "ignore the allocation policy and start a new media set")
	f.StringArrayVar(&groupFilter, "group-filter", nil,
		"group filter, e.g. 'type:vm' or 'group:vm/100' (repeatable)")
	f.BoolVar(&latestOnly, "latest-only", false, "back up latest snapshots only")
	f.Int64Var(&maxDepth, "max-depth", 0, "namespace recursion depth (0 = no recursion)")
	f.StringVar(&notificationMode, "notification-mode", "",
		"notification delivery mode: legacy-sendmail or notification-system")
	f.StringVar(&notifyUser, "notify-user", "", "user ID to notify")
	f.StringVar(&ns, "ns", "", "namespace")
	f.Int64Var(&workerThreads, "worker-threads", 0, "number of threads to use for the tape backup job")
	cli.MustMarkRequired(cmd, "drive")
	cli.MustMarkRequired(cmd, "pool")
	cli.MustMarkRequired(cmd, "store")
	return cmd
}
