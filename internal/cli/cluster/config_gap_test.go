package cluster

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestConfigApiversion_Success verifies `pve cluster config apiversion`
// queries GET /cluster/config/apiversion and renders the result.
func TestConfigApiversion_Success(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/cluster/config/apiversion", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, 10)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "config", "apiversion"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/config/apiversion", gotPath)
}

// TestConfigApiversion_ServerError verifies a server error surfaces correctly.
func TestConfigApiversion_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/config/apiversion", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.Error(t, run(&buf, "config", "apiversion"))
}

// TestConfigQdevice_Success verifies `pve cluster config qdevice` queries
// GET /cluster/config/qdevice and renders the result.
func TestConfigQdevice_Success(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/config/qdevice", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"state": "established",
			"model": "ffsplit",
			"votes": 1,
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "config", "qdevice"))

	require.Equal(t, "/api2/json/cluster/config/qdevice", gotPath)
	require.Contains(t, buf.String(), "established")
}

// TestConfigQdevice_ServerError verifies a server error (e.g., no qdevice) surfaces correctly.
func TestConfigQdevice_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/config/qdevice", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusServiceUnavailable, "no qdevice")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.Error(t, run(&buf, "config", "qdevice"))
}

// TestConfigTotem_Success verifies `pve cluster config totem` queries
// GET /cluster/config/totem and renders the result.
func TestConfigTotem_Success(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/config/totem", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"cluster_name": "prod",
			"transport":    "knet",
			"token":        5000,
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "config", "totem"))

	require.Equal(t, "/api2/json/cluster/config/totem", gotPath)
	require.Contains(t, buf.String(), "knet")
}

// TestConfigTotem_ServerError verifies a server error surfaces correctly.
func TestConfigTotem_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/config/totem", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.Error(t, run(&buf, "config", "totem"))
}

// TestConfigCommandTree_GapCommands verifies apiversion, qdevice, and totem are registered.
func TestConfigCommandTree_GapCommands(t *testing.T) {
	root := newClusterCmd(&cli.Deps{})
	var configCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "config" {
			configCmd = c
		}
	}
	require.NotNil(t, configCmd)

	names := make(map[string]bool)
	for _, c := range configCmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["apiversion"], "config must expose apiversion sub-command")
	require.True(t, names["qdevice"], "config must expose qdevice sub-command")
	require.True(t, names["totem"], "config must expose totem sub-command")
	// Existing commands must still be present.
	require.True(t, names["join"], "config must still expose join sub-command")
	require.True(t, names["nodes"], "config must still expose nodes sub-command")
}
