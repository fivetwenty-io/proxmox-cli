package pdm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	pdmpbs "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/pbs"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newPbsDatastoreCmd builds `pmx pdm pbs datastore` — inspect a PBS remote's
// datastores, their namespaces, backup snapshots, and RRD disk-usage metrics.
func newPbsDatastoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "datastore",
		Short: "Inspect a PBS remote's datastores",
	}
	cmd.AddCommand(
		newPbsDatastoreLsCmd(),
		newPbsDatastoreNamespacesCmd(),
		newPbsDatastoreSnapshotsCmd(),
		newPbsDatastoreRrddataCmd(),
	)
	return cmd
}

// pbsDatastoreEntry is a table-relevant subset of one element of GET
// /pbs/remotes/{remote}/datastore (21 configuration fields total per the PDM
// API schema); every field is still preserved losslessly in Raw via the
// paired sort below.
type pbsDatastoreEntry struct {
	Name    string  `json:"name"`
	Path    *string `json:"path,omitempty"`
	Comment *string `json:"comment,omitempty"`
}

// newPbsDatastoreLsCmd builds `pmx pdm pbs datastore ls <remote>` — list a
// PBS remote's datastores (GET /pbs/remotes/{remote}/datastore), sorted by
// datastore name.
func newPbsDatastoreLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <remote>",
		Short: "List a PBS remote's datastores",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote := args[0]

			resp, err := deps.PDM.Pbs.ListRemotesDatastore(cmd.Context(), remote)
			if err != nil {
				return fmt.Errorf("list datastores on PBS remote %q: %w", remote, err)
			}

			items := rawItemsOf(resp)
			type dsRow struct {
				entry pbsDatastoreEntry
				raw   map[string]any
			}
			table := make([]dsRow, 0, len(items))

			for _, raw := range items {
				var e pbsDatastoreEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode datastore entry on PBS remote %q: %w", remote, err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode datastore entry on PBS remote %q: %w", remote, err)
				}

				table = append(table, dsRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Name < table[j].entry.Name })

			headers := []string{"NAME", "PATH", "COMMENT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{e.Name, strPtrString(e.Path), strPtrString(e.Comment)})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// pbsNamespaceEntry is the decoded shape of one element of GET
// /pbs/remotes/{remote}/datastore/{datastore}/namespaces.
type pbsNamespaceEntry struct {
	Comment *string `json:"comment,omitempty"`
	Ns      string  `json:"ns"`
}

// newPbsDatastoreNamespacesCmd builds `pmx pdm pbs datastore namespaces
// <remote> <datastore>` — list the namespaces of a PBS remote's datastore
// (GET /pbs/remotes/{remote}/datastore/{datastore}/namespaces), sorted by ns.
func newPbsDatastoreNamespacesCmd() *cobra.Command {
	var (
		maxDepth int64
		parent   string
	)
	cmd := &cobra.Command{
		Use:   "namespaces <remote> <datastore>",
		Short: "List a datastore's namespaces",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, datastore := args[0], args[1]
			fl := cmd.Flags()

			params := &pdmpbs.ListRemotesDatastoreNamespacesParams{}
			if fl.Changed("max-depth") {
				params.MaxDepth = int64Ptr(maxDepth)
			}
			if fl.Changed("parent") {
				params.Parent = strPtr(parent)
			}

			resp, err := deps.PDM.Pbs.ListRemotesDatastoreNamespaces(cmd.Context(), remote, datastore, params)
			if err != nil {
				return fmt.Errorf("list namespaces of datastore %q on PBS remote %q: %w", datastore, remote, err)
			}

			items := rawItemsOf(resp)
			type nsRow struct {
				entry pbsNamespaceEntry
				raw   map[string]any
			}
			table := make([]nsRow, 0, len(items))

			for _, raw := range items {
				var e pbsNamespaceEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode namespace entry of datastore %q on PBS remote %q: %w", datastore, remote, err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode namespace entry of datastore %q on PBS remote %q: %w", datastore, remote, err)
				}

				table = append(table, nsRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool { return table[i].entry.Ns < table[j].entry.Ns })

			headers := []string{"NS", "COMMENT"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{e.Ns, strPtrString(e.Comment)})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&maxDepth, "max-depth", 0, "maximum depth to recursively list namespaces")
	f.StringVar(&parent, "parent", "", "only list namespaces under this parent namespace")
	return cmd
}

// pbsSnapshotEntry is the decoded shape of one element of GET
// /pbs/remotes/{remote}/datastore/{datastore}/snapshots.
type pbsSnapshotEntry struct {
	BackupId   string  `json:"backup-id"`
	BackupTime int64   `json:"backup-time"`
	BackupType string  `json:"backup-type"`
	Comment    *string `json:"comment,omitempty"`
	Owner      *string `json:"owner,omitempty"`
	Protected  bool    `json:"protected"`
	Size       *int64  `json:"size,omitempty"`
}

