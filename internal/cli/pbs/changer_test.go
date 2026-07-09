package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// tapeChangerConfigPath is the base /config/changer endpoint.
const tapeChangerConfigPath = "/api2/json/config/changer"

// tapeChangerRuntimePath is the base /tape/changer endpoint (runtime, with
// autodetected model info).
const tapeChangerRuntimePath = "/api2/json/tape/changer"

// tapeChangerName is the sample changer name reused across changer tests.
const tapeChangerName = "changer1"

// --- changer ls ---------------------------------------------------------------

func TestTapeChangerLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+tapeChangerRuntimePath, &rec, []map[string]any{
		{"name": "changer2", "path": "/dev/sg5", "model": "ML6000"},
		{"name": "changer1", "path": "/dev/sg4", "model": "TS4500", "vendor": "IBM", "serial": "XYZ123"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, tapeChangerRuntimePath, rec.path)

	out := buf.String()
	require.Contains(t, out, "changer1")
	require.Contains(t, out, "changer2")
	require.Contains(t, out, "TS4500")
	require.Contains(t, out, "IBM")
}

func TestTapeChangerLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+tapeChangerRuntimePath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list tape changers")
}

// --- changer show ---------------------------------------------------------------

func TestTapeChangerShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+tapeChangerConfigPath+"/"+tapeChangerName, map[string]any{
		"name": tapeChangerName, "path": "/dev/sg4", "export-slots": "1,2,3",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "show", tapeChangerName)
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "/dev/sg4")
	require.Contains(t, out, "1,2,3")
}

func TestTapeChangerShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+tapeChangerConfigPath+"/"+tapeChangerName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such changer")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "show", tapeChangerName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get tape changer")
}

// --- changer add ---------------------------------------------------------------

func TestTapeChangerAdd_CreatesChanger(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapeChangerConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "add", tapeChangerName, "--path", "/dev/sg4")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapeChangerConfigPath, rec.path)
	require.Equal(t, tapeChangerName, rec.form.Get("name"))
	require.Equal(t, "/dev/sg4", rec.form.Get("path"))
	require.Contains(t, buf.String(), "Tape changer \"changer1\" created.")
}

func TestTapeChangerAdd_RequiresPath(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "add", tapeChangerName)
	require.Error(t, err)
}

func TestTapeChangerAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapeChangerConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid changer")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "add", tapeChangerName, "--path", "/dev/sg4")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create tape changer")
}

func TestTapeChangerAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapeChangerConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "add", "audit-changer",
		"--path", "/dev/sg9",
		"--eject-before-unload",
		"--export-slots", "10,11,12",
	)
	require.NoError(t, err)

	want := map[string]string{
		"name":                "audit-changer",
		"path":                "/dev/sg9",
		"eject-before-unload": "1",
		"export-slots":        "10,11,12",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

// --- changer update ---------------------------------------------------------------

func TestTapeChangerUpdate_UpdatesChanger(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+tapeChangerConfigPath+"/"+tapeChangerName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "update", tapeChangerName,
		"--path", "/dev/sg7", "--digest", "abc123", "--delete", "export-slots")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, tapeChangerConfigPath+"/"+tapeChangerName, rec.path)
	require.Equal(t, "/dev/sg7", rec.form.Get("path"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"export-slots"}, rec.form["delete"])
	require.Contains(t, buf.String(), "Tape changer \"changer1\" updated.")
}

func TestTapeChangerUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+tapeChangerConfigPath+"/"+tapeChangerName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "update", tapeChangerName, "--path", "/dev/sg7")
	require.NoError(t, err)
	require.Equal(t, "/dev/sg7", rec.form.Get("path"))

	for _, key := range []string{"eject-before-unload", "export-slots", "delete", "digest"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestTapeChangerUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "update", tapeChangerName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestTapeChangerUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "update", tapeChangerName, "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestTapeChangerUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+tapeChangerConfigPath+"/"+tapeChangerName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "update", tapeChangerName, "--path", "/dev/sg7")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update tape changer")
}

