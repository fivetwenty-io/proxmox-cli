package node_test

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	_ "github.com/fivetwenty-io/proxmox-cli/internal/cli/node"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// recordedRequest captures the method, path, and body of a fake-server request.
type recordedRequest struct {
	method string
	path   string
	query  string
	body   string
}

// writeFakeConfig writes a config file with a single token-auth target that
// points at the fake server, and returns its path. The resulting target builds
// the same pve.Options as testhelper.FakePVE: HTTP transport plus the dummy
// "root@pam!test=<uuid>" API token.
func writeFakeConfig(t *testing.T, f *testhelper.FakePVE) string {
	t.Helper()
	host, portStr, err := net.SplitHostPort(f.Server.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	cfg := &config.Config{
		CurrentContext: "fake",
		Contexts: map[string]*config.Context{
			"fake": {
				Host:     host,
				Port:     port,
				Protocol: "http",
				Realm:    "",
				Auth: config.AuthBlock{
					Type:     "token",
					Username: "root@pam",
					TokenID:  "test",
					Secret:   "00000000-0000-0000-0000-000000000000",
				},
				TLS: config.TLSBlock{Insecure: true},
			},
		},
	}
	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, config.SaveForce(path, cfg))
	return path
}

// newNodeRoot builds the real root command wired to the fake server via a
// generated config file, with the given runner installed through a
// PersistentPreRunE wrapper that runs after the standard one. The returned
// args slice is the prefix every invocation needs (config path + format).
func newNodeRoot(t *testing.T, f *testhelper.FakePVE, format output.Format, runner exec.Runner) (
	*cobra.Command, *bytes.Buffer, []string,
) {
	t.Helper()
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_OUTPUT", "")

	cfgPath := writeFakeConfig(t, f)

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	addNodeGroup(root)

	// Wrap the standard PersistentPreRunE so the test runner replaces the real
	// exec.Runner after Deps are built.
	std := root.PersistentPreRunE
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := std(cmd, args); err != nil {
			return err
		}
		cli.GetDeps(cmd).Runner = runner
		return nil
	}

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)

	prefix := []string{"--config", cfgPath, "--output", string(format)}
	return root, &buf, prefix
}

// recordOn registers a handler at pattern that records the request and replies
// with the PVE-shaped payload.
func recordOn(f *testhelper.FakePVE, pattern string, rec *recordedRequest, payload any) {
	f.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.query = r.URL.RawQuery
		rec.body = buf.String()
		testhelper.WriteData(w, payload)
	})
}

// ---------------------------------------------------------------------------
// node list
// ---------------------------------------------------------------------------

func TestNodeList_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes", &rec, []any{
		map[string]any{
			"node": "pve1", "status": "online", "cpu": 0.25, "maxcpu": 8,
			"mem": 1000, "maxmem": 2000, "uptime": 99, "ssl_fingerprint": "AA:BB",
		},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "list"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes", rec.path)
	out := buf.String()
	require.Contains(t, out, "pve1")
	require.Contains(t, out, "online")
	require.Contains(t, out, "AA:BB")
}

func TestNodeList_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "list"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "list nodes")
}

// ---------------------------------------------------------------------------
// node status
// ---------------------------------------------------------------------------

func TestNodeStatus_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/status", &rec, map[string]any{
		"cpu":        0.5,
		"pveversion": "pve-manager/8.1.4",
		"loadavg":    []string{"0.10", "0.20", "0.30"},
		"memory":     map[string]any{"total": 2147483648, "used": 1073741824},
		"rootfs":     map[string]any{"total": 10737418240, "used": 5368709120},
		"cpuinfo":    map[string]any{"model": "Intel Xeon", "cpus": 8},
		"current-kernel": map[string]any{
			"sysname": "Linux", "release": "6.5.11-7-pve",
		},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "status", "pve1"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/status", rec.path)
	out := buf.String()
	require.Contains(t, out, "pve1")
	require.Contains(t, out, "pve-manager/8.1.4")
	// Curated columns restored from the typed response.
	require.Contains(t, out, "MEM")
	require.Contains(t, out, "DISK")
	require.Contains(t, out, "Intel Xeon")
	require.Contains(t, out, "6.5.11-7-pve")
}

