package pdm

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestPermissionLs_SendsFiltersAndRendersTree asserts that `permission ls`
// forwards --auth-id/--path and renders a flattened path/priv/propagate table.
func TestPermissionLs_SendsFiltersAndRendersTree(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/access/permissions", &rec, map[string]any{
		"/vms":     map[string]any{"VM.Audit": true, "VM.Config.Disk": false},
		"/storage": map[string]any{"Datastore.Audit": true},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPermissionLsCmd(), "ls", "--auth-id", "alpha@pam", "--path", "/vms")
	require.NoError(t, err)

	require.Equal(t, "alpha@pam", rec.query.Get("auth-id"))
	require.Equal(t, "/vms", rec.query.Get("path"))

	out := buf.String()
	require.Contains(t, out, "/storage")
	require.Contains(t, out, "Datastore.Audit")
	require.Contains(t, out, "/vms")
	require.Contains(t, out, "VM.Audit")
	require.Contains(t, out, "VM.Config.Disk")
}

// TestPermissionLs_EmptyResponseRendersNoRows asserts that an empty/absent
// permissions response renders without error.
func TestPermissionLs_EmptyResponseRendersNoRows(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/access/permissions", map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newPermissionLsCmd(), "ls")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "{}")
}
