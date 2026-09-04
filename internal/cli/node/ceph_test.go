package node_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
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

func TestNodeCeph_CmdSafety(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/ceph/cmd-safety", &rec, map[string]any{
		"safe": true, "status": "no other OSDs would be affected",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "cmd-safety",
		"--action", "stop", "--id", "osd.0", "--service", "osd"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, rec.query, "action=stop")
	require.Contains(t, rec.query, "id=osd.0")
	require.Contains(t, rec.query, "service=osd")
	require.Contains(t, buf.String(), "no other OSDs would be affected")
}

func TestNodeCeph_CmdSafety_RequiresFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("GET /api2/json/nodes/pve1/ceph/cmd-safety", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "cmd-safety", "--action", "stop"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "required flag(s)")
	require.False(t, called, "no API call must be made when required flags are missing")
}

func TestNodeCeph_CmdSafety_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/ceph/cmd-safety", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "cmd-safety",
		"--action", "stop", "--id", "osd.0", "--service", "osd"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "check Ceph command safety on node")
}

func TestNodeCeph_Cfg(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/ceph/cfg", []any{
		map[string]any{"section": "global", "name": "auth_cluster_required", "value": "cephx"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	// cfg is now a sub-group; the original list functionality lives under cfg index.
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "cfg", "index"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "cephx")
}

func TestNodeCephOsd_List(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/ceph/osd", map[string]any{
		"flags": "noout",
		// The outermost "root" PVE sends is an unnamed container; the CRUSH
		// roots are its children.
		"root": map[string]any{"leaf": 0, "children": []any{
			map[string]any{
				"name": "default", "id": "-1", "type": "root", "reweight": -1, "leaf": 0,
				"children": []any{
					map[string]any{
						"name": "pve1", "id": -3, "type": "host", "reweight": -1, "leaf": 0,
						"children": []any{
							map[string]any{
								"name": "osd.0", "id": 0, "type": "osd", "device_class": "ssd",
								"status": "up", "in": 1, "crush_weight": 0.09769, "reweight": 1,
								"pgs": 33, "leaf": 1,
							},
						},
					},
				},
			},
		}},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "list"))

	require.NoError(t, root.Execute())
	// The CRUSH tree is one row per bucket and OSD; the cluster-wide flags are
	// not per-OSD and stay in -o json.
	out := buf.String()
	require.Contains(t, out, "default")
	require.Contains(t, out, "pve1")
	require.Contains(t, out, "osd.0")
}

// TestNodeCephOsd_Get verifies `osd get` reads the metadata child endpoint:
// GET /nodes/{node}/ceph/osd/{osdid} itself is only a directory index.
func TestNodeCephOsd_Get(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/ceph/osd/0/metadata", map[string]any{
		"osd": map[string]any{"name": "osd.0", "ceph_version": "19.2.0"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "get", "0"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "osd.0")
}

func TestNodeCephOsd_LvInfo(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/ceph/osd/0/lv-info", &rec, map[string]any{
		"creation_time": "2024-01-01T00:00:00",
		"lv_name":       "osd-block-0",
		"lv_path":       "/dev/ceph-vg/osd-block-0",
		"lv_size":       10737418240,
		"lv_uuid":       "abc-uuid",
		"vg_name":       "ceph-vg",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "lv-info", "0", "--type", "block"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/ceph/osd/0/lv-info", rec.path)
	require.Contains(t, rec.query, "type=block")
	require.Contains(t, buf.String(), "osd-block-0")
	require.Contains(t, buf.String(), "ceph-vg")
}

func TestNodeCephOsd_LvInfo_NoTypeFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/ceph/osd/0/lv-info", &rec, map[string]any{
		"creation_time": "2024-01-01T00:00:00",
		"lv_name":       "osd-block-0",
		"lv_path":       "/dev/ceph-vg/osd-block-0",
		"lv_size":       10737418240,
		"lv_uuid":       "abc-uuid",
		"vg_name":       "ceph-vg",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "lv-info", "0"))

	require.NoError(t, root.Execute())
	// --type was not passed, so it must be omitted from the request.
	require.NotContains(t, rec.query, "type=")
	require.Contains(t, buf.String(), "osd-block-0")
}

func TestNodeCephOsd_LvInfo_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/ceph/osd/0/lv-info", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "lv-info", "0"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), `get logical volume info for Ceph OSD "0" on node`)
}

func TestNodeCephOsd_Metadata(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/ceph/osd/0/metadata", &rec, map[string]any{
		"osd": map[string]any{"id": 0, "hostname": "pve1"},
		"devices": []any{map[string]any{
			"device": "block", "dev_node": "/dev/sdb", "physical_device": "sdb",
			"type": "ssd", "size": 107369988096, "support_discard": true,
		}},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "metadata", "0"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/ceph/osd/0/metadata", rec.path)
	require.Contains(t, buf.String(), "pve1")
	require.Contains(t, buf.String(), "/dev/sdb")
}

