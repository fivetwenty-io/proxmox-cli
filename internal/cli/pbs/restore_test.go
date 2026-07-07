package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// pathTapeRestore is the POST /tape/restore endpoint.
const pathTapeRestore = "/api2/json/tape/restore"

// tapeRestoreMediaSet is a sample media set UUID reused across restore tests.
const tapeRestoreMediaSet = "11111111-1111-1111-1111-111111111111"

func TestTapeRestore_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+pathTapeRestore, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeRestoreCmd(), "restore",
		"--drive", "drive1", "--media-set", tapeRestoreMediaSet, "--store", "store1")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, pathTapeRestore, rec.path)
	require.Equal(t, "drive1", rec.form.Get("drive"))
	require.Equal(t, tapeRestoreMediaSet, rec.form.Get("media-set"))
	require.Equal(t, "store1", rec.form.Get("store"))
	require.Contains(t, buf.String(),
		`Tape restore of media set "`+tapeRestoreMediaSet+`" to datastore "store1" finished.`)
}

func TestTapeRestore_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+pathTapeRestore, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeRestoreCmd(), "restore",
		"--drive", "drive1", "--media-set", tapeRestoreMediaSet, "--store", "store1")
	require.NoError(t, err)

	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "finished")
}

func TestTapeRestore_RequiresDrive(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeRestoreCmd(), "restore", "--media-set", tapeRestoreMediaSet, "--store", "store1")
	require.Error(t, err)
}

func TestTapeRestore_RequiresMediaSet(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeRestoreCmd(), "restore", "--drive", "drive1", "--store", "store1")
	require.Error(t, err)
}

func TestTapeRestore_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeRestoreCmd(), "restore", "--drive", "drive1", "--media-set", tapeRestoreMediaSet)
	require.Error(t, err)
}

func TestTapeRestore_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+pathTapeRestore, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "restore failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeRestoreCmd(), "restore",
		"--drive", "drive1", "--media-set", tapeRestoreMediaSet, "--store", "store1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "tape restore of media set")
}

func TestTapeRestore_RejectsNonUPIDResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("POST "+pathTapeRestore, "not-a-upid")

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeRestoreCmd(), "restore",
		"--drive", "drive1", "--media-set", tapeRestoreMediaSet, "--store", "store1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a UPID")
}

func TestTapeRestore_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+pathTapeRestore, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeRestoreCmd(), "restore",
		"--drive", "drive1",
		"--media-set", tapeRestoreMediaSet,
		"--store", "store1",
		"--namespaces", "ns1", "--namespaces", "ns2",
		"--notification-mode", "notification-system",
		"--notify-user", "root@pam",
		"--owner", "root@pam",
		"--snapshots", "vm/100/2024-01-01T00:00:00Z", "--snapshots", "vm/101/2024-01-01T00:00:00Z",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)

	want := map[string]string{
		"drive":             "drive1",
		"media-set":         tapeRestoreMediaSet,
		"store":             "store1",
		"notification-mode": "notification-system",
		"notify-user":       "root@pam",
		"owner":             "root@pam",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.ElementsMatch(t, []string{"ns1", "ns2"}, rec.form["namespaces"])
	require.ElementsMatch(t,
		[]string{"vm/100/2024-01-01T00:00:00Z", "vm/101/2024-01-01T00:00:00Z"}, rec.form["snapshots"])
}

func TestTapeRestore_OmitsUnsetOptionalFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+pathTapeRestore, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeRestoreCmd(), "restore",
		"--drive", "drive1", "--media-set", tapeRestoreMediaSet, "--store", "store1")
	require.NoError(t, err)

	for _, key := range []string{"namespaces", "notification-mode", "notify-user", "owner", "snapshots"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}
