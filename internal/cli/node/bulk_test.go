package node_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestNodeStartall_ForwardsFields verifies `pmx pve node startall` posts the VMID
// list and --force to the startall endpoint and omits unset flags.
func TestNodeStartall_ForwardsFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/startall", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "startall", "--vmids", "100,101", "--force", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/startall", rec.path)
	form, err := url.ParseQuery(rec.body)
	require.NoError(t, err)
	require.Equal(t, "100,101", form.Get("vms"))
	require.Equal(t, "1", form.Get("force"))
	_, hasMW := form["max-workers"]
	require.False(t, hasMW, "unset --max-workers must be omitted from the request body")
	require.Contains(t, buf.String(), "Start-all started")
}

// TestNodeStartall_RequiresYes verifies startall refuses to act without --yes.
func TestNodeStartall_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/startall", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "startall"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "startall must not POST without --yes")
}

// TestNodeStartall_WarnsOnNonResidentVmids verifies --vmids entries that are
// not on the target node (or unknown to the cluster) produce stderr warnings
// while the bulk action still runs.
func TestNodeStartall_WarnsOnNonResidentVmids(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"type": "lxc", "vmid": 100, "node": "pve3", "id": "lxc/100"},
		})
	})
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/startall", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "startall", "--vmids", "100,999", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method, "the bulk action must still be submitted")
	out := buf.String()
	require.Contains(t, out, `warning: guest 100 is on node "pve3", not "pve1"`)
	require.Contains(t, out, "warning: guest 999 not found in the cluster")
}

// TestNodeStartall_RequiresNode verifies startall fails clearly with no node.
func TestNodeStartall_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "startall", "--yes"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

// TestNodeStartall_AsyncReturnsUPID verifies --async prints the task UPID without
// waiting for completion.
func TestNodeStartall_AsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:startall::root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/startall", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, upid)
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--async", "--node", "pve1", "node", "startall", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), upid)
}

// TestNodeStopall_ForwardsForceStop verifies stopall forwards --force-stop.
func TestNodeStopall_ForwardsForceStop(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/stopall", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "stopall", "--force-stop", "--timeout", "30", "--yes"))

	require.NoError(t, root.Execute())
	form, err := url.ParseQuery(rec.body)
	require.NoError(t, err)
	require.Equal(t, "1", form.Get("force-stop"))
	require.Equal(t, "30", form.Get("timeout"))
	require.Contains(t, buf.String(), "Stop-all started")
}

// TestNodeSuspendall_Succeeds verifies suspendall posts to the suspendall endpoint.
func TestNodeSuspendall_Succeeds(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/suspendall", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "suspendall", "--vmids", "100", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "/api2/json/nodes/pve1/suspendall", rec.path)
	form, err := url.ParseQuery(rec.body)
	require.NoError(t, err)
	require.Equal(t, "100", form.Get("vms"))
	require.Contains(t, buf.String(), "Suspend-all started")
}

// TestNodeMigrateall_RequiresTargetNode verifies migrateall fails without the
// required --target-node flag.
func TestNodeMigrateall_RequiresTargetNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/migrateall", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "migrateall", "--yes"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "target-node")
	require.False(t, called, "migrateall must not POST without --target-node")
}

// TestNodeMigrateall_ForwardsFields verifies migrateall posts the target node and
// VMID list.
func TestNodeMigrateall_ForwardsFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/migrateall", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "migrateall",
		"--target-node", "pve2", "--vmids", "100", "--with-local-disks", "--yes"))

	require.NoError(t, root.Execute())
	form, err := url.ParseQuery(rec.body)
	require.NoError(t, err)
	require.Equal(t, "pve2", form.Get("target"))
	require.Equal(t, "100", form.Get("vms"))
	require.Equal(t, "1", form.Get("with-local-disks"))
	require.Contains(t, buf.String(), "Migrate-all started")
}

