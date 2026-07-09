package pdm

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

const configWebauthnPath = "/api2/json/config/access/tfa/webauthn"

// --- config webauthn show ---------------------------------------------------------------

func TestConfigWebauthnShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+configWebauthnPath, map[string]any{
		"id": "pdm.example.com", "rp": "PDM", "origin": "https://pdm.example.com", "allow-subdomains": true,
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigWebauthnCmd(), "webauthn", "show")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "pdm.example.com")
	require.Contains(t, out, "PDM")
}

func TestConfigWebauthnShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+configWebauthnPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "get failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigWebauthnCmd(), "webauthn", "show")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get webauthn configuration")
}

// --- config webauthn update ---------------------------------------------------------------

func TestConfigWebauthnUpdate_UpdatesConfig(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+configWebauthnPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigWebauthnCmd(), "webauthn", "update",
		"--id", "pdm.example.com", "--rp", "PDM", "--origin", "https://pdm.example.com",
		"--allow-subdomains", "--digest", "abc123", "--delete", "origin")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "pdm.example.com", rec.form.Get("id"))
	require.Equal(t, "PDM", rec.form.Get("rp"))
	require.Equal(t, "https://pdm.example.com", rec.form.Get("origin"))
	require.Equal(t, "1", rec.form.Get("allow-subdomains"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"origin"}, rec.form["delete"])
	require.Contains(t, buf.String(), "Webauthn configuration updated.")
}

func TestConfigWebauthnUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+configWebauthnPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigWebauthnCmd(), "webauthn", "update", "--rp", "PDM")
	require.NoError(t, err)
	require.Equal(t, "PDM", rec.form.Get("rp"))

	for _, key := range []string{"id", "origin", "allow-subdomains", "delete", "digest"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestConfigWebauthnUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigWebauthnCmd(), "webauthn", "update")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes given")
}

func TestConfigWebauthnUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigWebauthnCmd(), "webauthn", "update", "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestConfigWebauthnUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+configWebauthnPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigWebauthnCmd(), "webauthn", "update", "--rp", "PDM")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update webauthn configuration")
}

// --- config webauthn group wiring ---------------------------------------------------------------

func TestNewConfigWebauthnCmd_RegistersAllSubcommands(t *testing.T) {
	cmd := newConfigWebauthnCmd()
	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"show", "update"} {
		require.True(t, names[want], "expected `webauthn %s` to be registered", want)
	}
}
