package pbs

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"

	pbsstatus "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/status"
)

// newStatusCmd builds `pmx pbs status` — datastore-level usage and capacity
// reporting (GET /status/datastore-usage).
func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show datastore usage and capacity status",
		Long:  "Report disk usage, mount status, and capacity estimates for Proxmox Backup Server datastores.",
	}
	cmd.AddCommand(newPbsStatusDatastoreUsageCmd())
	return cmd
}

// pbsStatusDatastoreUsageEntry is the decoded shape of one element returned
// by GET /status/datastore-usage. History is kept as raw JSON: it is a
// numeric time series whose length depends on how much RRD data is
// available, so it is not rendered as a table column but is preserved
// losslessly for JSON/YAML output.
type pbsStatusDatastoreUsageEntry struct {
	Avail             *int64          `json:"avail,omitempty"`
	BackendType       string          `json:"backend-type"`
	Error             *string         `json:"error,omitempty"`
	EstimatedFullDate *int64          `json:"estimated-full-date,omitempty"`
	GcStatus          json.RawMessage `json:"gc-status,omitempty"`
	History           json.RawMessage `json:"history,omitempty"`
	HistoryDelta      *int64          `json:"history-delta,omitempty"`
	HistoryStart      *int64          `json:"history-start,omitempty"`
	MountStatus       string          `json:"mount-status"`
	Store             string          `json:"store"`
	Total             *int64          `json:"total,omitempty"`
	Used              *int64          `json:"used,omitempty"`
}

// decodePbsStatusDatastoreUsageEntries decodes a Status.ListDatastoreUsage
// raw-array response into typed entries, skipping any element that fails to
// decode.
func decodePbsStatusDatastoreUsageEntries(resp *pbsstatus.ListDatastoreUsageResponse) []pbsStatusDatastoreUsageEntry {
	entries := make([]pbsStatusDatastoreUsageEntry, 0)
	if resp == nil {
		return entries
	}

	for _, raw := range *resp {
		var e pbsStatusDatastoreUsageEntry

		err := json.Unmarshal(raw, &e)
		if err != nil {
			continue
		}

		entries = append(entries, e)
	}

	return entries
}

// newPbsStatusDatastoreUsageCmd builds `pmx pbs status datastore-usage` —
// list disk usage, mount status, and capacity estimates for every datastore
// visible to the caller (GET /status/datastore-usage).
func newPbsStatusDatastoreUsageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "datastore-usage",
		Short: "List datastore usage and full-date estimates",
		Long: "List disk usage, mount status, and a linear-regression estimate of when " +
			"each datastore will run out of space, based on the last month of RRD data " +
			"(GET /status/datastore-usage).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Status.ListDatastoreUsage(cmd.Context())
			if err != nil {
				return fmt.Errorf("list datastore usage: %w", err)
			}

			entries := decodePbsStatusDatastoreUsageEntries(resp)
			sort.Slice(entries, func(i, j int) bool { return entries[i].Store < entries[j].Store })

			headers := []string{
				"STORE", "BACKEND-TYPE", "MOUNT-STATUS", "TOTAL", "USED", "AVAIL", "ESTIMATED-FULL-DATE", "ERROR",
			}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Store,
					e.BackendType,
					e.MountStatus,
					pbsFormatOptionalInt64(e.Total),
					pbsFormatOptionalInt64(e.Used),
					pbsFormatOptionalInt64(e.Avail),
					pbsFormatOptionalInt64(e.EstimatedFullDate),
					pbsFormatOptionalString(e.Error),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