// newPbsDatastoreSnapshotsCmd builds `pmx pdm pbs datastore snapshots
// <remote> <datastore>` — list a datastore's backup snapshots (GET
// /pbs/remotes/{remote}/datastore/{datastore}/snapshots), sorted by the
// composite identifying key (backup-type, backup-id, backup-time) — a
// datastore commonly holds several snapshots of the same type/ID taken at
// different times, so backup-type/backup-id alone is not unique.
func newPbsDatastoreSnapshotsCmd() *cobra.Command {
	var ns string
	cmd := &cobra.Command{
		Use:   "snapshots <remote> <datastore>",
		Short: "List a datastore's backup snapshots",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, datastore := args[0], args[1]

			params := &pdmpbs.ListRemotesDatastoreSnapshotsParams{}
			if cmd.Flags().Changed("ns") {
				params.Ns = strPtr(ns)
			}

			resp, err := deps.PDM.Pbs.ListRemotesDatastoreSnapshots(cmd.Context(), remote, datastore, params)
			if err != nil {
				return fmt.Errorf("list snapshots of datastore %q on PBS remote %q: %w", datastore, remote, err)
			}

			items := rawItemsOf(resp)
			type snapRow struct {
				entry pbsSnapshotEntry
				raw   map[string]any
			}
			table := make([]snapRow, 0, len(items))

			for _, raw := range items {
				var e pbsSnapshotEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode snapshot entry of datastore %q on PBS remote %q: %w", datastore, remote, err)
				}

				var m map[string]any

				err = json.Unmarshal(raw, &m)
				if err != nil {
					return fmt.Errorf("decode snapshot entry of datastore %q on PBS remote %q: %w", datastore, remote, err)
				}

				table = append(table, snapRow{entry: e, raw: m})
			}
			sort.Slice(table, func(i, j int) bool {
				a, b := table[i].entry, table[j].entry
				if a.BackupType != b.BackupType {
					return a.BackupType < b.BackupType
				}
				if a.BackupId != b.BackupId {
					return a.BackupId < b.BackupId
				}
				return a.BackupTime < b.BackupTime
			})

			headers := []string{"TYPE", "ID", "TIME", "SIZE", "PROTECTED", "OWNER"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.entry
				rows = append(rows, []string{
					e.BackupType, e.BackupId, int64PtrString(&e.BackupTime), int64PtrString(e.Size),
					strconv.FormatBool(e.Protected), strPtrString(e.Owner),
				})
				raws = append(raws, t.raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&ns, "ns", "", "only list snapshots in this namespace")
	return cmd
}

// pbsDatastoreRrdEntry mirrors the fixed field set returned by GET
// /pbs/remotes/{remote}/datastore/{datastore}/rrddata (6 fields total per
// the PDM API schema); unlike pbsNodeRrdEntry's larger host-metrics schema,
// every field is included here.
type pbsDatastoreRrdEntry struct {
	Time          int64    `json:"time"`
	DiskAvailable *float64 `json:"disk-available,omitempty"`
	DiskRead      *float64 `json:"disk-read,omitempty"`
	DiskTotal     *float64 `json:"disk-total,omitempty"`
	DiskUsed      *float64 `json:"disk-used,omitempty"`
	DiskWrite     *float64 `json:"disk-write,omitempty"`
}

// newPbsDatastoreRrddataCmd builds `pmx pdm pbs datastore rrddata <remote>
// <datastore>` — read RRD disk-usage stats for a datastore (GET
// /pbs/remotes/{remote}/datastore/{datastore}/rrddata). Time-series data:
// rendered in server order, not sorted, matching every other RRD listing in
// this package.
func newPbsDatastoreRrddataCmd() *cobra.Command {
	var (
		timeframe string
		cf        string
	)
	cmd := &cobra.Command{
		Use:   "rrddata <remote> <datastore>",
		Short: "Read a datastore's disk-usage RRD metrics",
		Long: "Read RRD (round-robin database) disk-usage stats for a PBS remote's " +
			"datastore over the given time frame and consolidation function (GET " +
			"/pbs/remotes/{remote}/datastore/{datastore}/rrddata).",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, datastore := args[0], args[1]

			if !stringInSlice(timeframe, validRemoteRrdTimeframes) {
				return fmt.Errorf("get rrddata for datastore %q on PBS remote %q: --timeframe must be one of %s (got %q)",
					datastore, remote, strings.Join(validRemoteRrdTimeframes, ", "), timeframe)
			}
			if !stringInSlice(cf, validRemoteRrdConsolidations) {
				return fmt.Errorf("get rrddata for datastore %q on PBS remote %q: --cf must be one of %s (got %q)",
					datastore, remote, strings.Join(validRemoteRrdConsolidations, ", "), cf)
			}

			params := &pdmpbs.ListRemotesDatastoreRrddataParams{Cf: cf, Timeframe: timeframe}

			resp, err := deps.PDM.Pbs.ListRemotesDatastoreRrddata(cmd.Context(), remote, datastore, params)
			if err != nil {
				return fmt.Errorf("get rrddata for datastore %q on PBS remote %q: %w", datastore, remote, err)
			}

			items := rawItemsOf(resp)
			entries, err := nodeDecodeArray[pbsDatastoreRrdEntry](items)
			if err != nil {
				return fmt.Errorf("decode rrddata for datastore %q on PBS remote %q: %w", datastore, remote, err)
			}

			headers := []string{"TIME", "DISK-AVAILABLE", "DISK-READ", "DISK-TOTAL", "DISK-USED", "DISK-WRITE"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					int64PtrString(&e.Time), float64PtrString(e.DiskAvailable), float64PtrString(e.DiskRead),
					float64PtrString(e.DiskTotal), float64PtrString(e.DiskUsed), float64PtrString(e.DiskWrite),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&timeframe, "timeframe", "", "RRD time frame: hour|day|week|month|year|decade (required)")
	f.StringVar(&cf, "cf", "AVERAGE", "RRD consolidation function: MAX|AVERAGE")
	cli.MustMarkRequired(cmd, "timeframe")
	return cmd
}
