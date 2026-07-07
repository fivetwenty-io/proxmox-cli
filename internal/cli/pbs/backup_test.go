package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// pathTapeBackup is the POST /tape/backup endpoint.
const pathTapeBackup = "/api2/json/tape/backup"

func TestTapeBackup_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+pathTapeBackup, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeBackupCmd(), "backup",
		"--drive", "drive1", "--pool", "pool1", "--store", "store1")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, pathTapeBackup, rec.path)
	require.Equal(t, "drive1", rec.form.Get("drive"))
	require.Equal(t, "pool1", rec.form.Get("pool"))
	require.Equal(t, "store1", rec.form.Get("store"))
	require.Contains(t, buf.String(), `Tape backup of datastore "store1" to drive "drive1" finished.`)
}

func TestTapeBackup_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+pathTapeBackup, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeBackupCmd(), "backup",
		"--drive", "drive1", "--pool", "pool1", "--store", "store1")
	require.NoError(t, err)

	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "finished")
}

func TestTapeBackup_RequiresDrive(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeBackupCmd(), "backup", "--pool", "pool1", "--store", "store1")
	require.Error(t, err)
}

func TestTapeBackup_RequiresPool(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeBackupCmd(), "backup", "--drive", "drive1", "--store", "store1")
	require.Error(t, err)
}

func TestTapeBackup_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeBackupCmd(), "backup", "--drive", "drive1", "--pool", "pool1")
	require.Error(t, err)
}

func TestTapeBackup_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+pathTapeBackup, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "backup failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeBackupCmd(), "backup",
		"--drive", "drive1", "--pool", "pool1", "--store", "store1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "tape backup of datastore")
}

func TestTapeBackup_RejectsNonUPIDResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("POST "+pathTapeBackup, "not-a-upid")

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeBackupCmd(), "backup",
		"--drive", "drive1", "--pool", "pool1", "--store", "store1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a UPID")
}

func TestTapeBackup_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+pathTapeBackup, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeBackupCmd(), "backup",
		"--drive", "drive1",
		"--pool", "pool1",
		"--store", "store1",
		"--eject-media",
		"--export-media-set",
		"--force-media-set",
		"--group-filter", "type:vm", "--group-filter", "type:ct",
		"--latest-only",
		"--max-depth", "3",
		"--notification-mode", "notification-system",
		"--notify-user", "root@pam",
		"--ns", "myns",
		"--worker-threads", "4",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)

	want := map[string]string{
		"drive":             "drive1",
		"pool":              "pool1",
		"store":             "store1",
		"eject-media":       "1",
		"export-media-set":  "1",
		"force-media-set":   "1",
		"latest-only":       "1",
		"max-depth":         "3",
		"notification-mode": "notification-system",
		"notify-user":       "root@pam",
		"ns":                "myns",
		"worker-threads":    "4",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.ElementsMatch(t, []string{"type:vm", "type:ct"}, rec.form["group-filter"])
}

func TestTapeBackup_OmitsUnsetOptionalFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+pathTapeBackup, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeBackupCmd(), "backup",
		"--drive", "drive1", "--pool", "pool1", "--store", "store1")
	require.NoError(t, err)

	for _, key := range []string{
		"eject-media", "export-media-set", "force-media-set", "group-filter", "latest-only",
		"max-depth", "notification-mode", "notify-user", "ns", "worker-threads",
	} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}
