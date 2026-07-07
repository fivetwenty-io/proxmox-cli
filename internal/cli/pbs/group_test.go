package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// Endpoint paths under testStore, reused across test cases (goconst).
// testStore itself is defined in snapshot_test.go (same package).
const (
	groupsPath      = "/api2/json/admin/datastore/" + testStore + "/groups"
	groupNotesPath  = "/api2/json/admin/datastore/" + testStore + "/group-notes"
	vm100GroupRef   = "vm/100"
	hostPbs1GroupID = "host/pbs1"
)

// --- group ls --------------------------------------------------------------

func TestGroupLs_RendersFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET "+groupsPath, &rec, []map[string]any{
		{
			"backup-type":  "vm",
			"backup-id":    "100",
			"backup-count": 5,
			"last-backup":  1700000000,
			"owner":        "root@pam",
			"comment":      "nightly guest backups",
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupLsCmd(), "ls", "--store", testStore)
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, groupsPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "vm/100")
	require.Contains(t, out, "5")
	require.Contains(t, out, "2023-11-14T22:13:20Z")
	require.Contains(t, out, "root@pam")
	require.Contains(t, out, "nightly guest backups")
}

func TestGroupLs_WithNamespace(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET "+groupsPath, &rec, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupLsCmd(), "ls", "--store", testStore, "--ns", "team-a")
	require.NoError(t, err)
	require.Equal(t, "team-a", rec.query.Get("ns"))
}

func TestGroupLs_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET "+groupsPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "datastore unavailable")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupLsCmd(), "ls", "--store", testStore)
	require.Error(t, err)
}

func TestGroupLs_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupLsCmd(), "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "store")
}

// --- group delete --------------------------------------------------------------

func TestGroupDelete_Success(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE "+groupsPath, &rec, map[string]any{
		"removed-groups":      1,
		"removed-snapshots":   3,
		"protected-snapshots": 0,
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupDeleteCmd(), "delete", vm100GroupRef, "--store", testStore, "--yes")
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, groupsPath, rec.path)
	require.Equal(t, "vm", rec.query.Get("backup-type"))
	require.Equal(t, "100", rec.query.Get("backup-id"))
	_, hasErrorOnProtected := rec.query["error-on-protected"]
	require.False(t, hasErrorOnProtected, "unset --error-on-protected must be omitted from the request")

	out := buf.String()
	require.Contains(t, out, "deleted")
	require.Contains(t, out, "3 snapshots removed")
}

func TestGroupDelete_ErrorOnProtectedFlag(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE "+groupsPath, &rec, map[string]any{
		"removed-groups":      0,
		"removed-snapshots":   0,
		"protected-snapshots": 2,
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupDeleteCmd(), "delete", hostPbs1GroupID,
		"--store", testStore, "--error-on-protected", "--yes")
	require.NoError(t, err)
	require.Equal(t, "1", rec.query.Get("error-on-protected"))
}

func TestGroupDelete_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("DELETE "+groupsPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "group has protected snapshots")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupDeleteCmd(), "delete", vm100GroupRef, "--store", testStore, "--yes")
	require.Error(t, err)
}

func TestGroupDelete_WithoutConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var called bool
	f.HandleFunc("DELETE "+groupsPath, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, map[string]any{})
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupDeleteCmd(), "delete", vm100GroupRef, "--store", testStore)
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.False(t, called, "no request must be issued without --yes")
}

func TestGroupDelete_InvalidRef(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var called bool
	f.HandleFunc("DELETE "+groupsPath, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, map[string]any{})
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupDeleteCmd(), "delete", "not-a-ref", "--store", testStore)
	require.Error(t, err)
	require.False(t, called, "malformed group ref must not reach the API")
}

func TestGroupDelete_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupDeleteCmd(), "delete", vm100GroupRef)
	require.Error(t, err)
	require.Contains(t, err.Error(), "store")
}

// --- group notes --------------------------------------------------------------

func TestGroupNotes_Get(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET "+groupNotesPath, &rec, "group-level notes")

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupNotesCmd(), "notes", vm100GroupRef, "--store", testStore)
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, groupNotesPath, rec.path)
	require.Equal(t, "vm", rec.query.Get("backup-type"))
	require.Equal(t, "100", rec.query.Get("backup-id"))
	require.Contains(t, buf.String(), "group-level notes")
}

func TestGroupNotes_Set(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT "+groupNotesPath, &rec, map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupNotesCmd(), "notes", vm100GroupRef, "--store", testStore, "--set", "new notes")
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, groupNotesPath, rec.path)
	require.Equal(t, "new notes", rec.form.Get("notes"))
	require.Equal(t, "vm", rec.form.Get("backup-type"))
	require.Equal(t, "100", rec.form.Get("backup-id"))
	require.Contains(t, buf.String(), "updated")
}

func TestGroupNotes_GetServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET "+groupNotesPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such group")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupNotesCmd(), "notes", vm100GroupRef, "--store", testStore)
	require.Error(t, err)
}

func TestGroupNotes_SetServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("PUT "+groupNotesPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "backend error")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupNotesCmd(), "notes", vm100GroupRef, "--store", testStore, "--set", "x")
	require.Error(t, err)
}

func TestGroupNotes_UnexpectedResponseType(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET "+groupNotesPath, new(recordedRequest), map[string]any{"unexpected": true})

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupNotesCmd(), "notes", vm100GroupRef, "--store", testStore)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected response type")
}

func TestGroupNotes_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newGroupNotesCmd(), "notes", vm100GroupRef)
	require.Error(t, err)
	require.Contains(t, err.Error(), "store")
}

// --- misc coverage --------------------------------------------------------------

func TestNewGroupCmd_HasAllSubcommands(t *testing.T) {
	cmd := newGroupCmd()
	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"ls", "delete", "notes"} {
		require.True(t, names[want], "missing sub-command %q", want)
	}
}
