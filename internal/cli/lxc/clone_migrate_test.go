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

func TestClone_BlockByDefault(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:0:0:0:vzclone:101:root@pam:"
	var gotMethod, gotPath string
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/clone", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = recordBody(t, r)
		testhelper.WriteData(w, upid)
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+upid+"/status",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, map[string]any{
				"upid": upid, "status": "stopped", "exitstatus": "OK",
				"type": "vzclone", "node": "pve1",
			})
		})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "clone", "101", "--newid", "999")
	require.NoError(t, run())

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/clone", gotPath)
	require.EqualValues(t, 999, body["newid"])
	// Blocking mode prints a confirmation, not the raw UPID.
	require.NotContains(t, buf.String(), upid)
	require.Contains(t, buf.String(), "999")
}

func TestClone_AsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:0:0:0:vzclone:101:root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/clone",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, upid)
		})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "clone", "101", "--newid", "999", "--async")
	require.NoError(t, run())
	require.Contains(t, buf.String(), upid)
}

func TestClone_RequiresNewid(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/clone", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, "UPID:pve1:0:0:0:vzclone:101:root@pam:")
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "clone", "101")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "newid")
	require.False(t, called, "clone must not be issued without --newid")
}

func TestClone_FlagParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:0:0:0:vzclone:101:root@pam:"
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/clone", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, upid)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "clone", "101", "--newid", "999",
		"--hostname", "ct-clone", "--full", "--pool", "pve-cli",
		"--storage", "local-lvm", "--description", "d", "--snapname", "snap1",
		"--target-node", "pve2", "--bwlimit", "10240")
	require.NoError(t, run())

	require.EqualValues(t, 999, body["newid"])
	require.Equal(t, "ct-clone", body["hostname"])
	require.Equal(t, true, body["full"])
	require.Equal(t, "pve-cli", body["pool"])
	require.Equal(t, "local-lvm", body["storage"])
	require.Equal(t, "d", body["description"])
	require.Equal(t, "snap1", body["snapname"])
	require.Equal(t, "pve2", body["target"])
	require.EqualValues(t, 10240, body["bwlimit"])
}

func TestClone_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/clone",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteError(w, http.StatusInternalServerError, "boom")
		})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "clone", "101", "--newid", "999")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "clone container")
}

func TestMigrate_BlockByDefault(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:0:0:0:vzmigrate:101:root@pam:"
	var gotMethod, gotPath string
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/migrate", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = recordBody(t, r)
		testhelper.WriteData(w, upid)
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+upid+"/status",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, map[string]any{
				"upid": upid, "status": "stopped", "exitstatus": "OK",
				"type": "vzmigrate", "node": "pve1",
			})
		})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "migrate", "101", "--target-node", "pve2")
	require.NoError(t, run())

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/migrate", gotPath)
	require.Equal(t, "pve2", body["target"])
	require.NotContains(t, buf.String(), upid)
	require.Contains(t, buf.String(), "pve2")
}

func TestMigrate_RequiresTarget(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/migrate", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, "UPID:pve1:0:0:0:vzmigrate:101:root@pam:")
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "migrate", "101")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "target-node")
	require.False(t, called, "migrate must not be issued without --target-node")
}

func TestMigrate_FlagParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:0:0:0:vzmigrate:101:root@pam:"
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/migrate", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, upid)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "migrate", "101", "--target-node", "pve2",
		"--restart", "--online", "--targetstorage", "local-lvm", "--timeout", "120", "--bwlimit", "5120")
	require.NoError(t, run())

	require.Equal(t, "pve2", body["target"])
	require.Equal(t, true, body["restart"])
	require.Equal(t, true, body["online"])
	require.Equal(t, "local-lvm", body["target-storage"])
	require.EqualValues(t, 120, body["timeout"])
	require.EqualValues(t, 5120, body["bwlimit"])
}

func TestMigrate_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/migrate",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteError(w, http.StatusForbidden, "denied")
		})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "migrate", "101", "--target-node", "pve2")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "migrate container")
}

// TestCloneMigrate_NoLocalTargetFlag guards against re-introducing a local
// --target flag on these subcommands: the root command owns a persistent
// -t/--target (config target selector), so a local --target would shadow it.
// Destination-node selection must use --target-node.
func TestCloneMigrate_NoLocalTargetFlag(t *testing.T) {
	for _, name := range []string{"clone", "migrate"} {
		var sub *cobra.Command
		for _, c := range newGroupCmd(&cli.Deps{}).Commands() {
			if c.Name() == name {
				sub = c
			}
		}
		require.NotNil(t, sub, "%s subcommand must be registered", name)
		require.Nil(t, sub.Flags().Lookup("target"),
			"%s must not define a local --target (it shadows the global -t/--target); use --target-node", name)
		require.Nil(t, sub.Flags().Lookup("node"),
			"%s must not define a local --node (it shadows the global --node)", name)
		require.NotNil(t, sub.Flags().Lookup("target-node"),
			"%s must expose --target-node for destination selection", name)
	}
}
