package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// tapeMediaListPath is the /tape/media/list endpoint.
const tapeMediaListPath = "/api2/json/tape/media/list"

// tapeMediaContentPath is the /tape/media/content endpoint.
const tapeMediaContentPath = "/api2/json/tape/media/content"

// tapeMediaSetsPath is the /tape/media/media-sets endpoint.
const tapeMediaSetsPath = "/api2/json/tape/media/media-sets"

// tapeMediaMovePath is the /tape/media/move endpoint.
const tapeMediaMovePath = "/api2/json/tape/media/move"

// tapeMediaDestroyPath is the /tape/media/destroy endpoint.
const tapeMediaDestroyPath = "/api2/json/tape/media/destroy"

// tapeMediaUuid is the sample media UUID reused across media tests.
const tapeMediaUuid = "12345678-1234-1234-1234-123456789abc"

// --- media ls ---------------------------------------------------------------

func TestTapeMediaLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+tapeMediaListPath, &rec, []map[string]any{
		{
			"label-text": "TAPE02", "uuid": "u2", "status": "writable", "location": "online-changer1",
			"catalog": true, "expired": false, "ctime": 1700000000,
		},
		{
			"label-text": "TAPE01", "uuid": "u1", "status": "full", "location": "vault-offsite",
			"pool": "pool1", "media-set-name": "set1", "catalog": true, "expired": false,
			"ctime": 1690000000, "bytes-used": 500000,
		},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "ls", "--pool", "pool1",
		"--update-status", "--update-status-changer", "changer1")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, tapeMediaListPath, rec.path)
	require.Equal(t, "pool1", rec.query.Get("pool"))
	require.Equal(t, "1", rec.query.Get("update-status"))
	require.Equal(t, "changer1", rec.query.Get("update-status-changer"))

	out := buf.String()
	require.Contains(t, out, "TAPE01")
	require.Contains(t, out, "TAPE02")
	require.Contains(t, out, "vault-offsite")
}

func TestTapeMediaLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+tapeMediaListPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list tape media")
}

// --- media content ---------------------------------------------------------------

func TestTapeMediaContent_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+tapeMediaContentPath, &rec, []map[string]any{
		{
			"store": "store2", "snapshot": "vm/200/2024-01-01T00:00:00Z", "label-text": "TAPE01",
			"uuid": "u1", "pool": "pool1", "media-set-name": "set1", "media-set-uuid": "ms1",
			"media-set-ctime": 1700000000, "seq-nr": 0, "backup-time": 1700000001,
		},
		{
			"store": "store1", "snapshot": "vm/100/2024-01-01T00:00:00Z", "label-text": "TAPE01",
			"uuid": "u1", "pool": "pool1", "media-set-name": "set1", "media-set-uuid": "ms1",
			"media-set-ctime": 1700000000, "seq-nr": 0, "backup-time": 1690000000,
		},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "content",
		"--backup-id", "100", "--backup-type", "vm", "--label-text", "TAPE01",
		"--media", tapeMediaUuid, "--media-set", "ms1", "--pool", "pool1")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, tapeMediaContentPath, rec.path)
	require.Equal(t, "100", rec.query.Get("backup-id"))
	require.Equal(t, "vm", rec.query.Get("backup-type"))
	require.Equal(t, "TAPE01", rec.query.Get("label-text"))
	require.Equal(t, tapeMediaUuid, rec.query.Get("media"))
	require.Equal(t, "ms1", rec.query.Get("media-set"))
	require.Equal(t, "pool1", rec.query.Get("pool"))

	out := buf.String()
	require.Contains(t, out, "store1")
	require.Contains(t, out, "store2")
}

func TestTapeMediaContent_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+tapeMediaContentPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "content failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "content")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list tape media content")
}

// --- media sets ---------------------------------------------------------------

