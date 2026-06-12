package access

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestAccess_RoleCreate_ForwardsPrivs(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/roles", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		rec.body = captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "role", "create", "pve-cli-role", "--privs", "VM.Audit,Datastore.Audit"))

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "/api2/json/access/roles", rec.path)
	require.Equal(t, "pve-cli-role", rec.body["roleid"])
	require.Equal(t, "VM.Audit,Datastore.Audit", rec.body["privs"])
	require.Contains(t, buf.String(), "created")
}

func TestAccess_RoleCreate_OmitsPrivsWhenUnset(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("POST /api2/json/access/roles", func(w http.ResponseWriter, r *http.Request) {
		rec.body = captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "role", "create", "pve-cli-role"))

	require.Equal(t, "pve-cli-role", rec.body["roleid"])
	_, hasPrivs := rec.body["privs"]
	require.False(t, hasPrivs, "unset --privs must not be forwarded")
}

func TestAccess_RoleSet_RequiresPrivs(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var called bool
	f.HandleFunc("PUT /api2/json/access/roles/pve-cli-role", func(w http.ResponseWriter, r *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "role", "set", "pve-cli-role")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--privs is required")
	require.False(t, called, "set must not PUT without --privs")
}

func TestAccess_RoleSet_ForwardsPrivsAndAppend(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("PUT /api2/json/access/roles/pve-cli-role", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		rec.body = captureBody(r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "role", "set", "pve-cli-role", "--privs", "VM.Audit", "--append"))

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "VM.Audit", rec.body["privs"])
	require.Equal(t, "1", rec.body["append"])
	require.Contains(t, buf.String(), "updated")
}

func TestAccess_RoleDelete_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var called bool
	f.HandleFunc("DELETE /api2/json/access/roles/pve-cli-role", func(w http.ResponseWriter, r *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "role", "delete", "pve-cli-role")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "delete must not issue a DELETE without --yes")
}

func TestAccess_RoleDelete_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordReq
	f.HandleFunc("DELETE /api2/json/access/roles/pve-cli-role", func(w http.ResponseWriter, r *http.Request) {
		rec.method, rec.path = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "role", "delete", "pve-cli-role", "--yes"))

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, "/api2/json/access/roles/pve-cli-role", rec.path)
	require.Contains(t, buf.String(), "deleted")
}

func TestAccess_RoleCreate_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/access/roles", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "role already exists")
	})

	deps := newDeps(t, f, output.FormatTable)
	var buf bytes.Buffer
	err := run(deps, &buf, "role", "create", "pve-cli-role")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create role")
}

func TestAccess_RoleCommandTree(t *testing.T) {
	cmd := newRoleCmd()
	want := map[string]bool{"list": false, "get": false, "create": false, "set": false, "delete": false}
	for _, c := range cmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		require.Truef(t, found, "access role must expose a %q sub-command", name)
	}
}
