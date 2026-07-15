package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// tapePoolConfigPath is the base /config/media-pool endpoint (`tape pool` CRUD).
const tapePoolConfigPath = "/api2/json/config/media-pool"

// tapePoolName is the sample pool name reused across `tape pool` tests.
const tapePoolName = "pool1"

// --- tape pool ls --------------------------------------------------------

func TestTapePoolLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+tapePoolConfigPath, []map[string]any{
		{"name": "pool2", "allocation": "always", "retention": "keep"},
		{"name": "pool1", "allocation": "continue", "retention": "overwrite", "comment": "primary pool"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapePoolCmd(), "pool", "ls")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "pool1")
	require.Contains(t, out, "pool2")
	require.Contains(t, out, "primary pool")
}

func TestTapePoolLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+tapePoolConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapePoolCmd(), "pool", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list tape media pools")
}

// --- tape pool show --------------------------------------------------------

func TestTapePoolShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+tapePoolConfigPath+"/"+tapePoolName, map[string]any{
		"name": tapePoolName, "allocation": "always", "retention": "keep", "comment": "nightly pool",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapePoolCmd(), "pool", "show", tapePoolName)
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "nightly pool")
	require.Contains(t, out, "always")
}

func TestTapePoolShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+tapePoolConfigPath+"/"+tapePoolName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such pool")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapePoolCmd(), "pool", "show", tapePoolName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "show tape media pool")
}

// --- tape pool add -----------------------------------------------------

func TestTapePoolAdd_CreatesPool(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapePoolConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapePoolCmd(), "pool", "add", tapePoolName,
		"--allocation", "always", "--retention", "keep")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapePoolConfigPath, rec.path)
	require.Equal(t, tapePoolName, rec.form.Get("name"))
	require.Equal(t, "always", rec.form.Get("allocation"))
	require.Equal(t, "keep", rec.form.Get("retention"))
	require.Contains(t, buf.String(), "Tape media pool \"pool1\" created.")
}

func TestTapePoolAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapePoolConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid pool")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapePoolCmd(), "pool", "add", tapePoolName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "create tape media pool")
}

func TestTapePoolAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapePoolConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapePoolCmd(), "pool", "add", "audit-pool",
		"--allocation", "continue",
		"--comment", "audit comment",
		"--encrypt", "aa:bb:cc",
		"--retention", "overwrite",
		"--template", "{{'%Y-%m'}}",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)

	want := map[string]string{
		"name":       "audit-pool",
		"allocation": "continue",
		"comment":    "audit comment",
		"encrypt":    "aa:bb:cc",
		"retention":  "overwrite",
		"template":   "{{'%Y-%m'}}",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

// --- tape pool update ----------------------------------------------------

func TestTapePoolUpdate_UpdatesPool(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+tapePoolConfigPath+"/"+tapePoolName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapePoolCmd(), "pool", "update", tapePoolName,
		"--retention", "keep", "--delete", "comment", "--delete", "template")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, tapePoolConfigPath+"/"+tapePoolName, rec.path)
	require.Equal(t, "keep", rec.form.Get("retention"))
	require.ElementsMatch(t, []string{"comment", "template"}, rec.form["delete"])
	require.Contains(t, buf.String(), "Tape media pool \"pool1\" updated.")
}

func TestTapePoolUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+tapePoolConfigPath+"/"+tapePoolName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapePoolCmd(), "pool", "update", tapePoolName, "--retention", "keep")
	require.NoError(t, err)
	require.Equal(t, "keep", rec.form.Get("retention"))

	for _, key := range []string{"allocation", "comment", "encrypt", "template", "delete"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestTapePoolUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapePoolCmd(), "pool", "update", tapePoolName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestTapePoolUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapePoolCmd(), "pool", "update", tapePoolName, "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestTapePoolUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+tapePoolConfigPath+"/"+tapePoolName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapePoolCmd(), "pool", "update", tapePoolName, "--retention", "keep")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update tape media pool")
}

// --- tape pool delete ------------------------------------------------------

func TestTapePoolDelete_DeletesPool(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+tapePoolConfigPath+"/"+tapePoolName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapePoolCmd(), "pool", "delete", tapePoolName, "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, tapePoolConfigPath+"/"+tapePoolName, rec.path)
	require.Contains(t, buf.String(), "Tape media pool \"pool1\" deleted.")
}

func TestTapePoolDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+tapePoolConfigPath+"/"+tapePoolName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapePoolCmd(), "pool", "delete", tapePoolName, "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete tape media pool")
}

func TestTapePoolDelete_WithoutConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	var called bool
	f.HandleFunc("DELETE "+tapePoolConfigPath+"/"+tapePoolName, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapePoolCmd(), "pool", "delete", tapePoolName)
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.False(t, called, "no request must be issued without --yes")
}
