package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// realmPamConfigPath is the /config/access/pam endpoint (singleton, no id).
const realmPamConfigPath = "/api2/json/config/access/pam"

// --- realm pam show ---------------------------------------------------------------

func TestRealmPamShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+realmPamConfigPath, map[string]any{
		"realm": "pam", "type": "pam", "comment": "built-in PAM realm", "default": true,
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPamCmd(), "pam", "show")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "pam")
	require.Contains(t, out, "built-in PAM realm")
}

func TestRealmPamShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+realmPamConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "get failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPamCmd(), "pam", "show")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get PAM realm")
}

// --- realm pam update ---------------------------------------------------------------

func TestRealmPamUpdate_UpdatesRealm(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+realmPamConfigPath, &rec, map[string]any{
		"realm": "pam", "type": "pam",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPamCmd(), "pam", "update",
		"--comment", "updated", "--default", "--digest", "abc123", "--delete", "comment")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, realmPamConfigPath, rec.path)
	require.Equal(t, "updated", rec.form.Get("comment"))
	require.Equal(t, "1", rec.form.Get("default"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"comment"}, rec.form["delete"])
	require.Contains(t, buf.String(), "PAM realm updated.")
}

func TestRealmPamUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+realmPamConfigPath, &rec, map[string]any{
		"realm": "pam", "type": "pam",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPamCmd(), "pam", "update", "--comment", "only-comment")
	require.NoError(t, err)
	require.Equal(t, "only-comment", rec.form.Get("comment"))

	for _, key := range []string{"default", "delete", "digest"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestRealmPamUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPamCmd(), "pam", "update")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes given")
}

func TestRealmPamUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPamCmd(), "pam", "update", "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestRealmPamUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+realmPamConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmPamCmd(), "pam", "update", "--comment", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update PAM realm")
}
