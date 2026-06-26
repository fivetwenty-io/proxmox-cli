package cluster

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// ---------------------------------------------------------------------------
// firewall get-single commands
// ---------------------------------------------------------------------------

// TestClusterFirewallAliasGet_Success verifies `pve cluster firewall alias get <name>`
// queries GET /cluster/firewall/aliases/{name} and renders a single-object result.
func TestClusterFirewallAliasGet_Success(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/cluster/firewall/aliases/web-servers", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"name":    "web-servers",
			"cidr":    "192.168.1.0/24",
			"comment": "web tier",
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "alias", "get", "web-servers"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/firewall/aliases/web-servers", gotPath)
	require.Contains(t, buf.String(), "192.168.1.0/24")
}

// TestClusterFirewallAliasGet_ServerError verifies a server error surfaces correctly.
func TestClusterFirewallAliasGet_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/firewall/aliases/missing", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "alias not found")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "firewall", "alias", "get", "missing"))
}

// TestClusterFirewallGroupGet_Success verifies `pve cluster firewall group get <group> <pos>`
// queries GET /cluster/firewall/groups/{group}/{pos} and renders a single-object result.
func TestClusterFirewallGroupGet_Success(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/cluster/firewall/groups/mygroup/0", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"pos":    0,
			"type":   "in",
			"action": "ACCEPT",
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "group", "get", "mygroup", "0"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/firewall/groups/mygroup/0", gotPath)
	require.Contains(t, buf.String(), "ACCEPT")
}

// TestClusterFirewallGroupGet_ServerError verifies a server error surfaces correctly.
func TestClusterFirewallGroupGet_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/firewall/groups/nogroup/99", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "rule not found")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "firewall", "group", "get", "nogroup", "99"))
}

// TestClusterFirewallIpsetGet_Success verifies `pve cluster firewall ipset get <name> <cidr>`
// queries GET /cluster/firewall/ipset/{name}/{cidr} and renders a single-object result.
func TestClusterFirewallIpsetGet_Success(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/cluster/firewall/ipset/myset/10.0.0.1", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		raw, _ := json.Marshal(map[string]any{
			"cidr":    "10.0.0.1",
			"nomatch": false,
			"comment": "primary",
		})
		testhelper.WriteData(w, json.RawMessage(raw))
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "firewall", "ipset", "get", "myset", "10.0.0.1"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/firewall/ipset/myset/10.0.0.1", gotPath)
	require.Contains(t, buf.String(), "10.0.0.1")
}

// TestClusterFirewallIpsetGet_ServerError verifies a server error surfaces correctly.
func TestClusterFirewallIpsetGet_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/firewall/ipset/noset/1.2.3.4", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "cidr not found")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "firewall", "ipset", "get", "noset", "1.2.3.4"))
}

// TestClusterFirewallGetSubcommands verifies alias/group/ipset each expose a get sub-command.
func TestClusterFirewallGetSubcommands(t *testing.T) {
	root := Group(&cli.Deps{})
	var fw *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "firewall" {
			fw = c
		}
	}
	require.NotNil(t, fw, "firewall sub-command must be registered")

	// Build a map of firewall sub-commands by name.
	fwSubs := make(map[string]*cobra.Command)
	for _, c := range fw.Commands() {
		fwSubs[c.Name()] = c
	}

	for _, parent := range []string{"alias", "group", "ipset"} {
		sub, ok := fwSubs[parent]
		require.True(t, ok, "firewall must expose %s sub-command", parent)
		names := make(map[string]bool)
		for _, c := range sub.Commands() {
			names[c.Name()] = true
		}
		require.True(t, names["get"], "firewall %s must expose get sub-command", parent)
	}
}

// ---------------------------------------------------------------------------
// cluster bulk-action guest
// ---------------------------------------------------------------------------

