package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestNodeTasksLs_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/tasks", &rec, []map[string]any{
		{"upid": validUPID, "node": nodeDefaultName, "pid": 1, "pstart": 1, "starttime": 1000,
			"status": "running", "type": "gc", "user": "root@pam"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "tasks", "ls",
		"--errors", "--running", "--limit", "5", "--start", "1", "--since", "100", "--until", "200",
		"--status", "OK", "--status", "running", "--store", "store1", "--type", "gc", "--user", "root@pam",
	)
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "1", rec.query.Get("errors"))
	require.Equal(t, "1", rec.query.Get("running"))
	require.Equal(t, "5", rec.query.Get("limit"))
	require.Equal(t, "1", rec.query.Get("start"))
	require.Equal(t, "100", rec.query.Get("since"))
	require.Equal(t, "200", rec.query.Get("until"))
	require.Equal(t, []string{"OK", "running"}, rec.query["statusfilter"])
	require.Equal(t, "store1", rec.query.Get("store"))
	require.Equal(t, "gc", rec.query.Get("typefilter"))
	require.Equal(t, "root@pam", rec.query.Get("userfilter"))
	require.Contains(t, buf.String(), validUPID)
}

func TestNodeTasksLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+nodeAPIBase+"/tasks", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "tasks", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list tasks on node")
}

func TestNodeTasksShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/tasks/"+validUPID+"/status", &rec, map[string]any{
		"upid": validUPID, "node": nodeDefaultName, "pid": 1, "pstart": 1, "starttime": 1000,
		"status": "stopped", "type": "gc", "user": "root@pam", "exitstatus": "OK",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "tasks", "show", validUPID)
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "OK")
}

func TestNodeTasksLog_RendersLines(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/tasks/"+validUPID+"/log", &rec, []map[string]any{
		{"n": 1, "t": "task log line"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "tasks", "log", validUPID, "--start", "0", "--limit", "50")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "0", rec.query.Get("start"))
	require.Equal(t, "50", rec.query.Get("limit"))
	require.Contains(t, buf.String(), "task log line")
}

func TestNodeTasksLog_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+nodeAPIBase+"/tasks/"+validUPID+"/log", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "tasks", "log", validUPID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read log of task")
}

func TestNodeTasksDelete_Stops(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+nodeAPIBase+"/tasks/"+validUPID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "tasks", "delete", validUPID)
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, rec.method)
	require.Contains(t, buf.String(), "stopped")
}

func TestNodeTasksDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+nodeAPIBase+"/tasks/"+validUPID, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "tasks", "delete", validUPID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stop task")
}
