package cluster

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newBackupCmd builds the `pve cluster backup` sub-tree: scheduled vzdump backup
// job management plus a cluster-wide coverage audit.
func newBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Manage cluster-wide backup schedules",
		Long: "List, create, inspect, update, and delete scheduled vzdump backup jobs, " +
			"and audit which guests are covered by a backup schedule.",
	}
	cmd.AddCommand(
		newBackupListCmd(),
		newBackupGetCmd(),
		newBackupCreateCmd(),
		newBackupSetCmd(),
		newBackupDeleteCmd(),
		newBackupInfoCmd(),
		newBackupIncludedVolumesCmd(),
	)
	return cmd
}

// newBackupIncludedVolumesCmd builds `pve cluster backup included-volumes <id>`.
// It shows which disks and volumes a backup job covers, which is essential for
// auditing backup scope before a maintenance window.
func newBackupIncludedVolumesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "included-volumes <id>",
		Short: "Show the volumes included in a backup job",
		Long: "List every disk and volume that a scheduled backup job covers. " +
			"Useful for auditing backup scope before a maintenance window.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			id := args[0]
			resp, err := deps.API.Cluster.ListBackupIncludedVolumes(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("list backup included volumes %q: %w", id, err)
			}
			// The response has a top-level Children field containing the tree of
			// included volumes. Render it as a dynamic table so every field surfaces.
			entries := make([]map[string]any, 0)
			if resp != nil {
				for _, raw := range resp.Children {
					var m map[string]any
					if err := json.Unmarshal(raw, &m); err != nil {
						return fmt.Errorf("decode included volume entry: %w", err)
					}
					entries = append(entries, m)
				}
			}
			headers, rows := dynamicTable(entries)
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
}

// newBackupListCmd builds `pve cluster backup list`.
func newBackupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List scheduled backup jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)

			resp, err := deps.API.Cluster.ListBackup(cmd.Context())
			if err != nil {
				return fmt.Errorf("list backup jobs: %w", err)
			}

			headers := []string{"ID", "SCHEDULE", "STORAGE", "MODE", "ENABLED", "VMID", "COMMENT"}
			rows := make([][]string, 0)
			raw := make([]map[string]any, 0)
			if resp != nil {
				for _, rawJob := range *resp {
					var m map[string]any
					if err := json.Unmarshal(rawJob, &m); err != nil {
						return fmt.Errorf("decode backup job: %w", err)
					}
					raw = append(raw, m)
					rows = append(rows, []string{
						anyCell(m["id"]),
						anyCell(m["schedule"]),
						anyCell(m["storage"]),
						anyCell(m["mode"]),
						enabledCell(m),
						vmidCell(m),
						anyCell(m["comment"]),
					})
				}
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: raw}, deps.Format)
		},
	}
}

// newBackupGetCmd builds `pve cluster backup get <id>`.
func newBackupGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show a single backup job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			id := args[0]

			resp, err := deps.API.Cluster.GetBackup(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("get backup job %q: %w", id, err)
			}

			single, raw, err := objectToSingle(resp)
			if err != nil {
				return fmt.Errorf("decode backup job %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: raw}, deps.Format)
		},
	}
}

// backupFlags collects the mutable backup-job attributes shared by create and set.
type backupFlags struct {
	schedule      string
	storage       string
	mode          string
	vmid          string
	all           bool
	pool          string
	comment       string
	enabled       bool
	compress      string
	mailto        string
	notesTemplate string
	runNode       string
}

// register binds the backup-job attribute flags shared by create and set onto cmd.
func (bf *backupFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&bf.schedule, "schedule", "", "backup schedule as a systemd calendar event, e.g. \"02:30\" or \"sat 03:00\"")
	fl.StringVar(&bf.storage, "storage", "", "store the resulting backups on this storage")
	fl.StringVar(&bf.mode, "mode", "", "backup mode: snapshot|suspend|stop")
	fl.StringVar(&bf.vmid, "vmid", "", "comma-separated guest IDs to back up")
	fl.BoolVar(&bf.all, "all", false, "back up all guests on the cluster")
	fl.StringVar(&bf.pool, "pool", "", "back up all guests in this pool")
	fl.StringVar(&bf.comment, "comment", "", "description for the job")
	fl.BoolVar(&bf.enabled, "enabled", true, "enable the job")
	fl.StringVar(&bf.compress, "compress", "", "compression: 0|1|gzip|lzo|zstd")
	fl.StringVar(&bf.mailto, "mailto", "", "comma-separated email addresses for notifications")
	fl.StringVar(&bf.notesTemplate, "notes-template", "", "template for backup notes (supports {{guestname}}, {{node}}, {{vmid}})")
	fl.StringVar(&bf.runNode, "run-node", "", "only run the job when executed on this node")
}

