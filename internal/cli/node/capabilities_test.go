package node_test

import (
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/exec"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// ---- capabilities qemu cpu -------------------------------------------------

func TestNodeCapabilities_QemuCpu(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/capabilities/qemu/cpu", &rec, []any{
		map[string]any{"name": "host", "vendor": "AMD", "custom": 0},
		map[string]any{"name": "qemu64", "vendor": "QEMU", "custom": 0},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "capabilities", "qemu", "cpu"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/capabilities/qemu/cpu", rec.path)
	out := buf.String()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "host")
	require.Contains(t, out, "qemu64")
}

func TestNodeCapabilities_QemuCpuArchQuery(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/capabilities/qemu/cpu", &rec, []any{})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "capabilities", "qemu", "cpu", "--arch", "aarch64"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "arch=aarch64")
}

// ---- capabilities qemu machines --------------------------------------------

func TestNodeCapabilities_QemuMachines(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/capabilities/qemu/machines", &rec, []any{
		map[string]any{"id": "pc", "type": "i440fx", "version": "9.0"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "capabilities", "qemu", "machines"))

	require.NoError(t, root.Execute())
	require.Equal(t, "/api2/json/nodes/pve1/capabilities/qemu/machines", rec.path)
	require.Contains(t, buf.String(), "i440fx")
}

func TestNodeCapabilities_QemuMachinesArchQuery(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/capabilities/qemu/machines", &rec, []any{})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "capabilities", "qemu", "machines", "--arch", "aarch64"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "arch=aarch64")
}

func TestNodeCapabilities_QemuMachinesAPIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/capabilities/qemu/machines", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "capabilities", "qemu", "machines"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "list QEMU machine capabilities")
}

// ---- capabilities qemu migration -------------------------------------------

func TestNodeCapabilities_QemuMigration(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/capabilities/qemu/migration", &rec, map[string]any{
		"has-dbus-vmstate": 1,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "capabilities", "qemu", "migration"))

	require.NoError(t, root.Execute())
	require.Equal(t, "/api2/json/nodes/pve1/capabilities/qemu/migration", rec.path)
	require.Contains(t, buf.String(), "has-dbus-vmstate")
}

func TestNodeCapabilities_QemuMigrationAPIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/capabilities/qemu/migration", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "capabilities", "qemu", "migration"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get QEMU migration capabilities")
}

func TestNodeCapabilities_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "capabilities", "qemu", "cpu"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeCapabilities_CommandTree(t *testing.T) {
	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	addNodeGroup(root)

	var node, caps, qemu *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "node" {
			node = c
		}
	}
	require.NotNil(t, node)
	for _, c := range node.Commands() {
		if c.Name() == "capabilities" {
			caps = c
		}
	}
	require.NotNil(t, caps, "node must expose a capabilities sub-command")
	for _, c := range caps.Commands() {
		if c.Name() == "qemu" {
			qemu = c
		}
	}
	require.NotNil(t, qemu, "capabilities must expose a qemu sub-command")

	names := map[string]bool{}
	for _, c := range qemu.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"cpu", "machines", "migration"} {
		require.True(t, names[want], "expected capabilities qemu sub-command %q", want)
	}
}
