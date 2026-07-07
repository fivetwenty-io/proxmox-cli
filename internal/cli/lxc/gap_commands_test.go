package lxc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// ---------------------------------------------------------------------------
// metrics (ListLxcRrddata)
// ---------------------------------------------------------------------------

func TestMetrics_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath, gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/rrddata", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []any{
			map[string]any{
				"time": 1700000000, "cpu": 0.05, "mem": 268435456,
				"maxmem": 536870912, "disk": 1073741824, "maxdisk": 8589934592,
				"netin": 1024, "netout": 512,
			},
		})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "metrics", "101", "--timeframe", "hour")
	require.NoError(t, run())

	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/rrddata", gotPath)
	require.Contains(t, gotQuery, "timeframe=hour")
	out := buf.String()
	require.Contains(t, out, "CPU")
	require.Contains(t, out, "MEM")
}

func TestMetrics_WithCF(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/rrddata", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []any{})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "metrics", "101", "--timeframe", "day", "--cf", "MAX")
	require.NoError(t, run())
	require.Contains(t, gotQuery, "cf=MAX")
}

func TestMetrics_RequiresTimeframe(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "metrics", "101")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--timeframe")
}

func TestMetrics_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/rrddata", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "rrd error")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "metrics", "101", "--timeframe", "hour")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get metrics for container")
}

// ---------------------------------------------------------------------------
// config pending (ListLxcPending)
// ---------------------------------------------------------------------------

func TestConfigPending_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/pending", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{"key": "memory", "value": 512, "pending": 1024},
			map[string]any{"key": "hostname", "value": "old", "pending": nil},
		})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "pending", "101")
	require.NoError(t, run())

	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/pending", gotPath)
	out := buf.String()
	require.Contains(t, out, "KEY")
	require.Contains(t, out, "memory")
	require.Contains(t, out, "hostname")
}

func TestConfigPending_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/pending", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "not found")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "pending", "101")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "pending config for container")
}

// ---------------------------------------------------------------------------
// feature (ListLxcFeature)
// ---------------------------------------------------------------------------

func TestFeature_HasFeatureTrue(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/feature", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{"hasFeature": 1})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "feature", "101", "--feature", "clone")
	require.NoError(t, run())

	require.Contains(t, gotQuery, "feature=clone")
	out := buf.String()
	require.Contains(t, out, "true")
}

func TestFeature_HasFeatureFalse(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/feature", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"hasFeature": 0})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "feature", "101", "--feature", "snapshot")
	require.NoError(t, run())
	require.Contains(t, buf.String(), "false")
}

func TestFeature_WithSnapname(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/feature", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{"hasFeature": 1})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "feature", "101", "--feature", "clone", "--snapname", "snap1")
	require.NoError(t, run())
	require.Contains(t, gotQuery, "snapname=snap1")
}

func TestFeature_RequiresFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "feature", "101")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--feature")
}

func TestFeature_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/feature", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "forbidden")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "feature", "101", "--feature", "clone")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "check feature")
}

// ---------------------------------------------------------------------------
// snapshot show (ListLxcSnapshotConfig)
// ---------------------------------------------------------------------------

func TestSnapshotShow_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/snapshot/snap1/config", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"hostname": "web", "memory": 512, "description": "before upgrade",
		})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "snapshot", "show", "101", "snap1")
	require.NoError(t, run())

	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/snapshot/snap1/config", gotPath)
	out := buf.String()
	require.Contains(t, out, "hostname")
	require.Contains(t, out, "web")
}

func TestSnapshotShow_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/snapshot/nope/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such snapshot")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "snapshot", "show", "101", "nope")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get config for snapshot")
}

// ---------------------------------------------------------------------------
// snapshot update (UpdateLxcSnapshotConfig)
// ---------------------------------------------------------------------------

func TestSnapshotUpdate_SendsDescription(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod string
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/snapshot/snap1/config", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "snapshot", "update", "101", "snap1", "--description", "new desc")
	require.NoError(t, run())

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "new desc", body["description"])
	require.Contains(t, buf.String(), "updated")
}

func TestSnapshotUpdate_RequiresFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "snapshot", "update", "101", "snap1")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no fields")
}

func TestSnapshotUpdate_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/snapshot/snap1/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "snapshot", "update", "101", "snap1", "--description", "x")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "update snapshot")
}

// ---------------------------------------------------------------------------
// migrate check (ListLxcMigrate GET)
// ---------------------------------------------------------------------------

func TestMigrateCheck_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/migrate", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"running":       false,
			"allowed-nodes": []string{"pve2", "pve3"},
		})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "migrate", "check", "101")
	require.NoError(t, run())

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/migrate", gotPath)
	out := buf.String()
	require.Contains(t, out, "running")
}

func TestMigrateCheck_WithTargetNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/migrate", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{"running": false})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "migrate", "check", "101", "--target-node", "pve2")
	require.NoError(t, run())
	require.Contains(t, gotQuery, "target=pve2")
}

func TestMigrateCheck_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/migrate", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "forbidden")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "migrate", "check", "101")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "check migration feasibility")
}

// Ensure existing migrate (POST) still works as a leaf alongside the check subcommand.
func TestMigratePost_StillWorks(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:0:0:0:vzmigrate:101:root@pam:"
	var gotMethod string
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/migrate", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
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
}

// ---------------------------------------------------------------------------
// rrd (ListLxcRrd)
// ---------------------------------------------------------------------------

