package cluster

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	pvecluster "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/cluster"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// clusterLogEntry is the decoded shape of one entry from cluster.ListLog.
// Pointer fields render as an empty cell when the API omits them rather than as
// a misleading zero.
type clusterLogEntry struct {
	Time *int64 `json:"time"`
	Node string `json:"node"`
	Pid  *int64 `json:"pid"`
	// Uid is decoded as raw JSON because PVE renders it inconsistently: some
	// entries carry a numeric uid, others a string (a username or "0"). A typed
	// *int64 fails to decode the string form, so scalarCell normalizes both.
	Uid json.RawMessage `json:"uid"`
	Tag string          `json:"tag"`
	Msg string          `json:"msg"`
}

// scalarCell renders a raw JSON scalar (string or number) as a plain string;
// quoted strings are unquoted, null and absent values render as an empty cell.
func scalarCell(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	return string(raw)
}

// newLogCmd builds `pve cluster log`.
func newLogCmd() *cobra.Command {
	var maxEntries int64

	cmd := &cobra.Command{
		Use:   "log",
		Short: "Read cluster log entries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pvecluster.ListLogParams{}
			if maxEntries != 0 {
				params.Max = &maxEntries
			}

			resp, err := deps.API.Cluster.ListLog(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("read cluster log: %w", err)
			}

			headers := []string{"TIME", "NODE", "PID", "UID", "TAG", "MSG"}
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e clusterLogEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode cluster log entry: %w", err)
					}
					rows = append(rows, []string{
						formatIntPtr(e.Time),
						e.Node,
						formatIntPtr(e.Pid),
						scalarCell(e.Uid),
						e.Tag,
						e.Msg,
					})
				}
			}

			result := output.Result{Headers: headers, Rows: rows}
			if resp != nil {
				result.Raw = *resp
			}
			return deps.Out.Render(cmd.OutOrStdout(), result, deps.Format)
		},
	}

	cmd.Flags().Int64Var(&maxEntries, "max", 50, "maximum number of log entries to return")
	return cmd
}
