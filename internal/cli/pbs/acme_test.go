package pbs

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// ---------------------------------------------------------------------------
// acme account ls
// ---------------------------------------------------------------------------

func TestAcmeAccountLs_ListsAccountsSortedByName(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/acme/account", &rec, []map[string]any{
		{"name": "zzz"},
		{"name": "aaa"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountLsCmd(), "ls")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/config/acme/account", rec.path)

	out := buf.String()
	aIdx := strings.Index(out, "aaa")
	zIdx := strings.Index(out, "zzz")
	require.True(t, aIdx >= 0 && zIdx >= 0)
	require.Less(t, aIdx, zIdx, "entries must be sorted by name")
}

func TestAcmeAccountLs_EmptyList(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/config/acme/account", &recordedRequest{}, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountLsCmd(), "ls")
	require.NoError(t, err)
}

func TestAcmeAccountLs_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/config/acme/account", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "permission denied")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountLsCmd(), "ls")
	require.Error(t, err)
	require.ErrorContains(t, err, "permission denied")
}

// ---------------------------------------------------------------------------
// acme account show
// ---------------------------------------------------------------------------

func TestAcmeAccountShow_RendersAccountAndPreservesNestedProviderData(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/acme/account/default", &rec, map[string]any{
		"directory": "https://acme-v02.api.letsencrypt.org/directory",
		"location":  "https://acme-v02.api.letsencrypt.org/acme/acct/12345",
		"tos":       "https://letsencrypt.org/documents/LE-SA-v1.4.pdf",
		"account": map[string]any{
			"status":  "valid",
			"contact": []string{"mailto:admin@example.com"},
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountShowCmd(), "show", "default")
	require.NoError(t, err)
	require.Equal(t, "/api2/json/config/acme/account/default", rec.path)

	out := buf.String()
	require.Contains(t, out, "acme-v02.api.letsencrypt.org")
	require.Contains(t, out, "\"status\": \"valid\"")
	require.Contains(t, out, "admin@example.com")
}

func TestAcmeAccountShow_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountShowCmd(), "show", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestAcmeAccountShow_NotFound(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/config/acme/account/ghost", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such account 'ghost'")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountShowCmd(), "show", "ghost")
	require.Error(t, err)
	require.ErrorContains(t, err, "no such account")
}

// ---------------------------------------------------------------------------
// acme account add
// ---------------------------------------------------------------------------

func TestAcmeAccountAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/config/acme/account", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountAddCmd(), "add", "myaccount",
		"--contact", "admin@example.com",
		"--directory", "https://acme-staging-v02.api.letsencrypt.org/directory",
		"--eab-kid", "kid123",
		"--eab-hmac-key", "hmac456",
		"--tos-url", "https://letsencrypt.org/documents/LE-SA-v1.4.pdf",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)

	want := map[string]string{
		"name":         "myaccount",
		"contact":      "admin@example.com",
		"directory":    "https://acme-staging-v02.api.letsencrypt.org/directory",
		"eab_kid":      "kid123",
		"eab_hmac_key": "hmac456",
		"tos_url":      "https://letsencrypt.org/documents/LE-SA-v1.4.pdf",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.Contains(t, buf.String(), "myaccount")
}

func TestAcmeAccountAdd_OmitsUnsetOptionalFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/config/acme/account", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountAddCmd(), "add", "myaccount", "--contact", "admin@example.com")
	require.NoError(t, err)
	require.Equal(t, "myaccount", rec.form.Get("name"))
	require.Equal(t, "admin@example.com", rec.form.Get("contact"))

	for _, key := range []string{"directory", "eab_kid", "eab_hmac_key", "tos_url"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestAcmeAccountAdd_MissingContactRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountAddCmd(), "add", "myaccount")
	require.Error(t, err)
}

func TestAcmeAccountAdd_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountAddCmd(), "add", "", "--contact", "admin@example.com")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestAcmeAccountAdd_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("POST /api2/json/config/acme/account", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "account 'myaccount' already exists")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountAddCmd(), "add", "myaccount", "--contact", "admin@example.com")
	require.Error(t, err)
	require.ErrorContains(t, err, "already exists")
}

// ---------------------------------------------------------------------------
// acme account update
// ---------------------------------------------------------------------------

func TestAcmeAccountUpdate_SendsContact(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/config/acme/account/myaccount", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountUpdateCmd(), "update", "myaccount", "--contact", "new@example.com")
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "new@example.com", rec.form.Get("contact"))
}

