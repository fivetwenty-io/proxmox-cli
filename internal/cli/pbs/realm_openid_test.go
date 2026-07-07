package pbs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// realmOpenidConfigPath is the base /config/access/openid endpoint.
const realmOpenidConfigPath = "/api2/json/config/access/openid"

// realmOpenidName is the sample OpenID realm name reused across openid tests.
const realmOpenidName = "oidc1"

// --- realm openid ls ---------------------------------------------------------------

func TestRealmOpenidLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+realmOpenidConfigPath, &rec, []map[string]any{
		{"realm": "oidc2", "issuer-url": "https://idp2.example.com", "client-id": "client2"},
		{"realm": "oidc1", "issuer-url": "https://idp1.example.com", "client-id": "client1", "comment": "primary IdP"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, realmOpenidConfigPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "oidc1")
	require.Contains(t, out, "oidc2")
	require.Contains(t, out, "https://idp1.example.com")
}

func TestRealmOpenidLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+realmOpenidConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list OpenID realms")
}

// --- realm openid show ---------------------------------------------------------------

func TestRealmOpenidShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+realmOpenidConfigPath+"/"+realmOpenidName, map[string]any{
		"realm": realmOpenidName, "issuer-url": "https://idp1.example.com", "client-id": "client1",
		"comment": "primary IdP",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "show", realmOpenidName)
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "https://idp1.example.com")
	require.Contains(t, out, "primary IdP")
}

func TestRealmOpenidShow_DefaultsTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+realmOpenidConfigPath+"/"+realmOpenidName, map[string]any{
		"realm": realmOpenidName, "issuer-url": "https://idp1.example.com", "client-id": "client1",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "show", realmOpenidName, "--defaults")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "client1")
	require.Contains(t, out, "email profile (default)", "scopes defaults to email profile")
}

// TestRealmOpenidShow_DefaultsJSON verifies the JSON set/defaults shape and
// that the write-only client key is never resurrected as an "unset"
// default: it is excluded from the schema table entirely.
func TestRealmOpenidShow_DefaultsJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+realmOpenidConfigPath+"/"+realmOpenidName, map[string]any{
		"realm": realmOpenidName, "issuer-url": "https://idp1.example.com", "client-id": "client1",
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "show", realmOpenidName, "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"set"`)
	require.Contains(t, buf.String(), `"defaults"`)

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "email profile", got.Defaults["scopes"])
	require.NotContains(t, got.Defaults, "client-key", "client-key must not appear even as an unset default")
}

func TestRealmOpenidShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+realmOpenidConfigPath+"/"+realmOpenidName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such realm")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "show", realmOpenidName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get OpenID realm")
}

// --- realm openid add ---------------------------------------------------------------

func TestRealmOpenidAdd_CreatesRealm(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+realmOpenidConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "add", realmOpenidName,
		"--client-id", "client1", "--issuer-url", "https://idp1.example.com")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, realmOpenidConfigPath, rec.path)
	require.Equal(t, realmOpenidName, rec.form.Get("realm"))
	require.Equal(t, "client1", rec.form.Get("client-id"))
	require.Equal(t, "https://idp1.example.com", rec.form.Get("issuer-url"))
	require.Contains(t, buf.String(), `OpenID realm "oidc1" created.`)
}

func TestRealmOpenidAdd_RequiresClientId(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "add", realmOpenidName,
		"--issuer-url", "https://idp1.example.com")
	require.Error(t, err)
}

func TestRealmOpenidAdd_RequiresIssuerUrl(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "add", realmOpenidName, "--client-id", "client1")
	require.Error(t, err)
}

func TestRealmOpenidAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+realmOpenidConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid realm")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "add", realmOpenidName,
		"--client-id", "client1", "--issuer-url", "https://idp1.example.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create OpenID realm")
}

func TestRealmOpenidAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+realmOpenidConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "add", "audit-oidc",
		"--client-id", "client1",
		"--issuer-url", "https://idp1.example.com",
		"--acr-values", "urn:mace:incommon:iap:silver",
		"--audiences", "aud1,aud2",
		"--autocreate",
		"--client-key", "secret",
		"--comment", "audit comment",
		"--default",
		"--prompt", "consent",
		"--scopes", "email profile",
		"--username-claim", "sub",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)

	want := map[string]string{
		"realm":          "audit-oidc",
		"client-id":      "client1",
		"issuer-url":     "https://idp1.example.com",
		"acr-values":     "urn:mace:incommon:iap:silver",
		"audiences":      "aud1,aud2",
		"autocreate":     "1",
		"client-key":     "secret",
		"comment":        "audit comment",
		"default":        "1",
		"prompt":         "consent",
		"scopes":         "email profile",
		"username-claim": "sub",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

// --- realm openid update ---------------------------------------------------------------

func TestRealmOpenidUpdate_UpdatesRealm(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+realmOpenidConfigPath+"/"+realmOpenidName, &rec, map[string]any{
		"realm": realmOpenidName, "client-id": "client1", "issuer-url": "https://idp1.example.com",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "update", realmOpenidName,
		"--comment", "updated", "--digest", "abc123", "--delete", "comment", "--delete", "prompt")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, realmOpenidConfigPath+"/"+realmOpenidName, rec.path)
	require.Equal(t, "updated", rec.form.Get("comment"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"comment", "prompt"}, rec.form["delete"])
	require.Contains(t, buf.String(), `OpenID realm "oidc1" updated.`)
}

func TestRealmOpenidUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+realmOpenidConfigPath+"/"+realmOpenidName, &rec, map[string]any{
		"realm": realmOpenidName, "client-id": "client1", "issuer-url": "https://idp1.example.com",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "update", realmOpenidName,
		"--client-id", "client1-new",
		"--issuer-url", "https://idp1-new.example.com",
		"--acr-values", "urn:mace:incommon:iap:silver",
		"--audiences", "aud1,aud2",
		"--autocreate",
		"--client-key", "secret",
		"--comment", "audit comment",
		"--default",
		"--prompt", "consent",
		"--scopes", "email profile",
		"--digest", "abc123",
	)
	require.NoError(t, err)

	want := map[string]string{
		"client-id":  "client1-new",
		"issuer-url": "https://idp1-new.example.com",
		"acr-values": "urn:mace:incommon:iap:silver",
		"audiences":  "aud1,aud2",
		"autocreate": "1",
		"client-key": "secret",
		"comment":    "audit comment",
		"default":    "1",
		"prompt":     "consent",
		"scopes":     "email profile",
		"digest":     "abc123",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	_, present := rec.form["username-claim"]
	require.False(t, present, "username-claim has no update flag: it is immutable after realm creation")
}

func TestRealmOpenidUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+realmOpenidConfigPath+"/"+realmOpenidName, &rec, map[string]any{
		"realm": realmOpenidName, "client-id": "client1", "issuer-url": "https://idp1.example.com",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "update", realmOpenidName, "--comment", "only-comment")
	require.NoError(t, err)
	require.Equal(t, "only-comment", rec.form.Get("comment"))

	for _, key := range []string{
		"client-id", "issuer-url", "acr-values", "audiences", "autocreate", "client-key",
		"default", "prompt", "scopes", "delete", "digest",
	} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestRealmOpenidUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "update", realmOpenidName)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes given")
}

func TestRealmOpenidUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "update", realmOpenidName, "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestRealmOpenidUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+realmOpenidConfigPath+"/"+realmOpenidName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "update", realmOpenidName, "--comment", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update OpenID realm")
}

// --- realm openid delete ---------------------------------------------------------------

func TestRealmOpenidDelete_DeletesRealm(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+realmOpenidConfigPath+"/"+realmOpenidName, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "delete", realmOpenidName, "--digest", "abc123", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, realmOpenidConfigPath+"/"+realmOpenidName, rec.path)
	require.Equal(t, "abc123", rec.query.Get("digest"))
	require.Contains(t, buf.String(), `OpenID realm "oidc1" deleted.`)
}

func TestRealmOpenidDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+realmOpenidConfigPath+"/"+realmOpenidName, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "delete", realmOpenidName, "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete OpenID realm")
}

func TestRealmOpenidDelete_WithoutConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	var called bool
	f.HandleFunc("DELETE "+realmOpenidConfigPath+"/"+realmOpenidName, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmOpenidCmd(), "openid", "delete", realmOpenidName)
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.False(t, called, "no request must be issued without --yes")
}
