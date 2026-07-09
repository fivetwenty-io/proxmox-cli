package pdm

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

const configCertificatePath = "/api2/json/config/certificate"

// --- config certificate show ---------------------------------------------------------------

func TestConfigCertificateShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+configCertificatePath, map[string]any{
		"acme": "prod", "acmedomain0": "domain=example.com",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigCertificateCmd(), "certificate", "show")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "prod")
	require.Contains(t, out, "domain=example.com")
}

func TestConfigCertificateShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+configCertificatePath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "get failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigCertificateCmd(), "certificate", "show")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get certificate configuration")
}

// --- config certificate update ---------------------------------------------------------------

func TestConfigCertificateUpdate_UpdatesConfig(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+configCertificatePath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigCertificateCmd(), "certificate", "update",
		"--acme", "prod", "--acmedomain0", "domain=example.com", "--digest", "abc123", "--delete", "acmedomain1")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, "prod", rec.form.Get("acme"))
	require.Equal(t, "domain=example.com", rec.form.Get("acmedomain0"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"acmedomain1"}, rec.form["delete"])
	require.Contains(t, buf.String(), "Certificate configuration updated.")
}

func TestConfigCertificateUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+configCertificatePath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigCertificateCmd(), "certificate", "update", "--acme", "prod")
	require.NoError(t, err)
	require.Equal(t, "prod", rec.form.Get("acme"))

	for _, key := range []string{"acmedomain0", "acmedomain1", "acmedomain2", "acmedomain3", "acmedomain4", "delete", "digest"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestConfigCertificateUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigCertificateCmd(), "certificate", "update")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestConfigCertificateUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigCertificateCmd(), "certificate", "update", "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestConfigCertificateUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+configCertificatePath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newConfigCertificateCmd(), "certificate", "update", "--acme", "prod")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update certificate configuration")
}

// --- config certificate group wiring ---------------------------------------------------------------

func TestNewConfigCertificateCmd_RegistersAllSubcommands(t *testing.T) {
	cmd := newConfigCertificateCmd()
	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"show", "update"} {
		require.True(t, names[want], "expected `certificate %s` to be registered", want)
	}
}
