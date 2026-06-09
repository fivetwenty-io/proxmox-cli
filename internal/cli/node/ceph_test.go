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

const cephUPID = "UPID:pve1:00000001:00000002:AABBCCDD:cephcreatepool:rbd:root@pam:"

// cephOK registers a task-status handler that reports the worker finished OK so
// the synchronous WaitTask path resolves.
func cephOK(f *testhelper.FakePVE, upid string) {
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})
}

// ---- read-only -------------------------------------------------------------

func TestNodeCeph_Status(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/ceph/status", map[string]any{
		"health":  map[string]any{"status": "HEALTH_OK"},
		"fsid":    "abc-123",
		"quorum":  []any{0, 1, 2},
		"version": "19.2.0",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "status"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "abc-123")
}

func TestNodeCeph_Cfg(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/ceph/cfg", []any{
		map[string]any{"section": "global", "name": "auth_cluster_required", "value": "cephx"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "cfg"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "cephx")
}

func TestNodeCephOsd_List(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/ceph/osd", map[string]any{
		"flags": "noout",
		"root":  map[string]any{"name": "default", "id": -1},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "list"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "noout")
}

func TestNodeCephOsd_Get(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/ceph/osd/0", []any{
		map[string]any{"name": "osd.0", "ceph_version": "19.2.0"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "get", "0"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "osd.0")
}

func TestNodeCephPool_List(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/ceph/pool", []any{
		map[string]any{"pool_name": "rbd", "size": 3, "min_size": 2},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "pool", "list"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "rbd")
}

func TestNodeCephPool_Status(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/ceph/pool/rbd/status", &rec, map[string]any{
		"name": "rbd", "size": 3, "pg_num": 128,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "pool", "status", "rbd", "--verbose"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "verbose=1")
	require.Contains(t, buf.String(), "rbd")
}

// ---- OSD create / delete ---------------------------------------------------

func TestNodeCephOsd_Create_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/osd", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "create", "--dev", "/dev/sdb"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "no API call must be made without confirmation")
}

func TestNodeCephOsd_Create_BlocksUntilDone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/osd", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		_ = r.ParseForm()
		rec.body = r.Form.Encode()
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "create",
		"--dev", "/dev/sdb", "--encrypted", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "dev=%2Fdev%2Fsdb")
	require.Contains(t, rec.body, "encrypted=1")
	// --crush-device-class was not passed, so it must be omitted.
	require.NotContains(t, rec.body, "crush-device-class")
	require.Contains(t, buf.String(), "created")
}

func TestNodeCephOsd_Create_AsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/osd", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, cephUPID)
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--async", "--node", "pve1", "node", "ceph", "osd", "create",
		"--dev", "/dev/sdb", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), cephUPID)
}

func TestNodeCephOsd_Delete_WithCleanup(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("DELETE /api2/json/nodes/pve1/ceph/osd/0", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.query = r.URL.RawQuery
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "delete", "0", "--cleanup", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, rec.query, "cleanup=1")
	require.Contains(t, buf.String(), "destroyed")
}

func TestNodeCephOsd_In_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/osd/0/in", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "in", "0"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeCephOsd_Out_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/ceph/osd/0/out", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "out", "0", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, buf.String(), "marked out")
}

func TestNodeCephOsd_Scrub_Deep(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/osd/0/scrub", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		_ = r.ParseForm()
		rec.body = r.Form.Encode()
		testhelper.WriteData(w, nil)
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "scrub", "0", "--deep", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.body, "deep=1")
	require.Contains(t, buf.String(), "Scrub requested")
}

// ---- pool create / set / delete --------------------------------------------

func TestNodeCephPool_Create_BlocksUntilDone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/pool", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		_ = r.ParseForm()
		rec.body = r.Form.Encode()
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "pool", "create", "rbd",
		"--size", "3", "--min-size", "2", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "name=rbd")
	require.Contains(t, rec.body, "size=3")
	require.Contains(t, rec.body, "min_size=2")
	require.Contains(t, buf.String(), "created")
}

func TestNodeCephPool_Create_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/pool", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "pool", "create", "rbd"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeCephPool_Set_RequiresChange(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("PUT /api2/json/nodes/pve1/ceph/pool/rbd", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "pool", "set", "rbd", "--yes"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes")
	require.False(t, called)
}

func TestNodeCephPool_Set_ForwardsChanged(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("PUT /api2/json/nodes/pve1/ceph/pool/rbd", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		_ = r.ParseForm()
		rec.body = r.Form.Encode()
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "pool", "set", "rbd",
		"--pg-autoscale-mode", "on", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "PUT", rec.method)
	require.Contains(t, rec.body, "pg_autoscale_mode=on")
	// size was not passed, so it must be omitted.
	require.NotContains(t, rec.body, "size=")
	require.Contains(t, buf.String(), "updated")
}

func TestNodeCephPool_Delete_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("DELETE /api2/json/nodes/pve1/ceph/pool/rbd", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.query = r.URL.RawQuery
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "pool", "delete", "rbd", "--force", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, rec.query, "force=1")
	require.Contains(t, buf.String(), "destroyed")
}

// ---- daemons (mon/mds/mgr/fs) ----------------------------------------------

func TestNodeCephMon_Create_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/mon/pve1", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "mon", "create", "pve1"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeCephMon_Delete_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("DELETE /api2/json/nodes/pve1/ceph/mon/pve1", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "mon", "delete", "pve1", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), "destroyed")
}

