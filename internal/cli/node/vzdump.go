package node

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newVzdumpCmd builds `pve node vzdump` — an on-demand backup of one or more
// guests on the resolved node, plus read-only sub-commands for defaults and
// config extraction. The command itself backs up guests; sub-commands provide
// additional functionality without conflicting.
func newVzdumpCmd() *cobra.Command {
	var (
		vmid          string
		storage       string
		mode          string
		compress      string
		pool          string
		all           bool
		protected     bool
		remove        bool
		notesTemplate string
		mailto        string

		bwlimit                int64
		ionice                 int64
		fleecing               string
		lockwait               int64
		stopwait               int64
		tmpdir                 string
		dumpdir                string
		script                 string
		stdexcludes            bool
		stdout                 bool
		exclude                string
		excludePath            []string
		zstd                   int64
		pigz                   int64
		notificationMode       string
		pbsChangeDetectionMode string
		performance            string
		jobID                  string
		pruneBackups           string
		stop                   bool
		quiet                  bool
	)
	cmd := &cobra.Command{
		Use:   "vzdump",
		Short: "Create an on-demand backup of one or more guests",
		Long: "Run vzdump on the resolved node to back up the guests selected by --vmid, " +
			"--pool, or --all to the given --storage. The command blocks until the backup " +
			"task finishes unless --async is set. Use the sub-commands defaults and " +
			"extract-config to inspect vzdump configuration.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PVE_NODE, or configure a default node")
			}

			params := &nodes.CreateVzdumpParams{}
			fl := cmd.Flags()
			if fl.Changed("vmid") {
				params.Vmid = &vmid
			}
			if fl.Changed("storage") {
				params.Storage = &storage
			}
			if fl.Changed("mode") {
				params.Mode = &mode
			}
			if fl.Changed("compress") {
				params.Compress = &compress
			}
			if fl.Changed("pool") {
				params.Pool = &pool
			}
			if fl.Changed("all") {
				params.All = &all
			}
			if fl.Changed("protected") {
				params.Protected = &protected
			}
			if fl.Changed("remove") {
				params.Remove = &remove
			}
			if fl.Changed("notes-template") {
				params.NotesTemplate = &notesTemplate
			}
			if fl.Changed("mailto") {
				params.Mailto = &mailto
			}
			if fl.Changed("bwlimit") {
				params.Bwlimit = &bwlimit
			}
			if fl.Changed("ionice") {
				params.Ionice = &ionice
			}
			if fl.Changed("fleecing") {
				params.Fleecing = &fleecing
			}
			if fl.Changed("lockwait") {
				params.Lockwait = &lockwait
			}
			if fl.Changed("stopwait") {
				params.Stopwait = &stopwait
			}
			if fl.Changed("tmpdir") {
				params.Tmpdir = &tmpdir
			}
			if fl.Changed("dumpdir") {
				params.Dumpdir = &dumpdir
			}
			if fl.Changed("script") {
				params.Script = &script
			}
			if fl.Changed("stdexcludes") {
				params.Stdexcludes = &stdexcludes
			}
			if fl.Changed("stdout") {
				params.Stdout = &stdout
			}
			if fl.Changed("exclude") {
				params.Exclude = &exclude
			}
			if fl.Changed("exclude-path") {
				params.ExcludePath = excludePath
			}
			if fl.Changed("zstd") {
				params.Zstd = &zstd
			}
			if fl.Changed("pigz") {
				params.Pigz = &pigz
			}
			if fl.Changed("notification-mode") {
				params.NotificationMode = &notificationMode
			}
			if fl.Changed("pbs-change-detection-mode") {
				params.PbsChangeDetectionMode = &pbsChangeDetectionMode
			}
			if fl.Changed("performance") {
				params.Performance = &performance
			}
			if fl.Changed("job-id") {
				params.JobId = &jobID
			}
			if fl.Changed("prune-backups") {
				params.PruneBackups = &pruneBackups
			}
			if fl.Changed("stop") {
				params.Stop = &stop
			}
			if fl.Changed("quiet") {
				params.Quiet = &quiet
			}

			resp, err := deps.API.Nodes.CreateVzdump(cmd.Context(), deps.Node, params)
			if err != nil {
				return fmt.Errorf("start vzdump on node %q: %w", deps.Node, err)
			}

			var raw json.RawMessage
			if resp != nil {
				raw = json.RawMessage(*resp)
			}
			upid, err := apiclient.UPIDFromRaw(raw)
			if err != nil {
				return fmt.Errorf("start vzdump on node %q: %w", deps.Node, err)
			}

			if deps.Async {
				return deps.Out.Render(cmd.OutOrStdout(),
					output.Result{
						Single:  map[string]string{"upid": upid},
						Raw:     map[string]string{"upid": upid},
						Message: upid,
					}, deps.Format)
			}

			if err := apiclient.WaitTask(cmd.Context(), deps.API, upid, nil); err != nil {
				return fmt.Errorf("vzdump on node %q: %w", deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Backup completed on node %q.", deps.Node)}, deps.Format)
		},
	}

	fl := cmd.Flags()
	fl.StringVar(&vmid, "vmid", "", "comma-separated guest IDs to back up")
	fl.StringVar(&storage, "storage", "", "store the resulting backup on this storage")
	fl.StringVar(&mode, "mode", "", "backup mode: snapshot|suspend|stop")
	fl.StringVar(&compress, "compress", "", "compression: 0|1|gzip|lzo|zstd")
	fl.StringVar(&pool, "pool", "", "back up all guests in this pool")
	fl.BoolVar(&all, "all", false, "back up all guests on the node")
	fl.BoolVar(&protected, "protected", false, "mark the resulting backup as protected")
	fl.BoolVar(&remove, "remove", false, "prune older backups according to the storage retention settings")
	fl.StringVar(&notesTemplate, "notes-template", "", "template for backup notes (supports {{guestname}}, {{node}}, {{vmid}})")
	fl.StringVar(&mailto, "mailto", "", "comma-separated email addresses for notifications")
	fl.Int64Var(&bwlimit, "bwlimit", 0, "I/O rate limit in KiB/s (0 for unlimited)")
	fl.Int64Var(&ionice, "ionice", 7, "ionice priority for the backup process (0-8)")
	fl.StringVar(&fleecing, "fleecing", "", "backup fleecing options, for example enabled=1,storage=local-lvm")
	fl.Int64Var(&lockwait, "lockwait", 0, "maximal time to wait for the global lock, in minutes")
	fl.Int64Var(&stopwait, "stopwait", 0, "maximal time to wait until a guest is stopped, in minutes")
	fl.StringVar(&tmpdir, "tmpdir", "", "store temporary files in this directory")
	fl.StringVar(&dumpdir, "dumpdir", "", "store the resulting backup in this directory instead of a storage")
	fl.StringVar(&script, "script", "", "run this hook script during the backup")
	fl.BoolVar(&stdexcludes, "stdexcludes", true, "exclude default temporary and log files")
	fl.BoolVar(&stdout, "stdout", false, "write the backup to stdout instead of a file")
	fl.StringVar(&exclude, "exclude", "", "comma-separated guest IDs to exclude (only with --all)")
	fl.StringArrayVar(&excludePath, "exclude-path", nil,
		"path or glob to exclude from the backup; repeat for multiple paths")
	fl.Int64Var(&zstd, "zstd", 1, "number of zstd threads (0 for all available cores)")
	fl.Int64Var(&pigz, "pigz", 0, "number of pigz threads when using gzip (0 to use a single thread)")
	fl.StringVar(&notificationMode, "notification-mode", "",
		"how to send notifications: auto, legacy-sendmail, or notification-system")
	fl.StringVar(&pbsChangeDetectionMode, "pbs-change-detection-mode", "",
		"PBS change detection mode: legacy, data, or metadata")
	fl.StringVar(&performance, "performance", "",
		"performance tuning, for example max-workers=4,pbs-entries-max=1048576")
	fl.StringVar(&jobID, "job-id", "", "job ID to attribute this backup to (informational)")
	fl.StringVar(&pruneBackups, "prune-backups", "",
		"retention policy, for example keep-last=3,keep-daily=7 (implies --remove)")
	fl.BoolVar(&stop, "stop", false, "stop any running backup job before starting this one")
	fl.BoolVar(&quiet, "quiet", false, "run quietly, only logging warnings and errors")

	cmd.AddCommand(
		newVzdumpDefaultsCmd(),
		newVzdumpExtractConfigCmd(),
	)
	return cmd
}

