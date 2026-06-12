package node_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/exec"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// ---- list ------------------------------------------------------------------

func TestNodeDisks_List(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/disks/list", &rec, []any{
		map[string]any{
			"devpath": "/dev/sdb", "type": "ssd", "size": 512110190592,
			"model": "Samsung", "serial": "S1", "health": "PASSED", "used": "LVM",
		},
		map[string]any{
			"devpath": "/dev/sda", "type": "hdd", "size": 1000204886016,
			"model": "WDC", "serial": "W2", "health": "PASSED", "used": "partitions",
		},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "list"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/disks/list", rec.path)
	out := buf.String()
	require.Contains(t, out, "DEVPATH")
	require.Contains(t, out, "HEALTH")
	require.Contains(t, out, "/dev/sda")
	require.Contains(t, out, "512110190592")
	// Rows are sorted by devpath: /dev/sda precedes /dev/sdb.
	require.Less(t, strings.Index(out, "/dev/sda"), strings.Index(out, "/dev/sdb"))
}

func TestNodeDisks_List_JSONLossless(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/disks/list", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"devpath": "/dev/sda", "wwn": "0x5000abcdef", "rpm": 7200},
		})
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatJSON, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "list"))

	require.NoError(t, root.Execute())
	// Fields outside the table columns (wwn, rpm) must survive in JSON output.
	require.Contains(t, buf.String(), "0x5000abcdef")
}

// ---- smart -----------------------------------------------------------------

func TestNodeDisks_Smart(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/disks/smart", &rec, map[string]any{
		"health": "PASSED",
		"type":   "ata",
		"attributes": []any{
			map[string]any{"name": "Temperature_Celsius", "raw": "35"},
		},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "smart", "--disk", "/dev/sda"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, rec.query, "disk=%2Fdev%2Fsda")
	out := buf.String()
	require.Contains(t, out, "health")
	require.Contains(t, out, "PASSED")
}

func TestNodeDisks_Smart_RequiresDisk(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "smart"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "disk")
}

// ---- create ----------------------------------------------------------------

func TestNodeDisks_CreateLvm_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/disks/lvm", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "create", "lvm",
		"--device", "/dev/sdb", "--name", "vg-data"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "no API call must be made without confirmation")
}

func TestNodeDisks_CreateLvm_BlocksUntilDone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:diskinit:vg-data:root@pam:"
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/disks/lvm", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		_ = r.ParseForm()
		rec.body = r.Form.Encode()
		testhelper.WriteData(w, upid)
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "create", "lvm",
		"--device", "/dev/sdb", "--name", "vg-data", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "device=%2Fdev%2Fsdb")
	require.Contains(t, rec.body, "name=vg-data")
	// --add-storage was not passed, so it must be omitted from the request.
	require.NotContains(t, rec.body, "add_storage")
	require.Contains(t, buf.String(), "created")
}

func TestNodeDisks_CreateLvm_AsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:diskinit:vg-data:root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/disks/lvm", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, upid)
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--async", "--node", "pve1", "node", "disks", "create", "lvm",
		"--device", "/dev/sdb", "--name", "vg-data", "--yes"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), upid)
}

func TestNodeDisks_CreateLvmthin_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/disks/lvmthin", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "create", "lvmthin",
		"--device", "/dev/sdb", "--name", "thin-data"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "no API call must be made without confirmation")
}

func TestNodeDisks_CreateLvmthin_ForwardsFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/disks/lvmthin", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "create", "lvmthin",
		"--device", "/dev/sdb", "--name", "thin-data", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "device=%2Fdev%2Fsdb")
	require.Contains(t, rec.body, "name=thin-data")
	// --add-storage was not passed, so it must be omitted from the request.
	require.NotContains(t, rec.body, "add_storage")
	require.Contains(t, buf.String(), "created")
}

func TestNodeDisks_CreateZfs_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/disks/zfs", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "create", "zfs",
		"--devices", "/dev/sdb", "--name", "tank", "--raidlevel", "single"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "no API call must be made without confirmation")
}

func TestNodeDisks_CreateZfs_RequiresRaidlevel(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "create", "zfs",
		"--devices", "/dev/sdb", "--name", "tank", "--yes"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "raidlevel")
}

func TestNodeDisks_CreateZfs_ForwardsFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/disks/zfs", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "create", "zfs",
		"--devices", "/dev/sdb,/dev/sdc", "--name", "tank", "--raidlevel", "mirror", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "devices=%2Fdev%2Fsdb%2C%2Fdev%2Fsdc")
	require.Contains(t, rec.body, "name=tank")
	require.Contains(t, rec.body, "raidlevel=mirror")
	require.Contains(t, buf.String(), "created")
}

func TestNodeDisks_CreateDirectory_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/disks/directory", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "create", "directory",
		"--device", "/dev/sdb", "--name", "backups", "--filesystem", "ext4", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "filesystem=ext4")
	require.Contains(t, buf.String(), "created")
}

// ---- init-gpt + wipe -------------------------------------------------------

func TestNodeDisks_InitGpt_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/disks/initgpt", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "init-gpt", "--disk", "/dev/sdb"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeDisks_InitGpt_ForwardsFields(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/disks/initgpt", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "init-gpt",
		"--disk", "/dev/sdb", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, rec.body, "disk=%2Fdev%2Fsdb")
	require.Contains(t, buf.String(), "GPT label written")
}

func TestNodeDisks_Wipe_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "PUT /api2/json/nodes/pve1/disks/wipedisk", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "wipe", "--disk", "/dev/sdb", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "PUT", rec.method)
	require.Contains(t, rec.body, "disk=%2Fdev%2Fsdb")
	require.Contains(t, buf.String(), "wiped")
}

func TestNodeDisks_Wipe_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("PUT /api2/json/nodes/pve1/disks/wipedisk", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "wipe", "--disk", "/dev/sdb"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

// ---- node scoping + command tree -------------------------------------------

func TestNodeDisks_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "disks", "list"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeDisks_CommandTree(t *testing.T) {
	root, cleanup := cli.NewRootCmd()
	defer cleanup()
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
	disks := find(nodeCmd, "disks")
	require.NotNil(t, disks, "node disks command must be registered")

	for _, verb := range []string{"list", "smart", "create", "init-gpt", "wipe"} {
		require.NotNil(t, find(disks, verb), "disks must expose %q", verb)
	}
	create := find(disks, "create")
	for _, verb := range []string{"lvm", "lvmthin", "zfs", "directory"} {
		require.NotNil(t, find(create, verb), "disks create must expose %q", verb)
	}
}