// TestNodeMigrateall_ForwardsMaxWorkers verifies --max-workers reaches the API.
// Proxmox VE rejects the whole request when neither this nor datacenter.cfg's
// max_workers is set, so dropping the flag would make the command unusable on a
// default cluster.
func TestNodeMigrateall_ForwardsMaxWorkers(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/migrateall", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "migrateall",
		"--target-node", "pve2", "--max-workers", "1", "--yes"))

	require.NoError(t, root.Execute())
	form, err := url.ParseQuery(rec.body)
	require.NoError(t, err)
	require.Equal(t, "1", form.Get("max-workers"))
}

// TestNodeMigrateall_HelpNamesTheWorkerRequirement guards the note that tells an
// operator why an otherwise complete command line is refused by the server.
func TestNodeMigrateall_HelpNamesTheWorkerRequirement(t *testing.T) {
	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	addNodeGroup(root)

	cmd, _, err := root.Find([]string{"node", "migrateall"})
	require.NoError(t, err)
	require.Contains(t, cmd.Long, "max_workers in datacenter.cfg")
	require.Contains(t, cmd.Flags().Lookup("max-workers").Usage, "required unless")
}

// TestNodeMigrateall_RequiresYes verifies migrateall refuses to act without
// --yes even when the required --target-node is supplied.
func TestNodeMigrateall_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/migrateall", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "migrateall", "--target-node", "pve2"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "migrateall must not POST without --yes")
}

// TestNodeWakeonlan_RequiresYes verifies wakeonlan refuses to act without --yes.
func TestNodeWakeonlan_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/wakeonlan", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "wakeonlan"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "wakeonlan must not POST without --yes")
}

// TestNodeWakeonlan_SendsPacket verifies wakeonlan posts to the wakeonlan endpoint
// and renders a success message even when the response is a MAC (not a UPID).
func TestNodeWakeonlan_SendsPacket(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/wakeonlan", &rec, "AA:BB:CC:DD:EE:FF")

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "wakeonlan", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/wakeonlan", rec.path)
	require.Contains(t, buf.String(), "Wake-on-LAN packet sent")
	require.Contains(t, buf.String(), "(AA:BB:CC:DD:EE:FF)",
		"the MAC the packet was sent to must be rendered in the message")
}

// TestNodeStartall_WaitsForTask drives the default synchronous path: the POST
// returns a real UPID, the command polls the task-status endpoint, and only
// renders the done message after the task ends OK.
func TestNodeStartall_WaitsForTask(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:startall::root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/startall", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, upid)
	})
	statusHit := false
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", func(w http.ResponseWriter, _ *http.Request) {
		statusHit = true
		testhelper.WriteData(w, map[string]any{"status": "stopped", "exitstatus": "OK", "upid": upid})
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "startall", "--yes"))

	require.NoError(t, root.Execute())
	require.True(t, statusHit, "the synchronous path must poll the task-status endpoint")
	require.Contains(t, buf.String(), "Start-all started")
}

// TestNodeStartall_WaitTaskError verifies a failed task surfaces the task-wait
// error wrap rather than the success message.
func TestNodeStartall_WaitTaskError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:startall::root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/startall", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, upid)
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"status": "stopped", "exitstatus": "command 'qm start' failed: exit code 1", "upid": upid,
		})
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "startall", "--yes"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "bulk action on node \"pve1\"")
}

// TestNodeBulk_CommandTree verifies the node group exposes every bulk verb.
func TestNodeBulk_CommandTree(t *testing.T) {
	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	addNodeGroup(root)
	var nodeCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "node" {
			nodeCmd = c
		}
	}
	require.NotNil(t, nodeCmd, "node group must be registered")

	names := make(map[string]bool)
	for _, c := range nodeCmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"startall", "stopall", "suspendall", "migrateall", "wakeonlan"} {
		require.True(t, names[want], "expected node sub-command %q", want)
	}
}
