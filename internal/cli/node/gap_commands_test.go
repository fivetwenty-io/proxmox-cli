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
// disks ls directory / lvm / lvmthin / zfs
// ---------------------------------------------------------------------------

func TestNodeDisksLs_Directory(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/disks/directory", &rec, []any{
		map[string]any{"path": "/mnt/data", "type": "dir", "unitfile": "mnt-data.mount"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "ls", "directory"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/disks/directory", rec.path)
	require.Contains(t, buf.String(), "/mnt/data")
}

func TestNodeDisksLs_Directory_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/disks/directory", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "ls", "directory"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "list directory disks on node")
}

func TestNodeDisksLs_Lvm(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	// ListDisksLvm returns an object with a children array.
	recordOn(f, "GET /api2/json/nodes/pve1/disks/lvm", &rec, map[string]any{
		"leaf":     1,
		"children": []any{map[string]any{"name": "pve", "size": 500000}},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "ls", "lvm"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/disks/lvm", rec.path)
	require.Contains(t, buf.String(), "pve")
}

func TestNodeDisksLs_Lvm_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/disks/lvm", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "ls", "lvm"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "list LVM disks on node")
}

func TestNodeDisksLs_Lvmthin(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/disks/lvmthin", &rec, []any{
		map[string]any{"lv": "data", "vg": "pve", "lv_size": 10000},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "ls", "lvmthin"))

	require.NoError(t, root.Execute())
	require.Equal(t, "/api2/json/nodes/pve1/disks/lvmthin", rec.path)
	require.Contains(t, buf.String(), "data")
}

func TestNodeDisksLs_Zfs(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/disks/zfs", &rec, []any{
		map[string]any{"name": "tank", "size": 1000000, "state": "ONLINE"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "ls", "zfs"))

	require.NoError(t, root.Execute())
	require.Equal(t, "/api2/json/nodes/pve1/disks/zfs", rec.path)
	require.Contains(t, buf.String(), "tank")
}

// TestNodeGapCommands_RequiresNode verifies every gap-command sub-command fails
// clearly when no node is resolvable from context or flags.
func TestNodeGapCommands_RequiresNode(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{
			name: "disks ls zfs",
			args: []string{"node", "disks", "ls", "zfs"},
		},
		{
			name: "disks get zfs",
			args: []string{"node", "disks", "get", "zfs", "tank"},
		},
		{
			name: "disks delete zfs",
			args: []string{"node", "disks", "delete", "zfs", "tank", "--yes"},
		},
		{
			name: "rrddata",
			args: []string{"node", "rrddata", "--timeframe", "hour"},
		},
		{
			name: "netstat",
			args: []string{"node", "netstat"},
		},
		{
			name: "vzdump defaults",
			args: []string{"node", "vzdump", "defaults"},
		},
		{
			name: "vzdump extract-config",
			args: []string{"node", "vzdump", "extract-config", "--volume", "local:backup/foo.vma"},
		},
		{
			name: "capabilities qemu cpu-flags",
			args: []string{"node", "capabilities", "qemu", "cpu-flags"},
		},
		{
			name: "hardware mdev",
			args: []string{"node", "hardware", "mdev", "0000:03:00.0"},
		},
		{
			name: "query-url-metadata",
			args: []string{"node", "query-url-metadata", "--url", "https://example.com/iso"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
			root.SetArgs(append(prefix, tc.args...))
			err := root.Execute()
			require.Error(t, err)
			require.Contains(t, err.Error(), "no node specified")
		})
	}
}

// ---------------------------------------------------------------------------
// disks get zfs
// ---------------------------------------------------------------------------

func TestNodeDisksGet_Zfs(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/disks/zfs/tank", &rec, map[string]any{
		"name": "tank", "state": "ONLINE", "scan": "scrub repaired",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "get", "zfs", "tank"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/disks/zfs/tank", rec.path)
	require.Contains(t, buf.String(), "ONLINE")
}

