package node_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestNodeMaintenance_EnableWithYes verifies enable sends the enable value to
// PUT /nodes/{node}/maintenance once --yes confirms it, and reports it in every
// output format.
func TestNodeMaintenance_EnableWithYes(t *testing.T) {
	for _, format := range []output.Format{output.FormatTable, output.FormatJSON, output.FormatYAML} {
		t.Run(string(format), func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec recordedRequest
			recordForm(f, "PUT /api2/json/nodes/pve1/maintenance", &rec, nil)

			root, buf, prefix := newNodeRoot(t, f, format, exec.Fake())
			root.SetArgs(append(prefix, "--node", "pve1", "node", "maintenance", "enable", "--yes"))

			require.NoError(t, root.Execute())
			require.Equal(t, http.MethodPut, rec.method)
			require.Equal(t, "/api2/json/nodes/pve1/maintenance", rec.path)
			require.Equal(t, "maintenance-mode=enable", rec.query)
			require.Contains(t, buf.String(), "enabled")
		})
	}
}

// TestNodeMaintenance_EnableRefusesWithoutYes verifies a script that omits
// --yes is refused before any request goes out.
func TestNodeMaintenance_EnableRefusesWithoutYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("PUT /api2/json/nodes/pve1/maintenance", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "maintenance", "enable"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "no request may be sent without confirmation")
}

// TestNodeMaintenance_Disable verifies disable needs no confirmation and sends
// the disable value.
func TestNodeMaintenance_Disable(t *testing.T) {
	for _, format := range []output.Format{output.FormatTable, output.FormatJSON, output.FormatYAML} {
		t.Run(string(format), func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec recordedRequest
			recordForm(f, "PUT /api2/json/nodes/pve1/maintenance", &rec, nil)

			root, buf, prefix := newNodeRoot(t, f, format, exec.Fake())
			root.SetArgs(append(prefix, "--node", "pve1", "node", "maintenance", "disable"))

			require.NoError(t, root.Execute())
			require.Equal(t, http.MethodPut, rec.method)
			require.Equal(t, "maintenance-mode=disable", rec.query)
			require.Contains(t, buf.String(), "disabled")
		})
	}
}

// TestNodeMaintenance_RequiresNode verifies both verbs fail clearly with no
// node, before any request.
func TestNodeMaintenance_RequiresNode(t *testing.T) {
	for _, verb := range [][]string{{"enable", "--yes"}, {"disable"}} {
		f := testhelper.NewFakePVE(t)
		root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
		root.SetArgs(append(prefix, append([]string{"node", "maintenance"}, verb...)...))

		err := root.Execute()
		require.Error(t, err, verb[0])
		require.Contains(t, err.Error(), "no node specified")
	}
}

// TestNodeMaintenance_WrapsServerError verifies a server refusal names the
// node and the mode.
func TestNodeMaintenance_WrapsServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/maintenance", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "Permission check failed")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "maintenance", "enable", "--yes"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), `enable maintenance mode on node "pve1"`)
}
