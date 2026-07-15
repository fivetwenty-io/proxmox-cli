package lxc

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestFeaturesShow_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"features": "nesting=1,mount=nfs;cifs", "digest": "x"})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "features", "show", "101")
	require.NoError(t, run())

	out := buf.String()
	require.Contains(t, out, "FEATURE")
	require.Contains(t, out, "nesting")
	require.Contains(t, out, "true")
	require.Contains(t, out, "keyctl")
	require.Contains(t, out, "false")
	require.Contains(t, out, "nfs;cifs")
}

// TestFeaturesSet_MergeOnlyChangedFlags proves that setting one flag preserves
// the others and that default-valued keys are omitted from the wire string.
func TestFeaturesSet_MergeOnlyChangedFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"features": "nesting=1", "unprivileged": 1, "digest": "x"})
	})
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "features", "set", "101", "--keyctl")
	require.NoError(t, run())

	// nesting preserved from the read, keyctl overlaid, everything else omitted.
	require.Equal(t, "nesting=1,keyctl=1", body["features"])
	require.Contains(t, buf.String(), "features updated")
}

func TestFeaturesSet_DisableRemovesFromString(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"features": "nesting=1,fuse=1", "unprivileged": 1, "digest": "x"})
	})
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "features", "set", "101", "--fuse=false")
	require.NoError(t, run())

	require.Equal(t, "nesting=1", body["features"])
}

func TestFeaturesSet_DisablingLastFeatureDeletesKey(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"features": "nesting=1", "unprivileged": 1, "digest": "x"})
	})
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "features", "set", "101", "--nesting=false")
	require.NoError(t, run())

	// Everything is back at defaults: the key is deleted, not set to "".
	require.Equal(t, "features", body["delete"])
	require.NotContains(t, body, "features")
	require.Contains(t, buf.String(), "reset to defaults")
}

func TestFeaturesSet_Reset(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"features": "nesting=1", "unprivileged": 1, "digest": "x"})
	})
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "features", "set", "101", "--reset")
	require.NoError(t, run())

	require.Equal(t, "features", body["delete"])
	require.NotContains(t, body, "features")
	require.Contains(t, buf.String(), "reset to defaults")
}

func TestFeaturesSet_DigestPassthrough(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"features": "", "unprivileged": 1, "digest": "x"})
	})
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "features", "set", "101", "--nesting", "--digest", "deadbeef")
	require.NoError(t, run())

	require.Equal(t, "deadbeef", body["digest"])
	require.Equal(t, "nesting=1", body["features"])
}

func TestFeaturesSet_NoFlagsErrors(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"features": "nesting=1", "unprivileged": 1, "digest": "x"})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "features", "set", "101")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "no feature flags")
}

func TestFeaturesSet_PrivilegedWarning(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"features": "", "unprivileged": 0, "digest": "x"})
	})
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "security", "features", "set", "101", "--nesting")
	require.NoError(t, run())

	require.Contains(t, buf.String(), "WARNING")
	require.Contains(t, buf.String(), "privileged container")
}
