package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// tapeJobConfigPath is the base /config/tape-backup-job endpoint (`tape job` CRUD).
const tapeJobConfigPath = "/api2/json/config/tape-backup-job"

// tapeJobBackupPath is the /tape/backup endpoint (`tape job run`, `tape job status`).
const tapeJobBackupPath = "/api2/json/tape/backup"

// tapeJobID is the sample job ID reused across `tape job` tests.
const tapeJobID = "job1"

// --- tape job ls -----------------------------------------------------------

func TestTapeJobLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+tapeJobConfigPath, []map[string]any{
		{"id": "job2", "store": "store2", "pool": "pool2", "drive": "drive0", "schedule": "daily"},
		{"id": "job1", "store": "store1", "pool": "pool1", "drive": "drive0", "schedule": "hourly", "comment": "nightly"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "ls")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "job1")
	require.Contains(t, out, "job2")
	require.Contains(t, out, "nightly")
}

func TestTapeJobLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+tapeJobConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list tape backup jobs")
}

// --- tape job show -----------------------------------------------------------

func TestTapeJobShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+tapeJobConfigPath+"/"+tapeJobID, map[string]any{
		"id": tapeJobID, "store": "store1", "pool": "pool1", "drive": "drive0", "schedule": "daily",
		"comment": "nightly job",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "show", tapeJobID)
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "nightly job")
	require.Contains(t, out, "daily")
}

func TestTapeJobShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+tapeJobConfigPath+"/"+tapeJobID, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such job")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "show", tapeJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "show tape backup job")
}

// --- tape job add --------------------------------------------------------

func TestTapeJobAdd_CreatesJob(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapeJobConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "add", tapeJobID,
		"--drive", "drive0", "--pool", "pool1", "--store", "store1", "--schedule", "daily")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapeJobConfigPath, rec.path)
	require.Equal(t, tapeJobID, rec.form.Get("id"))
	require.Equal(t, "drive0", rec.form.Get("drive"))
	require.Equal(t, "pool1", rec.form.Get("pool"))
	require.Equal(t, "store1", rec.form.Get("store"))
	require.Equal(t, "daily", rec.form.Get("schedule"))
	require.Contains(t, buf.String(), "Tape backup job \"job1\" created.")
}

func TestTapeJobAdd_RequiresDrive(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "add", tapeJobID, "--pool", "pool1", "--store", "store1")
	require.Error(t, err)
}

func TestTapeJobAdd_RequiresPool(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "add", tapeJobID, "--drive", "drive0", "--store", "store1")
	require.Error(t, err)
}

func TestTapeJobAdd_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "add", tapeJobID, "--drive", "drive0", "--pool", "pool1")
	require.Error(t, err)
}

func TestTapeJobAdd_RejectsInvalidNotificationMode(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "add", tapeJobID,
		"--drive", "drive0", "--pool", "pool1", "--store", "store1", "--notification-mode", "sideways")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--notification-mode")
}

func TestTapeJobAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapeJobConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid job")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "add", tapeJobID,
		"--drive", "drive0", "--pool", "pool1", "--store", "store1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create tape backup job")
}

func TestTapeJobAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapeJobConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "add", "audit-job",
		"--drive", "drive0",
		"--pool", "pool1",
		"--store", "store1",
		"--comment", "audit comment",
		"--eject-media",
		"--export-media-set",
		"--group-filter", "type:vm", "--group-filter", "type:ct",
		"--latest-only",
		"--max-depth", "3",
		"--notification-mode", "notification-system",
		"--notify-user", "root@pam",
		"--ns", "myns",
		"--schedule", "daily",
		"--worker-threads", "4",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)

	want := map[string]string{
		"id":                "audit-job",
		"drive":             "drive0",
		"pool":              "pool1",
		"store":             "store1",
		"comment":           "audit comment",
		"eject-media":       "1",
		"export-media-set":  "1",
		"latest-only":       "1",
		"max-depth":         "3",
		"notification-mode": "notification-system",
		"notify-user":       "root@pam",
		"ns":                "myns",
		"schedule":          "daily",
		"worker-threads":    "4",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.ElementsMatch(t, []string{"type:vm", "type:ct"}, rec.form["group-filter"])
}

// --- tape job update -----------------------------------------------------

