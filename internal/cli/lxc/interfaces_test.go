package lxc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestLxcInterfaces_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/interfaces", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{
				"name": "eth0", "hwaddr": "AA:BB:CC:DD:EE:FF",
				"inet": "172.30.0.10/24", "inet6": "fe80::1/64",
			},
			map[string]any{"name": "lo", "inet": "127.0.0.1/8"},
		})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "interfaces", "101")
	require.NoError(t, run())

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/interfaces", gotPath)

	out := buf.String()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "HWADDR")
	require.Contains(t, out, "INET")
	require.Contains(t, out, "eth0")
	require.Contains(t, out, "AA:BB:CC:DD:EE:FF")
	require.Contains(t, out, "172.30.0.10/24")
	require.Contains(t, out, "lo")
}

// TestLxcInterfaces_HardwareAddressFallback verifies the table falls back to the
// "hardware-address" key when the legacy "hwaddr" field is absent.
func TestLxcInterfaces_HardwareAddressFallback(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/interfaces", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"name": "eth0", "hardware-address": "12:34:56:78:9A:BC"},
		})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "interfaces", "101")
	require.NoError(t, run())
	require.Contains(t, buf.String(), "12:34:56:78:9A:BC")
}

// TestLxcInterfaces_JSONLossless verifies the raw response is emitted unchanged
// in JSON mode, preserving fields the table does not surface.
func TestLxcInterfaces_JSONLossless(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/interfaces", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"name": "eth0", "hwaddr": "AA:BB", "inet": "172.30.0.10/24", "extra": "kept"},
		})
	})

	deps := newDeps(t, f, output.FormatJSON, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "interfaces", "101")
	require.NoError(t, run())

	var parsed []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed),
		"interfaces JSON must be valid; got: %s", buf.String())
	require.Len(t, parsed, 1)
	require.Equal(t, "kept", parsed[0]["extra"])
}

func TestLxcInterfaces_Empty(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/interfaces", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "interfaces", "101")
	require.NoError(t, run())
	require.Contains(t, buf.String(), "NAME")
}

// TestLxcInterfaces_RejectsNonNumericVMID verifies the positional vmid is
// validated before any API call, matching the other lxc subcommands.
func TestLxcInterfaces_RejectsNonNumericVMID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "interfaces", "not-a-number")
	require.Error(t, run())
}

func TestLxcInterfaces_NoNode_Errors(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "interfaces", "101")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "node")
}

func TestLxcInterfaces_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/interfaces", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "interfaces", "101")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "list interfaces for container")
}

func TestLxcInterfaces_RegisteredAndArgs(t *testing.T) {
	cmd := newGroupCmd(nil)
	var ifaces *cobra.Command
	for _, c := range cmd.Commands() {
		if c.Name() == "interfaces" {
			ifaces = c
		}
	}
	require.NotNil(t, ifaces, "interfaces sub-command must be registered")
	require.Error(t, ifaces.Args(ifaces, []string{}), "interfaces requires a vmid")
	require.Error(t, ifaces.Args(ifaces, []string{"1", "2"}), "interfaces takes exactly one vmid")
	require.NoError(t, ifaces.Args(ifaces, []string{"101"}))
}