func TestNodeDisksGet_Zfs_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/disks/zfs/tank", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "not found")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "get", "zfs", "tank"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get ZFS pool")
}

// ---------------------------------------------------------------------------
// disks delete directory / lvm / lvmthin / zfs — --yes gate
// ---------------------------------------------------------------------------

func TestNodeDisksDelete_Directory_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/nodes/pve1/disks/directory/backups", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "delete", "directory", "backups"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeDisksDelete_Directory_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "DELETE /api2/json/nodes/pve1/disks/directory/backups", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "delete", "directory", "backups", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), "deleted")
}

func TestNodeDisksDelete_Directory_CleanupFlagsOmittedByDefault(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "DELETE /api2/json/nodes/pve1/disks/directory/backups", &rec, nil)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "delete", "directory", "backups", "--yes"))

	require.NoError(t, root.Execute())
	require.NotContains(t, rec.query, "cleanup_config")
	require.NotContains(t, rec.query, "cleanup_disks")
}

func TestNodeDisksDelete_Lvm_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/nodes/pve1/disks/lvm/vg-data", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "delete", "lvm", "vg-data"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeDisksDelete_Lvm_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "DELETE /api2/json/nodes/pve1/disks/lvm/vg-data", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "delete", "lvm", "vg-data", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), "deleted")
}

func TestNodeDisksDelete_Lvmthin_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/nodes/pve1/disks/lvmthin/data", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "delete", "lvmthin", "data",
		"--volume-group", "pve"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeDisksDelete_Lvmthin_RequiresVolumeGroup(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "delete", "lvmthin", "data", "--yes"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "volume-group")
}

func TestNodeDisksDelete_Lvmthin_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "DELETE /api2/json/nodes/pve1/disks/lvmthin/data", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "delete", "lvmthin", "data",
		"--volume-group", "pve", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), "deleted")
}

func TestNodeDisksDelete_Zfs_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/nodes/pve1/disks/zfs/tank", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "delete", "zfs", "tank"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestNodeDisksDelete_Zfs_WithYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "DELETE /api2/json/nodes/pve1/disks/zfs/tank", &rec, nil)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "disks", "delete", "zfs", "tank", "--yes"))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), "deleted")
}

// TestNodeDisks_ExtendedCommandTree verifies the new ls/get/delete sub-trees.
func TestNodeDisks_ExtendedCommandTree(t *testing.T) {
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
	disks := find(nodeCmd, "disks")
	require.NotNil(t, disks)

	// ls sub-tree
	ls := find(disks, "ls")
	require.NotNil(t, ls, "disks must expose ls")
	for _, sub := range []string{"directory", "lvm", "lvmthin", "zfs"} {
		require.NotNil(t, find(ls, sub), "disks ls must expose %q", sub)
	}

	// get sub-tree
	get := find(disks, "get")
	require.NotNil(t, get, "disks must expose get")
	require.NotNil(t, find(get, "zfs"), "disks get must expose zfs")

	// delete sub-tree
	del := find(disks, "delete")
	require.NotNil(t, del, "disks must expose delete")
	for _, sub := range []string{"directory", "lvm", "lvmthin", "zfs"} {
		require.NotNil(t, find(del, sub), "disks delete must expose %q", sub)
	}
}

// ---------------------------------------------------------------------------
// rrddata
// ---------------------------------------------------------------------------

func TestNodeRrddata_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/rrddata", &rec, []any{
		map[string]any{"time": 1700000000, "cpu": 0.1, "mem": 1073741824},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "rrddata", "--timeframe", "hour"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/rrddata", rec.path)
	require.Contains(t, rec.query, "timeframe=hour")
	require.Contains(t, buf.String(), "CPU")
}

func TestNodeRrddata_WithCf(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/rrddata", &rec, []any{})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "rrddata", "--timeframe", "day", "--cf", "MAX"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "cf=MAX")
}

