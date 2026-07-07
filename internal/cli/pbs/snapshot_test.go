package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// testStore is the datastore name used by every test in this file.
const testStore = "store1"

// Endpoint paths under testStore, reused across test cases (goconst).
const (
	snapshotsPath = "/api2/json/admin/datastore/" + testStore + "/snapshots"
	filesPath     = "/api2/json/admin/datastore/" + testStore + "/files"
	protectedPath = "/api2/json/admin/datastore/" + testStore + "/protected"
	notesPath     = "/api2/json/admin/datastore/" + testStore + "/notes"
)

// vm100Ref is a canonical snapshot reference reused across test cases.
const vm100Ref = "vm/100/1700000000"

// --- parseSnapshotRef / parseGroupRef unit tests -----------------------------

func TestParseSnapshotRef_EpochForm(t *testing.T) {
	btype, bid, btime, err := parseSnapshotRef("vm/100/1700000000")
	require.NoError(t, err)
	require.Equal(t, "vm", btype)
	require.Equal(t, "100", bid)
	require.Equal(t, int64(1700000000), btime)
}

func TestParseSnapshotRef_RFC3339Form(t *testing.T) {
	btype, bid, btime, err := parseSnapshotRef("ct/mycontainer/2023-11-14T22:13:20Z")
	require.NoError(t, err)
	require.Equal(t, "ct", btype)
	require.Equal(t, "mycontainer", bid)
	require.Equal(t, int64(1700000000), btime)
}

func TestParseSnapshotRef_BadSegmentCount(t *testing.T) {
	cases := []string{"vm/100", "vm/100/1700000000/extra", "novalidref"}
	for _, ref := range cases {
		_, _, _, err := parseSnapshotRef(ref)
		require.Error(t, err, "ref %q should be rejected", ref)
		require.Contains(t, err.Error(), "want <type>/<id>/<time>")
	}
}

func TestParseSnapshotRef_BadType(t *testing.T) {
	_, _, _, err := parseSnapshotRef("docker/100/1700000000")
	require.Error(t, err)
	require.Contains(t, err.Error(), "vm, ct, host")
}

func TestParseSnapshotRef_EmptyID(t *testing.T) {
	_, _, _, err := parseSnapshotRef("vm//1700000000")
	require.Error(t, err)
	require.Contains(t, err.Error(), "backup id must not be empty")
}

func TestParseSnapshotRef_BadTime(t *testing.T) {
	_, _, _, err := parseSnapshotRef("vm/100/not-a-time")
	require.Error(t, err)
	require.Contains(t, err.Error(), "neither a Unix epoch nor an RFC3339 timestamp")
}

func TestParseGroupRef_Valid(t *testing.T) {
	btype, bid, err := parseGroupRef("host/pbs1")
	require.NoError(t, err)
	require.Equal(t, "host", btype)
	require.Equal(t, "pbs1", bid)
}

func TestParseGroupRef_BadSegmentCount(t *testing.T) {
	_, _, err := parseGroupRef("vm/100/1700000000")
	require.Error(t, err)
	require.Contains(t, err.Error(), "want <type>/<id>")
}

func TestParseGroupRef_BadType(t *testing.T) {
	_, _, err := parseGroupRef("docker/100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "vm, ct, host")
}

func TestParseGroupRef_EmptyID(t *testing.T) {
	_, _, err := parseGroupRef("vm/")
	require.Error(t, err)
	require.Contains(t, err.Error(), "backup id must not be empty")
}

// --- snapshot ls --------------------------------------------------------------

func TestSnapshotLs_RendersFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET "+snapshotsPath, &rec, []map[string]any{
		{
			"backup-type": "vm",
			"backup-id":   "100",
			"backup-time": 1700000000,
			"size":        123456,
			"owner":       "root@pam",
			"protected":   true,
			"comment":     "nightly",
			"verification": map[string]any{
				"state": "ok",
				"upid":  validUPID,
			},
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotLsCmd(), "ls", "--store", testStore)
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, snapshotsPath, rec.path)
	require.Empty(t, rec.query.Get("backup-type"), "unfiltered ls must not send backup-type")

	out := buf.String()
	require.Contains(t, out, "vm/100/2023-11-14T22:13:20Z")
	require.Contains(t, out, "123456")
	require.Contains(t, out, "root@pam")
	require.Contains(t, out, "true")
	require.Contains(t, out, "ok")
	require.Contains(t, out, "nightly")
}

