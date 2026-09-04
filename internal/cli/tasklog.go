package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/tasks"
	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// WaitOptionsFor returns a task-wait policy bounded by timeoutSeconds while
// leaving the polling cadence at the SDK default. A non-positive value means
// "wait as long as the task takes". The policy itself lives in the apiclient
// package, because the wait funnels there apply the same rule to every caller
// that passes no options of its own; this is the spelling the command layer
// uses when it holds a bound in hand.
func WaitOptionsFor(timeoutSeconds int64) *tasks.WaitOptions {
	return apiclient.WaitOptionsFor(timeoutSeconds)
}

// TaskLogLine is one line of GET /nodes/{node}/tasks/{upid}/log.
type TaskLogLine struct {
	N pve.PVEInt `json:"n"`
	T string     `json:"t"`
}

// taskLogLimit is the page size TaskLogResult requests when the caller passes
// nil params. It exists for the --dry-run bulk restart use case: the worker
// writes its whole plan to the log, and the endpoint's default page of 50
// lines would truncate a multi-daemon plan. A log longer than the limit is
// still cut at taskLogLimit lines; `pmx pve task log` and `pmx pve node task
// log` expose --limit/--start for reading the rest.
const taskLogLimit int64 = 5000

// TaskLogResult fetches a task's log on the node that ran it and renders it
// as numbered lines: headers N and T, one row per line, and the decoded lines
// in Raw. It is the single decoder behind `pmx pve task log`, `pmx pve node
// task log`, and the --dry-run plan of the Ceph restart-bulk commands. A nil
// params requests the first taskLogLimit lines; a non-nil params is sent as
// given, so callers with --limit/--start flags forward exactly what the
// operator set and nothing else.
func TaskLogResult(
	ctx context.Context, deps *Deps, node, upid string, params *nodes.ListTasksLogParams,
) (output.Result, error) {
	if params == nil {
		limit := taskLogLimit
		params = &nodes.ListTasksLogParams{Limit: &limit}
	}
	resp, err := deps.API.Nodes.ListTasksLog(ctx, node, upid, params)
	if err != nil {
		return output.Result{}, fmt.Errorf("get log for task %q on node %q: %w", upid, node, err)
	}

	lines := make([]TaskLogLine, 0)
	rows := make([][]string, 0)
	if resp != nil {
		for i, raw := range *resp {
			var line TaskLogLine
			if err := json.Unmarshal(raw, &line); err != nil {
				return output.Result{}, fmt.Errorf("decode task log line %d: %w", i, err)
			}
			lines = append(lines, line)
			rows = append(rows, []string{strconv.FormatInt(line.N.Int(), 10), line.T})
		}
	}
	return output.Result{Headers: []string{"N", "T"}, Rows: rows, Raw: lines}, nil
}