func TestTapeMediaSets_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+tapeMediaSetsPath, &rec, []map[string]any{
		{"media-set-name": "set2", "media-set-uuid": "ms2", "pool": "pool2", "media-set-ctime": 1700000000},
		{"media-set-name": "set1", "media-set-uuid": "ms1", "pool": "pool1", "media-set-ctime": 1690000000},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "sets")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, tapeMediaSetsPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "set1")
	require.Contains(t, out, "set2")
}

func TestTapeMediaSets_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+tapeMediaSetsPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "sets failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "sets")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list tape media sets")
}

// --- media move ---------------------------------------------------------------

func TestTapeMediaMove_MovesToVault(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapeMediaMovePath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "move",
		"--uuid", tapeMediaUuid, "--label-text", "TAPE01", "--vault-name", "offsite")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapeMediaMovePath, rec.path)
	require.Equal(t, tapeMediaUuid, rec.form.Get("uuid"))
	require.Equal(t, "TAPE01", rec.form.Get("label-text"))
	require.Equal(t, "offsite", rec.form.Get("vault-name"))
	require.Contains(t, buf.String(), `Tape media moved to vault "offsite".`)
}

func TestTapeMediaMove_MovesOffline(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapeMediaMovePath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "move", "--uuid", tapeMediaUuid)
	require.NoError(t, err)

	_, present := rec.form["vault-name"]
	require.False(t, present, "vault-name must be omitted from the body when unset")
	require.Contains(t, buf.String(), "Tape media moved to offline.")
}

func TestTapeMediaMove_RequiresUuidOrLabelText(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "move", "--vault-name", "offsite")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--uuid or --label-text is required")
}

func TestTapeMediaMove_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapeMediaMovePath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "move failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "move", "--uuid", tapeMediaUuid)
	require.Error(t, err)
	require.Contains(t, err.Error(), "move tape media")
}

// --- media destroy ---------------------------------------------------------------

func TestTapeMediaDestroy_DestroysMedia(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+tapeMediaDestroyPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "destroy", "--uuid", tapeMediaUuid, "--force", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, tapeMediaDestroyPath, rec.path)
	require.Equal(t, tapeMediaUuid, rec.query.Get("uuid"))
	require.Equal(t, "1", rec.query.Get("force"))
	require.Contains(t, buf.String(), "Tape media destroyed.")
}

func TestTapeMediaDestroy_RequiresUuidOrLabelText(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "destroy", "--force")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--uuid or --label-text is required")
}

func TestTapeMediaDestroy_RequiresYes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+tapeMediaDestroyPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "destroy", "--uuid", tapeMediaUuid)
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Contains(t, err.Error(), "--yes/-y")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

func TestTapeMediaDestroy_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+tapeMediaDestroyPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "destroy failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "destroy", "--label-text", "TAPE01", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "destroy tape media")
}

// --- media set-status ---------------------------------------------------------------

func TestTapeMediaSetStatus_SetsStatus(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapeMediaListPath+"/"+tapeMediaUuid+"/status", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "set-status", tapeMediaUuid, "--status", "damaged")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapeMediaListPath+"/"+tapeMediaUuid+"/status", rec.path)
	require.Equal(t, "damaged", rec.form.Get("status"))
	require.Contains(t, buf.String(), `status set to "damaged"`)
}

func TestTapeMediaSetStatus_ClearsStatusWhenOmitted(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapeMediaListPath+"/"+tapeMediaUuid+"/status", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "set-status", tapeMediaUuid)
	require.NoError(t, err)

	_, present := rec.form["status"]
	require.False(t, present, "status must be omitted from the body when unset")
	require.Contains(t, buf.String(), "status cleared")
}

func TestTapeMediaSetStatus_RejectsEmptyStatus(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "set-status", tapeMediaUuid, "--status", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--status")
}

func TestTapeMediaSetStatus_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapeMediaListPath+"/"+tapeMediaUuid+"/status",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteError(w, http.StatusInternalServerError, "set status failed")
		})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeMediaCmd(), "media", "set-status", tapeMediaUuid, "--status", "retired")
	require.Error(t, err)
	require.Contains(t, err.Error(), "set tape media status")
}