// newVzdumpDefaultsCmd builds `pve node vzdump defaults` — shows the effective
// backup defaults configured in the datacenter configuration for the resolved
// node.
func newVzdumpDefaultsCmd() *cobra.Command {
	var storage string
	cmd := &cobra.Command{
		Use:   "defaults",
		Short: "Show effective vzdump backup defaults for the node",
		Long: "Show the effective vzdump backup defaults for the resolved node as derived " +
			"from the datacenter configuration. Optionally scope to a specific storage.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			// The typed client method cannot decode this endpoint: PVE returns
			// nested objects (e.g. `fleecing`) where the generated struct expects
			// scalar strings. Fetch the raw object and render every key generically.
			var params map[string]any
			if cmd.Flags().Changed("storage") {
				params = map[string]any{"storage": storage}
			}
			path := fmt.Sprintf("/nodes/%s/vzdump/defaults", deps.Node)
			data, err := deps.API.Raw.GetCtx(cmd.Context(), path, params)
			if err != nil {
				return fmt.Errorf("get vzdump defaults on node %q: %w", deps.Node, err)
			}
			single, raw, err := objectToSingle(data)
			if err != nil {
				return fmt.Errorf("get vzdump defaults on node %q: %w", deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&storage, "storage", "", "scope defaults to this storage identifier")
	return cmd
}

// newVzdumpExtractConfigCmd builds `pve node vzdump extract-config` — reads the
// guest configuration embedded in a backup archive. The --volume flag is required.
func newVzdumpExtractConfigCmd() *cobra.Command {
	var volume string
	cmd := &cobra.Command{
		Use:   "extract-config",
		Short: "Extract the guest configuration from a backup archive",
		Long: "Read the guest configuration stored inside a backup archive volume. The " +
			"--volume flag is required and must be a valid storage volume identifier.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if err := requireNode(deps); err != nil {
				return err
			}
			resp, err := deps.API.Nodes.ListVzdumpExtractconfig(cmd.Context(), deps.Node,
				&nodes.ListVzdumpExtractconfigParams{Volume: volume})
			if err != nil {
				return fmt.Errorf("extract config from volume %q on node %q: %w", volume, deps.Node, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: string(rawOrNil(resp)), Raw: resp}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&volume, "volume", "", "storage volume identifier of the backup archive (required)")
	cli.MustMarkRequired(cmd, "volume")
	return cmd
}
