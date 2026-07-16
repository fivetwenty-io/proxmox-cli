package lxc

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// --- lxc hookscript ------------------------------------------------------------

func TestHookscript_Get(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/200/config", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{"hookscript": "local:snippets/hook.pl", "cores": 2})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "hookscript", "get", "200")
	require.NoError(t, run())
	require.Equal(t, "/api2/json/nodes/pve1/lxc/200/config", gotPath)
	require.Contains(t, buf.String(), "local:snippets/hook.pl")
}

func TestHookscript_Get_NoneConfigured(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/200/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"cores": 2})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "hookscript", "get", "200")
	require.NoError(t, run())
	require.Contains(t, buf.String(), "no hookscript")
}

func TestHookscript_Set(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body url.Values
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/200/config", func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		body, err = url.ParseQuery(string(raw))
		require.NoError(t, err)
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "hookscript", "set", "200", "local:snippets/hook.pl")
	require.NoError(t, run())
	require.Equal(t, "local:snippets/hook.pl", body.Get("hookscript"))
	require.Contains(t, buf.String(), "WARNING: the hookscript executes on the HOST")
	require.Contains(t, buf.String(), "Hookscript")
}

func TestHookscript_Unset(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body url.Values
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/200/config", func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		body, err = url.ParseQuery(string(raw))
		require.NoError(t, err)
		testhelper.WriteData(w, nil)
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "hookscript", "unset", "200")
	require.NoError(t, run())
	require.Equal(t, "hookscript", body.Get("delete"))
	require.Contains(t, buf.String(), "removed")
}
