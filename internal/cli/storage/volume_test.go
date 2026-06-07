package storage

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

const testVolume = "local:backup/vzdump-qemu-100-2026_01_01-00_00_00.vma.zst"

// volumeContentPath is the decoded node content endpoint for testVolume. The
// client percent-encodes the colon and slash, and the server decodes them back
// to this path before routing.
const volumeContentPath = "/api2/json/nodes/pve1/storage/local/content/" + testVolume

// TestVolumeGet_RendersAttributes verifies volume get reads the content endpoint
// for the volume and renders its attributes.
func TestVolumeGet_RendersAttributes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "GET "+volumeContentPath, &rec, map[string]any{
		"format":    "vma.zst",
		"size":      1048576,
		"used":      524288,
		"notes":     "pve-cli-e2e marker",
		"protected": 1,
		"path":      "/var/lib/vz/dump/vzdump-qemu-100.vma.zst",
	})

	out, err := run(t, f, "--node", "pve1", "volume", "get", testVolume)
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, volumeContentPath, rec.path)
	require.Contains(t, out, "pve-cli-e2e marker")
	require.Contains(t, out, "1048576")
}

// TestVolumeGet_InvalidVolumeID verifies a volume ID without a storage prefix is
// rejected before any request is made.
func TestVolumeGet_InvalidVolumeID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "--node", "pve1", "volume", "get", "no-colon-here")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid volume ID")
}

// TestVolumeGet_RequiresNode verifies the command fails without a node.
func TestVolumeGet_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "volume", "get", testVolume)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

// TestVolumeSet_ForwardsNotesAndProtected verifies set forwards both attributes
// to the content update endpoint.
func TestVolumeSet_ForwardsNotesAndProtected(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+volumeContentPath, &rec, nil)

	out, err := run(t, f, "--node", "pve1", "volume", "set", testVolume,
		"--notes", "pve-cli-e2e marker", "--protected")
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, volumeContentPath, rec.path)
	require.Equal(t, "pve-cli-e2e marker", rec.form.Get("notes"))
	require.Equal(t, "1", rec.form.Get("protected"))
	require.Contains(t, out, "updated")
}

// TestVolumeSet_ClearsNotesWithEmptyString verifies an explicit empty --notes is
// forwarded (to clear existing notes) rather than omitted.
func TestVolumeSet_ClearsNotesWithEmptyString(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+volumeContentPath, &rec, nil)

	_, err := run(t, f, "--node", "pve1", "volume", "set", testVolume, "--notes", "")
	require.NoError(t, err)
	_, ok := rec.form["notes"]
	require.True(t, ok, "empty notes must still be sent")
	require.Equal(t, "", rec.form.Get("notes"))
	_, hasProtected := rec.form["protected"]
	require.False(t, hasProtected, "protected must be omitted when unset")
}

// TestVolumeSet_RequiresAFlag verifies set with no attribute flag is rejected.
func TestVolumeSet_RequiresAFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "--node", "pve1", "volume", "set", testVolume)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nothing to update")
}

// TestVolumeCopy_BlocksUntilComplete verifies copy posts the target to the
// content endpoint, waits on the returned task, and reports success.
func TestVolumeCopy_BlocksUntilComplete(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:imgcopy::root@pam:"
	var rec recordedRequest
	recordJSON(f, "POST "+volumeContentPath, &rec, upid)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	out, err := run(t, f, "--node", "pve1", "volume", "copy", testVolume,
		"--target-volume", "zfs-1:vm-100-disk-0")
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, volumeContentPath, rec.path)
	require.Equal(t, "zfs-1:vm-100-disk-0", rec.form.Get("target"))
	require.Contains(t, out, "Copied")
}

// TestVolumeCopy_TargetNodeForwarded verifies --target-node is forwarded only
// when set.
func TestVolumeCopy_TargetNodeForwarded(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:imgcopy::root@pam:"
	var rec recordedRequest
	recordJSON(f, "POST "+volumeContentPath, &rec, upid)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	_, err := run(t, f, "--node", "pve1", "volume", "copy", testVolume,
		"--target-volume", "zfs-1:vm-100-disk-0", "--target-node", "pve2")
	require.NoError(t, err)
	require.Equal(t, "pve2", rec.form.Get("target_node"))
}

// TestVolumeCopy_RequiresTarget verifies --target is mandatory.
func TestVolumeCopy_RequiresTarget(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "--node", "pve1", "volume", "copy", testVolume)
	require.Error(t, err)
	require.Contains(t, err.Error(), "target")
}

// --- volume delete ---

// TestVolumeDelete_RequiresYes verifies delete refuses without --yes.
func TestVolumeDelete_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE "+volumeContentPath, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	_, err := run(t, f, "--node", "pve1", "volume", "delete", testVolume)
	require.Error(t, err)
	require.Contains(t, err.Error(), "without --yes")
	require.False(t, called, "delete must not contact the server without --yes")
}