func TestAcmeAccountUpdate_NoChangesRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountUpdateCmd(), "update", "myaccount")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes given")
}

func TestAcmeAccountUpdate_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountUpdateCmd(), "update", "", "--contact", "x@example.com")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestAcmeAccountUpdate_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("PUT /api2/json/config/acme/account/myaccount", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such account")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountUpdateCmd(), "update", "myaccount", "--contact", "x@example.com")
	require.Error(t, err)
	require.ErrorContains(t, err, "no such account")
}

// ---------------------------------------------------------------------------
// acme account delete
// ---------------------------------------------------------------------------

func TestAcmeAccountDelete_Success(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/config/acme/account/myaccount", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountDeleteCmd(), "delete", "myaccount", "--force")
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, "1", rec.query.Get("force"))
	require.Contains(t, buf.String(), "myaccount")
}

func TestAcmeAccountDelete_NoForceOmitsParam(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/config/acme/account/myaccount", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountDeleteCmd(), "delete", "myaccount")
	require.NoError(t, err)
	_, hasForce := rec.query["force"]
	require.False(t, hasForce)
}

func TestAcmeAccountDelete_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountDeleteCmd(), "delete", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestAcmeAccountDelete_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("DELETE /api2/json/config/acme/account/myaccount", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "provider unreachable")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeAccountDeleteCmd(), "delete", "myaccount")
	require.Error(t, err)
	require.ErrorContains(t, err, "provider unreachable")
}

// ---------------------------------------------------------------------------
// acme plugin ls
// ---------------------------------------------------------------------------

func TestAcmePluginLs_ListsPluginsSortedByPlugin(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/acme/plugins", &rec, []map[string]any{
		{"plugin": "zzz", "type": "dns", "api": "cloudflare"},
		{"plugin": "aaa", "type": "dns", "api": "route53", "validation-delay": 30},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginLsCmd(), "ls")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/config/acme/plugins", rec.path)

	out := buf.String()
	aIdx := strings.Index(out, "aaa")
	zIdx := strings.Index(out, "zzz")
	require.True(t, aIdx >= 0 && zIdx >= 0)
	require.Less(t, aIdx, zIdx, "entries must be sorted by plugin id")
	require.Contains(t, out, "route53")
	require.Contains(t, out, "30")
}

func TestAcmePluginLs_EmptyList(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/config/acme/plugins", &recordedRequest{}, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginLsCmd(), "ls")
	require.NoError(t, err)
}

func TestAcmePluginLs_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/config/acme/plugins", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "permission denied")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginLsCmd(), "ls")
	require.Error(t, err)
	require.ErrorContains(t, err, "permission denied")
}

// ---------------------------------------------------------------------------
// acme plugin show
// ---------------------------------------------------------------------------

func TestAcmePluginShow_RendersPlugin(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/acme/plugins/dns1", &rec, map[string]any{
		"plugin": "dns1", "type": "dns", "api": "cloudflare", "data": "base64data",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginShowCmd(), "show", "dns1")
	require.NoError(t, err)
	require.Equal(t, "/api2/json/config/acme/plugins/dns1", rec.path)
	require.Contains(t, buf.String(), "cloudflare")
}

func TestAcmePluginShow_EmptyIDRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginShowCmd(), "show", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestAcmePluginShow_NotFound(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/config/acme/plugins/ghost", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such plugin 'ghost'")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginShowCmd(), "show", "ghost")
	require.Error(t, err)
	require.ErrorContains(t, err, "no such plugin")
}

// ---------------------------------------------------------------------------
// acme plugin add
// ---------------------------------------------------------------------------

func TestAcmePluginAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/config/acme/plugins", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginAddCmd(), "add", "dns1",
		"--type", "dns",
		"--api", "cloudflare",
		"--data", "ZG5zLWFwaS10b2tlbj1zM2NyM3Q=",
		"--disable",
		"--validation-delay", "30",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)

	want := map[string]string{
		"id":               "dns1",
		"type":             "dns",
		"api":              "cloudflare",
		"data":             "ZG5zLWFwaS10b2tlbj1zM2NyM3Q=",
		"disable":          "1",
		"validation-delay": "30",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.Contains(t, buf.String(), "dns1")
}

func TestAcmePluginAdd_OmitsUnsetOptionalFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/config/acme/plugins", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginAddCmd(), "add", "dns1",
		"--type", "dns", "--api", "cloudflare", "--data", "ZGF0YQ==")
	require.NoError(t, err)
	require.Equal(t, "dns1", rec.form.Get("id"))

	for _, key := range []string{"disable", "validation-delay"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestAcmePluginAdd_MissingRequiredFlagsRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginAddCmd(), "add", "dns1")
	require.Error(t, err)
}

