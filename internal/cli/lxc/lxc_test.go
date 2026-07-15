package lxc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// newTestCmd builds the lxc group command, injects deps via context, captures
// stdout/stderr in buf, and sets args.
func newTestCmd(t *testing.T, deps *cli.Deps, buf *bytes.Buffer, args ...string) func() error {
	t.Helper()
	cmd := Group(&cli.Deps{})
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	return cmd.Execute
}

// newDeps returns a Deps wired to the fake server with a real renderer.
func newDeps(t *testing.T, f *testhelper.FakePVE, format output.Format, node string, async bool) *cli.Deps {
	t.Helper()
	ac, err := apiclient.NewAPIClient(fakeOptions(t, f))
	require.NoError(t, err)
	return &cli.Deps{API: ac, Out: output.New(), Format: format, Node: node, Async: async}
}

// fakeOptions returns pve.Options that correctly target the fake server. The
// shared testhelper places the full host:port in Options.Host with Port left at
// zero, which produces an invalid "host:port:8006" base URL; this splits the
// listener address into discrete Host and Port fields so requests reach the
// server.
func fakeOptions(t *testing.T, f *testhelper.FakePVE) pve.Options {
	t.Helper()
	opts := f.Options
	host, portStr, err := net.SplitHostPort(f.Server.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	opts.Host = host
	opts.Port = port
	return opts
}

// recordBody reads a form-encoded request body and returns its values as a map.
// The PVE client submits POST/PUT bodies as application/x-www-form-urlencoded;
// each first value is decoded into a typed any (numbers and booleans coerced).
func recordBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	if len(raw) == 0 {
		return map[string]any{}
	}
	vals, err := url.ParseQuery(string(raw))
	require.NoError(t, err)
	m := make(map[string]any, len(vals))
	for k, v := range vals {
		if len(v) == 0 {
			continue
		}
		s := v[0]
		switch s {
		case "1", "true":
			m[k] = true
		case "0", "false":
			m[k] = false
		default:
			if n, err := strconv.Atoi(s); err == nil {
				m[k] = n
			} else {
				m[k] = s
			}
		}
	}
	return m
}

func TestList_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{
				"vmid": 101, "name": "web", "status": "running",
				"mem": 268435456, "maxmem": 536870912, "swap": 0,
				"disk": 1073741824, "maxdisk": 8589934592, "uptime": 3600,
			},
			map[string]any{
				"vmid": 102, "name": "db", "status": "stopped",
			},
		})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "list")
	require.NoError(t, run())

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc", gotPath)

	out := buf.String()
	require.Contains(t, out, "VMID")
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "STATUS")
	require.Contains(t, out, "101")
	require.Contains(t, out, "web")
	require.Contains(t, out, "running")
	require.Contains(t, out, "102")
	require.Contains(t, out, "db")
}

func TestList_NoNode_Errors(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "list")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "node")
}

func TestStatus_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/status/current", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"vmid": 101, "name": "web", "status": "running",
			"cpu": 0.05, "mem": 268435456, "maxmem": 536870912,
			"disk": 1073741824, "maxdisk": 8589934592, "swap": 0, "uptime": 3600,
		})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "status", "101")
	require.NoError(t, run())

	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/status/current", gotPath)
	out := buf.String()
	require.Contains(t, out, "web")
	require.Contains(t, out, "running")
}

// TestStatus_SwapIsMaxswap verifies the status table labels the Maxswap value
// as "maxswap" (not "swap"), since the LXC status endpoint exposes no current
// swap figure, and that JSON output carries the full typed response.
func TestStatus_SwapIsMaxswap(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/status/current", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"vmid": 101, "name": "web", "status": "running",
			"cpu": 0.05, "mem": 268435456, "maxmem": 536870912,
			"maxswap": 134217728, "disk": 1073741824, "maxdisk": 8589934592, "uptime": 3600,
		})
	})

	deps := newDeps(t, f, output.FormatJSON, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "status", "101")
	require.NoError(t, run())

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed),
		"status JSON must be valid; got: %s", buf.String())
	// Raw typed response is emitted losslessly, carrying the maxswap field.
	require.Contains(t, parsed, "maxswap")
	require.Contains(t, parsed, "maxdisk")
}

