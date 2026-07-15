package node_test

import (
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// ---- list ------------------------------------------------------------------

func TestNodeReplication_List(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/replication", &rec, []any{
		map[string]any{"id": "100-0", "guest": 100, "target": "pve2", "schedule": "*/15"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "replication", "list"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/replication", rec.path)
	out := buf.String()
	require.Contains(t, out, "100-0")
	require.Contains(t, out, "pve2")
	require.Contains(t, out, "TARGET")
}

func TestNodeReplication_ListWithGuest(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/replication", &rec, []any{})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "replication", "list", "--guest", "100"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "guest=100")
}

func TestNodeReplication_ListOmitsUnsetGuest(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/replication", &rec, []any{})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "replication", "list"))

	require.NoError(t, root.Execute())
	require.NotContains(t, rec.query, "guest")
}

// ---- status ----------------------------------------------------------------

func TestNodeReplication_Status(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/replication/100-0/status", &rec, map[string]any{
		"id": "100-0", "guest": 100, "fail_count": 0, "last_sync": 1700000000, "duration": 1.23,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "replication", "status", "100-0"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/replication/100-0/status", rec.path)
	require.Contains(t, buf.String(), "100-0")
}

// ---- log -------------------------------------------------------------------

func TestNodeReplication_Log(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/replication/100-0/log", &rec, []any{
		map[string]any{"n": 0, "t": "start replication job"},
		map[string]any{"n": 1, "t": "end replication job"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "replication", "log", "100-0"))

	require.NoError(t, root.Execute())
	require.Equal(t, "/api2/json/nodes/pve1/replication/100-0/log", rec.path)
	out := buf.String()
	require.Contains(t, out, "start replication job")
	require.Contains(t, out, "T")
}

func TestNodeReplication_LogWithLimitStart(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/replication/100-0/log", &rec, []any{})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "replication", "log", "100-0",
		"--limit", "5", "--start", "2"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "limit=5")
	require.Contains(t, rec.query, "start=2")
}

// ---- run -------------------------------------------------------------------

func TestNodeReplication_RunRequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/replication/100-0/schedule_now", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "replication", "run", "100-0"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "no API call must be made without confirmation")
}

func TestNodeReplication_RunBlocksUntilDone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:pvesr:100-0:root@pam:"
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/replication/100-0/schedule_now", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		testhelper.WriteData(w, upid)
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "replication", "run", "100-0", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/replication/100-0/schedule_now", rec.path)
	require.Contains(t, buf.String(), "scheduled to run now")
}

func TestNodeReplication_RunAsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:pvesr:100-0:root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/replication/100-0/schedule_now", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, upid)
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--async", "--node", "pve1", "node", "replication", "run", "100-0", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), upid)
}

func TestNodeReplication_RunNonUPIDFallback(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	// nil data exercises the non-UPID fallback to a plain success message.
	recordOn(f, "POST /api2/json/nodes/pve1/replication/100-0/schedule_now", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "replication", "run", "100-0", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, buf.String(), "scheduled to run now")
}

// ---- node scoping + command tree -------------------------------------------

func TestNodeReplication_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "replication", "list"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeReplication_CommandTree(t *testing.T) {
	root, cleanup := cli.NewRootCmd("pmx")
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
	repl := find(nodeCmd, "replication")
	require.NotNil(t, repl, "node replication command must be registered")

	for _, verb := range []string{"list", "status", "log", "run"} {
		require.NotNil(t, find(repl, verb), "replication must expose %q", verb)
	}
}
