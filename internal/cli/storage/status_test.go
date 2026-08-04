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
func TestStorageNodeScoped_AutoResolvesNode(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		pattern string
		payload map[string]any
	}{
		{name: "status", args: []string{"status", testStorage},
			pattern: "GET /api2/json/nodes/pve2/storage/local/status",
			payload: map[string]any{"type": "dir", "content": "iso"}},
		{name: "identity", args: []string{"identity", testStorage},
			pattern: "GET /api2/json/nodes/pve2/storage/local/identity",
			payload: map[string]any{"id": "/var/lib/vz", "type": "dir"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			storageOnNodes(f, testStorage, "pve2")
			var rec recordedRequest
			recordJSON(f, tc.pattern, &rec, tc.payload)

			_, err := run(t, f, tc.args...)
			require.NoError(t, err)
			require.NotEmpty(t, rec.method, "request must hit the resolved node")
		})
	}
}

// TestStorageStatus_EmptyNodeFlagAutoResolves verifies `--node ""` behaves as
// if the flag were absent: the node is still resolved from the cluster instead
// of issuing a request against /nodes//storage/.
func TestStorageStatus_EmptyNodeFlagAutoResolves(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	storageOnNodes(f, testStorage, "pve2")
	var rec recordedRequest
	recordJSON(f, "GET /api2/json/nodes/pve2/storage/local/status", &rec,
		map[string]any{"type": "dir", "content": "iso"})

	_, err := run(t, f, "--node", "", "status", testStorage)
	require.NoError(t, err)
	require.Equal(t, "/api2/json/nodes/pve2/storage/local/status", rec.path)
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