func TestNodeCephMds_Create_ForwardsHotstandby(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/mds/pve1", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		_ = r.ParseForm()
		rec.body = r.Form.Encode()
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "mds", "create", "pve1", "--hotstandby", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.body, "hotstandby=1")
	require.Contains(t, buf.String(), "created")
}

func TestNodeCephMds_Delete_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("DELETE /api2/json/nodes/pve1/ceph/mds/pve1", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "mds", "delete", "pve1", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), "destroyed")
}

func TestNodeCephMds_Delete_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/nodes/pve1/ceph/mds/pve1", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "mds", "delete", "pve1"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeCephMgr_Delete_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("DELETE /api2/json/nodes/pve1/ceph/mgr/pve1", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "mgr", "delete", "pve1", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), "destroyed")
}

func TestNodeCephMgr_Delete_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/nodes/pve1/ceph/mgr/pve1", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "mgr", "delete", "pve1"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeCephMgr_Create_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/mgr/pve1", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "mgr", "create", "pve1", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, buf.String(), "created")
}

func TestNodeCephFs_Create_ForwardsFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/fs/cephfs", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		_ = r.ParseForm()
		rec.body = r.Form.Encode()
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "fs", "create", "cephfs",
		"--add-storage", "--pg-num", "64", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.body, "add-storage=1")
	require.Contains(t, rec.body, "pg_num=64")
	require.Contains(t, buf.String(), "created")
}

func TestNodeCephFs_Delete_ForwardsFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("DELETE /api2/json/nodes/pve1/ceph/fs/cephfs", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.query = r.URL.RawQuery
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "fs", "delete", "cephfs",
		"--remove-pools", "--remove-storages", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, rec.query, "remove-pools=1")
	require.Contains(t, rec.query, "remove-storages=1")
	require.Contains(t, buf.String(), "destroyed")
}

func TestNodeCephFs_Delete_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/nodes/pve1/ceph/fs/cephfs", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "fs", "delete", "cephfs"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

// ---- init + service control ------------------------------------------------

func TestNodeCeph_Init_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/init", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "init", "--size", "3"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeCeph_Init_ForwardsFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/init", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		_ = r.ParseForm()
		rec.body = r.Form.Encode()
		testhelper.WriteData(w, nil)
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "init",
		"--size", "3", "--min-size", "2", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.body, "size=3")
	require.Contains(t, rec.body, "min_size=2")
	require.Contains(t, buf.String(), "initialized")
}

func TestNodeCeph_Start_BlocksUntilDone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/start", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		_ = r.ParseForm()
		rec.body = r.Form.Encode()
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "start", "--service", "osd.0", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "service=osd.0")
	require.Contains(t, buf.String(), "issued")
}

func TestNodeCeph_Stop_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/stop", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "stop"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

// ---- guard / success completeness ------------------------------------------

func TestNodeCephOsd_Delete_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/nodes/pve1/ceph/osd/0", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "delete", "0"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeCephOsd_In_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/ceph/osd/0/in", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "in", "0", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, buf.String(), "marked in")
}

func TestNodeCephOsd_Out_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/osd/0/out", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "out", "0"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeCephOsd_Scrub_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/osd/0/scrub", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "scrub", "0"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeCephMon_Create_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/mon/pve1", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "mon", "create", "pve1", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, buf.String(), "created")
}

func TestNodeCephMon_Delete_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/nodes/pve1/ceph/mon/pve1", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "mon", "delete", "pve1"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeCephPool_Delete_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/nodes/pve1/ceph/pool/rbd", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "pool", "delete", "rbd"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeCeph_Start_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/start", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "start"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeCeph_Stop_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/stop", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		_ = r.ParseForm()
		rec.body = r.Form.Encode()
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "stop", "--service", "osd.0", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "service=osd.0")
	require.Contains(t, buf.String(), "issued")
}

func TestNodeCeph_Restart_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/restart", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		_ = r.ParseForm()
		rec.body = r.Form.Encode()
		testhelper.WriteData(w, cephUPID)
	})
	cephOK(f, cephUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "restart", "--service", "mon.pve1", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "service=mon.pve1")
	require.Contains(t, buf.String(), "issued")
}

func TestNodeCeph_Restart_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/restart", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "restart"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

// ---- node scoping + command tree -------------------------------------------

func TestNodeCeph_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "ceph", "status"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeCeph_CommandTree(t *testing.T) {
	root := cli.NewRootCmd()
	cli.AddGroups(root, &cli.Deps{})

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
	ceph := find(nodeCmd, "ceph")
	require.NotNil(t, ceph, "node ceph command must be registered")

	for _, verb := range []string{
		"status", "cfg", "osd", "pool", "mon", "mds", "mgr", "fs", "init", "start", "stop", "restart",
	} {
		require.NotNil(t, find(ceph, verb), "ceph must expose %q", verb)
	}
	osd := find(ceph, "osd")
	for _, verb := range []string{"list", "get", "create", "delete", "in", "out", "scrub"} {
		require.NotNil(t, find(osd, verb), "ceph osd must expose %q", verb)
	}
	pool := find(ceph, "pool")
	for _, verb := range []string{"list", "get", "status", "create", "set", "delete"} {
		require.NotNil(t, find(pool, verb), "ceph pool must expose %q", verb)
	}
}
