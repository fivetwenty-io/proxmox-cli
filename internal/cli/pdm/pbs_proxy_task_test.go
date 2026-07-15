package pdm

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// TestPbsTaskLs_PreservesServerOrder asserts that `pbs task ls` renders
// tasks in the order the server returned them, without re-sorting (a task
// list is a log, not a set of named entities).
func TestPbsTaskLs_PreservesServerOrder(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/pbs/remotes/backup1/tasks", []map[string]any{
		{"upid": "UPID:pbs-node1:00000002:...:second:", "node": "pbs-node1", "pid": 2, "pstart": 1, "starttime": 2000, "user": "root@pam", "worker_type": "aptupdate"},
		{"upid": "UPID:pbs-node1:00000001:...:first:", "node": "pbs-node1", "pid": 1, "pstart": 1, "starttime": 1000, "user": "root@pam", "worker_type": "aptupdate"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsTaskLsCmd(), "ls", "backup1")
	require.NoError(t, err)

	rows := buf.String()
	secondIdx := strings.Index(rows, "UPID:pbs-node1:00000002")
	firstIdx := strings.Index(rows, "UPID:pbs-node1:00000001")
	require.GreaterOrEqual(t, secondIdx, 0)
	require.GreaterOrEqual(t, firstIdx, 0)
	require.Less(t, secondIdx, firstIdx, "task rows must preserve server order, not be sorted")
}

// TestPbsTaskStatus_SendsWaitFlag asserts that `pbs task status` forwards
// --wait and renders the task's status fields.
func TestPbsTaskStatus_SendsWaitFlag(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pbs/remotes/backup1/tasks/"+validUPID+"/status", &rec, map[string]any{
		"node": "pbs-node1", "pid": 100, "pstart": 1, "starttime": 1, "type": "aptupdate",
		"upid": validUPID, "user": "root@pam", "status": "stopped", "exitstatus": "OK",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsTaskStatusCmd(), "status", "backup1", validUPID, "--wait")
	require.NoError(t, err)

	require.Equal(t, "1", rec.query.Get("wait"))
	require.Contains(t, buf.String(), "stopped")
}

// TestPbsTaskLog_ReadsLines asserts that `pbs task log` decodes the
// paginated log-line response.
func TestPbsTaskLog_ReadsLines(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pbs/remotes/backup1/tasks/"+validUPID+"/log", &rec, []map[string]any{
		{"n": 1, "t": "starting apt update"},
		{"n": 2, "t": "done"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsTaskLogCmd(), "log", "backup1", validUPID, "--start", "0", "--limit", "50")
	require.NoError(t, err)

	require.Equal(t, "0", rec.query.Get("start"))
	require.Equal(t, "50", rec.query.Get("limit"))
	require.Contains(t, buf.String(), "starting apt update")
	require.Contains(t, buf.String(), "done")
}

// TestPbsTaskStop_RefusesWithoutConfirmation asserts the --yes/-y gate on
// `pbs task stop` blocks the request entirely when unset.
func TestPbsTaskStop_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsTaskStopCmd(), "stop", "backup1", validUPID)
	require.Error(t, err)
	require.ErrorContains(t, err,
		`refusing to stop task "`+validUPID+`" on PBS remote "backup1" without confirmation: pass --yes/-y`)
}

// TestPbsTaskStop_SendsRequestWithConfirmation asserts that passing --yes
// issues the stop request and reports success.
func TestPbsTaskStop_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/pbs/remotes/backup1/tasks/"+validUPID, &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsTaskStopCmd(), "stop", "backup1", validUPID, "--yes")
	require.NoError(t, err)

	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), `Task `+validUPID+` on PBS remote "backup1" stopped.`)
}