func TestNodeRrddata_CfOmittedWhenNotSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/rrddata", &rec, []any{})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "rrddata", "--timeframe", "hour"))

	require.NoError(t, root.Execute())
	require.NotContains(t, rec.query, "cf=")
}

func TestNodeRrddata_RequiresTimeframe(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "rrddata"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeframe")
}

func TestNodeRrddata_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/rrddata", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "rrddata", "--timeframe", "hour"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get rrddata on node")
}

// ---------------------------------------------------------------------------
// netstat
// ---------------------------------------------------------------------------

func TestNodeNetstat_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/netstat", &rec, []any{
		map[string]any{"dev": "eth0", "in": 1024, "out": 2048},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "netstat"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/netstat", rec.path)
	require.Contains(t, buf.String(), "eth0")
}

func TestNodeNetstat_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/netstat", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "netstat"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get netstat on node")
}

// ---------------------------------------------------------------------------
// vzdump defaults
// ---------------------------------------------------------------------------

func TestNodeVzdumpDefaults_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/vzdump/defaults", &rec, map[string]any{
		"compress": "lzo", "mode": "snapshot",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "vzdump", "defaults"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/vzdump/defaults", rec.path)
	require.Contains(t, buf.String(), "compress")
}

func TestNodeVzdumpDefaults_WithStorage(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/vzdump/defaults", &rec, map[string]any{})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "vzdump", "defaults", "--storage", "local"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "storage=local")
}

func TestNodeVzdumpDefaults_StorageOmittedWhenNotSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/vzdump/defaults", &rec, map[string]any{})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "vzdump", "defaults"))

	require.NoError(t, root.Execute())
	require.NotContains(t, rec.query, "storage=")
}

func TestNodeVzdumpDefaults_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/vzdump/defaults", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "vzdump", "defaults"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get vzdump defaults on node")
}

// ---------------------------------------------------------------------------
// vzdump extract-config
// ---------------------------------------------------------------------------

func TestNodeVzdumpExtractConfig_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/vzdump/extractconfig", &rec, "cores: 2\nmemory: 2048\n")

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "vzdump", "extract-config",
		"--volume", "local:backup/vzdump-qemu-100.vma.lzo"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/vzdump/extractconfig", rec.path)
	require.Contains(t, rec.query, "volume=")
	_ = buf.String() // output rendered
}

func TestNodeVzdumpExtractConfig_RequiresVolume(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "vzdump", "extract-config"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "volume")
}

func TestNodeVzdumpExtractConfig_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/vzdump/extractconfig", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such volume")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "vzdump", "extract-config",
		"--volume", "local:backup/no.vma"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "extract config from volume")
}

// ---------------------------------------------------------------------------
// capabilities qemu cpu-flags
// ---------------------------------------------------------------------------

func TestNodeCapabilities_QemuCpuFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/capabilities/qemu/cpu-flags", &rec, []any{
		map[string]any{"id": "pcid", "description": "PCID processor context IDs"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "capabilities", "qemu", "cpu-flags"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/capabilities/qemu/cpu-flags", rec.path)
	require.Contains(t, buf.String(), "pcid")
}

func TestNodeCapabilities_QemuCpuFlags_WithAccel(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/capabilities/qemu/cpu-flags", &rec, []any{})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "capabilities", "qemu", "cpu-flags",
		"--accel", "kvm"))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "accel=kvm")
}

func TestNodeCapabilities_QemuCpuFlags_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/capabilities/qemu/cpu-flags", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "capabilities", "qemu", "cpu-flags"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "list QEMU CPU flags on node")
}

func TestNodeCapabilities_CpuFlagsInCommandTree(t *testing.T) {
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
	caps := find(nodeCmd, "capabilities")
	qemu := find(caps, "qemu")
	require.NotNil(t, find(qemu, "cpu-flags"), "capabilities qemu must expose cpu-flags")
}

