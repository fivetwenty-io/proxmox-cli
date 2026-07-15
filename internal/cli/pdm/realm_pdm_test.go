package pdm

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// realmPdmConfigPath is the /config/access/pdm endpoint (singleton, no id).
const realmPdmConfigPath = "/api2/json/config/access/pdm"

// --- realm pdm show ---------------------------------------------------------------

func TestRealmPdmShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+realmPdmConfigPath, map[string]any{
		"realm": "pdm", "type": "pdm", "comment": "built-in PDM realm", "default": true,
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPdmCmd(), "pdm", "show")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "pdm")
	require.Contains(t, out, "built-in PDM realm")
}

func TestRealmPdmShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+realmPdmConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "get failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPdmCmd(), "pdm", "show")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get PDM realm")
}

// --- realm pdm update ---------------------------------------------------------------

func TestRealmPdmUpdate_UpdatesRealm(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+realmPdmConfigPath, &rec, map[string]any{
		"realm": "pdm", "type": "pdm",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPdmCmd(), "pdm", "update",
		"--comment", "updated", "--default", "--digest", "abc123", "--delete", "comment")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, realmPdmConfigPath, rec.path)
	require.Equal(t, "updated", rec.form.Get("comment"))
	require.Equal(t, "1", rec.form.Get("default"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"comment"}, rec.form["delete"])
	require.Contains(t, buf.String(), "PDM realm updated.")
}

func TestRealmPdmUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+realmPdmConfigPath, &rec, map[string]any{
		"realm": "pdm", "type": "pdm",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPdmCmd(), "pdm", "update", "--comment", "only-comment")
	require.NoError(t, err)
	require.Equal(t, "only-comment", rec.form.Get("comment"))

	for _, key := range []string{"default", "delete", "digest"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestRealmPdmUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPdmCmd(), "pdm", "update")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestRealmPdmUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPdmCmd(), "pdm", "update", "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestRealmPdmUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+realmPdmConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPdmCmd(), "pdm", "update", "--comment", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update PDM realm")
}