// applyCreate copies the changed flag values onto a CreateBackupParams.
func (bf *backupFlags) applyCreate(cmd *cobra.Command, p *pvecluster.CreateBackupParams) {
	fl := cmd.Flags()
	if fl.Changed("schedule") {
		p.Schedule = &bf.schedule
	}
	if fl.Changed("storage") {
		p.Storage = &bf.storage
	}
	if fl.Changed("mode") {
		p.Mode = &bf.mode
	}
	if fl.Changed("vmid") {
		p.Vmid = &bf.vmid
	}
	if fl.Changed("all") {
		p.All = &bf.all
	}
	if fl.Changed("pool") {
		p.Pool = &bf.pool
	}
	if fl.Changed("comment") {
		p.Comment = &bf.comment
	}
	if fl.Changed("enabled") {
		p.Enabled = &bf.enabled
	}
	if fl.Changed("compress") {
		p.Compress = &bf.compress
	}
	if fl.Changed("mailto") {
		p.Mailto = &bf.mailto
	}
	if fl.Changed("notes-template") {
		p.NotesTemplate = &bf.notesTemplate
	}
	if fl.Changed("run-node") {
		p.Node = &bf.runNode
	}
}

// applySet copies the changed flag values onto an UpdateBackupParams.
func (bf *backupFlags) applySet(cmd *cobra.Command, p *pvecluster.UpdateBackupParams) {
	fl := cmd.Flags()
	if fl.Changed("schedule") {
		p.Schedule = &bf.schedule
	}
	if fl.Changed("storage") {
		p.Storage = &bf.storage
	}
	if fl.Changed("mode") {
		p.Mode = &bf.mode
	}
	if fl.Changed("vmid") {
		p.Vmid = &bf.vmid
	}
	if fl.Changed("all") {
		p.All = &bf.all
	}
	if fl.Changed("pool") {
		p.Pool = &bf.pool
	}
	if fl.Changed("comment") {
		p.Comment = &bf.comment
	}
	if fl.Changed("enabled") {
		p.Enabled = &bf.enabled
	}
	if fl.Changed("compress") {
		p.Compress = &bf.compress
	}
	if fl.Changed("mailto") {
		p.Mailto = &bf.mailto
	}
	if fl.Changed("notes-template") {
		p.NotesTemplate = &bf.notesTemplate
	}
	if fl.Changed("run-node") {
		p.Node = &bf.runNode
	}
}

// newBackupCreateCmd builds `pve cluster backup create`.
func newBackupCreateCmd() *cobra.Command {
	var (
		id string
		bf backupFlags
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a scheduled backup job",
		Long: "Create a new scheduled vzdump backup job. Specify the guests to back up with " +
			"--vmid, --pool, or --all, and when to run with --schedule. If --id is omitted, " +
			"the job ID is generated by the server.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)

			params := &pvecluster.CreateBackupParams{}
			if cmd.Flags().Changed("id") {
				params.Id = &id
			}
			bf.applyCreate(cmd, params)

			if err := deps.API.Cluster.CreateBackup(cmd.Context(), params); err != nil {
				return fmt.Errorf("create backup job: %w", err)
			}

			msg := "Backup job created."
			if id != "" {
				msg = fmt.Sprintf("Backup job %q created.", id)
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "job ID (generated by the server if omitted)")
	bf.register(cmd)
	return cmd
}

// newBackupSetCmd builds `pve cluster backup set <id>`.
func newBackupSetCmd() *cobra.Command {
	var (
		bf     backupFlags
		delete string
	)
	cmd := &cobra.Command{
		Use:   "set <id>",
		Short: "Update a scheduled backup job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			id := args[0]

			params := &pvecluster.UpdateBackupParams{}
			bf.applySet(cmd, params)
			if cmd.Flags().Changed("delete") {
				params.Delete = &delete
			}

			if err := deps.API.Cluster.UpdateBackup(cmd.Context(), id, params); err != nil {
				return fmt.Errorf("update backup job %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Backup job %q updated.", id)}, deps.Format)
		},
	}
	bf.register(cmd)
	cmd.Flags().StringVar(&delete, "delete", "", "comma-separated list of settings to reset to their default")
	return cmd
}