// ---------------------------------------------------------------------------
// hardware pci mdev
// ---------------------------------------------------------------------------

func TestNodeHardware_PciMdev(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/hardware/pci/0000:03:00.0/mdev", &rec, []any{
		map[string]any{"type": "nvidia-35", "available": 4},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "hardware", "mdev", "0000:03:00.0"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/hardware/pci/0000:03:00.0/mdev", rec.path)
	require.Contains(t, buf.String(), "nvidia-35")
}

func TestNodeHardware_PciMdev_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/hardware/pci/0000:03:00.0/mdev", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "not found")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "hardware", "mdev", "0000:03:00.0"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "list mdev types")
}

func TestNodeHardware_MdevInCommandTree(t *testing.T) {
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
	hw := find(nodeCmd, "hardware")
	require.NotNil(t, find(hw, "mdev"), "hardware must expose mdev")
}

// ---------------------------------------------------------------------------
// query-url-metadata
// ---------------------------------------------------------------------------

func TestNodeQueryUrlMetadata_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/query-url-metadata", &rec, map[string]any{
		"filename": "debian-12.iso", "mimetype": "application/octet-stream", "size": 1234567890,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "query-url-metadata",
		"--url", "https://example.com/debian-12.iso"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/query-url-metadata", rec.path)
	require.Contains(t, rec.query, "url=")
	require.Contains(t, buf.String(), "debian-12.iso")
}

func TestNodeQueryUrlMetadata_RequiresUrl(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "query-url-metadata"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "url")
}

func TestNodeQueryUrlMetadata_VerifyCertOmittedByDefault(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/query-url-metadata", &rec, map[string]any{})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "query-url-metadata",
		"--url", "https://example.com/iso"))

	require.NoError(t, root.Execute())
	require.NotContains(t, rec.query, "verify_certificates=")
}

func TestNodeQueryUrlMetadata_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/query-url-metadata", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadGateway, "unreachable")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "query-url-metadata",
		"--url", "https://internal.example.com/iso"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "query URL metadata on node")
}

// ---------------------------------------------------------------------------
// services state
// ---------------------------------------------------------------------------

func TestNodeServicesState_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/services/pveproxy/state", &rec, map[string]any{
		"service": "pveproxy", "state": "running", "active-state": "active",
		"desc": "PVE API Proxy", "name": "pveproxy", "unit-state": "enabled",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "services", "state", "pve1", "pveproxy"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/services/pveproxy/state", rec.path)
	require.Contains(t, buf.String(), "running")
}

func TestNodeServicesState_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/services/nosvc/state", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "not found")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "services", "state", "pve1", "nosvc"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get state of service")
}

func TestNodeServicesState_CommandTree(t *testing.T) {
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
	svcs := find(nodeCmd, "services")
	require.NotNil(t, find(svcs, "state"), "services must expose state")
}

// ---------------------------------------------------------------------------
// vzdump command-tree (verify defaults + extract-config registered)
// ---------------------------------------------------------------------------

func TestNodeVzdump_ExtendedCommandTree(t *testing.T) {
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
	vzdump := find(nodeCmd, "vzdump")
	require.NotNil(t, vzdump)
	require.NotNil(t, find(vzdump, "defaults"), "vzdump must expose defaults")
	require.NotNil(t, find(vzdump, "extract-config"), "vzdump must expose extract-config")
}

// ---------------------------------------------------------------------------
// node-level command-tree (verify netstat + rrddata + query-url-metadata registered)
// ---------------------------------------------------------------------------

func TestNode_GapCommandsInCommandTree(t *testing.T) {
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
	require.NotNil(t, find(nodeCmd, "netstat"), "node must expose netstat")
	require.NotNil(t, find(nodeCmd, "rrddata"), "node must expose rrddata")
	require.NotNil(t, find(nodeCmd, "query-url-metadata"), "node must expose query-url-metadata")
}
