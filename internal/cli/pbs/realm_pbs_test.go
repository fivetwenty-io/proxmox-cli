package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// realmPbsConfigPath is the /config/access/pbs endpoint (singleton, no id).
const realmPbsConfigPath = "/api2/json/config/access/pbs"

// --- realm pbs show ---------------------------------------------------------------

func TestRealmPbsShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+realmPbsConfigPath, map[string]any{
		"realm": "pbs", "type": "pbs", "comment": "built-in PBS realm", "default": false,
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPbsCmd(), "pbs", "show")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "pbs")
	require.Contains(t, out, "built-in PBS realm")
}

func TestRealmPbsShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+realmPbsConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "get failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPbsCmd(), "pbs", "show")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get PBS realm")
}

// --- realm pbs update ---------------------------------------------------------------

func TestRealmPbsUpdate_UpdatesRealm(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+realmPbsConfigPath, &rec, map[string]any{
		"realm": "pbs", "type": "pbs",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPbsCmd(), "pbs", "update",
		"--comment", "updated", "--default", "--digest", "abc123", "--delete", "comment")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, realmPbsConfigPath, rec.path)
	require.Equal(t, "updated", rec.form.Get("comment"))
	require.Equal(t, "1", rec.form.Get("default"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"comment"}, rec.form["delete"])
	require.Contains(t, buf.String(), "PBS realm updated.")
}

func TestRealmPbsUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+realmPbsConfigPath, &rec, map[string]any{
		"realm": "pbs", "type": "pbs",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPbsCmd(), "pbs", "update", "--comment", "only-comment")
	require.NoError(t, err)
	require.Equal(t, "only-comment", rec.form.Get("comment"))

	for _, key := range []string{"default", "delete", "digest"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestRealmPbsUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPbsCmd(), "pbs", "update")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestRealmPbsUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPbsCmd(), "pbs", "update", "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestRealmPbsUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+realmPbsConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPbsCmd(), "pbs", "update", "--comment", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update PBS realm")
}
