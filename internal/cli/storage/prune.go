package storage

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// pruneEntry is the subset of a prunebackups element rendered in the table. The
// mark field reports the prune decision for each archive: keep, remove, or
// protected.
type pruneEntry struct {
	Volid string `json:"volid"`
	Ctime int64  `json:"ctime"`
	Mark  string `json:"mark"`
	Type  string `json:"type"`
	Vmid  int64  `json:"vmid"`
}

// keepFlags collects the prune-backups retention options. A value of -1 means the
// option was not set and is omitted from the assembled retention string.
type keepFlags struct {
	last    int64
	hourly  int64
	daily   int64
	weekly  int64
	monthly int64
	yearly  int64
	all     bool
}

// register binds the retention flags onto cmd.
func (kf *keepFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.Int64Var(&kf.last, "keep-last", -1, "keep the N most recent backups")
	fl.Int64Var(&kf.hourly, "keep-hourly", -1, "keep backups for the last N distinct hours")
	fl.Int64Var(&kf.daily, "keep-daily", -1, "keep backups for the last N distinct days")
	fl.Int64Var(&kf.weekly, "keep-weekly", -1, "keep backups for the last N distinct weeks")
	fl.Int64Var(&kf.monthly, "keep-monthly", -1, "keep backups for the last N distinct months")
	fl.Int64Var(&kf.yearly, "keep-yearly", -1, "keep backups for the last N distinct years")
	fl.BoolVar(&kf.all, "keep-all", false, "keep all backups (disables pruning)")
}

// pruneBackupsString assembles the prune-backups property value from the set
// retention flags, returning an empty string when none were provided. The server
// rejects a prune with no retention options, so callers must validate emptiness.
func (kf *keepFlags) pruneBackupsString(cmd *cobra.Command) string {
	fl := cmd.Flags()
	parts := make([]string, 0, 7)
	if kf.all {
		return "keep-all=1"
	}
	add := func(name, key string, v int64) {
		if fl.Changed(name) {
			parts = append(parts, key+"="+strconv.FormatInt(v, 10))
		}
	}
	add("keep-last", "keep-last", kf.last)
	add("keep-hourly", "keep-hourly", kf.hourly)
	add("keep-daily", "keep-daily", kf.daily)
	add("keep-weekly", "keep-weekly", kf.weekly)
	add("keep-monthly", "keep-monthly", kf.monthly)
	add("keep-yearly", "keep-yearly", kf.yearly)
	return strings.Join(parts, ",")
}

// newPruneCmd builds `pmx pve storage prune <storage>` — prune backup archives on a
// storage according to the given retention options. With --dry-run the command
// previews which archives would be removed without deleting anything.
func newPruneCmd() *cobra.Command {
	var (
		kf     keepFlags
		vmid   int64
		typ    string
		dryRun bool
		yes    bool
	)
	cmd := &cobra.Command{
		Use:   "prune <storage>",
		Short: "Prune backup archives on a storage by retention policy",
		Long: "Remove backup archives on the resolved node's storage that fall outside the " +
			"retention window described by the --keep-* options. Use --dry-run to preview the " +
			"prune decisions without deleting anything, and --vmid/--type to limit the scope.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			if deps.Node == "" {
				return fmt.Errorf("no node specified: use --node, set PMX_NODE, or configure a default node")
			}
			storage := args[0]
			fl := cmd.Flags()

			prune := kf.pruneBackupsString(cmd)
			if prune == "" {
				return fmt.Errorf("no retention options given: set at least one --keep-* option (or --keep-all)")
			}

			if dryRun {
				params := &nodes.ListStoragePrunebackupsParams{PruneBackups: &prune}
				if fl.Changed("vmid") {
					params.Vmid = &vmid
				}
				if fl.Changed("type") {
					params.Type = &typ
				}
				resp, err := deps.API.Nodes.ListStoragePrunebackups(cmd.Context(), deps.Node, storage, params)
				if err != nil {
					return fmt.Errorf("preview prune of storage %q on node %q: %w", storage, deps.Node, err)
				}
				return renderPrune(cmd, deps, rawListToEntries(resp))
			}

			if !yes {
				return fmt.Errorf("refusing to prune storage %q without --yes (use --dry-run to preview)", storage)
			}

			params := &nodes.DeleteStoragePrunebackupsParams{PruneBackups: &prune}
			if fl.Changed("vmid") {
				params.Vmid = &vmid
			}
			if fl.Changed("type") {
				params.Type = &typ
			}
			resp, err := deps.API.Nodes.DeleteStoragePrunebackups(cmd.Context(), deps.Node, storage, params)
			if err != nil {
				return fmt.Errorf("prune storage %q on node %q: %w", storage, deps.Node, err)
			}
			return renderPrune(cmd, deps, rawMessageToEntries(resp))
		},
	}

	kf.register(cmd)
	cmd.Flags().Int64Var(&vmid, "vmid", 0, "only prune backups for this guest")
	cmd.Flags().StringVar(&typ, "type", "", "only consider backups of this guest type: qemu|lxc")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview prune decisions without deleting")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm pruning without prompting")
	return cmd
}

// renderPrune renders prune decisions as a table plus lossless raw output.
func renderPrune(cmd *cobra.Command, deps *cli.Deps, entries []pruneEntry) error {
	headers := []string{"VOLID", "MARK", "TYPE", "VMID", "CTIME"}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		vmidCell := ""
		if e.Vmid != 0 {
			vmidCell = strconv.FormatInt(e.Vmid, 10)
		}
		ctimeCell := ""
		if e.Ctime != 0 {
			ctimeCell = strconv.FormatInt(e.Ctime, 10)
		}
		rows = append(rows, []string{e.Volid, e.Mark, e.Type, vmidCell, ctimeCell})
	}
	return deps.Out.Render(cmd.OutOrStdout(),
		output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
}

// rawListToEntries decodes a prunebackups list response into typed entries.
func rawListToEntries(resp *nodes.ListStoragePrunebackupsResponse) []pruneEntry {
	if resp == nil {
		return []pruneEntry{}
	}
	entries := make([]pruneEntry, 0, len(*resp))
	for _, raw := range *resp {
		var e pruneEntry
		if err := json.Unmarshal(raw, &e); err == nil {
			entries = append(entries, e)
		}
	}
	return entries
}

// rawMessageToEntries decodes the prune (DELETE) response, which is a raw JSON
// array of the same prune-decision elements, into typed entries.
func rawMessageToEntries(resp *nodes.DeleteStoragePrunebackupsResponse) []pruneEntry {
	entries := []pruneEntry{}
	if resp == nil || len(*resp) == 0 {
		return entries
	}
	if err := json.Unmarshal(*resp, &entries); err != nil {
		return []pruneEntry{}
	}
	return entries
}
