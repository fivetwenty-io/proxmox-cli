package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestNodeSyslog_RendersLines(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/syslog", &rec, []map[string]any{
		{"n": 1, "t": "line one"},
		{"n": 2, "t": "line two"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "syslog", "--service", "proxmox-backup", "--limit", "10")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "proxmox-backup", rec.query.Get("service"))
	require.Equal(t, "10", rec.query.Get("limit"))
	require.Contains(t, buf.String(), "line one")
	require.Contains(t, buf.String(), "line two")
}

func TestNodeSyslog_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+nodeAPIBase+"/syslog", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "syslog")
	require.Error(t, err)
	require.Contains(t, err.Error(), "read syslog")
}

func TestNodeJournal_RendersLines(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/journal", &rec, []string{"journal line 1", "journal line 2"})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "journal", "--lastentries", "50")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "50", rec.query.Get("lastentries"))
	require.Contains(t, buf.String(), "journal line 1")
}

func TestNodeJournal_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+nodeAPIBase+"/journal", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "journal")
	require.Error(t, err)
	require.Contains(t, err.Error(), "read journal")
}

func TestNodeJournal_ForwardsFilterFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/journal", &rec, []string{"line"})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "journal",
		"--priority", "3", "--service", "proxmox-backup*", "--unit", "proxmox-backup-proxy", "--kernel")
	require.NoError(t, err)

	require.Equal(t, "3", rec.query.Get("priority"))
	require.Equal(t, "proxmox-backup*", rec.query.Get("service"))
	require.Equal(t, "proxmox-backup-proxy", rec.query.Get("unit"))
	require.Equal(t, "1", rec.query.Get("kernel"))
	require.Empty(t, rec.query.Get("structured"))
}

func TestNodeJournal_StructuredUsesRawTransport(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/journal", &rec, []map[string]any{
		{"c": "s=85fd;i=1f2a53", "ty": "cursor"},
		{"h": "pbs-0", "ty": "host"},
		{"id": "proxmox-backup-proxy", "msg": "started", "p": 4, "pid": 1200, "t": 1725000000123456},
		{"c": "s=85fd;i=1f2a54", "ty": "cursor"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "journal", "--structured", "--lastentries", "5")
	require.NoError(t, err)

	require.Equal(t, "1", rec.query.Get("structured"))
	require.Equal(t, "5", rec.query.Get("lastentries"))
	require.Contains(t, buf.String(), "TIMESTAMP")
	require.Contains(t, buf.String(), "2024-08-30T06:40:00Z")
	require.Contains(t, buf.String(), "started")
	require.NotContains(t, buf.String(), "pbs-0", "host marker is not a row")
}

func TestNodeJournal_UnitsListsDistinctUnits(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/journal", &rec, []map[string]any{
		{"names": []string{"proxmox-backup-proxy.service", "proxmox-backup.service"}},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "journal", "--structured", "--units")
	require.NoError(t, err)

	require.Equal(t, "1", rec.query.Get("structured"))
	require.Equal(t, "1", rec.query.Get("units"))
	require.Contains(t, buf.String(), "UNIT")
	require.Contains(t, buf.String(), "proxmox-backup-proxy.service")
}

func TestNodeJournal_RejectsUnitsWithoutStructured(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "journal", "--units")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--units requires --structured")
}
