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

// TestClusterFirewallMacros_List verifies `pve cluster firewall macros list`
// queries GET /cluster/firewall/macros and renders a dynamic table.
func TestClusterFirewallMacros_List(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/cluster/firewall/macros", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{"macro": "SSH", "descr": "Secure Shell (SSH) traffic"},
			map[string]any{"macro": "HTTP", "descr": "HyperText Transfer Protocol (HTTP)"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "macros", "list"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/firewall/macros", gotPath)

	out := buf.String()
	require.Contains(t, out, "MACRO")
	require.Contains(t, out, "SSH")
	require.Contains(t, out, "HTTP")
}

// TestClusterFirewallMacros_ServerError verifies a server error surfaces correctly.
func TestClusterFirewallMacros_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/firewall/macros", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "firewall", "macros", "list"))
}

// TestClusterFirewallRefs_List verifies `pve cluster firewall refs list`
// queries GET /cluster/firewall/refs without a type param when unset.
func TestClusterFirewallRefs_List(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery string
	f.HandleFunc("GET /api2/json/cluster/firewall/refs", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []any{
			map[string]any{"name": "+pvecli-ips", "type": "ipset", "comment": "lab IP set"},
			map[string]any{"name": "pvecli-alias", "type": "alias"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "refs", "list"))

	require.NotContains(t, gotQuery, "type=", "omitted --type must not appear in query")

	out := buf.String()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "+pvecli-ips")
	require.Contains(t, out, "pvecli-alias")
}

// TestClusterFirewallRefs_TypeFilter verifies --type is forwarded when supplied.
func TestClusterFirewallRefs_TypeFilter(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery string
	f.HandleFunc("GET /api2/json/cluster/firewall/refs", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []any{
			map[string]any{"name": "+pvecli-ips", "type": "ipset"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "refs", "list", "--type", "ipset"))
	require.Contains(t, gotQuery, "type=ipset")
}

// TestClusterFirewallRefs_ServerError verifies a server error surfaces correctly.
func TestClusterFirewallRefs_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/firewall/refs", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "firewall", "refs", "list"))
}

// TestClusterFirewallCommandTree_GapCommands verifies macros and refs are registered.
func TestClusterFirewallCommandTree_GapCommands(t *testing.T) {
	root := newClusterCmd(&cli.Deps{})
	var fw *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "firewall" {
			fw = c
		}
	}
	require.NotNil(t, fw)

	names := make(map[string]bool)
	for _, c := range fw.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["macros"], "firewall must expose macros sub-command")
	require.True(t, names["refs"], "firewall must expose refs sub-command")
}
