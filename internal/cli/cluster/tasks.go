package cluster

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// clusterTaskEntry is the decoded shape of one entry from cluster.ListTasks.
// Starttime is a pointer so an absent value renders as an empty cell rather
// than a misleading zero.
type clusterTaskEntry struct {
	UPID      string `json:"upid"`
	Node      string `json:"node"`
	Type      string `json:"type"`
	ID        string `json:"id"`
	Starttime *int64 `json:"starttime"`
	Status    string `json:"status"`
	User      string `json:"user"`
}

// newTasksCmd builds `pmx cluster tasks`.
func newTasksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tasks",
		Short: "List recent cluster-wide tasks",
		Long: "List recent tasks from across the cluster with their UPID, node, type, target ID, " +
			"start time, status, and initiating user.",
		Example: `  pmx pve cluster tasks`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.API.Cluster.ListTasks(cmd.Context())
			if err != nil {
				return fmt.Errorf("list cluster tasks: %w", err)
			}

			headers := []string{"UPID", "NODE", "TYPE", "ID", "STARTTIME", "STATUS", "USER"}
			rows := make([][]string, 0)
			if resp != nil {
				for _, raw := range *resp {
					var e clusterTaskEntry
					if err := json.Unmarshal(raw, &e); err != nil {
						return fmt.Errorf("decode cluster task entry: %w", err)
					}
					rows = append(rows, []string{
						e.UPID,
						e.Node,
						e.Type,
						e.ID,
						formatIntPtr(e.Starttime),
						e.Status,
						e.User,
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
}