// --- changer delete ---------------------------------------------------------------

func TestTapeChangerDelete_DeletesChanger(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+tapeChangerConfigPath+"/"+tapeChangerName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "delete", tapeChangerName, "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, tapeChangerConfigPath+"/"+tapeChangerName, rec.path)
	require.Contains(t, buf.String(), "Tape changer \"changer1\" deleted.")
}

func TestTapeChangerDelete_RequiresYes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+tapeChangerConfigPath+"/"+tapeChangerName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "delete", tapeChangerName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Contains(t, err.Error(), "--yes/-y")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

func TestTapeChangerDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+tapeChangerConfigPath+"/"+tapeChangerName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "delete", tapeChangerName, "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete tape changer")
}

// --- changer scan ---------------------------------------------------------------

func TestTapeChangerScan_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET /api2/json/tape/scan-changers", &rec, []map[string]any{
		{"path": "/dev/sg5", "kind": "changer", "model": "ML6000", "vendor": "HP", "serial": "S2", "major": 21, "minor": 0},
		{"path": "/dev/sg4", "kind": "changer", "model": "TS4500", "vendor": "IBM", "serial": "S1", "major": 21, "minor": 1},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "scan")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/tape/scan-changers", rec.path)

	out := buf.String()
	require.Contains(t, out, "/dev/sg4")
	require.Contains(t, out, "/dev/sg5")
	require.Contains(t, out, "TS4500")
}

func TestTapeChangerScan_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET /api2/json/tape/scan-changers", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "scan failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "scan")
	require.Error(t, err)
	require.Contains(t, err.Error(), "scan tape changers")
}

// --- changer status ---------------------------------------------------------------

func TestTapeChangerStatus_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+tapeChangerRuntimePath+"/"+tapeChangerName+"/status", &rec, []map[string]any{
		{"entry-id": 1, "entry-kind": "slot", "label-text": "TAPE01"},
		{"entry-id": 0, "entry-kind": "drive", "state": "empty"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "status", tapeChangerName, "--cache")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, tapeChangerRuntimePath+"/"+tapeChangerName+"/status", rec.path)
	require.Equal(t, "1", rec.query.Get("cache"))

	out := buf.String()
	require.Contains(t, out, "TAPE01")
	require.Contains(t, out, "empty")
}

func TestTapeChangerStatus_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+tapeChangerRuntimePath+"/"+tapeChangerName+"/status",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteError(w, http.StatusInternalServerError, "status failed")
		})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "status", tapeChangerName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get tape changer status")
}

// --- changer transfer ---------------------------------------------------------------

func TestTapeChangerTransfer_TransfersMedia(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapeChangerRuntimePath+"/"+tapeChangerName+"/transfer", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "transfer", tapeChangerName, "--from", "3", "--to", "7")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapeChangerRuntimePath+"/"+tapeChangerName+"/transfer", rec.path)
	require.Equal(t, "3", rec.form.Get("from"))
	require.Equal(t, "7", rec.form.Get("to"))
	require.Contains(t, buf.String(), "Media transferred from slot 3 to slot 7")
}

func TestTapeChangerTransfer_RequiresFrom(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "transfer", tapeChangerName, "--to", "7")
	require.Error(t, err)
}

func TestTapeChangerTransfer_RequiresTo(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "transfer", tapeChangerName, "--from", "3")
	require.Error(t, err)
}

func TestTapeChangerTransfer_RejectsZeroFrom(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "transfer", tapeChangerName, "--from", "0", "--to", "7")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--from must be a positive slot number")
}

func TestTapeChangerTransfer_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapeChangerRuntimePath+"/"+tapeChangerName+"/transfer",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteError(w, http.StatusInternalServerError, "transfer failed")
		})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeChangerCmd(), "changer", "transfer", tapeChangerName, "--from", "3", "--to", "7")
	require.Error(t, err)
	require.Contains(t, err.Error(), "transfer media on tape changer")
}
