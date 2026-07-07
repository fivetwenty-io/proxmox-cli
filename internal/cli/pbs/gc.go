package pbs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"

	pbsadmin "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/admin"
)

// newGcCmd builds `pmx pbs gc` — run and inspect Proxmox Backup Server
// garbage-collection jobs (POST/GET /admin/datastore/{store}/gc, GET /admin/gc).
// There is no separate GC job configuration API: the schedule is a property
// of the datastore itself, so unlike prune and verify there is no `gc job`
// sub-tree here.
func newGcCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Run and inspect garbage-collection jobs",
		Long: "Trigger and report on Proxmox Backup Server garbage-collection " +
			"runs, which reclaim disk space from chunks no longer referenced by " +
			"any backup snapshot.",
	}
	cmd.AddCommand(newGcRunCmd(), newGcStatusCmd(), newGcLsCmd())
	return cmd
}

// gcStatusEntry is the decoded shape of a single garbage-collection status
// element. `gc status` (one datastore, GET /admin/datastore/{store}/gc) and
// `gc ls` (every datastore, GET /admin/gc) return identical fields, so both
// share this type.
type gcStatusEntry struct {
	CacheStats     json.RawMessage `json:"cache-stats,omitempty"`
	DiskBytes      int64           `json:"disk-bytes"`
	DiskChunks     int64           `json:"disk-chunks"`
	Duration       *int64          `json:"duration,omitempty"`
	IndexDataBytes int64           `json:"index-data-bytes"`
	IndexFileCount int64           `json:"index-file-count"`
	LastRunEndtime *int64          `json:"last-run-endtime,omitempty"`
	LastRunState   *string         `json:"last-run-state,omitempty"`
	NextRun        *int64          `json:"next-run,omitempty"`
	PendingBytes   int64           `json:"pending-bytes"`
	PendingChunks  int64           `json:"pending-chunks"`
	RemovedBad     int64           `json:"removed-bad"`
	RemovedBytes   int64           `json:"removed-bytes"`
	RemovedChunks  int64           `json:"removed-chunks"`
	Schedule       *string         `json:"schedule,omitempty"`
	StillBad       int64           `json:"still-bad"`
	Store          string          `json:"store"`
	Upid           *string         `json:"upid,omitempty"`
}

// gcCacheStats is the decoded shape of gcStatusEntry.CacheStats.
type gcCacheStats struct {
	Hits   int64 `json:"hits"`
	Misses int64 `json:"misses"`
}

// gcStatusSingle flattens a gcStatusEntry into a string map for table/plain/
// text rendering, including only the optional fields present on the response.
func gcStatusSingle(e gcStatusEntry) map[string]string {
	single := map[string]string{
		"store":            e.Store,
		"disk-bytes":       strconv.FormatInt(e.DiskBytes, 10),
		"disk-chunks":      strconv.FormatInt(e.DiskChunks, 10),
		"index-data-bytes": strconv.FormatInt(e.IndexDataBytes, 10),
		"index-file-count": strconv.FormatInt(e.IndexFileCount, 10),
		"pending-bytes":    strconv.FormatInt(e.PendingBytes, 10),
		"pending-chunks":   strconv.FormatInt(e.PendingChunks, 10),
		"removed-bad":      strconv.FormatInt(e.RemovedBad, 10),
		"removed-bytes":    strconv.FormatInt(e.RemovedBytes, 10),
		"removed-chunks":   strconv.FormatInt(e.RemovedChunks, 10),
		"still-bad":        strconv.FormatInt(e.StillBad, 10),
	}

	if e.Duration != nil {
		single["duration"] = strconv.FormatInt(*e.Duration, 10)
	}

	if e.LastRunEndtime != nil {
		single["last-run-endtime"] = strconv.FormatInt(*e.LastRunEndtime, 10)
	}

	if e.LastRunState != nil {
		single["last-run-state"] = *e.LastRunState
	}

	if e.NextRun != nil {
		single["next-run"] = strconv.FormatInt(*e.NextRun, 10)
	}

	if e.Schedule != nil {
		single["schedule"] = *e.Schedule
	}

	if e.Upid != nil {
		single["upid"] = *e.Upid
	}

	if len(e.CacheStats) > 0 {
		var stats gcCacheStats

		err := json.Unmarshal(e.CacheStats, &stats)
		if err == nil {
			single["cache-hits"] = strconv.FormatInt(stats.Hits, 10)
			single["cache-misses"] = strconv.FormatInt(stats.Misses, 10)
		}
	}

	return single
}

// newGcRunCmd builds `pmx pbs gc run` — start a garbage-collection run on one
// datastore (POST /admin/datastore/{store}/gc).
func newGcRunCmd() *cobra.Command {
	var store string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start a garbage-collection run on a datastore",
		Long: "Start garbage collection on a datastore, reclaiming disk space " +
			"from chunks no longer referenced by any backup snapshot (POST " +
			"/admin/datastore/{store}/gc). Runs as an asynchronous task; the " +
			"command blocks until it finishes unless --async is set.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if store == "" {
				return fmt.Errorf("--store is required")
			}

			resp, err := deps.PBS.Admin.CreateDatastoreGc(cmd.Context(), store)
			if err != nil {
				return fmt.Errorf("run gc on datastore %q: %w", store, err)
			}

			if resp == nil {
				return fmt.Errorf("run gc on datastore %q: nil response from PBS", store)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Garbage collection on datastore %q finished.", store))
		},
	}
	cmd.Flags().StringVar(&store, "store", "", "datastore name (required)")
	cli.MustMarkRequired(cmd, "store")
	return cmd
}

