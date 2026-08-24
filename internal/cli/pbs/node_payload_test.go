package pbs

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// capturedPayload loads a response captured verbatim from a live PBS 4.2
// server. Every decoder in this package hand-rolls its field tags from the
// documented schema, and PBS answers with different keys for several of them,
// so a fixture written from the schema proves nothing.
func capturedPayload(t *testing.T, name string) any {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)

	var payload any
	require.NoError(t, json.Unmarshal(raw, &payload))
	return payload
}

// renderCaptured runs one node verb against a captured payload and returns
// the rendered table.
func renderCaptured(t *testing.T, endpoint, fixture string, args ...string) string {
	t.Helper()

	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+endpoint, &rec, capturedPayload(t, fixture))

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, newNodeCmd(), args...))
	return buf.String()
}

// TestNodeTasksLs_CapturedPayload_FillsTypeAndID covers the worst of the four
// tag mismatches: PBS answers the task list with worker_type and worker_id,
// not the documented type and id, so TYPE and ID were blank on every row and
// the task list could not say what any task had done.
func TestNodeTasksLs_CapturedPayload_FillsTypeAndID(t *testing.T) {
	out := renderCaptured(t, "/tasks", "node_tasks.json", "node", "tasks", "ls")

	require.Contains(t, out, "verificationjob", "the task kind must reach the TYPE column")
	require.Contains(t, out, "backups:verify-stale", "the worker id must reach the ID column")
	require.Contains(t, out, "garbage_collection")
}

// TestNodeDisksLs_CapturedPayload_FillsTypeAndHealth covers the disk listing,
// which PBS answers with disk-type and status rather than the documented type
// and health.
func TestNodeDisksLs_CapturedPayload_FillsTypeAndHealth(t *testing.T) {
	out := renderCaptured(t, "/disks/list", "node_disks_list.json", "node", "disks", "ls")

	require.Contains(t, out, "/dev/nvme0n1")
	require.Contains(t, out, "ssd", "disk-type must reach the TYPE column")
	require.Contains(t, out, "passed", "status must reach the HEALTH column")
}

// TestNodeServicesLs_CapturedPayload_FillsActiveState covers the service
// list, which PBS answers with unit-state rather than the documented
// active-state.
func TestNodeServicesLs_CapturedPayload_FillsActiveState(t *testing.T) {
	out := renderCaptured(t, "/services", "node_services.json", "node", "services", "ls")

	require.Contains(t, out, "proxmox-backup")
	require.Contains(t, out, "enabled", "unit-state must reach the ACTIVE-STATE column")
}

// TestNodeAptVersions_CapturedPayload_FillsExtraInfo covers the package
// version list, where PBS sends ExtraInfo free text ("running kernel: ...",
// "running version: ...") and the decoder read a "runningkernel" key that
// does not exist.
func TestNodeAptVersions_CapturedPayload_FillsExtraInfo(t *testing.T) {
	out := renderCaptured(t, "/apt/versions", "node_apt_versions.json", "node", "apt", "versions")

	require.Contains(t, out, "proxmox-backup")
	// tablewriter spaces the hyphen in a header, so the rendered label is
	// "EXTRA - INFO".
	require.Contains(t, out, "EXTRA", "the column names what the field carries, not a kernel version")
	require.NotContains(t, out, "KERNEL", "the field is free text, not a kernel version")
	require.Contains(t, out, "running kernel")
}
