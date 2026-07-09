package pdm

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

const configViewsPath = "/api2/json/config/views"

// --- config view ls ---------------------------------------------------------------

func TestConfigViewLs_SortsById(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET "+configViewsPath, []map[string]any{
		{"id": "zeta", "include": []string{"tag=z"}},
		{"id": "alpha", "include": []string{"tag=a"}, "include-all": true},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newConfigViewCmd(), "view", "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["id"], "entries must sort by id")
	require.Equal(t, "zeta", got[1]["id"])

	include0, ok := got[0]["include"].([]any)
	require.True(t, ok)
	require.Equal(t, "tag=a", include0[0], "Raw entries must stay paired with their sorted row")
}

func TestConfigViewLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+configViewsPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigViewCmd(), "view", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list views")
}

// --- config view show ---------------------------------------------------------------

func TestConfigViewShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+configViewsPath+"/prod", map[string]any{
		"id": "prod", "include": []string{"tag=prod"}, "include-all": false,
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigViewCmd(), "view", "show", "prod")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "prod")
}

func TestConfigViewShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+configViewsPath+"/prod", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such view")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigViewCmd(), "view", "show", "prod")
	require.Error(t, err)
	require.Contains(t, err.Error(), `get view "prod"`)
}

// --- config view add ---------------------------------------------------------------

func TestConfigViewAdd_CreatesView(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+configViewsPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigViewCmd(), "view", "add", "prod",
		"--include", "tag=prod", "--include-all", "--layout", `{"cols":2}`)
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "prod", rec.form.Get("id"))
	require.Equal(t, "tag=prod", rec.form.Get("include"))
	require.Equal(t, "1", rec.form.Get("include-all"))
	require.Equal(t, `{"cols":2}`, rec.form.Get("layout"))
	require.Contains(t, buf.String(), `View "prod" created.`)
}

func TestConfigViewAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+configViewsPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid view")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigViewCmd(), "view", "add", "prod")
	require.Error(t, err)
	require.Contains(t, err.Error(), `create view "prod"`)
}

// --- config view update ---------------------------------------------------------------

func TestConfigViewUpdate_UpdatesView(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+configViewsPath+"/prod", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigViewCmd(), "view", "update", "prod",
		"--exclude", "tag=archived", "--digest", "abc123", "--delete", "layout")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "tag=archived", rec.form.Get("exclude"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"layout"}, rec.form["delete"])
	require.Contains(t, buf.String(), `View "prod" updated.`)
}

func TestConfigViewUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigViewCmd(), "view", "update", "prod")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestConfigViewUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigViewCmd(), "view", "update", "prod", "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestConfigViewUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+configViewsPath+"/prod", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigViewCmd(), "view", "update", "prod", "--include-all")
	require.Error(t, err)
	require.Contains(t, err.Error(), `update view "prod"`)
}

// --- config view delete ---------------------------------------------------------------

func TestConfigViewDelete_WithoutConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	var called bool
	f.HandleFunc("DELETE "+configViewsPath+"/prod", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigViewCmd(), "view", "delete", "prod")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.False(t, called, "no request must be issued without --yes")
}

func TestConfigViewDelete_DeletesView(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+configViewsPath+"/prod", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigViewCmd(), "view", "delete", "prod", "--digest", "abc123", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, "abc123", rec.query.Get("digest"))
	require.Contains(t, buf.String(), `View "prod" deleted.`)
}

func TestConfigViewDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+configViewsPath+"/prod", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigViewCmd(), "view", "delete", "prod", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), `delete view "prod"`)
}

// --- config view group wiring ---------------------------------------------------------------

func TestNewConfigViewCmd_RegistersAllSubcommands(t *testing.T) {
	cmd := newConfigViewCmd()
	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"ls", "show", "add", "update", "delete"} {
		require.True(t, names[want], "expected `view %s` to be registered", want)
	}
}