func TestStatus_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/status/current", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "status", "101")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "get status for container")
}

func TestConfigGet_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"hostname": "web", "cores": 2, "memory": 512, "swap": 256,
			"ostype": "debian", "rootfs": "local-lvm:vm-101-disk-0,size=8G",
			"net0": "name=eth0,bridge=vmbr0", "digest": "abc123",
		})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "get", "101")
	require.NoError(t, run())

	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/config", gotPath)
	out := buf.String()
	require.Contains(t, out, "hostname")
	require.Contains(t, out, "web")
	require.Contains(t, out, "cores")
	require.Contains(t, out, "ostype")
	require.Contains(t, out, "debian")
}

func TestConfigGet_Snapshot(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{"hostname": "snap-web", "digest": "x"})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "get", "101", "--snapshot", "pre-upgrade")
	require.NoError(t, run())
	require.Contains(t, gotQuery, "snapshot=pre-upgrade")
	require.Contains(t, buf.String(), "snap-web")
}

func TestConfigSet_SendsParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod string
	var body map[string]any
	f.HandleFunc("PUT /api2/json/nodes/pve1/lxc/101/config", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		body = recordBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "set", "101",
		"--hostname", "newname", "--memory", "1024", "--cores", "4", "--description", "d")
	require.NoError(t, run())

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "newname", body["hostname"])
	require.EqualValues(t, 1024, body["memory"])
	require.EqualValues(t, 4, body["cores"])
	require.Contains(t, buf.String(), "updated")
}

func TestConfigSet_NoFields_Errors(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "config", "set", "101")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no")
}

// lifecycleCase drives the start/stop/reboot/shutdown/suspend/resume table.
type lifecycleCase struct {
	name string
	path string
}

func TestLifecycle_BlockByDefault(t *testing.T) {
	cases := []lifecycleCase{
		{"start", "/api2/json/nodes/pve1/lxc/101/status/start"},
		{"stop", "/api2/json/nodes/pve1/lxc/101/status/stop"},
		{"reboot", "/api2/json/nodes/pve1/lxc/101/status/reboot"},
		{"shutdown", "/api2/json/nodes/pve1/lxc/101/status/shutdown"},
		{"suspend", "/api2/json/nodes/pve1/lxc/101/status/suspend"},
		{"resume", "/api2/json/nodes/pve1/lxc/101/status/resume"},
	}
	upid := "UPID:pve1:0000A1B2:000C3D4E:65000000:vzstart:101:root@pam:"

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var gotMethod, gotPath string
			f.HandleFunc("POST "+tc.path, func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				testhelper.WriteData(w, upid)
			})
			// Blocking path polls task status to completion.
			f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+upid+"/status",
				func(w http.ResponseWriter, _ *http.Request) {
					testhelper.WriteData(w, map[string]any{
						"upid": upid, "status": "stopped", "exitstatus": "OK",
						"type": "vzstart", "node": "pve1",
					})
				})

			deps := newDeps(t, f, output.FormatTable, "pve1", false)
			var buf bytes.Buffer
			run := newTestCmd(t, deps, &buf, tc.name, "101")
			require.NoError(t, run())

			require.Equal(t, http.MethodPost, gotMethod)
			require.Equal(t, tc.path, gotPath)
			// Blocking mode prints a human-readable confirmation, not the UPID.
			require.NotContains(t, buf.String(), upid)
			require.Contains(t, buf.String(), "101")
		})
	}
}

func TestLifecycle_AsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:0000A1B2:000C3D4E:65000000:vzstart:101:root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/status/start",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, upid)
		})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "start", "101")
	require.NoError(t, run())
	// Async mode prints the UPID and does NOT poll task status.
	require.Contains(t, buf.String(), upid)
}