func TestTapeJobUpdate_UpdatesJob(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+tapeJobConfigPath+"/"+tapeJobID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "update", tapeJobID,
		"--schedule", "weekly", "--digest", "abc123", "--delete", "comment", "--delete", "ns")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, tapeJobConfigPath+"/"+tapeJobID, rec.path)
	require.Equal(t, "weekly", rec.form.Get("schedule"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"comment", "ns"}, rec.form["delete"])
	require.Contains(t, buf.String(), "Tape backup job \"job1\" updated.")
}

func TestTapeJobUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+tapeJobConfigPath+"/"+tapeJobID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "update", tapeJobID, "--schedule", "weekly")
	require.NoError(t, err)
	require.Equal(t, "weekly", rec.form.Get("schedule"))

	for _, key := range []string{
		"store", "pool", "drive", "ns", "comment", "max-depth", "eject-media", "export-media-set",
		"latest-only", "notification-mode", "notify-user", "worker-threads", "delete", "digest",
	} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestTapeJobUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "update", tapeJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes given")
}

func TestTapeJobUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "update", tapeJobID, "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestTapeJobUpdate_RejectsInvalidNotificationMode(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "update", tapeJobID, "--notification-mode", "sideways")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--notification-mode")
}

func TestTapeJobUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+tapeJobConfigPath+"/"+tapeJobID, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "update", tapeJobID, "--schedule", "weekly")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update tape backup job")
}

// --- tape job delete -------------------------------------------------------

func TestTapeJobDelete_DeletesJob(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+tapeJobConfigPath+"/"+tapeJobID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "delete", tapeJobID, "--digest", "abc123")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, tapeJobConfigPath+"/"+tapeJobID, rec.path)
	require.Equal(t, "abc123", rec.query.Get("digest"))
	require.Contains(t, buf.String(), "Tape backup job \"job1\" deleted.")
}

func TestTapeJobDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+tapeJobConfigPath+"/"+tapeJobID, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "delete", tapeJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete tape backup job")
}

// --- tape job run (raw call, UPID-capturing, null-tolerant) ----------------

func TestTapeJobRun_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+tapeJobBackupPath+"/"+tapeJobID, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "run", tapeJobID)
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapeJobBackupPath+"/"+tapeJobID, rec.path)
	require.Contains(t, buf.String(), "Tape backup job \"job1\" finished.")
}

func TestTapeJobRun_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapeJobBackupPath+"/"+tapeJobID, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "run", tapeJobID)
	require.NoError(t, err)

	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "finished")
}

func TestTapeJobRun_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapeJobBackupPath+"/"+tapeJobID, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "run failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "run", tapeJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "run tape backup job")
}

// TestTapeJobRun_TolerantOfNullResponse exercises the apidoc's declared
// return type: PBS versions that genuinely reply with a JSON null body (no
// UPID) must produce a plain success message instead of a decode error.
func TestTapeJobRun_TolerantOfNullResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("POST "+tapeJobBackupPath+"/"+tapeJobID, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "run", tapeJobID)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "Tape backup job \"job1\" finished.")
}

// TestTapeJobRun_RejectsNonUPIDResponse confirms that a non-null,
// non-UPID-shaped response is still treated as an error (the null-tolerance
// does not swallow genuinely malformed responses).
func TestTapeJobRun_RejectsNonUPIDResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("POST "+tapeJobBackupPath+"/"+tapeJobID, "not-a-upid")

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "run", tapeJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a UPID")
}

// --- tape job status (typed list, run-status-rich) --------------------------

func TestTapeJobStatus_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+tapeJobBackupPath, []map[string]any{
		{
			"id": "job2", "store": "store2", "pool": "pool2", "drive": "drive0",
			"schedule": "daily", "last-run-state": "OK", "next-run": 1700000000,
		},
		{
			"id": "job1", "store": "store1", "pool": "pool1", "drive": "drive0",
			"schedule": "hourly", "last-run-state": "TAPE ERROR", "next-run": 1690000000,
			"next-media-label": "TAPE001",
		},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "status")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "job1")
	require.Contains(t, out, "job2")
	require.Contains(t, out, "TAPE ERROR")
	require.Contains(t, out, "TAPE001")
}

func TestTapeJobStatus_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+tapeJobBackupPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeJobCmd(), "job", "status")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list tape backup job status")
}
