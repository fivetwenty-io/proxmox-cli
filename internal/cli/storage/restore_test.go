package storage

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
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

// TestFileRestoreList_DecodesReturnedFilepaths verifies the base64 filepath the
// API returns is decoded before it is rendered, so a listed entry can be passed
// straight back to --filepath or to `download` without the caller decoding it.
func TestFileRestoreList_DecodesReturnedFilepaths(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var method string
	var q url.Values
	recordQuery(f, "GET /api2/json/nodes/pve1/storage/pbs/file-restore/list", &method, &q, []map[string]any{
		{"filepath": base64.StdEncoding.EncodeToString([]byte("/root.pxar.didx")),
			"type": "d", "leaf": 0, "text": "root.pxar.didx"},
		{"filepath": base64.StdEncoding.EncodeToString([]byte("root.pxar.didx/etc/hostname")),
			"type": "f", "leaf": 1, "text": "hostname", "size": 12},
	})

	out, err := run(t, f, "--node", "pve1", "-o", "json", "file-restore", "list", "pbs",
		"--volume", "backup/ct/100/2026-01-01T00:00:00Z")
	require.NoError(t, err)
	require.Contains(t, out, "/root.pxar.didx")
	require.Contains(t, out, "root.pxar.didx/etc/hostname")
	require.NotContains(t, out, base64.StdEncoding.EncodeToString([]byte("/root.pxar.didx")))
}

// TestFileRestoreList_KeepsUndecodableFilepath verifies an entry whose filepath
// is not base64 is rendered unchanged rather than dropped or mangled.
func TestFileRestoreList_KeepsUndecodableFilepath(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var method string
	var q url.Values
	recordQuery(f, "GET /api2/json/nodes/pve1/storage/pbs/file-restore/list", &method, &q, []map[string]any{
		{"filepath": "/not base64 at all", "type": "f", "leaf": 1},
	})

	out, err := run(t, f, "--node", "pve1", "-o", "json", "file-restore", "list", "pbs",
		"--volume", "snap")
	require.NoError(t, err)
	require.Contains(t, out, "/not base64 at all")
}

// TestFileRestore_RequiredFlags verifies that file-restore sub-commands fail when
// a required flag is omitted.
func TestFileRestore_RequiredFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "list missing volume",
			args:    []string{"--node", "pve1", "file-restore", "list", "pbs"},
			wantErr: "volume",
		},
		{
			name:    "download missing filepath",
			args:    []string{"--node", "pve1", "file-restore", "download", "pbs", "--volume", "snap"},
			wantErr: "filepath",
		},
		{
			name:    "import-metadata missing volume",
			args:    []string{"--node", "pve1", "import-metadata", "import"},
			wantErr: "volume",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			_, err := run(t, f, tc.args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestFileRestore_RequiresNode verifies that node-scoped restore commands fail
// clearly without a resolved node.
func TestFileRestore_AutoResolvesNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	storageOnNodes(f, "pbs", "pve2")
	var rec recordedRequest
	recordJSON(f, "GET /api2/json/nodes/pve2/storage/pbs/file-restore/list", &rec, []any{})

	_, err := run(t, f, "file-restore", "list", "pbs", "--volume", "snap")
	require.NoError(t, err)
	require.NotEmpty(t, rec.method, "request must hit the resolved node")
}

// TestFileRestoreDownload_WritesToOutputFile verifies the download command
// writes the returned bytes to --output-file and reports the byte count.
func TestFileRestoreDownload_WritesToOutputFile(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var q url.Values
	// The endpoint answers with the restored file's own bytes, not the API's
	// JSON envelope, so the fake serves them raw.
	body := []byte("file-contents\n")
	f.HandleFunc("GET /api2/json/nodes/pve1/storage/pbs/file-restore/download",
		func(w http.ResponseWriter, r *http.Request) {
			q = r.URL.Query()
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(body)
		})

	dir := t.TempDir()
	dst := filepath.Join(dir, "out.bin")
	out, err := run(t, f, "--node", "pve1", "file-restore", "download", "pbs",
		"--volume", "snap", "--filepath", "/etc/hostname", "--output-file", dst)
	require.NoError(t, err)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("/etc/hostname")), q.Get("filepath"))
	require.Equal(t, "snap", q.Get("volume"))
	require.Contains(t, out, "Wrote")

	data, readErr := os.ReadFile(dst)
	require.NoError(t, readErr)
	require.Equal(t, body, data)
}

// TestFileRestoreDownload_WritesBinaryVerbatim verifies a payload that is not
// valid JSON — every real download — reaches the disk byte for byte, rather
// than failing the decode the rest of the API's responses go through.
func TestFileRestoreDownload_WritesBinaryVerbatim(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	body := []byte{0x50, 0x4b, 0x03, 0x04, 0x00, 0xff, 0xfe, 0x7f, 0x00, 0x01}
	f.HandleFunc("GET /api2/json/nodes/pve1/storage/pbs/file-restore/download",
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(body)
		})

	dir := t.TempDir()
	dst := filepath.Join(dir, "out.zip")
	_, err := run(t, f, "--node", "pve1", "file-restore", "download", "pbs",
		"--volume", "snap", "--filepath", "/etc", "--output-file", dst)
	require.NoError(t, err)

	data, readErr := os.ReadFile(dst)
	require.NoError(t, readErr)
	require.Equal(t, body, data)
}

// TestFileRestoreDownload_TarForwarded verifies --tar is forwarded only when set.
func TestFileRestoreDownload_TarForwarded(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var q url.Values
	f.HandleFunc("GET /api2/json/nodes/pve1/storage/pbs/file-restore/download",
		func(w http.ResponseWriter, r *http.Request) {
			q = r.URL.Query()
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("x"))
		})

	dir := t.TempDir()
	dst := filepath.Join(dir, "out.tar.zst")
	_, err := run(t, f, "--node", "pve1", "file-restore", "download", "pbs",
		"--volume", "snap", "--filepath", "/etc", "--tar", "--output-file", dst)
	require.NoError(t, err)
	require.Equal(t, "1", q.Get("tar"))
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