// TestNodeStatus_JSONLossless verifies `node status -o json` emits the full
// typed API response (Raw), not just the curated subset, so memory/rootfs/
// cpuinfo/kernel are all retrievable by machine consumers.
func TestNodeStatus_JSONLossless(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/status", &rec, map[string]any{
		"cpu":        0.5,
		"pveversion": "pve-manager/8.1.4",
		"memory":     map[string]any{"total": 2147483648, "used": 1073741824},
		"rootfs":     map[string]any{"total": 10737418240, "used": 5368709120},
		"cpuinfo":    map[string]any{"model": "Intel Xeon", "cpus": 8},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatJSON, exec.Fake())
	root.SetArgs(append(prefix, "node", "status", "pve1"))

	require.NoError(t, root.Execute())

	// The shared test buffer may carry a leading stderr WARN line; isolate the
	// JSON document, which begins at the first '{'.
	out := buf.String()
	if i := strings.IndexByte(out, '{'); i >= 0 {
		out = out[i:]
	}
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &parsed),
		"output must be valid JSON; got: %s", buf.String())
	// The raw typed response must be present, including fields not in the table.
	require.Contains(t, parsed, "memory")
	require.Contains(t, parsed, "rootfs")
	require.Contains(t, parsed, "cpuinfo")
	require.Contains(t, parsed, "pveversion")
}

func TestNodeStatus_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/status", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such node")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "status", "pve1"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "get status for node")
}

// ---------------------------------------------------------------------------
// node ssh / shell / console / exec (shell-out)
// ---------------------------------------------------------------------------

// TestNodeSSH_ResolvesHostAndRunsInteractive places --port before <node>,
// which SetInterspersed(false) requires: pve's own connection flags must
// precede the node argument, since everything after it is ssh-option/
// remote-command passthrough (see remote.RunSSH).
func TestNodeSSH_ResolvesHostAndRunsInteractive(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	// Default cluster status maps pve1 -> 192.168.1.10.
	runner := exec.Fake()
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, runner)
	root.SetArgs(append(prefix, "node", "ssh", "--port", "2222", "pve1", "--", "uptime"))

	require.NoError(t, root.Execute())
	require.Len(t, runner.Calls, 1)
	c := runner.Calls[0]
	require.True(t, c.Interactive)
	require.Equal(t, "ssh", c.Name)
	require.Contains(t, c.Args, "root@192.168.1.10")
	require.Contains(t, strings.Join(c.Args, " "), "-p 2222")
	require.Equal(t, "uptime", c.Args[len(c.Args)-1])
}

func TestNodeSSH_FallsBackToNodeNameWhenUnresolved(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	// Cluster status with no matching node entry forces fallback to node name.
	f.HandleJSON("GET /api2/json/cluster/status", []any{
		map[string]any{"type": "cluster", "name": "c", "online": 1},
	})
	runner := exec.Fake()
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, runner)
	root.SetArgs(append(prefix, "node", "ssh", "--user", "admin", "othernode"))

	require.NoError(t, root.Execute())
	require.Len(t, runner.Calls, 1)
	require.Contains(t, runner.Calls[0].Args, "admin@othernode")
}

// TestNodeSSH_PassthroughOptionAfterNode verifies the new grammar: a
// leading-dash token after <node> with no "--" boundary is now reordered
// ahead of the destination as an ssh option instead of being rejected (the
// pre-passthrough behaviour treated any argument after <node> as the literal
// remote command, so "-v" would have been sent to the remote shell verbatim).
func TestNodeSSH_PassthroughOptionAfterNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	runner := exec.Fake()
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, runner)
	root.SetArgs(append(prefix, "node", "ssh", "pve1", "-v", "uptime"))

	require.NoError(t, root.Execute())
	require.Len(t, runner.Calls, 1)
	c := runner.Calls[0]
	require.Equal(t, []string{"-p", "22", "-v", "root@192.168.1.10", "uptime"}, c.Args)
}

// TestNodeSSH_DashDashForcesRemoteCommand verifies "--" still forces the
// remote-command boundary, so a leading-dash token after it is passed to the
// remote command literally rather than reordered as an ssh option.
func TestNodeSSH_DashDashForcesRemoteCommand(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	runner := exec.Fake()
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, runner)
	root.SetArgs(append(prefix, "node", "ssh", "pve1", "--", "-v"))

	require.NoError(t, root.Execute())
	require.Len(t, runner.Calls, 1)
	c := runner.Calls[0]
	require.Equal(t, []string{"-p", "22", "root@192.168.1.10", "-v"}, c.Args)
}

func TestNodeShell_RunsInteractiveNoRemoteCmd(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	runner := exec.Fake()
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, runner)
	root.SetArgs(append(prefix, "node", "shell", "pve1"))

	require.NoError(t, root.Execute())
	require.Len(t, runner.Calls, 1)
	c := runner.Calls[0]
	require.True(t, c.Interactive)
	require.Equal(t, "root@192.168.1.10", c.Args[len(c.Args)-1])
}

func TestNodeConsole_AliasesShell(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	runner := exec.Fake()
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, runner)
	root.SetArgs(append(prefix, "node", "console", "pve1"))

	require.NoError(t, root.Execute())
	require.Len(t, runner.Calls, 1)
	require.True(t, runner.Calls[0].Interactive)
}

