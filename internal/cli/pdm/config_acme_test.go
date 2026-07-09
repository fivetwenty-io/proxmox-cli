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

const (
	acmeAccountPath         = "/api2/json/config/acme/account"
	acmePluginsPath         = "/api2/json/config/acme/plugins"
	acmeDirectoriesPath     = "/api2/json/config/acme/directories"
	acmeChallengeSchemaPath = "/api2/json/config/acme/challenge-schema"
	acmeTosPath             = "/api2/json/config/acme/tos"
)

// --- config acme account ls ---------------------------------------------------------------

func TestConfigAcmeAccountLs_SortsByName(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET "+acmeAccountPath, []map[string]any{
		{"name": "zeta"},
		{"name": "alpha"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["name"], "entries must sort by name")
	require.Equal(t, "zeta", got[1]["name"])
}

func TestConfigAcmeAccountLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+acmeAccountPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list acme accounts")
}

// --- config acme account show ---------------------------------------------------------------

func TestConfigAcmeAccountShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+acmeAccountPath+"/prod", map[string]any{
		"directory": "https://acme.example/directory",
		"location":  "https://acme.example/account/1",
		"tos":       "https://acme.example/tos",
		"account":   map[string]any{"status": "valid"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "show", "prod")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "https://acme.example/directory")
}

func TestConfigAcmeAccountShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+acmeAccountPath+"/prod", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such account")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "show", "prod")
	require.Error(t, err)
	require.Contains(t, err.Error(), `show acme account "prod"`)
}

// --- config acme account add ---------------------------------------------------------------

func TestConfigAcmeAccountAdd_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+acmeAccountPath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "add", "prod", "--contact", "admin@example.com")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "prod", rec.form.Get("name"))
	require.Equal(t, "admin@example.com", rec.form.Get("contact"))
	require.Contains(t, buf.String(), `ACME account "prod" registered.`)
}

func TestConfigAcmeAccountAdd_Async(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("POST "+acmeAccountPath, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "add", "prod", "--contact", "admin@example.com")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "registered")
}

func TestConfigAcmeAccountAdd_RequiresContact(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "add", "prod")
	require.Error(t, err)
}

func TestConfigAcmeAccountAdd_RejectsNonUPIDResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("POST "+acmeAccountPath, "not-a-upid")

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "add", "prod", "--contact", "admin@example.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a UPID")
}

func TestConfigAcmeAccountAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+acmeAccountPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid account")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "add", "prod", "--contact", "admin@example.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), `register acme account "prod"`)
}

// --- config acme account update ---------------------------------------------------------------

func TestConfigAcmeAccountUpdate_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "PUT "+acmeAccountPath+"/prod", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "update", "prod", "--contact", "new@example.com")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "new@example.com", rec.form.Get("contact"))
	require.Contains(t, buf.String(), `ACME account "prod" updated.`)
}

func TestConfigAcmeAccountUpdate_Async(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("PUT "+acmeAccountPath+"/prod", validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "update", "prod", "--contact", "new@example.com")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
}

func TestConfigAcmeAccountUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "update", "prod")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestConfigAcmeAccountUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+acmeAccountPath+"/prod", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "update", "prod", "--contact", "x@example.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), `update acme account "prod"`)
}

// --- config acme account delete ---------------------------------------------------------------

func TestConfigAcmeAccountDelete_WithoutConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	var called bool
	f.HandleFunc("DELETE "+acmeAccountPath+"/prod", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, validUPID)
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "delete", "prod")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.False(t, called, "no request must be issued without --yes")
}

func TestConfigAcmeAccountDelete_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "DELETE "+acmeAccountPath+"/prod", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "delete", "prod", "--yes", "--force")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, "1", rec.query.Get("force"))
	require.Contains(t, buf.String(), `ACME account "prod" deactivated.`)
}

func TestConfigAcmeAccountDelete_Async(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("DELETE "+acmeAccountPath+"/prod", validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "delete", "prod", "--yes")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
}

func TestConfigAcmeAccountDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+acmeAccountPath+"/prod", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeAccountCmd(), "account", "delete", "prod", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), `deactivate acme account "prod"`)
}

// --- config acme plugin ls ---------------------------------------------------------------

func TestConfigAcmePluginLs_SortsAndStripsSecret(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET "+acmePluginsPath, []map[string]any{
		{"plugin": "zeta", "type": "dns", "api": "cf", "data": "secret-z"},
		{"plugin": "alpha", "type": "dns", "api": "route53", "data": "secret-a", "disable": true, "validation-delay": 30},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmePluginCmd(), "plugin", "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["plugin"], "entries must sort by plugin id")
	require.Equal(t, "zeta", got[1]["plugin"])
	require.NotContains(t, got[0], "data", "plugin data must be stripped from ls output")
	require.NotContains(t, got[1], "data", "plugin data must be stripped from ls output")
	require.NotContains(t, buf.String(), "secret-a")
	require.NotContains(t, buf.String(), "secret-z")
}

func TestConfigAcmePluginLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+acmePluginsPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmePluginCmd(), "plugin", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list acme plugins")
}

// --- config acme plugin show ---------------------------------------------------------------

func TestConfigAcmePluginShow_StripsSecretFromSingleAndRaw(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+acmePluginsPath+"/cfplugin", map[string]any{
		"plugin": "cfplugin", "type": "dns", "api": "cf", "data": "super-secret",
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmePluginCmd(), "plugin", "show", "cfplugin")
	require.NoError(t, err)
	require.NotContains(t, buf.String(), "super-secret")
	require.NotContains(t, buf.String(), `"data"`)
}

func TestConfigAcmePluginShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+acmePluginsPath+"/cfplugin", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such plugin")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmePluginCmd(), "plugin", "show", "cfplugin")
	require.Error(t, err)
	require.Contains(t, err.Error(), `show acme plugin "cfplugin"`)
}

// --- config acme plugin add ---------------------------------------------------------------

func TestConfigAcmePluginAdd_CreatesPlugin(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+acmePluginsPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmePluginCmd(), "plugin", "add", "cfplugin",
		"--type", "dns", "--api", "cf", "--data", "ZGF0YQ==")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "cfplugin", rec.form.Get("id"))
	require.Equal(t, "dns", rec.form.Get("type"))
	require.Equal(t, "cf", rec.form.Get("api"))
	require.Equal(t, "ZGF0YQ==", rec.form.Get("data"))
	require.Contains(t, buf.String(), `ACME plugin "cfplugin" created.`)
}

func TestConfigAcmePluginAdd_RequiresTypeApiData(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	cases := [][]string{
		{"plugin", "add", "cfplugin"},
		{"plugin", "add", "cfplugin", "--type", "dns"},
		{"plugin", "add", "cfplugin", "--type", "dns", "--api", "cf"},
	}
	for _, args := range cases {
		var buf bytes.Buffer
		err := run(deps, &buf, newConfigAcmePluginCmd(), args...)
		require.Error(t, err, "args=%v", args)
	}
}

func TestConfigAcmePluginAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+acmePluginsPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid plugin")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmePluginCmd(), "plugin", "add", "cfplugin",
		"--type", "dns", "--api", "cf", "--data", "ZGF0YQ==")
	require.Error(t, err)
	require.Contains(t, err.Error(), `create acme plugin "cfplugin"`)
}

// --- config acme plugin update ---------------------------------------------------------------

func TestConfigAcmePluginUpdate_UpdatesPlugin(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+acmePluginsPath+"/cfplugin", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmePluginCmd(), "plugin", "update", "cfplugin",
		"--disable", "--validation-delay", "60", "--digest", "abc123", "--delete", "api")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "1", rec.form.Get("disable"))
	require.Equal(t, "60", rec.form.Get("validation-delay"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"api"}, rec.form["delete"])
	require.Contains(t, buf.String(), `ACME plugin "cfplugin" updated.`)
}

func TestConfigAcmePluginUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmePluginCmd(), "plugin", "update", "cfplugin")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestConfigAcmePluginUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmePluginCmd(), "plugin", "update", "cfplugin", "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestConfigAcmePluginUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+acmePluginsPath+"/cfplugin", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmePluginCmd(), "plugin", "update", "cfplugin", "--api", "route53")
	require.Error(t, err)
	require.Contains(t, err.Error(), `update acme plugin "cfplugin"`)
}

// --- config acme plugin delete ---------------------------------------------------------------

func TestConfigAcmePluginDelete_WithoutConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	var called bool
	f.HandleFunc("DELETE "+acmePluginsPath+"/cfplugin", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmePluginCmd(), "plugin", "delete", "cfplugin")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.False(t, called, "no request must be issued without --yes")
}

func TestConfigAcmePluginDelete_DeletesPlugin(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+acmePluginsPath+"/cfplugin", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmePluginCmd(), "plugin", "delete", "cfplugin", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Contains(t, buf.String(), `ACME plugin "cfplugin" deleted.`)
}

func TestConfigAcmePluginDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+acmePluginsPath+"/cfplugin", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmePluginCmd(), "plugin", "delete", "cfplugin", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), `delete acme plugin "cfplugin"`)
}

// --- config acme directories ls ---------------------------------------------------------------

func TestConfigAcmeDirectoriesLs_SortsByName(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET "+acmeDirectoriesPath, []map[string]any{
		{"name": "Zeta CA", "url": "https://zeta.example/directory"},
		{"name": "Alpha CA", "url": "https://alpha.example/directory"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeDirectoriesCmd(), "directories", "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "Alpha CA", got[0]["name"])
	require.Equal(t, "Zeta CA", got[1]["name"])
	require.Equal(t, "https://alpha.example/directory", got[0]["url"])
}

func TestConfigAcmeDirectoriesLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+acmeDirectoriesPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeDirectoriesCmd(), "directories", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list acme directories")
}

// --- config acme challenge-schema ls ---------------------------------------------------------------

func TestConfigAcmeChallengeSchemaLs_SortsById(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET "+acmeChallengeSchemaPath, []map[string]any{
		{"id": "zeta", "name": "Zeta", "type": "dns", "schema": map[string]any{"key": "z"}},
		{"id": "alpha", "name": "Alpha", "type": "dns", "schema": map[string]any{"key": "a"}},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeChallengeSchemaCmd(), "challenge-schema", "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["id"])
	require.Equal(t, "zeta", got[1]["id"])

	schema0, ok := got[0]["schema"].(map[string]any)
	require.True(t, ok, "schema must round-trip as a nested object")
	require.Equal(t, "a", schema0["key"], "Raw entries must stay paired with their sorted row")
}

func TestConfigAcmeChallengeSchemaLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+acmeChallengeSchemaPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeChallengeSchemaCmd(), "challenge-schema", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list acme challenge schemas")
}

// --- config acme tos show ---------------------------------------------------------------

func TestConfigAcmeTosShow_RendersURL(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+acmeTosPath, &rec, "https://acme.example/tos")

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeTosCmd(), "tos", "show", "--directory", "https://acme.example/directory")
	require.NoError(t, err)

	require.Equal(t, "https://acme.example/directory", rec.query.Get("directory"))
	require.Contains(t, buf.String(), "https://acme.example/tos")
}

func TestConfigAcmeTosShow_NoTosPublished(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+acmeTosPath, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeTosCmd(), "tos", "show")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "No Terms of Service URL is published")
}

func TestConfigAcmeTosShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+acmeTosPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "tos failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigAcmeTosCmd(), "tos", "show")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get acme terms of service url")
}

// --- config acme group wiring ---------------------------------------------------------------

func TestNewConfigAcmeCmd_RegistersAllSubcommands(t *testing.T) {
	cmd := newConfigAcmeCmd()
	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"account", "plugin", "directories", "challenge-schema", "tos"} {
		require.True(t, names[want], "expected `acme %s` to be registered", want)
	}
}
