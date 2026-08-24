package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestNodeDisksLs_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/disks/list", &rec, []map[string]any{
		// disk-type and status, not the documented type and health: see
		// nodeDiskEntry for what PBS actually sends here.
		{"devpath": "/dev/sda", "size": 1000000, "disk-type": "ssd", "vendor": "Acme",
			"model": "X1", "serial": "S123", "status": "passed"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "ls",
		"--include-partitions", "--skip-smart", "--usage-type", "journal_disks")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "1", rec.query.Get("include-partitions"))
	require.Equal(t, "1", rec.query.Get("skipsmart"))
	require.Equal(t, "journal_disks", rec.query.Get("usage-type"))
	require.Contains(t, buf.String(), "/dev/sda")
	require.Contains(t, buf.String(), "ssd")
	require.Contains(t, buf.String(), "passed")
}

func TestNodeDisksLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+nodeAPIBase+"/disks/list", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list disks on node")
}

func TestNodeDisksSmart_RequiresDisk(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "smart")
	require.Error(t, err)
}

func TestNodeDisksSmart_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/disks/smart", &rec, map[string]any{
		"status": "PASSED", "attributes": []any{map[string]any{"name": "reallocated"}}, "wearout": 95,
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "smart", "--disk", "sda", "--healthonly")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "sda", rec.query.Get("disk"))
	require.Equal(t, "1", rec.query.Get("healthonly"))
	require.Contains(t, buf.String(), "PASSED")
}

func TestNodeDisksInitgpt_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+nodeAPIBase+"/disks/initgpt", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "initgpt", "--disk", "sdb", "--uuid", "abc-123")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "sdb", rec.form.Get("disk"))
	require.Equal(t, "abc-123", rec.form.Get("uuid"))
	require.Contains(t, buf.String(), "initialized")
}

func TestNodeDisksWipe_RequiresYes(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "wipe", "--disk", "sda1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes/-y")
}

func TestNodeDisksWipe_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+nodeAPIBase+"/disks/wipedisk", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "wipe", "--disk", "sda1", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "sda1", rec.form.Get("disk"))
	require.Contains(t, buf.String(), validUPID)
}

// --- directory --------------------------------------------------------------

func TestNodeDisksDirectoryLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/disks/directory", &rec, []map[string]any{
		{"name": "store1", "path": "/mnt/datastore/store1", "type": "ext4", "status": "mounted"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "directory", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "store1")
}

func TestNodeDisksDirectoryCreate_RequiresDisk(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "directory", "create", "store1")
	require.Error(t, err)
}

func TestNodeDisksDirectoryCreate_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+nodeAPIBase+"/disks/directory", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "directory", "create", "store1",
		"--disk", "sdb", "--filesystem", "ext4", "--add-datastore")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "store1", rec.form.Get("name"))
	require.Equal(t, "sdb", rec.form.Get("disk"))
	require.Equal(t, "ext4", rec.form.Get("filesystem"))
	require.Equal(t, "1", rec.form.Get("add-datastore"))
	require.Contains(t, buf.String(), "created")
}

func TestNodeDisksDirectoryDelete_RequiresYes(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "directory", "delete", "store1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes/-y")
}

// A real PBS removes the mount unit synchronously and answers with a null
// UPID. There is no task to wait for and nothing went wrong, so the command has
// to report success rather than a decode failure.
func TestNodeDisksDirectoryDelete_SynchronousResponseSucceeds(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+nodeAPIBase+"/disks/directory/store1",
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":null}`))
		})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "directory", "delete",
		"store1", "--yes")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "removed")
}

func TestNodeDisksDirectoryDelete_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "DELETE "+nodeAPIBase+"/disks/directory/store1", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "directory", "delete", "store1", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Contains(t, buf.String(), "removed")
}

// --- zfs ----------------------------------------------------------------

func TestNodeDisksZfsLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/disks/zfs", &rec, []map[string]any{
		{"name": "tank", "health": "ONLINE", "size": 1000, "dedup": 1.47, "frag": 3},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "zfs", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "tank")
	require.Contains(t, buf.String(), "ONLINE")
	require.Contains(t, buf.String(), "1.47")
}

// PBS reports the deduplication ratio as a float, so a whole-numbered ratio
// arrives as 1.0 and must not render with a trailing ".0".
func TestNodeDisksZfsLs_RendersWholeDedupRatioWithoutFraction(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/disks/zfs", &rec, []map[string]any{
		{"name": "tank", "health": "ONLINE", "dedup": 1.0},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "zfs", "ls")
	require.NoError(t, err)

	require.Contains(t, buf.String(), "1")
	require.NotContains(t, buf.String(), "1.0")
}

func TestNodeDisksZfsShow_RendersRaw(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/disks/zfs/tank", &rec, map[string]any{"name": "tank", "state": "ONLINE"})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "zfs", "show", "tank")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "ONLINE")
}

func TestNodeDisksZfsCreate_RequiresDevicesAndRaidlevel(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "zfs", "create", "tank")
	require.Error(t, err)
}

func TestNodeDisksZfsCreate_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+nodeAPIBase+"/disks/zfs", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "disks", "zfs", "create", "tank",
		"--devices", "sdb,sdc", "--raidlevel", "mirror", "--ashift", "12", "--compression", "lz4", "--add-datastore")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "tank", rec.form.Get("name"))
	require.Equal(t, "sdb,sdc", rec.form.Get("devices"))
	require.Equal(t, "mirror", rec.form.Get("raidlevel"))
	require.Equal(t, "12", rec.form.Get("ashift"))
	require.Equal(t, "lz4", rec.form.Get("compression"))
	require.Equal(t, "1", rec.form.Get("add-datastore"))
	require.Contains(t, buf.String(), "created")
}
