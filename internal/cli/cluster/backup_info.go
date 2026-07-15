package cluster

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newClusterBackupInfoCmd builds the `pmx pve cluster backup-info` sub-tree for backup
// coverage queries that span the whole cluster rather than a single job.
func newClusterBackupInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup-info",
		Short: "Query cluster-wide backup coverage",
		Long: "Query backup coverage across the whole cluster. The not-backed-up sub-command " +
			"lists every guest that has no backup job covering it.",
	}
	cmd.AddCommand(newBackupInfoNotBackedUpCmd())
	return cmd
}

// newBackupInfoNotBackedUpCmd builds `pmx pve cluster backup-info not-backed-up`.
// It calls GET /cluster/backup-info/not-backed-up and lists every guest that
// has no scheduled backup job covering it — essential for coverage audits.
func newBackupInfoNotBackedUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "not-backed-up",
		Short: "List guests with no backup coverage",
		Long: "List every guest in the cluster that is not covered by any scheduled backup " +
			"job. Essential for backup coverage audits.",
		Example: `  pmx pve cluster backup-info not-backed-up`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			resp, err := deps.API.Cluster.ListBackupInfoNotBackedUp(cmd.Context())
			if err != nil {
				return fmt.Errorf("list guests not backed up: %w", err)
			}
			entries := make([]map[string]any, 0)
			if resp != nil {
				for _, raw := range *resp {
					var m map[string]any
					if err := json.Unmarshal(raw, &m); err != nil {
						return fmt.Errorf("decode not-backed-up entry: %w", err)
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