// newBackupDeleteCmd builds `pve cluster backup delete <id>`.
func newBackupDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a scheduled backup job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := resolveDeps(cmd)
			id := args[0]
			if !yes {
				return fmt.Errorf("refusing to delete backup job %q without --yes", id)
			}
			if err := deps.API.Cluster.DeleteBackup(cmd.Context(), id); err != nil {
				return fmt.Errorf("delete backup job %q: %w", id, err)
			}
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Backup job %q deleted.", id)}, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm deletion without prompting")
	return cmd
}

// newBackupInfoCmd builds `pve cluster backup info` — a coverage audit listing
// every guest and whether a backup schedule includes it.
func newBackupInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Audit which guests are covered by a backup schedule",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := resolveDeps(cmd)

			resp, err := deps.API.Cluster.ListBackupInfo(cmd.Context())
			if err != nil {
				return fmt.Errorf("list backup coverage: %w", err)
			}

			entries := make([]map[string]any, 0)
			if resp != nil {
				for _, rawEntry := range *resp {
					var m map[string]any
					if err := json.Unmarshal(rawEntry, &m); err != nil {
						return fmt.Errorf("decode backup coverage entry: %w", err)
					}
					entries = append(entries, m)
				}
			}

			headers, rows := dynamicTable(entries)
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Headers: headers, Rows: rows, Raw: entries}, deps.Format)
		},
	}
}

// --- helpers ---

// anyCell renders an arbitrary decoded JSON scalar as a table cell.
func anyCell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "yes"
		}
		return "no"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}

// enabledCell renders the enabled flag, treating an absent flag as enabled (the
// PVE default for a freshly created job).
func enabledCell(m map[string]any) string {
	v, ok := m["enabled"]
	if !ok {
		return "yes"
	}
	if f, isNum := v.(float64); isNum {
		if f == 0 {
			return "no"
		}
		return "yes"
	}
	return anyCell(v)
}

// vmidCell renders the guest selection: the explicit VMID list, or "all" when the
// job has the all flag set, or the pool when pool-scoped.
func vmidCell(m map[string]any) string {
	if v := anyCell(m["vmid"]); v != "" {
		return v
	}
	if f, ok := m["all"].(float64); ok && f != 0 {
		return "all"
	}
	if p := anyCell(m["pool"]); p != "" {
		return "pool:" + p
	}
	return ""
}

// objectToSingle marshals a typed response object into a key/value Single map and
// a generic Raw object, so structured output preserves every field.
func objectToSingle(v any) (map[string]string, any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, nil, err
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, nil, err
	}
	single := make(map[string]string, len(obj))
	for k, val := range obj {
		single[k] = anyCell(val)
	}
	return single, obj, nil
}

// dynamicTable derives a stable, sorted column set from the union of keys across
// entries and renders each entry as a row. It is used for endpoints whose element
// shape is not statically known, so every returned field is surfaced.
func dynamicTable(entries []map[string]any) ([]string, [][]string) {
	keySet := map[string]struct{}{}
	for _, e := range entries {
		for k := range e {
			keySet[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	headers := make([]string, len(keys))
	for i, k := range keys {
		headers[i] = upperHeader(k)
	}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		row := make([]string, len(keys))
		for i, k := range keys {
			row[i] = anyCell(e[k])
		}
		rows = append(rows, row)
	}
	return headers, rows
}

// upperHeader renders a JSON key as an upper-case table header.
func upperHeader(k string) string {
	out := make([]rune, 0, len(k))
	for _, r := range k {
		switch {
		case r >= 'a' && r <= 'z':
			out = append(out, r-('a'-'A'))
		case r == '_':
			out = append(out, '-')
		default:
			out = append(out, r)
		}
	}
	return string(out)
}
