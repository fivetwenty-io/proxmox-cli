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