func TestNodeCephOsd_Metadata_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/ceph/osd/0/metadata", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "osd", "metadata", "0"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), `get metadata for Ceph OSD "0" on node`)
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

// TestNodeCephPool_Get verifies `pool get` reads the status child endpoint:
// GET /nodes/{node}/ceph/pool/{name} itself is only a directory index.
func TestNodeCephPool_Get(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/ceph/pool/rbd/status", &rec, map[string]any{
		"name": "rbd", "size": 3, "min_size": 2, "crush_rule": "replicated_rule",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "pool", "get", "rbd"))

	require.NoError(t, root.Execute())
	require.Equal(t, "/api2/json/nodes/pve1/ceph/pool/rbd/status", rec.path)
	require.Contains(t, buf.String(), "replicated_rule")
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

func TestNodeCeph_Releases(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/ceph/releases", &rec, []any{
		map[string]any{"available": 0, "is-default": 0, "release": "reef", "unsupported": 0, "version": "18.2"},
		map[string]any{"available": 1, "is-default": 1, "release": "squid", "unsupported": 0, "version": "19.2"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "releases"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, buf.String(), "RELEASE")
	require.Contains(t, buf.String(), "squid")
	require.Contains(t, buf.String(), "reef")
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
	ceph := find(nodeCmd, "ceph")
	require.NotNil(t, ceph, "node ceph command must be registered")

	for _, verb := range []string{
		"status", "cmd-safety", "cfg", "osd", "pool", "mon", "mds", "mgr", "fs",
		"init", "start", "stop", "restart", "releases", "restart-bulk",
	} {
		require.NotNil(t, find(ceph, verb), "ceph must expose %q", verb)
	}
	osd := find(ceph, "osd")
	for _, verb := range []string{"list", "get", "lv-info", "metadata", "create", "delete", "in", "out", "scrub"} {
		require.NotNil(t, find(osd, verb), "ceph osd must expose %q", verb)
	}
	pool := find(ceph, "pool")
	for _, verb := range []string{"list", "get", "status", "create", "set", "delete"} {
		require.NotNil(t, find(pool, verb), "ceph pool must expose %q", verb)
	}
}

const cephBulkUPID = "UPID:pve1:00001234:00000ABC:66D0F2A0:cephrestartbulk:osd:root@pam:"

func TestNodeCeph_RestartBulk_HelpNamesTheServerRefusals(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "ceph", "restart-bulk", "--help"))

	err := root.Execute()
	require.NoError(t, err)
	require.Contains(t, buf.String(), "checkpoint from an aborted run")
	require.Contains(t, buf.String(), "ceph-osd version cannot be read")
}

func TestNodeCeph_RestartBulk_RefusesWithoutYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "restart-bulk"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(),
		"refusing to rolling-restart Ceph OSDs on node \"pve1\" without confirmation")
}

func TestNodeCeph_RestartBulk_RejectsNonOSDServiceType(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "restart-bulk",
		"--service-type", "mon", "--yes"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid --service-type")
}

func TestNodeCeph_RestartBulk_ForwardsFlagsAndWaits(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/ceph/restart-bulk", &rec, cephBulkUPID)
	cephOK(f, cephBulkUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "restart-bulk",
		"--yes", "--force", "--only-outdated", "--set-noout=false", "--timeout", "900"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.query, "service-type=osd")
	require.Contains(t, rec.query, "force=1")
	require.Contains(t, rec.query, "only-outdated=1")
	require.Contains(t, rec.query, "set-noout=0")
	require.Contains(t, rec.query, "timeout=900")
	require.NotContains(t, rec.query, "resume")
	require.NotContains(t, rec.query, "dry-run")
	require.Contains(t, buf.String(), "restarted")
}

func TestNodeCeph_RestartBulk_ResumeForwardsResume(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/ceph/restart-bulk", &rec, cephBulkUPID)
	cephOK(f, cephBulkUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "restart-bulk", "--resume", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "resume=1")
	require.NotContains(t, rec.query, "only-outdated", "the saved plan is replayed; nothing else is sent")
	require.NotContains(t, rec.query, "set-noout")
	require.Contains(t, buf.String(), "restarted")
}

func TestNodeCeph_RestartBulk_DefaultSetNooutIsNotSent(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/ceph/restart-bulk", &rec, cephBulkUPID)
	cephOK(f, cephBulkUPID)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "restart-bulk", "--yes"))

	require.NoError(t, root.Execute())
	require.NotContains(t, rec.query, "set-noout", "server default must not be overridden by the flag default")
	require.NotContains(t, rec.query, "timeout=")
}

