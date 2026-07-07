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

// ---- local probes ----------------------------------------------------------

func TestNodeScan_Lvm(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/scan/lvm", &rec, []any{
		map[string]any{"vg": "pve", "size": 100, "free": 40},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "scan", "lvm"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/scan/lvm", rec.path)
	out := buf.String()
	// Union-key table headers are upper-cased from the JSON keys.
	require.Contains(t, out, "VG")
	require.Contains(t, out, "FREE")
	require.Contains(t, out, "pve")
}

func TestNodeScan_Zfs_JSONLossless(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/scan/zfs", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{map[string]any{"pool": "tank", "size": 999}})
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatJSON, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "scan", "zfs"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "tank")
}

func TestNodeScan_RequiredFlags(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "lvmthin missing vg",
			args:    []string{"--node", "pve1", "node", "scan", "lvmthin"},
			wantErr: "vg",
		},
		{
			name:    "nfs missing server",
			args:    []string{"--node", "pve1", "node", "scan", "nfs"},
			wantErr: "server",
		},
		{
			name:    "iscsi missing portal",
			args:    []string{"--node", "pve1", "node", "scan", "iscsi"},
			wantErr: "portal",
		},
		{
			name:    "pbs missing username",
			args:    []string{"--node", "pve1", "node", "scan", "pbs", "--server", "pbs.local"},
			wantErr: "username",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
			root.SetArgs(append(prefix, tc.args...))
			err := root.Execute()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// ---- remote probes ---------------------------------------------------------

func TestNodeScan_Nfs(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/scan/nfs", &rec, []any{
		map[string]any{"path": "/export/data", "options": "rw"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "scan", "nfs", "--server", "10.0.0.5"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, rec.query, "server=10.0.0.5")
	require.Contains(t, buf.String(), "/export/data")
}

// ---- node scoping + command tree -------------------------------------------

func TestNodeScan_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "scan", "lvm"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeScan_CommandTree(t *testing.T) {
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
	scan := find(nodeCmd, "scan")
	require.NotNil(t, scan, "node scan command must be registered")

	for _, verb := range []string{"lvm", "lvmthin", "zfs", "nfs", "cifs", "iscsi", "pbs"} {
		require.NotNil(t, find(scan, verb), "scan must expose %q", verb)
	}
}
