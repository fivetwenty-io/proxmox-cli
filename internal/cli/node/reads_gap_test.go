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

// ---------------------------------------------------------------------------
// node config set --location
// ---------------------------------------------------------------------------

func TestNodeConfigSet_Location(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "PUT /api2/json/nodes/pve1/config", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "config", "set",
		"--location", "eu-west-1"))

	require.NoError(t, root.Execute())
	require.Equal(t, "PUT", rec.method)
	require.Contains(t, rec.query, "location=eu-west-1")
}

func TestNodeConfigSet_LocationOmittedWhenNotSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "PUT /api2/json/nodes/pve1/config", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "config", "set",
		"--description", "no location"))

	require.NoError(t, root.Execute())
	require.NotContains(t, rec.query, "location=")
}

// ---------------------------------------------------------------------------
// node task status <upid>
// ---------------------------------------------------------------------------

func TestNodeTaskStatus_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:1:2:3:qmstart:100:root@pam:"
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/tasks/"+upid+"/status", &rec, map[string]any{
		"upid": upid, "status": "stopped", "exitstatus": "OK",
		"id": "100", "type": "qmstart", "user": "root@pam", "node": "pve1",
		"pid": 12345,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "task", "status", upid))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, buf.String(), "stopped")
	require.Contains(t, buf.String(), "qmstart")
}

func TestNodeTaskStatus_Running(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:1:2:3:qmstop:101:root@pam:"
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/tasks/"+upid+"/status", &rec, map[string]any{
		"upid": upid, "status": "running",
		"id": "101", "type": "qmstop", "user": "root@pam", "node": "pve1",
		"pid": 99,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "task", "status", upid))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "running")
}

func TestNodeTaskStatus_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "task", "status", "UPID:pve1:1:2:3:test:0:root@pam:"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeTaskStatus_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:1:2:3:qmstart:100:root@pam:"
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "not found")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "task", "status", upid))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get status of task")
}

func TestNodeTaskStatus_InCommandTree(t *testing.T) {
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
	task := find(nodeCmd, "task")
	require.NotNil(t, find(task, "status"), "task must expose status")
}

// ---------------------------------------------------------------------------
// node replication get <id>
// ---------------------------------------------------------------------------

func TestNodeReplicationGet_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/replication/100-0", &rec, []any{
		map[string]any{"key": "target", "value": "pve2"},
		map[string]any{"key": "rate", "value": 1.0},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "replication", "get", "100-0"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/replication/100-0", rec.path)
	require.Contains(t, buf.String(), "target")
}

func TestNodeReplicationGet_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "replication", "get", "100-0"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeReplicationGet_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/replication/100-0", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "not found")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "replication", "get", "100-0"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get replication job")
}

func TestNodeReplicationGet_InCommandTree(t *testing.T) {
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
	repl := find(nodeCmd, "replication")
	require.NotNil(t, find(repl, "get"), "replication must expose get")
}

// ---------------------------------------------------------------------------
// node ceph log
// ---------------------------------------------------------------------------

func TestNodeCephLog_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/ceph/log", &rec, []any{
		map[string]any{"n": 1, "t": "2024-01-01 00:00:00 pve1 mon.pve1 -- started"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "log"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/ceph/log", rec.path)
	require.Contains(t, buf.String(), "mon.pve1")
}

func TestNodeCephLog_WithLimitAndStart(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/ceph/log", &rec, []any{})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "log",
		"--limit", "100", "--start", "50"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "limit=100")
	require.Contains(t, rec.query, "start=50")
}

func TestNodeCephLog_LimitOmittedWhenNotSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/ceph/log", &rec, []any{})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "log"))

	require.NoError(t, root.Execute())
	require.NotContains(t, rec.query, "limit=")
	require.NotContains(t, rec.query, "start=")
}

func TestNodeCephLog_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "ceph", "log"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeCephLog_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/ceph/log", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "log"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get Ceph log on node")
}

// ---------------------------------------------------------------------------
// node ceph rules
// ---------------------------------------------------------------------------

func TestNodeCephRules_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/ceph/rules", &rec, []any{
		map[string]any{"name": "replicated_rule", "type": "replicated", "id": 0},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "rules"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/ceph/rules", rec.path)
	require.Contains(t, buf.String(), "replicated_rule")
}

func TestNodeCephRules_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "ceph", "rules"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeCephRules_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/ceph/rules", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "rules"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "list Ceph CRUSH rules on node")
}

// ---------------------------------------------------------------------------
// node ceph crush
// ---------------------------------------------------------------------------

func TestNodeCephCrush_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	// ListCephCrushResponse = json.RawMessage; API returns the crush map as a JSON-encoded string.
	recordOn(f, "GET /api2/json/nodes/pve1/ceph/crush", &rec, "# begin crush map\ntunable chooseleaf_descend_once 1\n")

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "crush"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/ceph/crush", rec.path)
	require.Contains(t, buf.String(), "crush map")
}

