package pdm

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	pveclient "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	pdmpve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/pve"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newPveStorageCmd builds `pmx pdm pve storage` — inspect a PVE remote
// node's storage: listing, per-storage status, and RRD metrics
// (/pve/remotes/{remote}/nodes/{node}/storage...).
//
// GetRemotesNodesStorage (GET .../storage/{storage}) is a directory-index
// leaf with no data of its own (returns only `error`, pve_gen.go:4409-4437,
// v3.6.0) and is excluded, matching every other product group in this
// package.
func newPveStorageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Inspect a PVE remote node's storage",
		Long: "Inspect a PVE remote node's storage: list datastores, show a single " +
			"datastore's status, and read RRD disk-usage metrics.",
	}
	cmd.AddCommand(newPveStorageLsCmd(), newPveStorageStatusCmd(), newPveStorageRrddataCmd())
	return cmd
}

// pveStorageEntry is the decoded shape of one element of GET
// /pve/remotes/{remote}/nodes/{node}/storage (pdm-apidoc.json, verified
// 2026-07-08).
type pveStorageEntry struct {
	Storage string             `json:"storage"`
	Type    string             `json:"type"`
	Content *string            `json:"content,omitempty"`
	Active  *pveclient.PVEBool `json:"active,omitempty"`
	Enabled *pveclient.PVEBool `json:"enabled,omitempty"`
	Shared  *pveclient.PVEBool `json:"shared,omitempty"`
	Total   *pveclient.PVEInt  `json:"total,omitempty"`
	Used    *pveclient.PVEInt  `json:"used,omitempty"`
	Avail   *pveclient.PVEInt  `json:"avail,omitempty"`
}

// validPveStorageContentTypes are the --content enum values accepted by
// GET /pve/remotes/{remote}/nodes/{node}/storage, per the PDM API schema.
var validPveStorageContentTypes = []string{"backup", "images", "import", "iso", "none", "rootdir", "snippets", "vztmpl"}

// newPveStorageLsCmd builds `pmx pdm pve storage ls <remote> <node>` — get
// status for all datastores (GET
// /pve/remotes/{remote}/nodes/{node}/storage), sorted by storage name like
// every other discrete-entity ls in this package.
func newPveStorageLsCmd() *cobra.Command {
	var (
		content       []string
		enabled       bool
		format        bool
		storageFilter string
		target        string
	)
	cmd := &cobra.Command{
		Use:   "ls <remote> <node>",
		Short: "List a PVE remote node's storage",
		Long:  "Get status for all datastores (GET /pve/remotes/{remote}/nodes/{node}/storage).",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node := args[0], args[1]
			fl := cmd.Flags()

			for _, c := range content {
				if !stringInSlice(c, validPveStorageContentTypes) {
					return fmt.Errorf("list storage on node %q of PVE remote %q: --content must be one of %s (got %q)",
						node, remote, strings.Join(validPveStorageContentTypes, ", "), c)
				}
			}

			params := &pdmpve.ListRemotesNodesStorageParams{}
			if fl.Changed("content") {
				params.Content = content
			}
			if fl.Changed("enabled") {
				params.Enabled = &enabled
			}
			if fl.Changed("format") {
				params.Format = &format
			}
			if fl.Changed("storage") {
				params.Storage = &storageFilter
			}
			if fl.Changed("target") {
				params.Target = &target
			}

			resp, err := deps.PDM.Pve.ListRemotesNodesStorage(cmd.Context(), remote, node, params)
			if err != nil {
				return fmt.Errorf("list storage on node %q of PVE remote %q: %w", node, remote, err)
			}

			items := rawItemsOf(resp)
			table, err := cli.DecodePairedRows[pveStorageEntry](items, "storage")
			if err != nil {
				return fmt.Errorf("decode storage entry on node %q of PVE remote %q: %w", node, remote, errors.Unwrap(err))
			}
			sort.Slice(table, func(i, j int) bool { return table[i].Entry.Storage < table[j].Entry.Storage })

			headers := []string{"STORAGE", "TYPE", "CONTENT", "ACTIVE", "ENABLED", "SHARED", "TOTAL", "USED", "AVAIL"}
			rows := make([][]string, 0, len(table))
			raws := make([]map[string]any, 0, len(table))

			for _, t := range table {
				e := t.Entry
				rows = append(rows, []string{
					e.Storage, e.Type, strPtrString(e.Content), pveBoolPtrString(e.Active), pveBoolPtrString(e.Enabled),
					pveBoolPtrString(e.Shared), pveIntPtrString(e.Total), pveIntPtrString(e.Used), pveIntPtrString(e.Avail),
				})
				raws = append(raws, t.Raw)
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: raws}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&content, "content", nil, "only list storage supporting this content type (repeatable)")
	f.BoolVar(&enabled, "enabled", false, "only include enabled storages")
	f.BoolVar(&format, "format", false, "include format information")
	f.StringVar(&storageFilter, "storage", "", "only list status for this storage")
	f.StringVar(&target, "target", "", "if different from node, only list shared storages accessible by both")
	return cmd
}