// newGcStatusCmd builds `pmx pbs gc status` — report the last/next
// garbage-collection run and space-reclamation counters for one datastore
// (GET /admin/datastore/{store}/gc).
func newGcStatusCmd() *cobra.Command {
	var store string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show a datastore's garbage-collection status",
		Long: "Report the last and next garbage-collection run along with chunk " +
			"and byte counters for a single datastore (GET /admin/datastore/{store}/gc).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			if store == "" {
				return fmt.Errorf("--store is required")
			}

			resp, err := deps.PBS.Admin.ListDatastoreGc(cmd.Context(), store)
			if err != nil {
				return fmt.Errorf("get gc status for datastore %q: %w", store, err)
			}

			if resp == nil {
				return fmt.Errorf("get gc status for datastore %q: nil response from PBS", store)
			}

			entry := gcStatusEntry{
				CacheStats:     resp.CacheStats,
				DiskBytes:      resp.DiskBytes.Int(),
				DiskChunks:     resp.DiskChunks.Int(),
				Duration:       (*int64)(resp.Duration),
				IndexDataBytes: resp.IndexDataBytes.Int(),
				IndexFileCount: resp.IndexFileCount.Int(),
				LastRunEndtime: (*int64)(resp.LastRunEndtime),
				LastRunState:   resp.LastRunState,
				NextRun:        (*int64)(resp.NextRun),
				PendingBytes:   resp.PendingBytes.Int(),
				PendingChunks:  resp.PendingChunks.Int(),
				RemovedBad:     resp.RemovedBad.Int(),
				RemovedBytes:   resp.RemovedBytes.Int(),
				RemovedChunks:  resp.RemovedChunks.Int(),
				Schedule:       resp.Schedule,
				StillBad:       resp.StillBad.Int(),
				Store:          resp.Store,
				Upid:           resp.Upid,
			}
			if entry.Store == "" {
				entry.Store = store
			}

			res := output.Result{Single: gcStatusSingle(entry), Raw: resp}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&store, "store", "", "datastore name (required)")
	cli.MustMarkRequired(cmd, "store")
	return cmd
}

// newGcLsCmd builds `pmx pbs gc ls` — list garbage-collection status across
// every datastore, or one when --store is given (GET /admin/gc).
func newGcLsCmd() *cobra.Command {
	var store string
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List garbage-collection status across datastores",
		Long: "List the garbage-collection schedule and last-run status for " +
			"every datastore visible to the caller, or a single one with --store " +
			"(GET /admin/gc).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pbsadmin.ListGcParams{}
			if cmd.Flags().Changed("store") {
				params.Store = strPtr(store)
			}

			resp, err := deps.PBS.Admin.ListGc(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list gc status: %w", err)
			}

			entries := decodeGcEntries(resp)
			sort.Slice(entries, func(i, j int) bool { return entries[i].Store < entries[j].Store })

			headers := []string{
				"STORE", "SCHEDULE", "LAST-RUN-STATE", "DURATION", "NEXT-RUN", "REMOVED-BYTES", "PENDING-BYTES",
			}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Store,
					pbsFormatOptionalString(e.Schedule),
					pbsFormatOptionalString(e.LastRunState),
					pbsFormatOptionalInt64(e.Duration),
					pbsFormatOptionalInt64(e.NextRun),
					strconv.FormatInt(e.RemovedBytes, 10),
					strconv.FormatInt(e.PendingBytes, 10),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&store, "store", "", "only list this datastore")
	return cmd
}

// decodeGcEntries decodes a ListGc raw-array response into typed entries,
// skipping any element that fails to decode.
func decodeGcEntries(resp *pbsadmin.ListGcResponse) []gcStatusEntry {
	entries := make([]gcStatusEntry, 0)
	if resp == nil {
		return entries
	}

	for _, raw := range *resp {
		var e gcStatusEntry

		err := json.Unmarshal(raw, &e)
		if err != nil {
			continue
		}

		entries = append(entries, e)
	}

	return entries
}

// pbsFormatOptionalString dereferences a *string for table rendering,
// returning "" for nil. Shared by the prune, gc, and verify command files.
func pbsFormatOptionalString(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

// pbsFormatOptionalInt64 dereferences a *int64 as a decimal string for table
// rendering, returning "" for nil. Shared by the prune, gc, and verify
// command files.
func pbsFormatOptionalInt64(v *int64) string {
	if v == nil {
		return ""
	}

	return strconv.FormatInt(*v, 10)
}

// pbsFormatOptionalBool dereferences a *bool for table rendering, returning
// "false" for nil (an unset optional PBS boolean defaults to false). Shared
// by the prune, gc, and verify command files.
func pbsFormatOptionalBool(b *bool) string {
	if b == nil {
		return "false"
	}

	return strconv.FormatBool(*b)
}
