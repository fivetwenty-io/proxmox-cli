package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// usersPath is the base /access/users endpoint.
const usersPath = "/api2/json/access/users"

// testUserid is the sample userid reused across user tests.
const testUserid = "alice@pbs"

// --- user ls ---------------------------------------------------------------

func TestUserLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+usersPath, &rec, []map[string]any{
		{"userid": "bob@pbs", "enable": true, "email": "bob@example.com"},
		{"userid": "alice@pbs", "enable": false, "comment": "primary admin"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, usersPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "alice@pbs")
	require.Contains(t, out, "bob@pbs")
	require.Contains(t, out, "primary admin")
}

func TestUserLs_IncludeTokensSendsFlag(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+usersPath, &rec, []map[string]any{})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "ls", "--include-tokens")
	require.NoError(t, err)
	require.Equal(t, "1", rec.query.Get("include_tokens"))
}

func TestUserLs_OmitsIncludeTokensWhenUnset(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+usersPath, &rec, []map[string]any{})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "ls")
	require.NoError(t, err)
	_, present := rec.query["include_tokens"]
	require.False(t, present)
}

func TestUserLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+usersPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list users")
}

// --- user show ---------------------------------------------------------------

func TestUserShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+usersPath+"/"+testUserid, map[string]any{
		"userid": testUserid, "email": "alice@example.com", "comment": "primary admin", "enable": true,
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "show", testUserid)
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "alice@example.com")
	require.Contains(t, out, "primary admin")
}

func TestUserShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+usersPath+"/"+testUserid, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such user")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "show", testUserid)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get user")
}

// --- user add ---------------------------------------------------------------

func TestUserAdd_CreatesUser(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+usersPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "add", testUserid, "--password", "secretpw12")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, usersPath, rec.path)
	require.Equal(t, testUserid, rec.form.Get("userid"))
	require.Equal(t, "secretpw12", rec.form.Get("password"))
	require.Contains(t, buf.String(), `User "alice@pbs" created.`)
}

func TestUserAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+usersPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid user")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "add", testUserid)
	require.Error(t, err)
	require.Contains(t, err.Error(), "create user")
}

func TestUserAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+usersPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "add", testUserid,
		"--comment", "audit comment",
		"--email", "alice@example.com",
		"--enable=false",
		"--expire", "1700000000",
		"--firstname", "Alice",
		"--lastname", "Anderson",
		"--password", "secretpw12",
	)
	require.NoError(t, err)

	want := map[string]string{
		"userid":    testUserid,
		"comment":   "audit comment",
		"email":     "alice@example.com",
		"enable":    "0",
		"expire":    "1700000000",
		"firstname": "Alice",
		"lastname":  "Anderson",
		"password":  "secretpw12",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

func TestUserAdd_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+usersPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "add", testUserid)
	require.NoError(t, err)

	for _, key := range []string{"comment", "email", "enable", "expire", "firstname", "lastname", "password"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

// --- user update ---------------------------------------------------------------

func TestUserUpdate_UpdatesUser(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+usersPath+"/"+testUserid, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "update", testUserid,
		"--comment", "new comment", "--digest", "abc123", "--delete", "email")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, usersPath+"/"+testUserid, rec.path)
	require.Equal(t, "new comment", rec.form.Get("comment"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"email"}, rec.form["delete"])
	require.Contains(t, buf.String(), `User "alice@pbs" updated.`)
}

func TestUserUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+usersPath+"/"+testUserid, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "update", testUserid,
		"--comment", "audit comment",
		"--delete", "email",
		"--delete", "firstname",
		"--digest", "abc123",
		"--email", "alice2@example.com",
		"--enable=false",
		"--expire", "1700000000",
		"--firstname", "Alicia",
		"--lastname", "Anders",
		"--password", "ignored-value",
	)
	require.NoError(t, err)

	want := map[string]string{
		"comment":   "audit comment",
		"digest":    "abc123",
		"email":     "alice2@example.com",
		"enable":    "0",
		"expire":    "1700000000",
		"firstname": "Alicia",
		"lastname":  "Anders",
		"password":  "ignored-value",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.ElementsMatch(t, []string{"email", "firstname"}, rec.form["delete"])
}

func TestUserUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+usersPath+"/"+testUserid, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "update", testUserid, "--comment", "only comment")
	require.NoError(t, err)
	require.Equal(t, "only comment", rec.form.Get("comment"))

	for _, key := range []string{"delete", "digest", "email", "enable", "expire", "firstname", "lastname", "password"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestUserUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "update", testUserid)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestUserUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "update", testUserid, "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestUserUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+usersPath+"/"+testUserid, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "update", testUserid, "--comment", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update user")
}

// --- user delete ---------------------------------------------------------------

func TestUserDelete_DeletesUser(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+usersPath+"/"+testUserid, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "delete", testUserid, "--digest", "abc123", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, usersPath+"/"+testUserid, rec.path)
	require.Equal(t, "abc123", rec.query.Get("digest"))
	require.Contains(t, buf.String(), `User "alice@pbs" deleted.`)
}

func TestUserDelete_OmitsDigestWhenUnset(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+usersPath+"/"+testUserid, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "delete", testUserid, "--yes")
	require.NoError(t, err)
	_, present := rec.query["digest"]
	require.False(t, present)
}

func TestUserDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+usersPath+"/"+testUserid, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "delete", testUserid, "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete user")
}

func TestUserDelete_WithoutConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	var called bool
	f.HandleFunc("DELETE "+usersPath+"/"+testUserid, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "delete", testUserid)
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.False(t, called, "no request must be issued without --yes")
}

// --- user unlock-tfa ---------------------------------------------------------------

func TestUserUnlockTfa_WasLockedRendersMessage(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+usersPath+"/"+testUserid+"/unlock-tfa", &rec, true)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "unlock-tfa", testUserid)
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, usersPath+"/"+testUserid+"/unlock-tfa", rec.path)
	require.Contains(t, buf.String(), "TFA unlocked")
}

func TestUserUnlockTfa_WasNotLockedRendersMessage(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "PUT "+usersPath+"/"+testUserid+"/unlock-tfa", &recordedRequest{}, false)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "unlock-tfa", testUserid)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "was not TFA-locked")
}

func TestUserUnlockTfa_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+usersPath+"/"+testUserid+"/unlock-tfa", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "unlock failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "unlock-tfa", testUserid)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unlock tfa")
}

// --- user passwd ---------------------------------------------------------------

func TestUserPasswd_ChangesPassword(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/access/password", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "passwd", testUserid, "--password", "newsecretpw", "--confirmation-password", "oldpw")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, testUserid, rec.form.Get("userid"))
	require.Equal(t, "newsecretpw", rec.form.Get("password"))
	require.Equal(t, "oldpw", rec.form.Get("confirmation-password"))
	require.Contains(t, buf.String(), "Password for user")
}

func TestUserPasswd_RequiresPasswordFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "passwd", testUserid)
	require.Error(t, err)
}

func TestUserPasswd_OmitsConfirmationPasswordWhenUnset(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/access/password", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "passwd", testUserid, "--password", "newsecretpw")
	require.NoError(t, err)
	_, present := rec.form["confirmation-password"]
	require.False(t, present)
}

func TestUserPasswd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/access/password", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "password change failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newUserCmd(), "user", "passwd", testUserid, "--password", "newsecretpw")
	require.Error(t, err)
	require.Contains(t, err.Error(), "change password")
}
