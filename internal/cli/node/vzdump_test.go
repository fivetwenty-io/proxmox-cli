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

// TestNodeVzdump_BlocksUntilTaskDone verifies `pve node vzdump` POSTs the backup
// request with the selected guest and storage, then blocks until the task ends.
func TestNodeVzdump_BlocksUntilTaskDone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:vzdump:100:root@pam:"
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/vzdump", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		_ = r.ParseForm()
		rec.query = r.Form.Encode()
		testhelper.WriteData(w, upid)
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "vzdump",
		"--vmid", "100", "--storage", "local", "--mode", "snapshot"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/vzdump", rec.path)
	require.Contains(t, rec.query, "vmid=100")
	require.Contains(t, rec.query, "storage=local")
	require.Contains(t, rec.query, "mode=snapshot")
	require.Contains(t, buf.String(), "completed")
}

// TestNodeVzdump_AsyncReturnsUPID verifies --async prints the task UPID without
// waiting for completion.
func TestNodeVzdump_AsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:vzdump:100:root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/vzdump", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, upid)
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--async", "--node", "pve1", "node", "vzdump", "--vmid", "100", "--storage", "local"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), upid)
}

// TestNodeVzdump_RequiresNode verifies the command fails clearly when no node is
// resolvable.
func TestNodeVzdump_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "vzdump", "--vmid", "100", "--storage", "local"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

// TestNodeVzdump_APIError verifies a server failure on the vzdump POST surfaces.
func TestNodeVzdump_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/vzdump", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "no storage")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "vzdump", "--vmid", "100", "--storage", "bad"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "start vzdump on node")
}

// TestNodeVzdump_BadUPID verifies that a POST returning no usable UPID surfaces
// a clear decode error instead of silently proceeding to wait on an empty task.
func TestNodeVzdump_BadUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/vzdump", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, "")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "vzdump", "--vmid", "100", "--storage", "local"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode UPID")
}

// TestNodeVzdump_NoLocalTargetFlag guards against shadowing the root's persistent
// -t/--target selector with a local --target anywhere in the node command tree.
func TestNodeVzdump_NoLocalTargetFlag(t *testing.T) {
	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	cli.AddGroups(root, &cli.Deps{})
	var nodeCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "node" {
			nodeCmd = c
		}
	}
	require.NotNil(t, nodeCmd, "node group must be registered")

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		require.Nil(t, c.Flags().Lookup("target"),
			"command %q must not define a local --target (collides with root -t/--target)", c.CommandPath())
		require.Nil(t, c.Flags().Lookup("node"),
			"command %q must not define a local --node (collides with root --node)", c.CommandPath())
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(nodeCmd)
}
