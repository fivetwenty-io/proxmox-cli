package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// realmLdapConfigPath is the base /config/access/ldap endpoint.
const realmLdapConfigPath = "/api2/json/config/access/ldap"

// realmLdapName is the sample LDAP realm name reused across ldap tests.
const realmLdapName = "ldap1"

// --- realm ldap ls ---------------------------------------------------------------

func TestRealmLdapLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+realmLdapConfigPath, &rec, []map[string]any{
		{"realm": "ldap2", "server1": "ldap2.example.com", "base-dn": "dc=example,dc=com", "user-attr": "uid"},
		{"realm": "ldap1", "server1": "ldap1.example.com", "base-dn": "dc=example,dc=com", "user-attr": "uid", "comment": "primary LDAP"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, realmLdapConfigPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "ldap1")
	require.Contains(t, out, "ldap2")
	require.Contains(t, out, "ldap1.example.com")
}

func TestRealmLdapLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+realmLdapConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list LDAP realms")
}

// --- realm ldap show ---------------------------------------------------------------

func TestRealmLdapShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+realmLdapConfigPath+"/"+realmLdapName, map[string]any{
		"realm": realmLdapName, "server1": "ldap1.example.com", "base-dn": "dc=example,dc=com",
		"user-attr": "uid", "comment": "primary LDAP",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "show", realmLdapName)
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "ldap1.example.com")
	require.Contains(t, out, "primary LDAP")
}

func TestRealmLdapShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+realmLdapConfigPath+"/"+realmLdapName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such realm")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "show", realmLdapName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get LDAP realm")
}

// --- realm ldap add ---------------------------------------------------------------

func TestRealmLdapAdd_CreatesRealm(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+realmLdapConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "add", realmLdapName,
		"--server1", "ldap1.example.com", "--base-dn", "dc=example,dc=com", "--user-attr", "uid")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, realmLdapConfigPath, rec.path)
	require.Equal(t, realmLdapName, rec.form.Get("realm"))
	require.Equal(t, "ldap1.example.com", rec.form.Get("server1"))
	require.Equal(t, "dc=example,dc=com", rec.form.Get("base-dn"))
	require.Equal(t, "uid", rec.form.Get("user-attr"))
	require.Contains(t, buf.String(), `LDAP realm "ldap1" created.`)
}

func TestRealmLdapAdd_RequiresBaseDn(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "add", realmLdapName,
		"--server1", "ldap1.example.com", "--user-attr", "uid")
	require.Error(t, err)
}

func TestRealmLdapAdd_RequiresServer1(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "add", realmLdapName,
		"--base-dn", "dc=example,dc=com", "--user-attr", "uid")
	require.Error(t, err)
}

func TestRealmLdapAdd_RequiresUserAttr(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "add", realmLdapName,
		"--server1", "ldap1.example.com", "--base-dn", "dc=example,dc=com")
	require.Error(t, err)
}

func TestRealmLdapAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+realmLdapConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid realm")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "add", realmLdapName,
		"--server1", "ldap1.example.com", "--base-dn", "dc=example,dc=com", "--user-attr", "uid")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create LDAP realm")
}

func TestRealmLdapAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+realmLdapConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "add", "audit-ldap",
		"--server1", "ldap1.example.com",
		"--base-dn", "dc=example,dc=com",
		"--user-attr", "uid",
		"--bind-dn", "cn=admin,dc=example,dc=com",
		"--capath", "/etc/ssl/certs",
		"--comment", "audit comment",
		"--default",
		"--filter", "(objectClass=posixAccount)",
		"--mode", "ldap+starttls",
		"--password", "secret",
		"--port", "389",
		"--server2", "ldap2.example.com",
		"--sync-attributes", "email=mail",
		"--sync-defaults-options", "remove-vanished=entry",
		"--user-classes", "posixAccount",
		"--verify",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)

	want := map[string]string{
		"realm":                 "audit-ldap",
		"server1":               "ldap1.example.com",
		"base-dn":               "dc=example,dc=com",
		"user-attr":             "uid",
		"bind-dn":               "cn=admin,dc=example,dc=com",
		"capath":                "/etc/ssl/certs",
		"comment":               "audit comment",
		"default":               "1",
		"filter":                "(objectClass=posixAccount)",
		"mode":                  "ldap+starttls",
		"password":              "secret",
		"port":                  "389",
		"server2":               "ldap2.example.com",
		"sync-attributes":       "email=mail",
		"sync-defaults-options": "remove-vanished=entry",
		"user-classes":          "posixAccount",
		"verify":                "1",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

// --- realm ldap update ---------------------------------------------------------------

func TestRealmLdapUpdate_UpdatesRealm(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+realmLdapConfigPath+"/"+realmLdapName, &rec, map[string]any{
		"realm": realmLdapName, "server1": "ldap1.example.com", "base-dn": "dc=example,dc=com", "user-attr": "uid",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "update", realmLdapName,
		"--comment", "updated", "--digest", "abc123", "--delete", "comment", "--delete", "filter")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, realmLdapConfigPath+"/"+realmLdapName, rec.path)
	require.Equal(t, "updated", rec.form.Get("comment"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"comment", "filter"}, rec.form["delete"])
	require.Contains(t, buf.String(), `LDAP realm "ldap1" updated.`)
}

func TestRealmLdapUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+realmLdapConfigPath+"/"+realmLdapName, &rec, map[string]any{
		"realm": realmLdapName, "server1": "ldap1.example.com", "base-dn": "dc=example,dc=com", "user-attr": "uid",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "update", realmLdapName,
		"--server1", "ldap1-new.example.com",
		"--base-dn", "dc=example,dc=com",
		"--user-attr", "uid",
		"--bind-dn", "cn=admin,dc=example,dc=com",
		"--capath", "/etc/ssl/certs",
		"--comment", "audit comment",
		"--default",
		"--filter", "(objectClass=posixAccount)",
		"--mode", "ldaps",
		"--password", "secret",
		"--port", "636",
		"--server2", "ldap2.example.com",
		"--sync-attributes", "email=mail",
		"--sync-defaults-options", "remove-vanished=entry",
		"--user-classes", "posixAccount",
		"--verify",
		"--digest", "abc123",
	)
	require.NoError(t, err)

	want := map[string]string{
		"server1":               "ldap1-new.example.com",
		"base-dn":               "dc=example,dc=com",
		"user-attr":             "uid",
		"bind-dn":               "cn=admin,dc=example,dc=com",
		"capath":                "/etc/ssl/certs",
		"comment":               "audit comment",
		"default":               "1",
		"filter":                "(objectClass=posixAccount)",
		"mode":                  "ldaps",
		"password":              "secret",
		"port":                  "636",
		"server2":               "ldap2.example.com",
		"sync-attributes":       "email=mail",
		"sync-defaults-options": "remove-vanished=entry",
		"user-classes":          "posixAccount",
		"verify":                "1",
		"digest":                "abc123",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

func TestRealmLdapUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+realmLdapConfigPath+"/"+realmLdapName, &rec, map[string]any{
		"realm": realmLdapName, "server1": "ldap1.example.com", "base-dn": "dc=example,dc=com", "user-attr": "uid",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "update", realmLdapName, "--comment", "only-comment")
	require.NoError(t, err)
	require.Equal(t, "only-comment", rec.form.Get("comment"))

	for _, key := range []string{
		"server1", "base-dn", "user-attr", "bind-dn", "capath", "default", "filter", "mode",
		"password", "port", "server2", "sync-attributes", "sync-defaults-options", "user-classes",
		"verify", "delete", "digest",
	} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestRealmLdapUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "update", realmLdapName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes given")
}

func TestRealmLdapUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "update", realmLdapName, "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestRealmLdapUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+realmLdapConfigPath+"/"+realmLdapName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "update", realmLdapName, "--comment", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update LDAP realm")
}

// --- realm ldap delete ---------------------------------------------------------------

func TestRealmLdapDelete_DeletesRealm(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+realmLdapConfigPath+"/"+realmLdapName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "delete", realmLdapName, "--digest", "abc123")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, realmLdapConfigPath+"/"+realmLdapName, rec.path)
	require.Equal(t, "abc123", rec.query.Get("digest"))
	require.Contains(t, buf.String(), `LDAP realm "ldap1" deleted.`)
}

func TestRealmLdapDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+realmLdapConfigPath+"/"+realmLdapName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmLdapCmd(), "ldap", "delete", realmLdapName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete LDAP realm")
}
