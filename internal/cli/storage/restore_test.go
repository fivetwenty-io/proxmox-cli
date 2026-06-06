package storage

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// recordQuery registers a handler that records the request method, path, and
// query parameters of a GET request and replies with the PVE {"data": payload}
// envelope. GET parameters are encoded by the client into the query string.
func recordQuery(f *testhelper.FakePVE, pattern string, method *string, gotQuery *url.Values, payload any) {
	f.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		*method = r.Method
		q := r.URL.Query()
		*gotQuery = q
		testhelper.WriteData(w, payload)
	})
}

// TestFileRestoreList_RendersEntries verifies the file-restore list command
// queries the node endpoint with the volume and a root filepath, and renders
// the returned directory entries.
func TestFileRestoreList_RendersEntries(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var method string
	var q url.Values
	recordQuery(f, "GET /api2/json/nodes/pve1/storage/pbs/file-restore/list", &method, &q, []map[string]any{
		{"filepath": "/etc", "type": "d", "leaf": 0, "size": 4096},
		{"filepath": "/etc/hostname", "type": "f", "leaf": 1, "size": 12},
	})

	out, err := run(t, f, "--node", "pve1", "file-restore", "list", "pbs",
		"--volume", "backup/vm/100/2026-01-01T00:00:00Z")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, method)
	require.Equal(t, "backup/vm/100/2026-01-01T00:00:00Z", q.Get("volume"))
	require.Equal(t, "/", q.Get("filepath"))
	require.Contains(t, out, "/etc/hostname")
}

// TestFileRestoreList_EncodesNonRootFilepath verifies a non-root --filepath is
// base64-encoded before being sent to the API.
func TestFileRestoreList_EncodesNonRootFilepath(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var method string
	var q url.Values
	recordQuery(f, "GET /api2/json/nodes/pve1/storage/pbs/file-restore/list", &method, &q, []map[string]any{})

	_, err := run(t, f, "--node", "pve1", "file-restore", "list", "pbs",
		"--volume", "snap", "--filepath", "/etc")
	require.NoError(t, err)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("/etc")), q.Get("filepath"))
}

// TestFileRestoreList_RequiresVolume verifies --volume is mandatory.
func TestFileRestoreList_RequiresVolume(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "--node", "pve1", "file-restore", "list", "pbs")
	require.Error(t, err)
	require.Contains(t, err.Error(), "volume")
}

// TestFileRestoreList_RequiresNode verifies the command fails without a node.
func TestFileRestoreList_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "file-restore", "list", "pbs", "--volume", "snap")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

// TestFileRestoreDownload_WritesToOutputFile verifies the download command
// writes the returned bytes to --output-file and reports the byte count.
func TestFileRestoreDownload_WritesToOutputFile(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var q url.Values
	f.HandleFunc("GET /api2/json/nodes/pve1/storage/pbs/file-restore/download",
		func(w http.ResponseWriter, r *http.Request) {
			q = r.URL.Query()
			testhelper.WriteData(w, "file-contents")
		})

	dir := t.TempDir()
	dst := filepath.Join(dir, "out.bin")
	out, err := run(t, f, "--node", "pve1", "file-restore", "download", "pbs",
		"--volume", "snap", "--filepath", "/etc/hostname", "--output-file", dst)
	require.NoError(t, err)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("/etc/hostname")), q.Get("filepath"))
	require.Contains(t, out, "Wrote")

	data, readErr := os.ReadFile(dst)
	require.NoError(t, readErr)
	require.NotEmpty(t, data)
}

// TestFileRestoreDownload_TarForwarded verifies --tar is forwarded only when set.
func TestFileRestoreDownload_TarForwarded(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var q url.Values
	f.HandleFunc("GET /api2/json/nodes/pve1/storage/pbs/file-restore/download",
		func(w http.ResponseWriter, r *http.Request) {
			q = r.URL.Query()
			testhelper.WriteData(w, "x")
		})

	dir := t.TempDir()
	dst := filepath.Join(dir, "out.tar.zst")
	_, err := run(t, f, "--node", "pve1", "file-restore", "download", "pbs",
		"--volume", "snap", "--filepath", "/etc", "--tar", "--output-file", dst)
	require.NoError(t, err)
	require.Equal(t, "1", q.Get("tar"))
}

// TestFileRestoreDownload_RequiresFilepath verifies --filepath is mandatory.
func TestFileRestoreDownload_RequiresFilepath(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "--node", "pve1", "file-restore", "download", "pbs", "--volume", "snap")
	require.Error(t, err)
	require.Contains(t, err.Error(), "filepath")
}

// TestImportMetadata_RendersFields verifies import-metadata queries the endpoint
// with the volume and renders the detected guest type and source.
func TestImportMetadata_RendersFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var method string
	var q url.Values
	recordQuery(f, "GET /api2/json/nodes/pve1/storage/import/import-metadata", &method, &q, map[string]any{
		"source": "esxi",
		"type":   "vm",
		"create-args": map[string]any{
			"name":   "imported",
			"memory": 2048,
		},
	})

	out, err := run(t, f, "--node", "pve1", "import-metadata", "import",
		"--volume", "import:vm.ova")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, method)
	require.Equal(t, "import:vm.ova", q.Get("volume"))
	require.Contains(t, out, "esxi")
}

// TestImportMetadata_RequiresVolume verifies --volume is mandatory.
func TestImportMetadata_RequiresVolume(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "--node", "pve1", "import-metadata", "import")
	require.Error(t, err)
	require.Contains(t, err.Error(), "volume")
}
