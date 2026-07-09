package pdm

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

const configNotesPath = "/api2/json/config/notes"

// --- config notes show ---------------------------------------------------------------

func TestConfigNotesShow_RendersNotes(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+configNotesPath, "Welcome to the datacenter.")

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigNotesCmd(), "notes", "show")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "Welcome to the datacenter.")
}

func TestConfigNotesShow_NoNotesSet(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+configNotesPath, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigNotesCmd(), "notes", "show")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "No notes are set.")
}

func TestConfigNotesShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+configNotesPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "get failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigNotesCmd(), "notes", "show")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get notes")
}

// --- config notes update ---------------------------------------------------------------

func TestConfigNotesUpdate_UpdatesNotes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+configNotesPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigNotesCmd(), "notes", "update", "--notes", "New notes.", "--digest", "abc123")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "New notes.", rec.form.Get("notes"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.Contains(t, buf.String(), "Notes updated.")
}

func TestConfigNotesUpdate_AllowsEmptyNotesToClear(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+configNotesPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigNotesCmd(), "notes", "update", "--notes", "")
	require.NoError(t, err)
	require.Equal(t, "", rec.form.Get("notes"))
	_, present := rec.form["notes"]
	require.True(t, present, "notes must be sent even when empty")
}

func TestConfigNotesUpdate_RequiresNotesFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigNotesCmd(), "notes", "update")
	require.Error(t, err)
}

func TestConfigNotesUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+configNotesPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigNotesCmd(), "notes", "update", "--notes", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update notes")
}

// --- config notes group wiring ---------------------------------------------------------------

func TestNewConfigNotesCmd_RegistersAllSubcommands(t *testing.T) {
	cmd := newConfigNotesCmd()
	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"show", "update"} {
		require.True(t, names[want], "expected `notes %s` to be registered", want)
	}
}
