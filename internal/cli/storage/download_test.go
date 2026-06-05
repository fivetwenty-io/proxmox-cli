package storage

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestStorageDownloadURL_BlocksUntilComplete verifies download-url posts the URL,
// filename, and content to the node endpoint, waits on the task, and reports
// success.
func TestStorageDownloadURL_BlocksUntilComplete(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:download::root@pam:"
	var rec recordedRequest
	recordJSON(f, "POST /api2/json/nodes/pve1/storage/local/download-url", &rec, upid)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	out, err := run(t, f, "--node", "pve1", "download-url", "local",
		"--url", "https://example.test/pve-cli.iso", "--filename", "pve-cli.iso", "--content", "iso")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/storage/local/download-url", rec.path)
	require.Equal(t, "https://example.test/pve-cli.iso", rec.form.Get("url"))
	require.Equal(t, "pve-cli.iso", rec.form.Get("filename"))
	require.Equal(t, "iso", rec.form.Get("content"))
	require.Contains(t, out, "Downloaded")
}

// TestStorageDownloadURL_ForwardsOptionalFlags verifies the optional checksum,
// compression, and certificate flags are forwarded only when set.
func TestStorageDownloadURL_ForwardsOptionalFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:download::root@pam:"
	var rec recordedRequest
	recordJSON(f, "POST /api2/json/nodes/pve1/storage/local/download-url", &rec, upid)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	_, err := run(t, f, "--node", "pve1", "download-url", "local",
		"--url", "https://example.test/pve-cli.iso", "--filename", "pve-cli.iso",
		"--checksum", "deadbeef", "--checksum-algorithm", "sha256",
		"--compression", "gz", "--verify-certificates=false")
	require.NoError(t, err)

	require.Equal(t, "deadbeef", rec.form.Get("checksum"))
	require.Equal(t, "sha256", rec.form.Get("checksum-algorithm"))
	require.Equal(t, "gz", rec.form.Get("compression"))
	require.Equal(t, "0", rec.form.Get("verify-certificates"))
}

// TestStorageDownloadURL_OmitsUnsetOptionalFlags verifies optional parameters are
// absent from the request body when their flags are not supplied.
func TestStorageDownloadURL_OmitsUnsetOptionalFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:download::root@pam:"
	var rec recordedRequest
	recordJSON(f, "POST /api2/json/nodes/pve1/storage/local/download-url", &rec, upid)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	_, err := run(t, f, "--node", "pve1", "download-url", "local",
		"--url", "https://example.test/pve-cli.iso", "--filename", "pve-cli.iso")
	require.NoError(t, err)

	require.Empty(t, rec.form.Get("checksum"))
	require.Empty(t, rec.form.Get("compression"))
	// verify-certificates is omitted when the flag is left at its default.
	require.Empty(t, rec.form.Get("verify-certificates"))
}

// TestStorageDownloadURL_AsyncReturnsUPID verifies --async prints the task UPID
// without waiting for completion.
func TestStorageDownloadURL_AsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:download::root@pam:"
	var rec recordedRequest
	recordJSON(f, "POST /api2/json/nodes/pve1/storage/local/download-url", &rec, upid)

	out, err := run(t, f, "--async", "--node", "pve1", "download-url", "local",
		"--url", "https://example.test/pve-cli.iso", "--filename", "pve-cli.iso")
	require.NoError(t, err)
	require.Contains(t, out, upid)
}

// TestStorageDownloadURL_RequiresURL verifies the command refuses to run without
// the required --url flag.
func TestStorageDownloadURL_RequiresURL(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "--node", "pve1", "download-url", "local", "--filename", "pve-cli.iso")
	require.Error(t, err)
	require.Contains(t, err.Error(), "url")
}

// TestStorageDownloadURL_RequiresFilename verifies the command refuses to run
// without the required --filename flag.
func TestStorageDownloadURL_RequiresFilename(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "--node", "pve1", "download-url", "local", "--url", "https://example.test/x.iso")
	require.Error(t, err)
	require.Contains(t, err.Error(), "filename")
}

// TestStorageDownloadURL_RequiresNode verifies the node-scoped command fails
// clearly without a resolved node.
func TestStorageDownloadURL_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "download-url", "local",
		"--url", "https://example.test/x.iso", "--filename", "pve-cli.iso")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

// TestStorageTransfer_InTree verifies both transfer commands are registered on
// the storage group.
func TestStorageTransfer_InTree(t *testing.T) {
	root := newGroupCmd(nil)
	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["upload"], "storage must expose an upload sub-command")
	require.True(t, names["download-url"], "storage must expose a download-url sub-command")
}