func TestNodeCephCrush_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "ceph", "crush"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeCephCrush_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/ceph/crush", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "crush"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get Ceph CRUSH map on node")
}

// ---------------------------------------------------------------------------
// command-tree check for log/rules/crush
// ---------------------------------------------------------------------------

func TestNodeCephReadsCommandTree(t *testing.T) {
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
	ceph := find(nodeCmd, "ceph")
	require.NotNil(t, ceph)
	require.NotNil(t, find(ceph, "log"), "ceph must expose log")
	require.NotNil(t, find(ceph, "rules"), "ceph must expose rules")
	require.NotNil(t, find(ceph, "crush"), "ceph must expose crush")
}

// ---------------------------------------------------------------------------
// node ceph cfg db
// ---------------------------------------------------------------------------

func TestNodeCephCfgDb_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/ceph/cfg/db", &rec, []any{
		map[string]any{"section": "global", "name": "osd_pool_default_size", "value": "3"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "cfg", "db"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/ceph/cfg/db", rec.path)
	require.Contains(t, buf.String(), "osd_pool_default_size")
}

func TestNodeCephCfgDb_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "ceph", "cfg", "db"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeCephCfgDb_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/ceph/cfg/db", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "cfg", "db"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get Ceph config-DB on node")
}

// ---------------------------------------------------------------------------
// node ceph cfg raw
// ---------------------------------------------------------------------------

func TestNodeCephCfgRaw_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/ceph/cfg/raw", &rec,
		"[global]\n\tfsid = aaaa-bbbb\n\tauth_cluster_required = cephx\n")

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "cfg", "raw"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/ceph/cfg/raw", rec.path)
	require.Contains(t, buf.String(), "cephx")
}

func TestNodeCephCfgRaw_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "ceph", "cfg", "raw"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeCephCfgRaw_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/ceph/cfg/raw", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "cfg", "raw"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get raw Ceph configuration on node")
}

// ---------------------------------------------------------------------------
// node ceph cfg value
// ---------------------------------------------------------------------------

func TestNodeCephCfgValue_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/ceph/cfg/value", &rec,
		map[string]any{"global/osd_pool_default_size": "3"})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "cfg", "value",
		"--keys", "global:osd_pool_default_size"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/ceph/cfg/value", rec.path)
	require.Contains(t, rec.query, "config-keys=")
	require.Contains(t, buf.String(), "osd_pool_default_size")
}

func TestNodeCephCfgValue_RequiresKeys(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "cfg", "value"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "keys")
}

func TestNodeCephCfgValue_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "ceph", "cfg", "value", "--keys", "global:auth"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeCephCfgValue_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/ceph/cfg/value", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid key")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "cfg", "value",
		"--keys", "global:nosuchkey"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get Ceph configuration values on node")
}

// ---------------------------------------------------------------------------
// command-tree check: cfg is now a sub-group with index/db/raw/value
// ---------------------------------------------------------------------------

func TestNodeCephCfgCommandTree(t *testing.T) {
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
	ceph := find(nodeCmd, "ceph")
	require.NotNil(t, ceph)
	cfg := find(ceph, "cfg")
	require.NotNil(t, cfg, "ceph must expose cfg")
	require.NotNil(t, find(cfg, "index"), "cfg must expose index")
	require.NotNil(t, find(cfg, "db"), "cfg must expose db")
	require.NotNil(t, find(cfg, "raw"), "cfg must expose raw")
	require.NotNil(t, find(cfg, "value"), "cfg must expose value")
}

// ---------------------------------------------------------------------------
// node execute
// ---------------------------------------------------------------------------

func TestNodeExecute_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/execute", &rec, []any{
		map[string]any{"exitcode": 0, "stdout": "Linux\n", "stderr": ""},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "execute",
		"--commands", `["uname -s"]`))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/execute", rec.path)
	require.Contains(t, rec.query, "commands=")
	// Table headers are uppercased; verify the response columns are rendered.
	require.Contains(t, buf.String(), "EXITCODE")
}

func TestNodeExecute_RejectsInvalidJSON(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "execute",
		"--commands", "not-valid-json"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--commands must be a valid JSON array")
}

func TestNodeExecute_RejectsNonArrayJSON(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "execute",
		"--commands", `{"cmd":"ls"}`))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--commands must be a valid JSON array")
}

func TestNodeExecute_RequiresCommands(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "execute"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "commands")
}

func TestNodeExecute_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "execute", "--commands", `["ls"]`))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeExecute_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/execute", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "not allowed")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "execute",
		"--commands", `["ls"]`))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "execute commands on node")
}

func TestNodeExecute_InCommandTree(t *testing.T) {
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
	require.NotNil(t, find(nodeCmd, "execute"), "node must expose execute")
}