// TestClusterBulkGuest_Success verifies `pve cluster bulk guest` queries
// GET /cluster/bulk-action/guest and renders a dynamic table.
func TestClusterBulkGuest_Success(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/cluster/bulk-action/guest", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{"vmid": 100, "name": "web01", "type": "qemu"},
			map[string]any{"vmid": 101, "name": "db01", "type": "lxc"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "bulk", "guest"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/bulk-action/guest", gotPath)

	out := buf.String()
	require.Contains(t, out, "web01")
	require.Contains(t, out, "db01")
}

// TestClusterBulkGuestAlias_Success verifies the command is also reachable as
// `pve cluster bulk-action guest` via the bulk-action alias.
func TestClusterBulkGuestAlias_Success(t *testing.T) {
	f, ac := newFakeClient(t)

	f.HandleFunc("GET /api2/json/cluster/bulk-action/guest", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"vmid": 100, "name": "web01"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "bulk-action", "guest"))
	require.Contains(t, buf.String(), "web01")
}

// TestClusterBulkGuest_ServerError verifies a server error surfaces correctly.
func TestClusterBulkGuest_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/bulk-action/guest", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "server error")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "bulk", "guest"))
}

// TestClusterBulkCommandTree_GuestPreview verifies the guest sub-command is registered.
func TestClusterBulkCommandTree_GuestPreview(t *testing.T) {
	root := Group(&cli.Deps{})
	var bulkCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "bulk" {
			bulkCmd = c
		}
	}
	require.NotNil(t, bulkCmd, "bulk sub-command must be registered")

	names := make(map[string]bool)
	for _, c := range bulkCmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["guest"], "bulk must expose guest sub-command")
}

// ---------------------------------------------------------------------------
// cluster qemu cpu-flags
// ---------------------------------------------------------------------------

// TestClusterQemuCpuFlags_List verifies `pve cluster qemu cpu-flags` queries
// GET /cluster/qemu/cpu-flags without params when none are set.
func TestClusterQemuCpuFlags_List(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath, gotQuery string
	f.HandleFunc("GET /api2/json/cluster/qemu/cpu-flags", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		testhelper.WriteData(w, []any{
			map[string]any{"flag": "pcid", "description": "PCID cpu flag"},
			map[string]any{"flag": "spec-ctrl", "description": "Spectre mitigation"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "qemu", "cpu-flags"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/qemu/cpu-flags", gotPath)
	require.NotContains(t, gotQuery, "accel=", "omitted --accel must not appear in query")
	require.NotContains(t, gotQuery, "arch=", "omitted --arch must not appear in query")

	out := buf.String()
	require.Contains(t, out, "pcid")
	require.Contains(t, out, "spec-ctrl")
}

// TestClusterQemuCpuFlags_WithAccel verifies --accel is forwarded when supplied.
func TestClusterQemuCpuFlags_WithAccel(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery string
	f.HandleFunc("GET /api2/json/cluster/qemu/cpu-flags", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []any{})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "qemu", "cpu-flags", "--accel", "kvm"))
	require.Contains(t, gotQuery, "accel=kvm")
}

// TestClusterQemuCpuFlags_WithArch verifies --arch is forwarded when supplied.
func TestClusterQemuCpuFlags_WithArch(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery string
	f.HandleFunc("GET /api2/json/cluster/qemu/cpu-flags", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []any{})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "qemu", "cpu-flags", "--arch", "x86_64"))
	require.Contains(t, gotQuery, "arch=x86_64")
}

// TestClusterQemuCpuFlags_ServerError verifies a server error surfaces correctly.
func TestClusterQemuCpuFlags_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/qemu/cpu-flags", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "qemu", "cpu-flags"))
}

// TestClusterQemuCommandTree_CpuFlags verifies qemu is registered and exposes cpu-flags.
func TestClusterQemuCommandTree_CpuFlags(t *testing.T) {
	root := Group(&cli.Deps{})
	var qemuCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "qemu" {
			qemuCmd = c
		}
	}
	require.NotNil(t, qemuCmd, "qemu sub-command must be registered under cluster")

	names := make(map[string]bool)
	for _, c := range qemuCmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["cpu-flags"], "qemu must expose cpu-flags sub-command")
}
