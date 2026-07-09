package pdm

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestNodeTaskLs_MapsFiltersAndRendersEntries asserts that `node task ls`
// maps its filter flags onto the wire request and renders the returned
// tasks without re-sorting them.
func TestNodeTaskLs_MapsFiltersAndRendersEntries(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/nodes/pdm-host/tasks", &rec, []map[string]any{
		{"upid": validUPID, "node": "pdm-host", "pid": 100, "pstart": 1, "starttime": 1000, "type": "aptupdate", "user": "root@pam", "status": "OK"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeTaskLsCmd(), "ls", "pdm-host", "--running", "--limit", "5")
	require.NoError(t, err)

	require.Equal(t, "1", rec.query.Get("running"))
	require.Equal(t, "5", rec.query.Get("limit"))
	require.Contains(t, buf.String(), validUPID)
	require.Contains(t, buf.String(), "aptupdate")
}

// TestNodeTaskStatus_RendersSingle asserts that `node task status` renders
// one task's status fields.
func TestNodeTaskStatus_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/tasks/"+validUPID+"/status", map[string]any{
		"upid": validUPID, "node": "pdm-host", "pid": 100, "pstart": 1, "starttime": 1000, "type": "aptupdate", "user": "root@pam", "status": "stopped",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeTaskStatusCmd(), "status", "pdm-host", validUPID)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "stopped")
}

// TestNodeTaskLog_RendersLines asserts that `node task log` decodes the
// paginated log lines from the typed ListTasksLog response.
func TestNodeTaskLog_RendersLines(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/tasks/"+validUPID+"/log", map[string]any{
		"active":  false,
		"success": 1,
		"total":   2,
		"data": []map[string]any{
			{"n": 1, "t": "starting task"},
			{"n": 2, "t": "task OK"},
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeTaskLogCmd(), "log", "pdm-host", validUPID)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "starting task")
	require.Contains(t, buf.String(), "task OK")
}

// TestNodeTaskStop_RefusesWithoutConfirmation asserts the --yes/-y gate on
// `node task stop` blocks the request entirely when unset.
func TestNodeTaskStop_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeTaskStopCmd(), "stop", "pdm-host", validUPID)
	require.Error(t, err)
	require.ErrorContains(t, err, `refusing to stop task "`+validUPID+`" on node "pdm-host" without confirmation: pass --yes/-y`)
}

// TestNodeTaskStop_SendsRequestWithConfirmation asserts that passing --yes
// issues the stop request.
func TestNodeTaskStop_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/nodes/pdm-host/tasks/"+validUPID, &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeTaskStopCmd(), "stop", "pdm-host", validUPID, "--yes")
	require.NoError(t, err)

	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), "stopped")
}
