package access

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestAccess_TfaList_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("GET /api2/json/access/tfa", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{"userid": "root@pam", "entries": []any{
				map[string]any{"id": "totp1", "type": "totp"},
				map[string]any{"id": "rec", "type": "recovery"},
			}},
			map[string]any{"userid": "alice@pve", "entries": []any{}},
		})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tfa", "list"))

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/access/tfa", rec.path)

	out := buf.String()
	require.Contains(t, out, "USERID")
	require.Contains(t, out, "ENTRIES")
	require.Contains(t, out, "root@pam")
	require.Contains(t, out, "alice@pve")
	// root@pam has two entries; the count must surface.
	require.Contains(t, out, "2")
}

func TestAccess_TfaGet_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("GET /api2/json/access/tfa/root@pam", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{"id": "totp1", "type": "totp", "description": "phone", "enable": 1, "created": 1700000000},
		})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tfa", "get", "root@pam"))

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/access/tfa/root@pam", rec.path)

	out := buf.String()
	require.Contains(t, out, "ID")
	require.Contains(t, out, "TYPE")
	require.Contains(t, out, "DESCRIPTION")
	require.Contains(t, out, "totp1")
	require.Contains(t, out, "phone")
	require.Contains(t, out, "1700000000")
}

func TestAccess_TfaDelete_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var called bool
	f.HandleFunc("DELETE /api2/json/access/tfa/root@pam/totp1", func(w http.ResponseWriter, r *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "tfa", "delete", "root@pam", "totp1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "delete must not issue a DELETE without --yes")
}

func TestAccess_TfaDelete_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	var password string
	f.HandleFunc("DELETE /api2/json/access/tfa/root@pam/totp1", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		password = r.URL.Query().Get("password")
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tfa", "delete", "root@pam", "totp1", "--yes", "--password", "secret"))

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, "/api2/json/access/tfa/root@pam/totp1", rec.path)
	// DELETE params are query-encoded; the password must be forwarded.
	require.Equal(t, "secret", password)
	require.Contains(t, buf.String(), "Deleted tfa entry")
}

func TestAccess_TfaDelete_OmitsPasswordWhenUnset(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var hasPassword bool
	f.HandleFunc("DELETE /api2/json/access/tfa/root@pam/totp1", func(w http.ResponseWriter, r *http.Request) {
		_, hasPassword = r.URL.Query()["password"]
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tfa", "delete", "root@pam", "totp1", "--yes"))

	// With --password unset, the only-changed-flags forwarding must omit the
	// query param entirely rather than send an empty value.
	require.False(t, hasPassword, "password query param must be absent when --password is not given")
}

func TestAccess_TfaUnlock_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var called bool
	f.HandleFunc("PUT /api2/json/access/users/alice@pve/unlock-tfa", func(w http.ResponseWriter, r *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "tfa", "unlock", "alice@pve")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "unlock must not issue a PUT without --yes")
}

func TestAccess_TfaUnlock_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/users/alice@pve/unlock-tfa", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tfa", "unlock", "alice@pve", "--yes"))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "/api2/json/access/users/alice@pve/unlock-tfa", rec.path)
	require.Contains(t, buf.String(), "Unlocked tfa")
}

func TestAccess_TfaUnlock_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("PUT /api2/json/access/users/alice@pve/unlock-tfa", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "user not found")
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "tfa", "unlock", "alice@pve", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unlock tfa")
}

func TestAccess_TfaCommandTree(t *testing.T) {
	cmd := newTfaCmd()
	want := map[string]bool{
		"list":      false,
		"get":       false,
		"get-entry": false,
		"create":    false,
		"set":       false,
		"delete":    false,
		"unlock":    false,
		"types":     false,
	}
	for _, c := range cmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		require.Truef(t, found, "access tfa must expose a %q sub-command", name)
	}
}

// ---------------------------------------------------------------------------
// tfa get-entry
// ---------------------------------------------------------------------------

func TestAccess_TfaGetEntry_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("GET /api2/json/access/tfa/root@pam/totp1", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"id": "totp1", "type": "totp", "description": "phone", "enable": 1, "created": 1700000000,
		})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tfa", "get-entry", "root@pam", "totp1"))

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/access/tfa/root@pam/totp1", rec.path)

	out := buf.String()
	require.Contains(t, out, "totp1")
	require.Contains(t, out, "totp")
	require.Contains(t, out, "phone")
	require.Contains(t, out, "1700000000")
}

func TestAccess_TfaGetEntry_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/access/tfa/root@pam/nope", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such entry")
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "tfa", "get-entry", "root@pam", "nope")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get tfa entry")
}

// ---------------------------------------------------------------------------
// tfa create
// ---------------------------------------------------------------------------

