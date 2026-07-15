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
