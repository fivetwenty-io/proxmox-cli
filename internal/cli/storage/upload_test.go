package storage

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// uploadCapture records the multipart fields the fake server received for an
// upload request. The Proxmox upload endpoint takes a multipart/form-data body,
// not a urlencoded one, so it cannot be captured by recordJSON.
type uploadCapture struct {
	path    string
	content string
	file    string
	dupName bool
}

// recordUpload registers a multipart handler that captures the content form
// field and the file part's filename, then replies with the task UPID. It also
// records whether "filename" was (incorrectly) sent as a duplicate form field,
// which the real PVE rejects.
func recordUpload(f *testhelper.FakePVE, pattern string, capt *uploadCapture, upid string) {
	f.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		capt.path = r.URL.Path
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			testhelper.WriteError(w, http.StatusBadRequest, "bad multipart")
			return
		}
		capt.content = r.FormValue("content")
		if files, ok := r.MultipartForm.File["filename"]; ok && len(files) > 0 {
			capt.file = files[0].Filename
		}
		if r.MultipartForm.Value["filename"] != nil {
			capt.dupName = true
		}
		testhelper.WriteData(w, upid)
	})
}

// writeTempFile creates a temporary file with the given base name and content,
// returning its absolute path.
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestStorageUpload_BlocksUntilComplete verifies the upload streams the file to
// the node storage endpoint, waits on the resulting task, and reports success.
func TestStorageUpload_BlocksUntilComplete(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:imgcopy::root@pam:"
	var capt uploadCapture
	recordUpload(f, "POST /api2/json/nodes/pve1/storage/local/upload", &capt, upid)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	path := writeTempFile(t, "pve-cli-test.iso", "fake-iso-bytes")
	out, err := run(t, f, "--node", "pve1", "upload", "local", "--file", path, "--content", "iso")
	require.NoError(t, err)

	require.Equal(t, "/api2/json/nodes/pve1/storage/local/upload", capt.path)
	require.Equal(t, "iso", capt.content)
	require.Equal(t, "pve-cli-test.iso", capt.file, "destination defaults to the source base name")
	require.False(t, capt.dupName, "filename must not be sent as a duplicate form field")
	require.Contains(t, out, "Uploaded")
}

// TestStorageUpload_FilenameOverride verifies --filename overrides the
// destination name independently of the local source path.
func TestStorageUpload_FilenameOverride(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:imgcopy::root@pam:"
	var capt uploadCapture
	recordUpload(f, "POST /api2/json/nodes/pve1/storage/local/upload", &capt, upid)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	path := writeTempFile(t, "local-name.iso", "data")
	_, err := run(t, f, "--node", "pve1", "upload", "local",
		"--file", path, "--filename", "pve-cli-renamed.iso")
	require.NoError(t, err)
	require.Equal(t, "pve-cli-renamed.iso", capt.file)
}

// TestStorageUpload_AsyncReturnsUPID verifies --async prints the task UPID
// without waiting for completion.
func TestStorageUpload_AsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:imgcopy::root@pam:"
	var capt uploadCapture
	recordUpload(f, "POST /api2/json/nodes/pve1/storage/local/upload", &capt, upid)

	path := writeTempFile(t, "pve-cli-test.iso", "data")
	out, err := run(t, f, "--async", "--node", "pve1", "upload", "local", "--file", path)
	require.NoError(t, err)
	require.Contains(t, out, upid)
}

// TestStorageUpload_RequiresFile verifies the command refuses to run without the
// required --file flag.
func TestStorageUpload_RequiresFile(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "--node", "pve1", "upload", "local")
	require.Error(t, err)
	require.Contains(t, err.Error(), "file")
}

// TestStorageUpload_OpenError verifies a missing local file is surfaced before
// any request is made.
func TestStorageUpload_OpenError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var called bool
	f.HandleFunc("POST /api2/json/nodes/pve1/storage/local/upload", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, "")
	})

	_, err := run(t, f, "--node", "pve1", "upload", "local", "--file", "/no/such/pve-cli-file.iso")
	require.Error(t, err)
	require.Contains(t, err.Error(), "open file")
	require.False(t, called, "no upload must be attempted when the source file cannot be opened")
}

// TestStorageUpload_RequiresNode verifies the node-scoped command fails clearly
// without a resolved node.
func TestStorageUpload_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	path := writeTempFile(t, "pve-cli-test.iso", "data")
	_, err := run(t, f, "upload", "local", "--file", path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}