func TestSnapshotLs_WithGroupFilterAndNamespace(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET "+snapshotsPath, &rec, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotLsCmd(), "ls", "vm/100", "--store", testStore, "--ns", "team-a")
	require.NoError(t, err)
	require.Equal(t, "vm", rec.query.Get("backup-type"))
	require.Equal(t, "100", rec.query.Get("backup-id"))
	require.Equal(t, "team-a", rec.query.Get("ns"))
}

func TestSnapshotLs_InvalidGroupFilter(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var called bool
	f.HandleFunc("GET "+snapshotsPath, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, []any{})
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotLsCmd(), "ls", "bogus", "--store", testStore)
	require.Error(t, err)
	require.False(t, called, "malformed group filter must not reach the API")
}

func TestSnapshotLs_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET "+snapshotsPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "datastore unavailable")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotLsCmd(), "ls", "--store", testStore)
	require.Error(t, err)
}

func TestSnapshotLs_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotLsCmd(), "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "store")
}

// --- snapshot show --------------------------------------------------------------

func TestSnapshotShow_RendersAllFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET "+snapshotsPath, new(recordedRequest), []map[string]any{
		{
			"backup-type": "vm",
			"backup-id":   "100",
			"backup-time": 1700000000,
			"size":        123456,
			"owner":       "root@pam",
			"protected":   false,
			"comment":     "nightly",
			"fingerprint": "aa:bb:cc",
			"verification": map[string]any{
				"state": "ok",
				"upid":  validUPID,
			},
			"files": []map[string]any{
				{"filename": "index.json.blob", "size": 1024},
				{"filename": "drive-scsi0.img.fidx", "size": 2048},
			},
		},
		{
			"backup-type": "vm",
			"backup-id":   "100",
			"backup-time": 1600000000,
			"protected":   false,
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotShowCmd(), "show", vm100Ref, "--store", testStore)
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "vm/100/2023-11-14T22:13:20Z")
	require.Contains(t, out, "123456")
	require.Contains(t, out, "root@pam")
	require.Contains(t, out, "aa:bb:cc")
	require.Contains(t, out, "ok")
	require.Contains(t, out, "index.json.blob")
	require.Contains(t, out, "drive-scsi0.img.fidx")
}

func TestSnapshotShow_NotFound(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET "+snapshotsPath, new(recordedRequest), []map[string]any{
		{"backup-type": "vm", "backup-id": "100", "backup-time": 1600000000, "protected": false},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotShowCmd(), "show", vm100Ref, "--store", testStore)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestSnapshotShow_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET "+snapshotsPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such datastore")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotShowCmd(), "show", vm100Ref, "--store", testStore)
	require.Error(t, err)
}

// --- snapshot files --------------------------------------------------------------

func TestSnapshotFiles_RendersFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET "+filesPath, &rec, []map[string]any{
		{"filename": "index.json.blob", "size": 1024, "crypt-mode": "none"},
		{"filename": "drive-scsi0.img.fidx", "size": 2048},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotFilesCmd(), "files", vm100Ref, "--store", testStore)
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, filesPath, rec.path)
	require.Equal(t, "vm", rec.query.Get("backup-type"))
	require.Equal(t, "100", rec.query.Get("backup-id"))
	require.Equal(t, "1700000000", rec.query.Get("backup-time"))

	out := buf.String()
	require.Contains(t, out, "index.json.blob")
	require.Contains(t, out, "1024")
	require.Contains(t, out, "none")
	require.Contains(t, out, "drive-scsi0.img.fidx")
	require.Contains(t, out, "2048")
}

func TestSnapshotFiles_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET "+filesPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such snapshot")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotFilesCmd(), "files", vm100Ref, "--store", testStore)
	require.Error(t, err)
}

