package lxc

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// --- disk resize ------------------------------------------------------------

func TestDiskResize_Sync(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/resize", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = recordBody(t, r)
		testhelper.WriteData(w, nil) // synchronous storages return null, not a UPID
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "disk", "resize", "101", "--disk", "rootfs", "--size", "+5G")
	require.NoError(t, run())

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/resize", gotPath)
	require.Equal(t, "rootfs", body["disk"])
	require.Equal(t, "+5G", body["size"])
	require.Contains(t, buf.String(), "resized to +5G")
}

func TestDiskResize_WorkerUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:0:0:0:resize:101:root@pam:"
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/resize",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, upid) // worker storages return a task UPID
		})
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+upid+"/status",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, map[string]any{
				"upid": upid, "status": "stopped", "exitstatus": "OK",
				"type": "resize", "node": "pve1",
			})
		})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "disk", "resize", "101", "--disk", "rootfs", "--size", "16G")
	require.NoError(t, run())
	require.Contains(t, buf.String(), "resized")
}

func TestDiskResize_RequiresDisk(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "disk", "resize", "101", "--size", "+5G")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "--disk is required")
}

func TestDiskResize_RequiresSize(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "disk", "resize", "101", "--disk", "rootfs")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "--size is required")
}

func TestDiskResize_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/resize",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteError(w, http.StatusInternalServerError, "boom")
		})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "disk", "resize", "101", "--disk", "rootfs", "--size", "+5G")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "resize volume")
}

// --- disk move --------------------------------------------------------------

func TestDiskMove_Blocking(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:0:0:0:move_volume:101:root@pam:"
	var gotMethod, gotPath string
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/move_volume", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = recordBody(t, r)
		testhelper.WriteData(w, upid)
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+upid+"/status",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, map[string]any{
				"upid": upid, "status": "stopped", "exitstatus": "OK",
				"type": "move_volume", "node": "pve1",
			})
		})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "disk", "move", "101", "--volume", "rootfs", "--storage", "local-lvm")
	require.NoError(t, run())

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/move_volume", gotPath)
	require.Equal(t, "rootfs", body["volume"])
	require.Equal(t, "local-lvm", body["storage"])
	require.NotContains(t, buf.String(), upid)
	require.Contains(t, buf.String(), "moved")
}

func TestDiskMove_RequiresVolume(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/move_volume", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, "UPID:pve1:0:0:0:move_volume:101:root@pam:")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "disk", "move", "101", "--storage", "local-lvm")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "--volume is required")
	require.False(t, called, "move must not be issued without --volume")
}

func TestDiskMove_RequiresStorageOrTarget(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/move_volume", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, "UPID:pve1:0:0:0:move_volume:101:root@pam:")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "disk", "move", "101", "--volume", "rootfs")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "--storage or --target-vmid is required")
	require.False(t, called, "move must not be issued without a target")
}

func TestDiskMove_FlagParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:0:0:0:move_volume:101:root@pam:"
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/move_volume", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, upid)
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+upid+"/status",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, map[string]any{
				"upid": upid, "status": "stopped", "exitstatus": "OK",
				"type": "move_volume", "node": "pve1",
			})
		})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "disk", "move", "101", "--volume", "mp0",
		"--target-vmid", "202", "--target-volume", "mp1", "--bwlimit", "5120", "--delete")
	require.NoError(t, run())

	require.Equal(t, "mp0", body["volume"])
	require.EqualValues(t, 202, body["target-vmid"])
	require.Equal(t, "mp1", body["target-volume"])
	require.EqualValues(t, 5120, body["bwlimit"])
	require.Equal(t, true, body["delete"])
}

func TestDiskMove_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/move_volume",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteError(w, http.StatusForbidden, "denied")
		})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "disk", "move", "101", "--volume", "rootfs", "--storage", "local-lvm")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "move volume")
}

// TestDisk_NoLocalTargetFlag guards against re-introducing a local --target flag
// on the disk subcommands: the root command owns a persistent -t/--target (config
// target selector), so a local --target would shadow it. Container/storage
// selection uses --target-vmid, --target-volume, and --storage.
func TestDisk_NoLocalTargetFlag(t *testing.T) {
	var disk *cobra.Command
	for _, c := range newGroupCmd(&cli.Deps{}).Commands() {
		if c.Name() == "disk" {
			disk = c
		}
	}
	require.NotNil(t, disk, "disk subcommand must be registered")
	for _, sub := range disk.Commands() {
		require.Nil(t, sub.Flags().Lookup("target"),
			"disk %s must not define a local --target (it shadows the global -t/--target)", sub.Name())
	}
}
