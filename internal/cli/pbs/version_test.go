package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// ---------------------------------------------------------------------------
// version
// ---------------------------------------------------------------------------

func TestVersion_RendersDefaultFakeResponse(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newVersionCmd(), "version")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "3.4")
	require.Contains(t, buf.String(), "abc123de")
}

func TestVersion_RendersCustomResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/version", &rec, map[string]any{
		"release": "4", "repoid": "deadbeef", "version": "4.0",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newVersionCmd(), "version")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/version", rec.path)
	require.Contains(t, buf.String(), "4.0")
	require.Contains(t, buf.String(), "deadbeef")
}

func TestVersion_RejectsExtraArgs(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newVersionCmd(), "version", "unexpected")
	require.Error(t, err)
}

func TestVersion_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/version", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "server starting up")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newVersionCmd(), "version")
	require.Error(t, err)
	require.ErrorContains(t, err, "server starting up")
}

// ---------------------------------------------------------------------------
// ping
// ---------------------------------------------------------------------------

func TestPing_RendersDefaultFakeResponse(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPingCmd(), "ping")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "true")
}

func TestPing_JSONOutputContainsPong(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/ping", &rec, map[string]any{"pong": true})

	var buf bytes.Buffer
	err := run(deps, &buf, newPingCmd(), "ping")
	require.NoError(t, err)
	require.Equal(t, "/api2/json/ping", rec.path)
	require.Contains(t, buf.String(), `"pong": true`)
}

func TestPing_RejectsExtraArgs(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPingCmd(), "ping", "unexpected")
	require.Error(t, err)
}

func TestPing_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/ping", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "daemon offline")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPingCmd(), "ping")
	require.Error(t, err)
	require.ErrorContains(t, err, "daemon offline")
}