func TestRrd_ReturnsFilename(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath, gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/rrd", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{"filename": "/var/lib/rrdcached/db/pve2/101/cpu.png"})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "rrd", "101", "--ds", "cpu", "--timeframe", "hour")
	require.NoError(t, run())

	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/rrd", gotPath)
	require.Contains(t, gotQuery, "ds=cpu")
	require.Contains(t, gotQuery, "timeframe=hour")
	require.Contains(t, buf.String(), "filename")
}

func TestRrd_RequiresFlag(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing --ds",
			args:    []string{"rrd", "101", "--timeframe", "hour"},
			wantErr: "--ds",
		},
		{
			name:    "missing --timeframe",
			args:    []string{"rrd", "101", "--ds", "cpu"},
			wantErr: "--timeframe",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			deps := newDeps(t, f, output.FormatTable, "pve1", false)
			var buf bytes.Buffer
			run := newTestCmd(t, deps, &buf, tc.args...)
			err := run()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestRrd_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/rrd", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "rrd unavailable")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "rrd", "101", "--ds", "cpu", "--timeframe", "hour")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get RRD graph for container")
}

// ---------------------------------------------------------------------------
// remote-migrate (CreateLxcRemoteMigrate) — --yes gate
// ---------------------------------------------------------------------------

func TestRemoteMigrate_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/remote_migrate", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, "UPID:pve1:0:0:0:vzrelocate:101:root@pam:")
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "remote-migrate", "101",
		"--target-endpoint", "https://remote:8006",
		"--target-storage", "local",
		"--target-bridge", "vmbr0",
	) // no --yes
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "POST must not be issued without --yes")
}

func TestRemoteMigrate_RequiresFlag(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name: "missing --target-endpoint",
			args: []string{
				"remote-migrate", "101",
				"--yes",
				"--target-storage", "local",
				"--target-bridge", "vmbr0",
			},
			wantErr: "--target-endpoint",
		},
		{
			name: "missing --target-storage",
			args: []string{
				"remote-migrate", "101",
				"--yes",
				"--target-endpoint", "https://remote:8006",
				"--target-bridge", "vmbr0",
			},
			wantErr: "--target-storage",
		},
		{
			name: "missing --target-bridge",
			args: []string{
				"remote-migrate", "101",
				"--yes",
				"--target-endpoint", "https://remote:8006",
				"--target-storage", "local",
			},
			wantErr: "--target-bridge",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			deps := newDeps(t, f, output.FormatTable, "pve1", true)
			var buf bytes.Buffer
			run := newTestCmd(t, deps, &buf, tc.args...)
			err := run()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestRemoteMigrate_WithYes_SendsBody(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod string
	var body map[string]any
	upid := "UPID:pve1:0:0:0:vzrelocate:101:root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/remote_migrate", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		body = recordBody(t, r)
		// Return a quoted UPID string so emitTask can parse it.
		raw, _ := json.Marshal(upid)
		testhelper.WriteData(w, json.RawMessage(raw))
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "remote-migrate", "101",
		"--yes",
		"--target-endpoint", "https://remote:8006",
		"--target-storage", "local",
		"--target-bridge", "vmbr0",
	)
	require.NoError(t, run())

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "https://remote:8006", body["target-endpoint"])
	require.Equal(t, "local", body["target-storage"])
	require.Equal(t, "vmbr0", body["target-bridge"])
}

// ---------------------------------------------------------------------------
// CommandTree registration
// ---------------------------------------------------------------------------

func TestGroupCommand_RegistersNewCommands(t *testing.T) {
	cmd := Group(&cli.Deps{})

	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}

	for _, want := range []string{
		"metrics", "feature", "rrd", "remote-migrate",
	} {
		require.True(t, names[want], "missing sub-command %q", want)
	}

	// Verify snapshot sub-group now also has show and update.
	var snap *cobra.Command
	for _, c := range cmd.Commands() {
		if c.Name() == "snapshot" {
			snap = c
		}
	}
	require.NotNil(t, snap)
	snapNames := map[string]bool{}
	for _, c := range snap.Commands() {
		snapNames[c.Name()] = true
	}
	for _, want := range []string{"show", "update"} {
		require.True(t, snapNames[want], "missing snapshot sub-command %q", want)
	}

	// Verify config sub-group has pending.
	var cfg *cobra.Command
	for _, c := range cmd.Commands() {
		if c.Name() == "config" {
			cfg = c
		}
	}
	require.NotNil(t, cfg)
	cfgNames := map[string]bool{}
	for _, c := range cfg.Commands() {
		cfgNames[c.Name()] = true
	}
	require.True(t, cfgNames["pending"], "missing config sub-command pending")

	// Verify migrate sub-group has check.
	var mig *cobra.Command
	for _, c := range cmd.Commands() {
		if c.Name() == "migrate" {
			mig = c
		}
	}
	require.NotNil(t, mig)
	migNames := map[string]bool{}
	for _, c := range mig.Commands() {
		migNames[c.Name()] = true
	}
	require.True(t, migNames["check"], "missing migrate sub-command check")

	// Verify to-template is registered at the top level.
	require.True(t, names["to-template"], "missing sub-command to-template")
}

// ---------------------------------------------------------------------------
// to-template (CreateLxcTemplate)
// ---------------------------------------------------------------------------

func TestToTemplate_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/template", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "to-template", "101")
	require.NoError(t, run())

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/template", gotPath)
	require.Contains(t, buf.String(), "101")
	require.Contains(t, buf.String(), "template")
}

func TestToTemplate_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/template", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "container is running")
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "to-template", "101")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "convert container")
	require.ErrorContains(t, err, "101")
}