func TestStart_PassesFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body map[string]any
	upid := "UPID:pve1:0:0:0:vzstart:101:root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/status/start",
		func(w http.ResponseWriter, r *http.Request) {
			body = recordBody(t, r)
			testhelper.WriteData(w, upid)
		})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "start", "101", "--skiplock", "--debug")
	require.NoError(t, run())
	require.Equal(t, true, body["skiplock"])
	require.Equal(t, true, body["debug"])
}

func TestStop_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/status/stop",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteError(w, http.StatusForbidden, "denied")
		})
	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "stop", "101")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "stop container")
}

func TestDelete_RequiresConfirmation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	called := false
	f.HandleFunc("DELETE /api2/json/nodes/pve1/lxc/101", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, "UPID:pve1:0:0:0:vzdestroy:101:root@pam:")
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "delete", "101") // no --yes
	require.Error(t, run())
	require.False(t, called, "DELETE must not be issued without confirmation")
}

func TestDelete_WithYes_Async(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	var gotQuery string
	upid := "UPID:pve1:0:0:0:vzdestroy:101:root@pam:"
	f.HandleFunc("DELETE /api2/json/nodes/pve1/lxc/101", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		testhelper.WriteData(w, upid)
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "delete", "101", "--yes", "--purge", "--force")
	require.NoError(t, run())

	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/101", gotPath)
	require.Contains(t, gotQuery, "purge=1")
	require.Contains(t, gotQuery, "force=1")
	require.Contains(t, buf.String(), upid)
}

func TestSnapshotList_Table(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/snapshot", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{
				"name": "pre-upgrade", "description": "before upgrade",
				"snaptime": 1700000000, "parent": "",
			},
			map[string]any{"name": "current", "description": "You are here!"},
		})
	})

	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "snapshot", "list", "101")
	require.NoError(t, run())

	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/snapshot", gotPath)
	out := buf.String()
	require.Contains(t, out, "SNAPNAME")
	require.Contains(t, out, "pre-upgrade")
	require.Contains(t, out, "current")
}

func TestSnapshotList_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/101/snapshot", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such container")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "snapshot", "list", "101")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "list snapshots for container")
}

func TestSnapshotRollback_Async(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	var body map[string]any
	upid := "UPID:pve1:0:0:0:vzrollback:101:root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/snapshot/snap1/rollback",
		func(w http.ResponseWriter, r *http.Request) {
			gotMethod, gotPath = r.Method, r.URL.Path
			body = recordBody(t, r)
			testhelper.WriteData(w, upid)
		})

	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "snapshot", "rollback", "101", "snap1", "--start")
	require.NoError(t, run())

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/snapshot/snap1/rollback", gotPath)
	require.Equal(t, true, body["start"])
}

func TestSnapshotRollback_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/snapshot/snap1/rollback",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteError(w, http.StatusInternalServerError, "boom")
		})
	deps := newDeps(t, f, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "snapshot", "rollback", "101", "snap1")
	err := run()
	require.Error(t, err)
	require.ErrorContains(t, err, "roll back container")
}

func TestGroupCommand_Registers(t *testing.T) {
	cmd := Group(&cli.Deps{})
	require.Equal(t, "lxc", cmd.Name())

	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{
		"list", "status", "config", "start", "stop", "reboot",
		"shutdown", "suspend", "resume", "delete", "snapshot",
	} {
		require.True(t, names[want], "missing sub-command %q", want)
	}

	var snap *cobra.Command
	for _, c := range cmd.Commands() {
		if c.Name() == "snapshot" {
			snap = c
		}
	}
	require.NotNil(t, snap, "snapshot sub-group must be registered")
	snapNames := map[string]bool{}
	for _, c := range snap.Commands() {
		snapNames[c.Name()] = true
	}
	for _, want := range []string{"list", "create", "delete", "rollback"} {
		require.True(t, snapNames[want], "missing snapshot sub-command %q", want)
	}
}
