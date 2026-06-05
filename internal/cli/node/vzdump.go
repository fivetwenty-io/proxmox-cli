package node

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newVzdumpCmd builds `pve node vzdump` — an on-demand backup of one or more
// guests on the resolved node. The operation is asynchronous: by default the
// command blocks until the vzdump task completes, or with --async it prints the
// task UPID and returns immediately.
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
	)
	cmd := &cobra.Command{
		Use:   "vzdump",
		Short: "Create an on-demand backup of one or more guests",
		Long: "Run vzdump on the resolved node to back up the guests selected by --vmid, " +
			"--pool, or --all to the given --storage. The command blocks until the backup " +
			"task finishes unless --async is set.",
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
	return cmd
}
