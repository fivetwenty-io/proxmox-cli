package storage

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

const testStorage = "local"

// statusPath is the node-scoped status endpoint for testStorage.
const statusPath = "/api2/json/nodes/pve1/storage/local/status"

// identityPath is the node-scoped identity endpoint for testStorage.
const identityPath = "/api2/json/nodes/pve1/storage/local/identity"

// TestStorageStatus_RendersFields verifies `pmx pve storage status` queries the
// correct endpoint and renders used/total/avail in the output.
func TestStorageStatus_RendersFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "GET "+statusPath, &rec, map[string]any{
		"type":    "dir",
		"content": "iso,backup",
		"total":   10737418240,
		"used":    2147483648,
		"avail":   8589934592,
		"active":  1,
		"enabled": 1,
		"shared":  0,
	})

	out, err := run(t, f, "--node", "pve1", "status", testStorage)
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, statusPath, rec.path)
	require.Contains(t, out, "10737418240")
	require.Contains(t, out, "2147483648")
	require.Contains(t, out, "8589934592")
	require.Contains(t, out, "dir")
}

// TestStorageNodeScoped_RequiresNode verifies that node-scoped storage commands
// fail clearly when no node is set.
func TestStorageNodeScoped_RequiresNode(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "status", args: []string{"status", testStorage}},
		{name: "identity", args: []string{"identity", testStorage}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			_, err := run(t, f, tc.args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), "no node specified")
		})
	}
}

// TestStorageStatus_ServerError verifies API errors are surfaced.
func TestStorageStatus_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET "+statusPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such storage")
	})

	_, err := run(t, f, "--node", "pve1", "status", testStorage)
	require.Error(t, err)
}

// TestStorageIdentity_RendersIdAndType verifies `pmx pve storage identity` renders
// the backend id and type fields.
func TestStorageIdentity_RendersIdAndType(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "GET "+identityPath, &rec, map[string]any{
		"id":   "/var/lib/vz",
		"type": "dir",
	})

	out, err := run(t, f, "--node", "pve1", "identity", testStorage)
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, identityPath, rec.path)
	require.Contains(t, out, "/var/lib/vz")
	require.Contains(t, out, "dir")
}

// TestStorageIdentity_ServerError verifies API errors are surfaced.
func TestStorageIdentity_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET "+identityPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "backend error")
	})

	_, err := run(t, f, "--node", "pve1", "identity", testStorage)
	require.Error(t, err)
}
