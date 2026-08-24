package pdm

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// capturedPayload loads a response captured verbatim from a live PDM 1.1
// server. The decoders in this package hand-roll their field tags from the
// documented schema, and PDM answers with different keys for several of them,
// so a fixture written from the schema proves nothing.
func capturedPayload(t *testing.T, name string) any {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)

	var payload any
	require.NoError(t, json.Unmarshal(raw, &payload))
	return payload
}

// TestNodeTaskLs_CapturedPayload_FillsType covers the tag mismatch that left
// the TYPE column blank on every row: PDM answers the task list with
// worker_type, not the documented type, so the task list could not say what
// any task had done.
func TestNodeTaskLs_CapturedPayload_FillsType(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET /api2/json/nodes/localhost/tasks", &rec,
		capturedPayload(t, "node_tasks.json"))

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, newNodeTaskLsCmd(), "ls", "localhost"))

	out := buf.String()
	require.Contains(t, out, "aptupdate", "the task kind must reach the TYPE column")
	require.Contains(t, out, "logrotate")
}

// TestNodeTaskLs_FillsIDFromWorkerID pins the other half of the same tag.
// PDM leaves worker_id null on its own node's tasks, so the captured payload
// cannot show the ID column filling; this drives the field directly.
func TestNodeTaskLs_FillsIDFromWorkerID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET /api2/json/nodes/localhost/tasks", &rec, []map[string]any{{
		"upid": validUPID, "node": "localhost", "pid": 100, "pstart": 1,
		"starttime": 1000, "status": "OK", "user": "root@pam",
		"worker_type": "qmstart", "worker_id": "4461",
	}})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, newNodeTaskLsCmd(), "ls", "localhost"))

	out := buf.String()
	require.Contains(t, out, "qmstart")
	require.Contains(t, out, "4461", "the worker id must reach the ID column")
}

// TestNodeAptVersions_CapturedPayload_FillsExtraInfo covers the column that
// named a field PDM never sends. The value is there under ExtraInfo, and for
// the kernel meta package it is the running kernel the old column name
// promised.
func TestNodeAptVersions_CapturedPayload_FillsExtraInfo(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET /api2/json/nodes/localhost/apt/versions", &rec,
		capturedPayload(t, "node_apt_versions.json"))

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, newNodeAptVersionsCmd(), "versions", "localhost"))

	out := buf.String()
	require.Contains(t, out, "running kernel:", "the extra info must reach its column")
	require.Contains(t, out, "EXTRA", "the column names what the field carries")
	require.NotContains(t, out, "KERNEL │", "the column no longer promises a kernel version")
}
