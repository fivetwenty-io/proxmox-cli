package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// testTokenName is the sample token-name reused across token tests.
const testTokenName = "mytoken"

// userTokenPath returns the base /access/users/{userid}/token endpoint.
func userTokenPath() string {
	return usersPath + "/" + testUserid + "/token"
}

// --- user token ls ---------------------------------------------------------------

func TestUserTokenLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+userTokenPath(), &rec, []map[string]any{
		{"tokenid": "alice@pbs!zzz", "enable": true, "expire": 1700000000},
		{"tokenid": "alice@pbs!aaa", "enable": false, "comment": "ci token"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "ls", testUserid)
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, userTokenPath(), rec.path)

	out := buf.String()
	require.Contains(t, out, "alice@pbs!zzz")
	require.Contains(t, out, "alice@pbs!aaa")
	require.Contains(t, out, "ci token")
}

func TestUserTokenLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+userTokenPath(), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "ls", testUserid)
	require.Error(t, err)
	require.Contains(t, err.Error(), "list tokens")
}

// --- user token show ---------------------------------------------------------------

func TestUserTokenShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+userTokenPath()+"/"+testTokenName, map[string]any{
		"tokenid": testUserid + "!" + testTokenName, "comment": "ci token", "enable": true, "expire": 1700000000,
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "show", testUserid, testTokenName)
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "ci token")
	require.Contains(t, out, "tokenid")
}

func TestUserTokenShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+userTokenPath()+"/"+testTokenName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such token")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "show", testUserid, testTokenName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get token")
}

// --- user token add ---------------------------------------------------------------

func TestUserTokenAdd_CreatesTokenAndShowsSecret(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+userTokenPath()+"/"+testTokenName, &rec, map[string]any{
		"tokenid": testUserid + "!" + testTokenName, "value": "the-secret-value",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "add", testUserid, testTokenName)
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, userTokenPath()+"/"+testTokenName, rec.path)

	out := buf.String()
	require.Contains(t, out, "the-secret-value")
	require.Contains(t, out, testUserid+"!"+testTokenName)
}

func TestUserTokenAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+userTokenPath()+"/"+testTokenName, &rec, map[string]any{
		"tokenid": testUserid + "!" + testTokenName, "value": "the-secret-value",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "add", testUserid, testTokenName,
		"--comment", "audit comment",
		"--digest", "abc123",
		"--enable=false",
		"--expire", "1700000000",
	)
	require.NoError(t, err)

	want := map[string]string{
		"comment": "audit comment",
		"digest":  "abc123",
		"enable":  "0",
		"expire":  "1700000000",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

func TestUserTokenAdd_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+userTokenPath()+"/"+testTokenName, &rec, map[string]any{
		"tokenid": testUserid + "!" + testTokenName, "value": "the-secret-value",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "add", testUserid, testTokenName)
	require.NoError(t, err)

	for _, key := range []string{"comment", "digest", "enable", "expire"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestUserTokenAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+userTokenPath()+"/"+testTokenName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid token")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "add", testUserid, testTokenName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "create token")
}

// --- user token update ---------------------------------------------------------------

func TestUserTokenUpdate_UpdatesMetadataOnly(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+userTokenPath()+"/"+testTokenName, &rec, map[string]any{})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "update", testUserid, testTokenName, "--comment", "updated")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "updated", rec.form.Get("comment"))
	require.Contains(t, buf.String(), "updated.")
	require.NotContains(t, buf.String(), "VALUE")
}

func TestUserTokenUpdate_RegenerateShowsNewSecret(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+userTokenPath()+"/"+testTokenName, &rec, map[string]any{"secret": "brand-new-secret"})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "update", testUserid, testTokenName, "--regenerate")
	require.NoError(t, err)

	require.Equal(t, "1", rec.form.Get("regenerate"))
	out := buf.String()
	require.Contains(t, out, "brand-new-secret")
	require.Contains(t, out, testTokenName)
}

func TestUserTokenUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+userTokenPath()+"/"+testTokenName, &rec, map[string]any{})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "update", testUserid, testTokenName,
		"--comment", "audit comment",
		"--delete", "expire",
		"--digest", "abc123",
		"--enable=false",
		"--expire", "1700000000",
		"--regenerate",
	)
	require.NoError(t, err)

	want := map[string]string{
		"comment":    "audit comment",
		"digest":     "abc123",
		"enable":     "0",
		"expire":     "1700000000",
		"regenerate": "1",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.ElementsMatch(t, []string{"expire"}, rec.form["delete"])
}

func TestUserTokenUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+userTokenPath()+"/"+testTokenName, &rec, map[string]any{})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "update", testUserid, testTokenName, "--comment", "only comment")
	require.NoError(t, err)

	for _, key := range []string{"delete", "digest", "enable", "expire", "regenerate"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestUserTokenUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "update", testUserid, testTokenName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes given")
}

func TestUserTokenUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "update", testUserid, testTokenName, "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestUserTokenUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+userTokenPath()+"/"+testTokenName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "update", testUserid, testTokenName, "--comment", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update token")
}

// --- user token delete ---------------------------------------------------------------

func TestUserTokenDelete_DeletesToken(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+userTokenPath()+"/"+testTokenName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "delete", testUserid, testTokenName,
		"--digest", "abc123", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, userTokenPath()+"/"+testTokenName, rec.path)
	require.Equal(t, "abc123", rec.query.Get("digest"))
	require.Contains(t, buf.String(), "deleted.")
}

func TestUserTokenDelete_OmitsDigestWhenUnset(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+userTokenPath()+"/"+testTokenName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "delete", testUserid, testTokenName, "--yes")
	require.NoError(t, err)
	_, present := rec.query["digest"]
	require.False(t, present)
}

func TestUserTokenDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+userTokenPath()+"/"+testTokenName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "delete", testUserid, testTokenName, "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete token")
}

func TestUserTokenDelete_WithoutConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	var called bool
	f.HandleFunc("DELETE "+userTokenPath()+"/"+testTokenName, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "token", "delete", testUserid, testTokenName)
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.False(t, called, "no request must be issued without --yes")
}
