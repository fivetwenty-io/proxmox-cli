package pdm

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// realmAdConfigPath is the base /config/access/ad endpoint.
const realmAdConfigPath = "/api2/json/config/access/ad"

// realmAdName is the sample AD realm name reused across ad tests.
const realmAdName = "ad1"

// --- realm ad ls ---------------------------------------------------------------

func TestRealmAdLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+realmAdConfigPath, &rec, []map[string]any{
		{"realm": "ad2", "server1": "dc2.example.com", "port": 389, "mode": "ldap"},
		{"realm": "ad1", "server1": "dc1.example.com", "comment": "primary AD"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, realmAdConfigPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "ad1")
	require.Contains(t, out, "ad2")
	require.Contains(t, out, "dc1.example.com")
}

func TestRealmAdLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+realmAdConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list AD realms")
}

// --- realm ad show ---------------------------------------------------------------

func TestRealmAdShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+realmAdConfigPath+"/"+realmAdName, map[string]any{
		"realm": realmAdName, "server1": "dc1.example.com", "comment": "primary AD",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "show", realmAdName)
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "dc1.example.com")
	require.Contains(t, out, "primary AD")
}

func TestRealmAdShow_DefaultsTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+realmAdConfigPath+"/"+realmAdName, map[string]any{
		"realm": realmAdName, "server1": "dc1.example.com",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "show", realmAdName, "--defaults")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "dc1.example.com")
	require.Contains(t, out, "ldap (default)", "mode defaults to ldap")
}

// TestRealmAdShow_DefaultsJSON verifies the JSON set/defaults shape and
// that the write-only bind password is never resurrected as an "unset"
// default: it is excluded from the schema table entirely.
func TestRealmAdShow_DefaultsJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+realmAdConfigPath+"/"+realmAdName, map[string]any{
		"realm": realmAdName, "server1": "dc1.example.com",
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "show", realmAdName, "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"set"`)
	require.Contains(t, buf.String(), `"defaults"`)

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "ldap", got.Defaults["mode"])
	require.NotContains(t, got.Defaults, "password", "password must not appear even as an unset default")
}

func TestRealmAdShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+realmAdConfigPath+"/"+realmAdName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such realm")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "show", realmAdName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get AD realm")
}

// --- realm ad add ---------------------------------------------------------------

func TestRealmAdAdd_CreatesRealm(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+realmAdConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "add", realmAdName, "--server1", "dc1.example.com")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, realmAdConfigPath, rec.path)
	require.Equal(t, realmAdName, rec.form.Get("realm"))
	require.Equal(t, "dc1.example.com", rec.form.Get("server1"))
	require.Contains(t, buf.String(), `AD realm "ad1" created.`)
}

func TestRealmAdAdd_RequiresServer1(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "add", realmAdName)
	require.Error(t, err)
}

func TestRealmAdAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+realmAdConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid realm")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "add", realmAdName, "--server1", "dc1.example.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create AD realm")
}

func TestRealmAdAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+realmAdConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "add", "audit-ad",
		"--server1", "dc1.example.com",
		"--base-dn", "dc=example,dc=com",
		"--bind-dn", "cn=admin,dc=example,dc=com",
		"--capath", "/etc/ssl/certs",
		"--comment", "audit comment",
		"--default",
		"--filter", "(objectClass=user)",
		"--mode", "ldaps",
		"--password", "secret",
		"--port", "636",
		"--server2", "dc2.example.com",
		"--sync-attributes", "email=mail",
		"--sync-defaults-options", "remove-vanished=entry",
		"--user-classes", "person,user",
		"--verify",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)

	want := map[string]string{
		"realm":                 "audit-ad",
		"server1":               "dc1.example.com",
		"base-dn":               "dc=example,dc=com",
		"bind-dn":               "cn=admin,dc=example,dc=com",
		"capath":                "/etc/ssl/certs",
		"comment":               "audit comment",
		"default":               "1",
		"filter":                "(objectClass=user)",
		"mode":                  "ldaps",
		"password":              "secret",
		"port":                  "636",
		"server2":               "dc2.example.com",
		"sync-attributes":       "email=mail",
		"sync-defaults-options": "remove-vanished=entry",
		"user-classes":          "person,user",
		"verify":                "1",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

// --- realm ad update ---------------------------------------------------------------

func TestRealmAdUpdate_UpdatesRealm(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+realmAdConfigPath+"/"+realmAdName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "update", realmAdName,
		"--comment", "updated", "--digest", "abc123", "--delete", "comment", "--delete", "filter")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, realmAdConfigPath+"/"+realmAdName, rec.path)
	require.Equal(t, "updated", rec.form.Get("comment"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"comment", "filter"}, rec.form["delete"])
	require.Contains(t, buf.String(), `AD realm "ad1" updated.`)
}

func TestRealmAdUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+realmAdConfigPath+"/"+realmAdName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "update", realmAdName,
		"--server1", "dc1-new.example.com",
		"--base-dn", "dc=example,dc=com",
		"--bind-dn", "cn=admin,dc=example,dc=com",
		"--capath", "/etc/ssl/certs",
		"--comment", "audit comment",
		"--default",
		"--filter", "(objectClass=user)",
		"--mode", "ldaps",
		"--password", "secret",
		"--port", "636",
		"--server2", "dc2.example.com",
		"--sync-attributes", "email=mail",
		"--sync-defaults-options", "remove-vanished=entry",
		"--user-classes", "person,user",
		"--verify",
		"--digest", "abc123",
	)
	require.NoError(t, err)

	want := map[string]string{
		"server1":               "dc1-new.example.com",
		"base-dn":               "dc=example,dc=com",
		"bind-dn":               "cn=admin,dc=example,dc=com",
		"capath":                "/etc/ssl/certs",
		"comment":               "audit comment",
		"default":               "1",
		"filter":                "(objectClass=user)",
		"mode":                  "ldaps",
		"password":              "secret",
		"port":                  "636",
		"server2":               "dc2.example.com",
		"sync-attributes":       "email=mail",
		"sync-defaults-options": "remove-vanished=entry",
		"user-classes":          "person,user",
		"verify":                "1",
		"digest":                "abc123",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

func TestRealmAdUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+realmAdConfigPath+"/"+realmAdName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "update", realmAdName, "--comment", "only-comment")
	require.NoError(t, err)
	require.Equal(t, "only-comment", rec.form.Get("comment"))

	for _, key := range []string{
		"server1", "base-dn", "bind-dn", "capath", "default", "filter", "mode", "password",
		"port", "server2", "sync-attributes", "sync-defaults-options", "user-classes", "verify",
		"delete", "digest",
	} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestRealmAdUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "update", realmAdName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes given")
}

func TestRealmAdUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "update", realmAdName, "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestRealmAdUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+realmAdConfigPath+"/"+realmAdName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "update", realmAdName, "--comment", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update AD realm")
}

// --- realm ad delete ---------------------------------------------------------------

func TestRealmAdDelete_DeletesRealm(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+realmAdConfigPath+"/"+realmAdName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "delete", realmAdName, "--digest", "abc123", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, realmAdConfigPath+"/"+realmAdName, rec.path)
	require.Equal(t, "abc123", rec.query.Get("digest"))
	require.Contains(t, buf.String(), `AD realm "ad1" deleted.`)
}

func TestRealmAdDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+realmAdConfigPath+"/"+realmAdName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "delete", realmAdName, "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete AD realm")
}

func TestRealmAdDelete_WithoutConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	var called bool
	f.HandleFunc("DELETE "+realmAdConfigPath+"/"+realmAdName, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmAdCmd(), "ad", "delete", realmAdName)
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.False(t, called, "no request must be issued without --yes")
}
