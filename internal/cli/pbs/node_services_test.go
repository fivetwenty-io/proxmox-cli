package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

func TestNodeServicesLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/services", &rec, []map[string]any{
		{"service": "proxmox-backup", "name": "proxmox-backup.service", "state": "running",
			"desc": "PBS core", "active-state": "active"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "services", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "proxmox-backup")
	require.Contains(t, buf.String(), "active")
}

func TestNodeServicesLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+nodeAPIBase+"/services", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "services", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list services on node")
}

func TestNodeServicesShow_RendersFriendlySingle(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/services/proxmox-backup/state", &rec, map[string]any{
		"service": "proxmox-backup", "state": "running", "desc": "PBS core",
		"active-state": "active", "unit-state": "enabled",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "services", "show", "proxmox-backup")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "enabled")
}

func TestNodeServicesState_RendersFullObject(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/services/proxmox-backup/state", &rec, map[string]any{
		"service": "proxmox-backup", "state": "running", "desc": "PBS core",
		"active-state": "active", "unit-state": "enabled",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "services", "state", "proxmox-backup")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "unit-state")
	require.Contains(t, buf.String(), "enabled")
}

func TestNodeServicesState_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+nodeAPIBase+"/services/proxmox-backup/state", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "services", "state", "proxmox-backup")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get state of service")
}

func TestNodeServicesStart_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+nodeAPIBase+"/services/proxmox-backup/start", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "services", "start", "proxmox-backup")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Contains(t, buf.String(), "started")
}

func TestNodeServicesStop_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+nodeAPIBase+"/services/proxmox-backup/stop", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "services", "stop", "proxmox-backup")
	require.NoError(t, err)

	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "stopped.")
}

func TestNodeServicesRestart_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+nodeAPIBase+"/services/proxmox-backup/restart", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "services", "restart", "proxmox-backup")
	require.Error(t, err)
	require.Contains(t, err.Error(), "restart service")
}

func TestNodeServicesReload_SendsRequest(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+nodeAPIBase+"/services/proxmox-backup/reload", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "services", "reload", "proxmox-backup")
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)
	require.Contains(t, buf.String(), "reloaded")
}
