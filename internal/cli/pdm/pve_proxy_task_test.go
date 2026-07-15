package pdm

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// TestPveTaskLs_PreservesServerOrder asserts that `pve task ls` renders
// tasks in the order the server returned them, without re-sorting, and
// forwards --node as a filter.
func TestPveTaskLs_PreservesServerOrder(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pve/remotes/cluster1/tasks", &rec, []map[string]any{
		{"upid": "UPID:pve1:00000002:...:second:", "id": "b", "node": "pve1", "pid": 2, "pstart": 1, "starttime": 2000, "user": "root@pam", "type": "aptupdate"},
		{"upid": "UPID:pve1:00000001:...:first:", "id": "a", "node": "pve1", "pid": 1, "pstart": 1, "starttime": 1000, "user": "root@pam", "type": "aptupdate"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveTaskLsCmd(), "ls", "cluster1", "--node", "pve1")
	require.NoError(t, err)

	require.Equal(t, "pve1", rec.query.Get("node"))

	rows := buf.String()
	secondIdx := strings.Index(rows, "UPID:pve1:00000002")
	firstIdx := strings.Index(rows, "UPID:pve1:00000001")
	require.GreaterOrEqual(t, secondIdx, 0)
	require.GreaterOrEqual(t, firstIdx, 0)
	require.Less(t, secondIdx, firstIdx, "task rows must preserve server order, not be sorted")
}

// TestPveTaskStatus_SendsWaitFlag asserts that `pve task status` forwards
// --wait and renders the task's status fields.
func TestPveTaskStatus_SendsWaitFlag(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pve/remotes/cluster1/tasks/"+validUPID+"/status", &rec, map[string]any{
		"id": "aptupdate", "node": "pve1", "pid": 100, "pstart": 1, "starttime": 1, "type": "aptupdate",
		"upid": validUPID, "user": "root@pam", "status": "stopped", "exitstatus": "OK",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveTaskStatusCmd(), "status", "cluster1", validUPID, "--wait")
	require.NoError(t, err)

	require.Equal(t, "1", rec.query.Get("wait"))
	require.Contains(t, buf.String(), "stopped")
}

// TestPveTaskLog_ReadsLines asserts that `pve task log` decodes the
// {n, t} log-line array and forwards --start/--limit on the wire.
func TestPveTaskLog_ReadsLines(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pve/remotes/cluster1/tasks/"+validUPID+"/log", &rec, []map[string]any{
		{"n": 1, "t": "starting apt update"},
		{"n": 2, "t": "done"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveTaskLogCmd(), "log", "cluster1", validUPID, "--start", "0", "--limit", "50")
	require.NoError(t, err)

	require.Equal(t, "0", rec.query.Get("start"))
	require.Equal(t, "50", rec.query.Get("limit"))
	require.Contains(t, buf.String(), "starting apt update")
	require.Contains(t, buf.String(), "done")
}

// TestPveTaskStop_RefusesWithoutConfirmation asserts the --yes/-y gate on
// `pve task stop` blocks the request entirely when unset.
func TestPveTaskStop_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveTaskStopCmd(), "stop", "cluster1", validUPID)
	require.Error(t, err)
	require.ErrorContains(t, err,
		`refusing to stop task "`+validUPID+`" on PVE remote "cluster1" without confirmation: pass --yes/-y`)
}

// TestPveTaskStop_SendsRequestWithConfirmation asserts that passing --yes
// issues the stop request and reports success.
func TestPveTaskStop_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/pve/remotes/cluster1/tasks/"+validUPID, &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveTaskStopCmd(), "stop", "cluster1", validUPID, "--yes")
	require.NoError(t, err)

	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), `Task `+validUPID+` on PVE remote "cluster1" stopped.`)
}
