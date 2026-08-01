package pbs

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// permissionsPath is the /access/permissions endpoint.
const permissionsPath = "/api2/json/access/permissions"

func TestPermissionLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+permissionsPath, &rec, map[string]any{
		"/datastore/store1": map[string]any{"Datastore.Audit": true, "Datastore.Modify": false},
		"/":                 map[string]any{"Sys.Audit": true},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPermissionCmd(), "permission", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, permissionsPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "/datastore/store1")
	require.Contains(t, out, "Datastore.Audit")
	require.Contains(t, out, "Sys.Audit")
}

func TestPermissionLs_SendsAuthIdAndPath(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+permissionsPath, &rec, map[string]any{})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPermissionCmd(), "permission", "ls",
		"--auth-id", "alice@pbs", "--path", "/datastore/store1")
	require.NoError(t, err)

	require.Equal(t, "alice@pbs", rec.query.Get("auth-id"))
	require.Equal(t, "/datastore/store1", rec.query.Get("path"))
}

func TestPermissionLs_OmitsFiltersWhenUnset(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+permissionsPath, &rec, map[string]any{})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPermissionCmd(), "permission", "ls")
	require.NoError(t, err)

	for _, key := range []string{"auth-id", "path"} {
		_, present := rec.query[key]
		require.False(t, present, "%s must be omitted from the query when unset", key)
	}
}

func TestPermissionLs_HandlesEmptyResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET "+permissionsPath, &recordedRequest{}, map[string]any{})

	// A user with no permissions at all: the table still needs its header row,
	// and the raw view stays the path→privilege object this endpoint returns
	// rather than becoming null.
	var table bytes.Buffer
	require.NoError(t, run(depsFor(t, pc, output.FormatTable, false), &table, newPermissionCmd(), "permission", "ls"))
	require.Contains(t, table.String(), "PATH")
	require.Contains(t, table.String(), "PRIV")

	var raw bytes.Buffer
	require.NoError(t, run(depsFor(t, pc, output.FormatJSON, false), &raw, newPermissionCmd(), "permission", "ls"))
	require.Equal(t, "{}", strings.TrimSpace(raw.String()))
}

func TestPermissionLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+permissionsPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPermissionCmd(), "permission", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list permissions")
}