func TestSnapshotFiles_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotFilesCmd(), "files", vm100Ref)
	require.Error(t, err)
	require.Contains(t, err.Error(), "store")
}

// --- snapshot delete --------------------------------------------------------------

func TestSnapshotDelete_Success(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE "+snapshotsPath, &rec, map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotDeleteCmd(), "delete", vm100Ref, "--store", testStore)
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, snapshotsPath, rec.path)
	require.Equal(t, "vm", rec.query.Get("backup-type"))
	require.Equal(t, "100", rec.query.Get("backup-id"))
	require.Equal(t, "1700000000", rec.query.Get("backup-time"))
	require.Contains(t, buf.String(), "deleted")
}

func TestSnapshotDelete_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("DELETE "+snapshotsPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "snapshot is protected")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotDeleteCmd(), "delete", vm100Ref, "--store", testStore)
	require.Error(t, err)
}

// --- snapshot protect / unprotect --------------------------------------------------------------

func TestSnapshotProtect_Success(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT "+protectedPath, &rec, map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotProtectCmd(), "protect", vm100Ref, "--store", testStore)
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, protectedPath, rec.path)
	require.Equal(t, "1", rec.form.Get("protected"))
	require.Equal(t, "vm", rec.form.Get("backup-type"))
	require.Equal(t, "100", rec.form.Get("backup-id"))
	require.Equal(t, "1700000000", rec.form.Get("backup-time"))
	require.Contains(t, buf.String(), "protected")
}

func TestSnapshotUnprotect_Success(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT "+protectedPath, &rec, map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotUnprotectCmd(), "unprotect", vm100Ref, "--store", testStore)
	require.NoError(t, err)
	require.Equal(t, "0", rec.form.Get("protected"))
	require.Contains(t, buf.String(), "unprotected")
}

func TestSnapshotProtect_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("PUT "+protectedPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "backend error")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotProtectCmd(), "protect", vm100Ref, "--store", testStore)
	require.Error(t, err)
}

func TestSnapshotProtect_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotProtectCmd(), "protect", vm100Ref)
	require.Error(t, err)
	require.Contains(t, err.Error(), "store")
}

// --- snapshot notes --------------------------------------------------------------

func TestSnapshotNotes_Get(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET "+notesPath, &rec, "these are my notes")

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotNotesCmd(), "notes", vm100Ref, "--store", testStore)
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, notesPath, rec.path)
	require.Equal(t, "vm", rec.query.Get("backup-type"))
	require.Equal(t, "100", rec.query.Get("backup-id"))
	require.Equal(t, "1700000000", rec.query.Get("backup-time"))
	require.Contains(t, buf.String(), "these are my notes")
}

func TestSnapshotNotes_Set(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT "+notesPath, &rec, map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotNotesCmd(), "notes", vm100Ref, "--store", testStore, "--set", "updated notes")
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, notesPath, rec.path)
	require.Equal(t, "updated notes", rec.form.Get("notes"))
	require.Contains(t, buf.String(), "updated")
}

func TestSnapshotNotes_GetServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET "+notesPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such snapshot")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotNotesCmd(), "notes", vm100Ref, "--store", testStore)
	require.Error(t, err)
}

func TestSnapshotNotes_SetServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("PUT "+notesPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "backend error")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotNotesCmd(), "notes", vm100Ref, "--store", testStore, "--set", "x")
	require.Error(t, err)
}

func TestSnapshotNotes_UnexpectedResponseType(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET "+notesPath, new(recordedRequest), map[string]any{"unexpected": true})

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotNotesCmd(), "notes", vm100Ref, "--store", testStore)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected response type")
}

func TestSnapshotNotes_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newSnapshotNotesCmd(), "notes", vm100Ref)
	require.Error(t, err)
	require.Contains(t, err.Error(), "store")
}

// --- misc coverage --------------------------------------------------------------

func TestNewSnapshotCmd_HasAllSubcommands(t *testing.T) {
	cmd := newSnapshotCmd()
	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"ls", "show", "files", "delete", "protect", "unprotect", "notes"} {
		require.True(t, names[want], "missing sub-command %q", want)
	}
}
