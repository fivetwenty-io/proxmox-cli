package task_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// runTask wires the real root command (so PersistentPreRunE builds live Deps
// from a config file pointing at the fake PVE server), attaches the task group,
// and executes it with args. It returns captured output and the execution error.
//
// node sets the target's default-node; when empty no node is configured so the
// --node-dependent commands must error.
func runTask(t *testing.T, f *testhelper.FakePVE, node string, format string, args ...string) (string, error) {
	t.Helper()

	host, port := splitHostPort(t, f.Server.Listener.Addr().String())
	cfgPath := writeConfig(t, host, port, f.Options.APIToken, node)

	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	root.SetContext(context.Background())
	cli.AddGroups(root, &cli.Deps{})

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)

	full := append([]string{
		"--config", cfgPath,
		"--output", format,
		"task",
	}, args...)
	root.SetArgs(full)

	err := root.Execute()
	return buf.String(), err
}

// splitHostPort splits a "host:port" address into its parts.
func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	idx := strings.LastIndex(addr, ":")
	require.Greater(t, idx, 0, "address must contain a port: %q", addr)
	p, err := strconv.Atoi(addr[idx+1:])
	require.NoError(t, err)
	return addr[:idx], p
}

// writeConfig writes a minimal token-auth config file pointing at the fake server.
func writeConfig(t *testing.T, host string, port int, apiToken, node string) string {
	t.Helper()

	// apiToken form: "user@realm!tokenid=secret"; split into the config fields.
	user, tokenID, secret := splitAPIToken(t, apiToken)

	defaultNode := ""
	if node != "" {
		defaultNode = "    default-node: " + node + "\n"
	}

	cfg := fmt.Sprintf(`current-context: fake
contexts:
  fake:
    host: %s
    port: %d
    protocol: http
    realm: pam
%s    auth:
      type: token
      username: %s
      token-id: %s
      secret: %q
    tls:
      insecure: true
`, host, port, defaultNode, user, tokenID, secret)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	require.NoError(t, os.WriteFile(path, []byte(cfg), 0o600))
	return path
}

// splitAPIToken parses "user@realm!tokenid=secret" into (user@realm, tokenid, secret).
func splitAPIToken(t *testing.T, apiToken string) (user, tokenID, secret string) {
	t.Helper()
	bang := strings.Index(apiToken, "!")
	require.Greater(t, bang, 0)
	user = apiToken[:bang]
	rest := apiToken[bang+1:]
	eq := strings.Index(rest, "=")
	require.Greater(t, eq, 0)
	return user, rest[:eq], rest[eq+1:]
}

const testUPID = "UPID:pve1:00001234:0000ABCD:00005000:vzdump:100:root@pam:"

// TestList_Success verifies that `task list` calls the node tasks endpoint and
// renders the task rows.
func TestList_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, []map[string]any{
			{
				"upid":      testUPID,
				"type":      "vzdump",
				"id":        "100",
				"node":      "pve1",
				"starttime": 1700000000,
				"endtime":   1700000100,
				"status":    "OK",
				"user":      "root@pam",
			},
		})
	})

	out, err := runTask(t, f, "pve1", "table", "list")
	require.NoError(t, err)

	require.Equal(t, "/api2/json/nodes/pve1/tasks", gotPath)
	require.Contains(t, out, "vzdump")
	require.Contains(t, out, "UPID:pve1:00001234")
	require.Contains(t, out, "root@pam")
}

// TestList_NoNode verifies that `task list` errors when no node is resolved.
func TestList_NoNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := runTask(t, f, "", "table", "list")
	require.Error(t, err)
	require.Contains(t, err.Error(), "node")
}

// TestList_Filters verifies that filter flags are forwarded as query params.
func TestList_Filters(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []map[string]any{})
	})

	_, err := runTask(t, f, "pve1", "json", "list",
		"--vmid", "100", "--typefilter", "vzdump", "--limit", "10")
	require.NoError(t, err)

	require.Contains(t, gotQuery, "vmid=100")
	require.Contains(t, gotQuery, "typefilter=vzdump")
	require.Contains(t, gotQuery, "limit=10")
}

// TestLog_Success verifies that `task log` renders the log lines.
func TestLog_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+testUPID+"/log", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, []map[string]any{
			{"n": 1, "t": "starting backup"},
			{"n": 2, "t": "backup finished"},
		})
	})

	out, err := runTask(t, f, "pve1", "table", "log", testUPID)
	require.NoError(t, err)

	require.Contains(t, gotPath, "/tasks/")
	require.Contains(t, gotPath, "/log")
	require.Contains(t, out, "starting backup")
	require.Contains(t, out, "backup finished")
}

// TestLog_ServerError verifies that an upstream error is surfaced.
func TestLog_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+testUPID+"/log", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "log read failed")
	})

	_, err := runTask(t, f, "pve1", "table", "log", testUPID)
	require.Error(t, err)
}

// TestStop_Success verifies that `task stop` issues a DELETE and prints a notice.
func TestStop_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotMethod, gotPath string
	f.HandleFunc("DELETE /api2/json/nodes/pve1/tasks/"+testUPID, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		testhelper.WriteData(w, nil)
	})

	out, err := runTask(t, f, "pve1", "table", "stop", testUPID)
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, gotMethod)
	require.Contains(t, gotPath, "/tasks/")
	require.Contains(t, out, "stopped")
}

// TestStop_NoNode verifies that `task stop` errors when no node is resolved.
func TestStop_NoNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := runTask(t, f, "", "table", "stop", testUPID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "node")
}

// TestWait_Success verifies that `task wait` polls the status endpoint (node
// parsed from the UPID) and renders the terminal status.
func TestWait_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+testUPID+"/status", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"upid":       testUPID,
			"status":     "stopped",
			"exitstatus": "OK",
		})
	})

	out, err := runTask(t, f, "", "table", "wait", testUPID)
	require.NoError(t, err)

	require.Contains(t, gotPath, "/status")
	require.Contains(t, out, "stopped")
	require.Contains(t, out, "OK")
}

// TestWait_TaskFailed verifies that a failed task surfaces an error.
func TestWait_TaskFailed(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+testUPID+"/status", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"upid":       testUPID,
			"status":     "stopped",
			"exitstatus": "command failed",
		})
	})

	_, err := runTask(t, f, "", "table", "wait", testUPID)
	require.Error(t, err)
}

// TestWait_InvalidUPID verifies that an unparsable UPID errors before any I/O.
func TestWait_InvalidUPID(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := runTask(t, f, "", "table", "wait", "not-a-upid")
	require.Error(t, err)
}

// TestGroupCmd_Subcommands verifies the leaf sub-commands are registered.
func TestGroupCmd_Subcommands(t *testing.T) {
	root, cleanup := cli.NewRootCmd()
	defer cleanup()
	root.SetContext(context.Background())
	cli.AddGroups(root, &cli.Deps{})

	var group *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "task" {
			group = c
			break
		}
	}
	require.NotNil(t, group, "task group must be registered")

	names := make(map[string]bool)
	for _, c := range group.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"list", "log", "wait", "stop"} {
		require.True(t, names[want], "expected sub-command %q", want)
	}
}