func TestAccess_TfaCreate_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/tfa/root@pam", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path, rec.body = r.Method, r.URL.Path, captureBody(r)
		testhelper.WriteData(w, map[string]any{
			"id":       "totp-new",
			"recovery": []string{},
		})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tfa", "create", "root@pam",
		"--type", "totp",
		"--password", "secret",
		"--description", "work phone",
	))

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "/api2/json/access/tfa/root@pam", rec.path)
	// type and description must reach the body; password is forwarded but we
	// do NOT assert its value in the test to follow the secret-handling rule.
	require.Equal(t, "totp", rec.body["type"])
	require.Equal(t, "work phone", rec.body["description"])
	// password must be present in the body (PVE requires it) but we only
	// assert presence, not value.
	_, hasPassword := rec.body["password"]
	require.True(t, hasPassword, "password must be forwarded to the API body")

	require.Contains(t, buf.String(), "totp-new")
}

func TestAccess_TfaCreate_RecoveryCodes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/access/tfa/alice@pve", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"id":       "rec1",
			"recovery": []string{"code-a", "code-b", "code-c"},
		})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tfa", "create", "alice@pve",
		"--type", "recovery",
		"--password", "secret",
	))

	// Recovery codes must appear in the rendered output.
	out := buf.String()
	require.Contains(t, out, "code-a")
	require.Contains(t, out, "code-b")
	require.Contains(t, out, "code-c")
}

func TestAccess_TfaCreate_RequiresType(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "tfa", "create", "root@pam", "--password", "s")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--type")
}

func TestAccess_TfaCreate_RejectsInvalidType(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "tfa", "create", "root@pam", "--type", "bogus", "--password", "s")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid --type")
}

func TestAccess_TfaCreate_RequiresPassword(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/access/tfa/root@pam", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, map[string]any{"id": "x"})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	// Feed empty stdin so the prompt returns empty password.
	err := runWithStdin(deps, &buf, "\n", "tfa", "create", "root@pam", "--type", "totp")
	require.Error(t, err)
	require.Contains(t, err.Error(), "password")
	require.False(t, called, "API must not be called without a password")
}

func TestAccess_TfaCreate_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/access/tfa/root@pam", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "permission denied")
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "tfa", "create", "root@pam", "--type", "totp", "--password", "secret")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create tfa entry")
}

// ---------------------------------------------------------------------------
// tfa set
// ---------------------------------------------------------------------------

func TestAccess_TfaSet_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/tfa/root@pam/totp1", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path, rec.body = r.Method, r.URL.Path, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tfa", "set", "root@pam", "totp1",
		"--description", "updated",
		"--password", "secret",
	))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "/api2/json/access/tfa/root@pam/totp1", rec.path)
	require.Equal(t, "updated", rec.body["description"])
	// password forwarded; assert presence only.
	_, hasPassword := rec.body["password"]
	require.True(t, hasPassword, "password must be forwarded to the API body")
	require.Contains(t, buf.String(), "updated")
}

func TestAccess_TfaSet_EnableFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/tfa/root@pam/totp1", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.body = r.Method, captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tfa", "set", "root@pam", "totp1",
		"--enable=false", "--password", "secret",
	))

	require.Equal(t, http.MethodPut, rec.method)
	// The API client marshals the bool param via JSON; false encodes as "0"
	// through PVE's bool-to-string convention (same as sibling token commands).
	// Acceptable values seen from the wire are "false" or "0"; we assert what
	// the library actually sends so we catch regressions.
	enableVal, _ := rec.body["enable"].(string)
	require.True(t, enableVal == "false" || enableVal == "0",
		"enable=false must encode to 'false' or '0', got %q", enableVal)
}

func TestAccess_TfaSet_RequiresAtLeastOneField(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("PUT /api2/json/access/tfa/root@pam/totp1", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	// Feed non-empty password via stdin so the password check passes, but no
	// other update fields are given — the command must still reject this.
	err := runWithStdin(deps, &buf, "secret\n", "tfa", "set", "root@pam", "totp1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least one")
	require.False(t, called, "API must not be called when no fields are being changed")
}

func TestAccess_TfaSet_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("PUT /api2/json/access/tfa/root@pam/totp1", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "tfa", "set", "root@pam", "totp1",
		"--description", "x", "--password", "secret")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update tfa entry")
}

// ---------------------------------------------------------------------------
// tfa types (ListUsersTfa)
// ---------------------------------------------------------------------------

func TestAccess_TfaTypes_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("GET /api2/json/access/users/root@pam/tfa", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"realm": "pam",
			"user":  "totp",
			"types": []string{"totp", "recovery"},
		})
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "tfa", "types", "root@pam"))

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/access/users/root@pam/tfa", rec.path)

	out := buf.String()
	require.Contains(t, out, "pam")
	require.Contains(t, out, "totp")
	require.Contains(t, out, "recovery")
}

func TestAccess_TfaTypes_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/access/users/root@pam/tfa", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "denied")
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "tfa", "types", "root@pam")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list tfa types")
}