func TestAcmePluginAdd_EmptyIDRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginAddCmd(), "add", "",
		"--type", "dns", "--api", "cloudflare", "--data", "ZGF0YQ==")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestAcmePluginAdd_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("POST /api2/json/config/acme/plugins", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "plugin 'dns1' already exists")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginAddCmd(), "add", "dns1",
		"--type", "dns", "--api", "cloudflare", "--data", "ZGF0YQ==")
	require.Error(t, err)
	require.ErrorContains(t, err, "already exists")
}

// ---------------------------------------------------------------------------
// acme plugin update
// ---------------------------------------------------------------------------

func TestAcmePluginUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/config/acme/plugins/dns1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginUpdateCmd(), "update", "dns1",
		"--api", "route53",
		"--data", "bmV3ZGF0YQ==",
		"--disable",
		"--validation-delay", "60",
		"--digest", "abc123",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPut, rec.method)

	want := map[string]string{
		"api":              "route53",
		"data":             "bmV3ZGF0YQ==",
		"disable":          "1",
		"validation-delay": "60",
		"digest":           "abc123",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

func TestAcmePluginUpdate_SendsOnlyChangedFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/config/acme/plugins/dns1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginUpdateCmd(), "update", "dns1", "--api", "route53")
	require.NoError(t, err)
	require.Equal(t, "route53", rec.form.Get("api"))

	for _, key := range []string{"data", "disable", "validation-delay", "digest", "delete"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestAcmePluginUpdate_DeleteProperties(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/config/acme/plugins/dns1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginUpdateCmd(), "update", "dns1", "--delete", "data", "--delete", "validation-delay")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"data", "validation-delay"}, rec.form["delete"])
}

func TestAcmePluginUpdate_EmptyDeleteEntryRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginUpdateCmd(), "update", "dns1", "--delete", "")
	require.Error(t, err)
}

func TestAcmePluginUpdate_NoChangesRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginUpdateCmd(), "update", "dns1")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes given")
}

func TestAcmePluginUpdate_EmptyIDRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginUpdateCmd(), "update", "", "--api", "route53")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestAcmePluginUpdate_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("PUT /api2/json/config/acme/plugins/dns1", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusPreconditionFailed, "digest mismatch")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginUpdateCmd(), "update", "dns1", "--api", "route53")
	require.Error(t, err)
	require.ErrorContains(t, err, "digest mismatch")
}

// ---------------------------------------------------------------------------
// acme plugin delete
// ---------------------------------------------------------------------------

func TestAcmePluginDelete_Success(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/config/acme/plugins/dns1", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginDeleteCmd(), "delete", "dns1")
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, rec.method)
	require.Contains(t, buf.String(), "dns1")
}

func TestAcmePluginDelete_EmptyIDRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginDeleteCmd(), "delete", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestAcmePluginDelete_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("DELETE /api2/json/config/acme/plugins/dns1", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such plugin")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmePluginDeleteCmd(), "delete", "dns1")
	require.Error(t, err)
	require.ErrorContains(t, err, "no such plugin")
}

// ---------------------------------------------------------------------------
// acme challenge-schema ls
// ---------------------------------------------------------------------------

func TestAcmeChallengeSchemaLs_ListsSortedByID(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/acme/challenge-schema", &rec, []map[string]any{
		{"id": "zzz", "name": "ZZZ Plugin", "type": "dns", "schema": map[string]any{"foo": "bar"}},
		{"id": "aaa", "name": "AAA Plugin", "type": "dns", "schema": map[string]any{}},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeChallengeSchemaLsCmd(), "ls")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/config/acme/challenge-schema", rec.path)

	out := buf.String()
	aIdx := strings.Index(out, "aaa")
	zIdx := strings.Index(out, "zzz")
	require.True(t, aIdx >= 0 && zIdx >= 0)
	require.Less(t, aIdx, zIdx, "entries must be sorted by id")
	require.Contains(t, out, "AAA Plugin")
}

func TestAcmeChallengeSchemaLs_RawPreservesSchema(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	recordJSON(f, "GET /api2/json/config/acme/challenge-schema", &recordedRequest{}, []map[string]any{
		{"id": "dns1", "name": "DNS1", "type": "dns", "schema": map[string]any{"apikey": map[string]any{"type": "string"}}},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeChallengeSchemaLsCmd(), "ls")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "apikey")
}

