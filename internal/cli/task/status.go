package task

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/tasks"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// newStatusCmd builds `pve task status <upid>`.
//
// The node is parsed directly from the UPID (field 1 of the colon-separated
// string), so neither --node nor PVE_NODE is required.  This mirrors the
// approach used by `pve task wait`, which also derives the node from the UPID
// rather than requiring it via deps.Node.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <upid>",
		Short: "Get the current status of a task",
		Long: `Read the current status of a Proxmox VE task identified by its UPID.

The node is resolved automatically from the UPID, so --node is not required.
This command performs a single, non-blocking read and does not poll.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)

			upid := args[0]
			parsed, err := tasks.ParseUPID(upid)
			if err != nil {
				return fmt.Errorf("parse upid %q: %w", upid, err)
			}

			resp, err := deps.API.Nodes.ListTasksStatus(cmd.Context(), parsed.Node, upid)
			if err != nil {
				return fmt.Errorf("get task status %q on node %q: %w", upid, parsed.Node, err)
			}

			single := buildStatusSingle(resp)
			res := output.Result{Single: single, Raw: single}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
}

// buildStatusSingle converts a ListTasksStatusResponse into the key/value map
// rendered by single-object output renderers.  Exitstatus is included only when
// the server returned it (i.e. the task has completed).
func buildStatusSingle(resp *nodes.ListTasksStatusResponse) map[string]string {
	m := map[string]string{
		"UPID":      resp.Upid,
		"STATUS":    resp.Status,
		"PID":       strconv.FormatInt(resp.Pid, 10),
		"TYPE":      resp.Type,
		"USER":      resp.User,
		"NODE":      resp.Node,
		"ID":        resp.Id,
		"STARTTIME": formatTimestamp(resp.Starttime),
	}
	if resp.Exitstatus != nil {
		m["EXITSTATUS"] = *resp.Exitstatus
	}
	return m
}
