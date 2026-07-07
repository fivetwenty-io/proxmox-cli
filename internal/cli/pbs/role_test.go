package pbs

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// rolesPath is the /access/roles endpoint.
const rolesPath = "/api2/json/access/roles"

func TestRoleLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+rolesPath, &rec, []map[string]any{
		{"roleid": "DatastoreReader", "privs": []string{"Datastore.Audit"}},
		{"roleid": "Admin", "privs": []string{"Sys.Modify", "Datastore.Modify"}, "comment": "full admin"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRoleCmd(), "role", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, rolesPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "Admin")
	require.Contains(t, out, "DatastoreReader")
	require.Contains(t, out, "Sys.Modify")
	require.Contains(t, out, "full admin")
}

func TestRoleLs_SortsByRoleid(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET "+rolesPath, &recordedRequest{}, []map[string]any{
		{"roleid": "Zebra", "privs": []string{}},
		{"roleid": "Audit", "privs": []string{"Sys.Audit"}},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRoleCmd(), "role", "ls")
	require.NoError(t, err)

	out := buf.String()
	require.Less(t, strings.Index(out, "Audit"), strings.Index(out, "Zebra"))
}

func TestRoleLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+rolesPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRoleCmd(), "role", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list roles")
}