func TestAcmeChallengeSchemaLs_EmptyList(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/config/acme/challenge-schema", &recordedRequest{}, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeChallengeSchemaLsCmd(), "ls")
	require.NoError(t, err)
}

func TestAcmeChallengeSchemaLs_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/config/acme/challenge-schema", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeChallengeSchemaLsCmd(), "ls")
	require.Error(t, err)
	require.ErrorContains(t, err, "boom")
}

// ---------------------------------------------------------------------------
// acme directories ls
// ---------------------------------------------------------------------------

func TestAcmeDirectoriesLs_ListsSortedByName(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/acme/directories", &rec, []map[string]any{
		{"name": "ZeroSSL", "url": "https://acme.zerossl.com/v2/DV90"},
		{"name": "Let's Encrypt", "url": "https://acme-v02.api.letsencrypt.org/directory"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeDirectoriesLsCmd(), "ls")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/config/acme/directories", rec.path)

	out := buf.String()
	leIdx := strings.Index(out, "Let's Encrypt")
	zsIdx := strings.Index(out, "ZeroSSL")
	require.True(t, leIdx >= 0 && zsIdx >= 0)
	require.Less(t, leIdx, zsIdx, "entries must be sorted by name")
}

func TestAcmeDirectoriesLs_EmptyList(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/config/acme/directories", &recordedRequest{}, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeDirectoriesLsCmd(), "ls")
	require.NoError(t, err)
}

func TestAcmeDirectoriesLs_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/config/acme/directories", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeDirectoriesLsCmd(), "ls")
	require.Error(t, err)
	require.ErrorContains(t, err, "boom")
}

// ---------------------------------------------------------------------------
// acme tos show
// ---------------------------------------------------------------------------

func TestAcmeTosShow_RendersURL(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/acme/tos", &rec, "https://letsencrypt.org/documents/LE-SA-v1.4.pdf")

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeTosShowCmd(), "show")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/config/acme/tos", rec.path)
	require.Contains(t, buf.String(), "letsencrypt.org/documents/LE-SA-v1.4.pdf")
}

func TestAcmeTosShow_SendsDirectoryParam(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/acme/tos", &rec, "https://example.com/tos")

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeTosShowCmd(), "show",
		"--directory", "https://acme-staging-v02.api.letsencrypt.org/directory")
	require.NoError(t, err)
	require.Equal(t, "https://acme-staging-v02.api.letsencrypt.org/directory", rec.query.Get("directory"))
}

func TestAcmeTosShow_NoDirectoryOmitsParam(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/acme/tos", &rec, "https://example.com/tos")

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeTosShowCmd(), "show")
	require.NoError(t, err)
	_, hasDirectory := rec.query["directory"]
	require.False(t, hasDirectory)
}

func TestAcmeTosShow_NoToSRendersMessageInsteadOfError(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/config/acme/tos", &recordedRequest{}, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeTosShowCmd(), "show")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "No Terms of Service URL")
}

func TestAcmeTosShow_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/config/acme/tos", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "unknown directory")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newAcmeTosShowCmd(), "show", "--directory", "https://bogus.example.com/directory")
	require.Error(t, err)
	require.ErrorContains(t, err, "unknown directory")
}

// ---------------------------------------------------------------------------
// group registration
// ---------------------------------------------------------------------------

func TestNewAcmeCmd_RegistersAllSubcommands(t *testing.T) {
	cmd := newAcmeCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"account", "plugin", "challenge-schema", "directories", "tos"} {
		require.True(t, names[want], "missing subcommand %q", want)
	}

	accountCmd := findSubcommand(t, cmd, "account")
	accountNames := map[string]bool{}
	for _, c := range accountCmd.Commands() {
		accountNames[c.Name()] = true
	}
	for _, want := range []string{"ls", "show", "add", "update", "delete"} {
		require.True(t, accountNames[want], "missing account verb %q", want)
	}

	pluginCmd := findSubcommand(t, cmd, "plugin")
	pluginNames := map[string]bool{}
	for _, c := range pluginCmd.Commands() {
		pluginNames[c.Name()] = true
	}
	for _, want := range []string{"ls", "show", "add", "update", "delete"} {
		require.True(t, pluginNames[want], "missing plugin verb %q", want)
	}

	require.NotNil(t, findSubcommand(t, findSubcommand(t, cmd, "challenge-schema"), "ls"))
	require.NotNil(t, findSubcommand(t, findSubcommand(t, cmd, "directories"), "ls"))
	require.NotNil(t, findSubcommand(t, findSubcommand(t, cmd, "tos"), "show"))
}