func TestNodeCeph_RestartBulk_AsyncPrintsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/ceph/restart-bulk", &rec, cephBulkUPID)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--async", "--node", "pve1", "node", "ceph", "restart-bulk", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), cephBulkUPID)
}

func TestNodeCeph_RestartBulk_DryRunNeedsNoYesAndPrintsTheTaskLog(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/ceph/restart-bulk", &rec, cephBulkUPID)
	cephOK(f, cephBulkUPID)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+cephBulkUPID+"/log", []map[string]any{
		{"n": 1, "t": "dry-run: would restart osd.0, osd.3"},
		{"n": 2, "t": "TASK OK"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "restart-bulk", "--dry-run"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "dry-run=1")
	require.Contains(t, buf.String(), "would restart osd.0, osd.3")
}

func TestNodeCeph_RestartBulk_FailedDryRunStillPrintsTheLog(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/ceph/restart-bulk", &rec, cephBulkUPID)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+cephBulkUPID+"/status", map[string]any{
		"status": "stopped", "exitstatus": "cluster is not healthy (HEALTH_WARN: PG_DEGRADED)", "upid": cephBulkUPID,
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+cephBulkUPID+"/log", []map[string]any{
		{"n": 1, "t": "HEALTH_WARN: PG_DEGRADED; refusing to plan without --force"},
		{"n": 2, "t": "TASK ERROR: cluster is not healthy (HEALTH_WARN: PG_DEGRADED)"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "restart-bulk", "--dry-run"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "ceph dry run on node \"pve1\"")
	require.Contains(t, buf.String(), "refusing to plan without --force", "the log is the point of a dry run")
}

// TestNodeCeph_RestartBulk_DryRunWaitTimeoutStaysNeutral pins the fix for a
// dry run whose client-side wait deadline expires: the worker only logged a
// plan, so the error must not claim a restart is in progress. The task type
// embedded in a real UPID is "cephrestartbulk", which itself contains the
// substring "restart", so this test uses a plan-only UPID to make the "no
// restart wording" assertion meaningful rather than trivially defeated by the
// task handle's own name.
func TestNodeCeph_RestartBulk_DryRunWaitTimeoutStaysNeutral(t *testing.T) {
	planUPID := "UPID:pve1:00001234:00000ABC:66D0F2A0:cephosdplan:osd:root@pam:"
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/ceph/restart-bulk", &rec, planUPID)
	ctx, expire := testhelper.ExpiringContext(context.Background())
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+planUPID+"/status", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"status": "running", "upid": planUPID})
		expire()
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+planUPID+"/log", []map[string]any{
		{"n": 1, "t": "dry-run: would restart osd.0, osd.3"},
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "restart-bulk", "--dry-run", "--wait-timeout", "1"))

	err := root.ExecuteContext(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "still running")
	require.Contains(t, err.Error(), planUPID)
	require.NotContains(t, err.Error(), "restart",
		"nothing was restarted by a --dry-run worker; the message must not claim one is in progress")
}

func TestNodeCeph_RestartBulk_NoTaskHandleIsAnError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("POST /api2/json/nodes/pve1/ceph/restart-bulk", nil) // {"data": null}

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "restart-bulk", "--yes"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "server returned no task handle")
	require.NotContains(t, buf.String(), "Ceph OSDs on node \"pve1\" restarted.")
}

func TestNodeCeph_RestartBulk_WaitTimeoutSaysTheRollContinues(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/ceph/restart-bulk", &rec, cephBulkUPID)
	ctx, expire := testhelper.ExpiringContext(context.Background())
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+cephBulkUPID+"/status", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"status": "running", "upid": cephBulkUPID})
		expire()
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "restart-bulk", "--yes", "--wait-timeout", "1"))

	err := root.ExecuteContext(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stopped waiting after 1s")
	require.Contains(t, err.Error(), "still running")
	require.Contains(t, err.Error(), cephBulkUPID)
	require.Contains(t, err.Error(), "rolling-restart ceph osds on node \"pve1\"",
		"the wait failure carries the same operation prefix as a submit failure")
	require.NotContains(t, err.Error(), "ceph operation on node",
		"restart-bulk names its operation itself rather than through the per-daemon renderer")
}

func TestNodeCeph_RestartBulk_RejectsNegativeWaitTimeout(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "restart-bulk", "--yes", "--wait-timeout=-5"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid --wait-timeout")
}

func TestNodeCeph_RestartBulk_RejectsTimeoutOutOfRange(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "restart-bulk", "--yes", "--timeout", "5"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid --timeout 5: want 30 to 1800 seconds")
}

func TestNodeCeph_RestartBulk_SurfacesAPIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/ceph/restart-bulk", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "HEALTH_ERR")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "ceph", "restart-bulk", "--yes"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "rolling-restart ceph osds on node \"pve1\"")
	require.Contains(t, err.Error(), "HEALTH_ERR")
}
