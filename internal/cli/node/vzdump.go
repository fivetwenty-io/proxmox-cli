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
	_ = cmd.MarkFlagRequired("volume")
	return cmd
}
