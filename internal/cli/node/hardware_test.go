package node_test

import (
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/exec"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestNodeHardware_PciList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/hardware/pci", &rec, []any{
		map[string]any{"id": "0000:00:02.0", "vendor_name": "Intel", "device_name": "VGA"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "hardware", "pci", "--verbose"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/hardware/pci", rec.path)
	require.Contains(t, rec.query, "verbose=1")
	out := buf.String()
	require.Contains(t, out, "0000:00:02.0")
	require.Contains(t, out, "Intel")
}

// TestNodeHardware_PciGet verifies a positional argument filters the PCI list
// client-side: GET /nodes/{node}/hardware/pci/{id} is only a directory index
// (mdev), so the device row comes from the list endpoint.
func TestNodeHardware_PciGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/hardware/pci", &rec, []any{
		map[string]any{"id": "0000:00:02.0", "iommugroup": 2, "device_name": "VGA"},
		map[string]any{"id": "0000:01:00.0", "iommugroup": 3, "device_name": "NIC"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	// A positional argument switches from list to a single-device lookup.
	root.SetArgs(append(prefix, "--node", "pve1", "node", "hardware", "pci", "0000:00:02.0"))

	require.NoError(t, root.Execute())
	require.Equal(t, "/api2/json/nodes/pve1/hardware/pci", rec.path)
	out := buf.String()
	require.Contains(t, out, "IOMMUGROUP")
	require.Contains(t, out, "VGA")
	require.NotContains(t, out, "NIC", "other devices must be filtered out")
}

// TestNodeHardware_PciGetDomainless verifies the domain-less ID form matches.
func TestNodeHardware_PciGetDomainless(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/hardware/pci", &rec, []any{
		map[string]any{"id": "0000:00:02.0", "device_name": "VGA"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "hardware", "pci", "00:02.0"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "VGA")
}

// TestNodeHardware_PciGetNotFound verifies an unknown ID errors instead of
// silently rendering an empty table.
func TestNodeHardware_PciGetNotFound(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/hardware/pci", &rec, []any{
		map[string]any{"id": "0000:00:02.0", "device_name": "VGA"},
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "hardware", "pci", "0000:99:00.0"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestNodeHardware_Usb(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/hardware/usb", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"busnum": 1, "devnum": 2, "product": "Keyboard"},
		})
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "hardware", "usb"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "Keyboard")
}

func TestNodeHardware_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "hardware", "pci"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeHardware_CommandTree(t *testing.T) {
	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	addNodeGroup(root)

	find := func(parent *cobra.Command, name string) *cobra.Command {
		for _, c := range parent.Commands() {
			if c.Name() == name {
				return c
			}
		}
		return nil
	}

	nodeCmd := find(root, "node")
	require.NotNil(t, nodeCmd)
	hw := find(nodeCmd, "hardware")
	require.NotNil(t, hw, "node hardware command must be registered")

	for _, verb := range []string{"pci", "usb"} {
		require.NotNil(t, find(hw, verb), "hardware must expose %q", verb)
	}
}