func TestNodeExec_RunsNonInteractiveWithCapture(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	runner := exec.Fake(exec.FakeResponse{Stdout: "hello-from-node\n"})
	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, runner)
	root.SetArgs(append(prefix, "node", "exec", "pve1", "--", "echo", "hi"))

	require.NoError(t, root.Execute())
	require.Len(t, runner.Calls, 1)
	c := runner.Calls[0]
	require.False(t, c.Interactive)
	require.Equal(t, "ssh", c.Name)
	require.Contains(t, c.Args, "root@192.168.1.10")
	require.Equal(t, []string{"echo", "hi"}, c.Args[len(c.Args)-2:])
	require.Contains(t, buf.String(), "hello-from-node")
}

func TestNodeExec_PropagatesRunnerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	runner := exec.Fake(exec.FakeResponse{ExitCode: 3})
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, runner)
	root.SetArgs(append(prefix, "node", "exec", "pve1", "--", "false"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "exec on node")
}

// ---------------------------------------------------------------------------
// node rsync (shell-out)
// ---------------------------------------------------------------------------

func TestNodeRsync_RewritesRemoteRefAndBuildsArgs(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	runner := exec.Fake()
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, runner)
	root.SetArgs(append(prefix, "node", "rsync", "pve1", "./local/", "pve1:/remote/", "--delete", "--port", "2200"))

	require.NoError(t, root.Execute())
	require.Len(t, runner.Calls, 1)
	c := runner.Calls[0]
	require.False(t, c.Interactive)
	require.Equal(t, "rsync", c.Name)
	joined := strings.Join(c.Args, " ")
	require.Contains(t, joined, "--delete")
	require.Contains(t, joined, "ssh -p 2200")
	// src stays local, dst rewritten to root@<ip>:/remote/.
	require.Equal(t, "./local/", c.Args[len(c.Args)-2])
	require.Equal(t, "root@192.168.1.10:/remote/", c.Args[len(c.Args)-1])
}

// TestNodeRsync_IdentityWithSpacesIsQuoted verifies the -e remote-shell string
// shell-quotes an identity path containing spaces so rsync, which word-splits
// the -e value, receives the full path as a single token.
func TestNodeRsync_IdentityWithSpacesIsQuoted(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	runner := exec.Fake()
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, runner)
	root.SetArgs(append(prefix, "node", "rsync", "pve1", "./local/", "pve1:/remote/",
		"--identity", "/Users/jane doe/.ssh/id"))

	require.NoError(t, root.Execute())
	require.Len(t, runner.Calls, 1)
	c := runner.Calls[0]
	// Find the -e value and confirm the identity is single-quoted intact.
	var eVal string
	for i, a := range c.Args {
		if a == "-e" && i+1 < len(c.Args) {
			eVal = c.Args[i+1]
		}
	}
	require.NotEmpty(t, eVal)
	require.Contains(t, eVal, "-i '/Users/jane doe/.ssh/id'")
}

func TestNodeRsync_PropagatesRunnerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	runner := exec.Fake(exec.FakeResponse{ExitCode: 23})
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, runner)
	root.SetArgs(append(prefix, "node", "rsync", "pve1", "a", "pve1:b"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "rsync to node")
}

// ---------------------------------------------------------------------------
// node task list / log / stop / wait
// ---------------------------------------------------------------------------

func TestNodeTaskList_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/tasks", &rec, []any{
		map[string]any{
			"upid": "UPID:pve1:1:2:3:qmstart:100:root@pam:", "type": "qmstart",
			"id": "100", "node": "pve1", "starttime": 10, "endtime": 20,
			"status": "OK", "user": "root@pam",
		},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "task", "list", "pve1", "--limit", "5"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/tasks", rec.path)
	require.Contains(t, rec.query, "limit=5")
	require.Contains(t, buf.String(), "qmstart")
}

func TestNodeTaskList_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "denied")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "task", "list", "pve1"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "list tasks on node")
}

func TestNodeTaskLog_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:1:2:3:qmstart:100:root@pam:"
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/tasks/"+upid+"/log", &rec, []any{
		map[string]any{"n": 1, "t": "starting task"},
		map[string]any{"n": 2, "t": "task OK"},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "task", "log", "pve1", upid))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Contains(t, buf.String(), "starting task")
	require.Contains(t, buf.String(), "task OK")
}

func TestNodeTaskStop_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:1:2:3:qmstart:100:root@pam:"
	var rec recordedRequest
	f.HandleFunc("DELETE /api2/json/nodes/pve1/tasks/"+upid, func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		testhelper.WriteData(w, nil)
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "task", "stop", "pve1", upid))

	require.NoError(t, root.Execute())
	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), "stopped")
}

