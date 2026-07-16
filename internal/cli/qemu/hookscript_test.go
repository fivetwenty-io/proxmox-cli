package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// --- qemu hookscript -----------------------------------------------------------

func TestHookscript_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{"hookscript": "local:snippets/hook.pl", "cores": 2})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "hookscript", "get", "100"))
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/config", gotPath)
	require.Contains(t, buf.String(), "local:snippets/hook.pl")
}

func TestHookscript_Get_NoneConfigured(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"cores": 2})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "hookscript", "get", "100"))
	require.Contains(t, buf.String(), "no hookscript")
}

func TestHookscript_Set(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "hookscript", "set", "100", "local:snippets/hook.pl"))
	require.Equal(t, "local:snippets/hook.pl", parseForm(t, body).Get("hookscript"))
	require.Contains(t, buf.String(), "WARNING: the hookscript executes on the HOST")
	require.Contains(t, buf.String(), "Hookscript")
}

func TestHookscript_Unset(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "hookscript", "unset", "100"))
	require.Equal(t, "hookscript", parseForm(t, body).Get("delete"))
	require.Contains(t, buf.String(), "removed")
}