// TestVolumeDelete_WithYes verifies delete issues DELETE to the content endpoint
// and reports success for a synchronous (null) response.
func TestVolumeDelete_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+volumeContentPath, &rec, nil)

	out, err := run(t, f, "--node", "pve1", "volume", "delete", testVolume, "--yes")
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, volumeContentPath, rec.path)
	require.Contains(t, out, "deleted")
}

// TestVolumeDelete_WithUpid verifies that a UPID response triggers task waiting.
func TestVolumeDelete_WithUpid(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000003:AABBCCDD:delobject::root@pam:"
	var rec recordedRequest
	recordJSON(f, "DELETE "+volumeContentPath, &rec, upid)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	out, err := run(t, f, "--node", "pve1", "volume", "delete", testVolume, "--yes")
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, rec.method)
	require.Contains(t, out, "deleted")
}

// TestVolumeDelete_InvalidVolumeID verifies a bare volume ID without a storage
// prefix is rejected after --yes validation.
func TestVolumeDelete_InvalidVolumeID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "--node", "pve1", "volume", "delete", "no-colon-here", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid volume ID")
}

// TestVolumeDelete_RequiresNode verifies the command fails without a node.
func TestVolumeDelete_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "volume", "delete", testVolume, "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

// TestVolumeDelete_ServerError verifies API errors are surfaced.
func TestVolumeDelete_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("DELETE "+volumeContentPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "locked")
	})

	_, err := run(t, f, "--node", "pve1", "volume", "delete", testVolume, "--yes")
	require.Error(t, err)
}

// --- volume alloc ---

// allocContentPath is the content endpoint for the alloc tests (no volume suffix,
// since alloc POSTs to the storage content collection).
const allocContentPath = "/api2/json/nodes/pve1/storage/local/content"

// TestVolumeAlloc_PostsParams verifies alloc sends all required params and renders
// the returned volume ID.
func TestVolumeAlloc_PostsParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "POST "+allocContentPath, &rec, "local:vm-200-disk-0")

	out, err := run(t, f, "--node", "pve1", "volume", "alloc",
		"--vmid", "200",
		"--filename", "local:vm-200-disk-0",
		"--size", "4G",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, allocContentPath, rec.path)
	require.Equal(t, "200", rec.form.Get("vmid"))
	require.Equal(t, "local:vm-200-disk-0", rec.form.Get("filename"))
	require.Equal(t, "4G", rec.form.Get("size"))
	require.Contains(t, out, "local:vm-200-disk-0")
}

// TestVolumeAlloc_ForwardsFormat verifies --format is forwarded when set.
func TestVolumeAlloc_ForwardsFormat(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "POST "+allocContentPath, &rec, "local:vm-201-disk-0")

	_, err := run(t, f, "--node", "pve1", "volume", "alloc",
		"--vmid", "201",
		"--filename", "local:vm-201-disk-0",
		"--size", "8G",
		"--format", "qcow2",
	)
	require.NoError(t, err)
	require.Equal(t, "qcow2", rec.form.Get("format"))
}

// TestVolumeAlloc_OmitsFormatWhenUnset verifies --format is omitted when not explicitly
// provided, since the storage plugin picks its own default.
func TestVolumeAlloc_OmitsFormatWhenUnset(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordJSON(f, "POST "+allocContentPath, &rec, "local:vm-202-disk-0")

	_, err := run(t, f, "--node", "pve1", "volume", "alloc",
		"--vmid", "202",
		"--filename", "local:vm-202-disk-0",
		"--size", "1G",
	)
	require.NoError(t, err)
	require.False(t, rec.form.Has("format"), "format must be omitted when not set")
}

// TestVolumeAlloc_RequiredFlags verifies that missing required flags are rejected.
func TestVolumeAlloc_RequiredFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing vmid",
			args: []string{"--filename", "local:vm-100-disk-0", "--size", "4G"},
			want: "vmid",
		},
		{
			name: "missing filename",
			args: []string{"--vmid", "100", "--size", "4G"},
			want: "filename",
		},
		{
			name: "missing size",
			args: []string{"--vmid", "100", "--filename", "local:vm-100-disk-0"},
			want: "size",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			_, err := run(t, f, append([]string{"--node", "pve1", "volume", "alloc"}, tc.args...)...)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestVolumeAlloc_InvalidFilenameFormat verifies that a --filename without a
// storage prefix is rejected with a clear error.
func TestVolumeAlloc_InvalidFilenameFormat(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "--node", "pve1", "volume", "alloc",
		"--vmid", "100",
		"--filename", "no-colon-here",
		"--size", "4G",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "storage")
}

// TestVolumeAlloc_RequiresNode verifies the command fails without a node.
func TestVolumeAlloc_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "volume", "alloc",
		"--vmid", "100",
		"--filename", "local:vm-100-disk-0",
		"--size", "4G",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

// TestVolumeAlloc_ServerError verifies API errors are surfaced.
func TestVolumeAlloc_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST "+allocContentPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "unsupported format")
	})

	_, err := run(t, f, "--node", "pve1", "volume", "alloc",
		"--vmid", "100",
		"--filename", "local:vm-100-disk-0",
		"--size", "4G",
	)
	require.Error(t, err)
}