// newPveStorageStatusCmd builds `pmx pdm pve storage status <remote> <node>
// <storage>` — get status for a specific datastore (GET
// /pve/remotes/{remote}/nodes/{node}/storage/{storage}/status).
func newPveStorageStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <remote> <node> <storage>",
		Short: "Show a PVE remote node's storage status",
		Long:  "Get status for a specific datastore (GET /pve/remotes/{remote}/nodes/{node}/storage/{storage}/status).",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node, storage := args[0], args[1], args[2]

			resp, err := deps.PDM.Pve.ListRemotesNodesStorageStatus(cmd.Context(), remote, node, storage)
			if err != nil {
				return fmt.Errorf("get status of storage %q on node %q of PVE remote %q: %w", storage, node, remote, err)
			}
			if resp == nil {
				return fmt.Errorf("get status of storage %q on node %q of PVE remote %q: empty response from server",
					storage, node, remote)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode status of storage %q on node %q of PVE remote %q: %w", storage, node, remote, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// pveStorageRrdEntry is the decoded shape of one element of GET
// /pve/remotes/{remote}/nodes/{node}/storage/{storage}/rrddata
// (pdm-apidoc.json, verified 2026-07-08).
type pveStorageRrdEntry struct {
	Time      int64    `json:"time"`
	DiskTotal *float64 `json:"disk-total,omitempty"`
	DiskUsed  *float64 `json:"disk-used,omitempty"`
}

// newPveStorageRrddataCmd builds `pmx pdm pve storage rrddata <remote>
// <node> <storage>` — read storage stats (GET
// /pve/remotes/{remote}/nodes/{node}/storage/{storage}/rrddata). Time-series
// data: rendered in server order, not sorted, matching every other RRD
// listing in this package.
func newPveStorageRrddataCmd() *cobra.Command {
	var (
		timeframe string
		cf        string
	)
	cmd := &cobra.Command{
		Use:   "rrddata <remote> <node> <storage>",
		Short: "Read a PVE remote node storage's RRD metrics",
		Long: "Read RRD (round-robin database) storage stats over the given time frame and " +
			"consolidation function (GET /pve/remotes/{remote}/nodes/{node}/storage/{storage}/rrddata).",
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			remote, node, storage := args[0], args[1], args[2]

			if !stringInSlice(timeframe, validRemoteRrdTimeframes) {
				return fmt.Errorf("get rrddata for storage %q on node %q of PVE remote %q: "+
					"--timeframe must be one of %s (got %q)",
					storage, node, remote, strings.Join(validRemoteRrdTimeframes, ", "), timeframe)
			}
			if !stringInSlice(cf, validRemoteRrdConsolidations) {
				return fmt.Errorf("get rrddata for storage %q on node %q of PVE remote %q: --cf must be one of %s (got %q)",
					storage, node, remote, strings.Join(validRemoteRrdConsolidations, ", "), cf)
			}

			params := &pdmpve.ListRemotesNodesStorageRrddataParams{Cf: cf, Timeframe: timeframe}

			resp, err := deps.PDM.Pve.ListRemotesNodesStorageRrddata(cmd.Context(), remote, node, storage, params)
			if err != nil {
				return fmt.Errorf("get rrddata for storage %q on node %q of PVE remote %q: %w", storage, node, remote, err)
			}

			items := rawItemsOf(resp)
			entries, err := nodeDecodeArray[pveStorageRrdEntry](items)
			if err != nil {
				return fmt.Errorf("decode rrddata for storage %q on node %q of PVE remote %q: %w", storage, node, remote, err)
			}

			headers := []string{"TIME", "DISK-TOTAL", "DISK-USED"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{int64PtrString(&e.Time), float64PtrString(e.DiskTotal), float64PtrString(e.DiskUsed)})
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