func TestNodeTaskStop_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:1:2:3:qmstart:100:root@pam:"
	f.HandleFunc("DELETE /api2/json/nodes/pve1/tasks/"+upid, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusConflict, "cannot stop")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "task", "stop", "pve1", upid))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "stop task")
}

func TestNodeTaskWait_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:qmstart:100:root@pam:"
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "task", "wait", upid, "--timeout", "5", "--interval", "5"))

	require.NoError(t, root.Execute())
	out := buf.String()
	require.Contains(t, out, "stopped")
	require.Contains(t, out, "OK")
}

func TestNodeTaskWait_InvalidUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "task", "wait", "not-a-upid", "--timeout", "2", "--interval", "5"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "wait for task")
}

// ---------------------------------------------------------------------------
// node services list / get / start / stop / restart / reload
// ---------------------------------------------------------------------------

func TestNodeServicesList_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/services", &rec, []any{
		map[string]any{
			"service": "pveproxy", "name": "pveproxy", "desc": "PVE API Proxy",
			"state": "running", "active-state": "active",
		},
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "services", "list", "pve1"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/services", rec.path)
	out := buf.String()
	require.Contains(t, out, "pveproxy")
	require.Contains(t, out, "running")
}

// TestNodeServicesGet_Success verifies `services get` reads the state child
// endpoint: GET /nodes/{node}/services/{service} is only a directory index.
func TestNodeServicesGet_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "GET /api2/json/nodes/pve1/services/pveproxy/state", &rec, map[string]any{
		"service": "pveproxy", "state": "running",
		"desc": "PVE API Proxy", "active-state": "active",
		"unit-state": "enabled",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "services", "get", "pve1", "pveproxy"))

	require.NoError(t, root.Execute())
	require.Equal(t, "GET", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/services/pveproxy/state", rec.path)
	out := buf.String()
	require.Contains(t, out, "running")
	require.Contains(t, out, "enabled")
}

func TestNodeServicesStart_BlocksUntilTaskDone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:srvstart:pveproxy:root@pam:"
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/services/pveproxy/start", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		testhelper.WriteData(w, upid)
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": upid,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "services", "start", "pve1", "pveproxy"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/services/pveproxy/start", rec.path)
	require.Contains(t, buf.String(), "started")
}

func TestNodeServicesStop_AsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:srvstop:pveproxy:root@pam:"
	var rec recordedRequest
	f.HandleFunc("POST /api2/json/nodes/pve1/services/pveproxy/stop", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		testhelper.WriteData(w, upid)
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--async", "node", "services", "stop", "pve1", "pveproxy"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Contains(t, buf.String(), upid)
}

func TestNodeServicesRestart_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/services/pveproxy/restart", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "restart failed")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "services", "restart", "pve1", "pveproxy"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "restart service")
}

// TestNodeServices_BlockingSuccessMessagePerVerb pins the per-verb past-tense
// success word emitted by the shared service-action command's blocking branch,
// so a swapped pastTense argument is caught for every verb.
func TestNodeServices_BlockingSuccessMessagePerVerb(t *testing.T) {
	cases := []struct {
		verb     string
		taskType string
		pastWord string
	}{
		{"start", "srvstart", "started"},
		{"stop", "srvstop", "stopped"},
		{"restart", "srvrestart", "restarted"},
		{"reload", "srvreload", "reloaded"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.verb, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			upid := "UPID:pve1:00000001:00000002:AABBCCDD:" + tc.taskType + ":pveproxy:root@pam:"
			f.HandleFunc("POST /api2/json/nodes/pve1/services/pveproxy/"+tc.verb,
				func(w http.ResponseWriter, _ *http.Request) {
					testhelper.WriteData(w, upid)
				})
			f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+upid+"/status", map[string]any{
				"status": "stopped", "exitstatus": "OK", "upid": upid,
			})

			root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
			root.SetArgs(append(prefix, "node", "services", tc.verb, "pve1", "pveproxy"))

			require.NoError(t, root.Execute())
			require.Contains(t, buf.String(), tc.pastWord)
		})
	}
}

// TestNodeServicesRestart_AsyncReturnsUPID asserts the --async branch of the
// restart verb returns the UPID without blocking.
func TestNodeServicesRestart_AsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:srvrestart:pveproxy:root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/services/pveproxy/restart",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, upid)
		})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--async", "node", "services", "restart", "pve1", "pveproxy"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), upid)
}

func TestNodeServicesReload_AsyncReturnsUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	upid := "UPID:pve1:00000001:00000002:AABBCCDD:srvreload:pveproxy:root@pam:"
	f.HandleFunc("POST /api2/json/nodes/pve1/services/pveproxy/reload", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, upid)
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--async", "node", "services", "reload", "pve1", "pveproxy"))

	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), upid)
}
