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

	defer withDeps(newDeps(t, f, output.FormatTable))()
	var buf bytes.Buffer
	require.NoError(t, run(&buf, "tfa", "list"))

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

	defer withDeps(newDeps(t, f, output.FormatTable))()
	var buf bytes.Buffer
	require.NoError(t, run(&buf, "tfa", "get", "root@pam"))

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

	defer withDeps(newDeps(t, f, output.FormatTable))()
	var buf bytes.Buffer
	err := run(&buf, "tfa", "delete", "root@pam", "totp1")
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

	defer withDeps(newDeps(t, f, output.FormatTable))()
	var buf bytes.Buffer
	require.NoError(t, run(&buf, "tfa", "delete", "root@pam", "totp1", "--yes", "--password", "secret"))

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

	defer withDeps(newDeps(t, f, output.FormatTable))()
	var buf bytes.Buffer
	require.NoError(t, run(&buf, "tfa", "delete", "root@pam", "totp1", "--yes"))

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

	defer withDeps(newDeps(t, f, output.FormatTable))()
	var buf bytes.Buffer
	err := run(&buf, "tfa", "unlock", "alice@pve")
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

	defer withDeps(newDeps(t, f, output.FormatTable))()
	var buf bytes.Buffer
	require.NoError(t, run(&buf, "tfa", "unlock", "alice@pve", "--yes"))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "/api2/json/access/users/alice@pve/unlock-tfa", rec.path)
	require.Contains(t, buf.String(), "Unlocked tfa")
}

func TestAccess_TfaUnlock_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("PUT /api2/json/access/users/alice@pve/unlock-tfa", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "user not found")
	})

	defer withDeps(newDeps(t, f, output.FormatTable))()
	var buf bytes.Buffer
	err := run(&buf, "tfa", "unlock", "alice@pve", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unlock tfa")
}

func TestAccess_TfaCommandTree(t *testing.T) {
	cmd := newTfaCmd()
	want := map[string]bool{"list": false, "get": false, "delete": false, "unlock": false}
	for _, c := range cmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		require.Truef(t, found, "access tfa must expose a %q sub-command", name)
	}
}
