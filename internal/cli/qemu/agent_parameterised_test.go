package qemu

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// --- agent exec -------------------------------------------------------------

func TestQemuAgentExec_Success(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/exec", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = readBody(t, r)
		testhelper.WriteData(w, map[string]any{"pid": 4242})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "agent", "exec", "100", "--command", "ls -la"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/agent/exec", gotPath)
	// The command slice ["ls", "-la"] is sent as JSON array.
	require.Contains(t, body, "ls")
	require.Contains(t, buf.String(), "4242")
}

func TestQemuAgentExec_WithInputData(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/exec", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, map[string]any{"pid": 1})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "agent", "exec", "100", "--command", "cat", "--input-data", "hello"))
	require.Contains(t, body, "hello")
}

func TestQemuAgentExec_MissingCommand(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "agent", "exec", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--command is required")
}

func TestQemuAgentExec_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/exec", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "agent not running")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "agent", "exec", "100", "--command", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent exec for VM 100")
}

// TestQemuAgent_RequiresNode consolidates shape-3 (node-required) cases across
// agent sub-commands. Each case runs with an empty node and expects "no node" in
// the error; no HTTP handler is registered because the check fires before the
// API call.
func TestQemuAgent_RequiresNode(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{
			name: "exec",
			args: []string{"agent", "exec", "100", "--command", "ls"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ac := newFakeClient(t)
			deps := depsFor(t, ac, output.FormatTable, "", false)

			var buf bytes.Buffer
			err := run(deps, &buf, tc.args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), "no node")
		})
	}
}

func TestQemuAgentExec_CommandTree(t *testing.T) {
	group := Group(nil)
	var agent *cobra.Command
	for _, c := range group.Commands() {
		if c.Name() == "agent" {
			agent = c
			break
		}
	}
	require.NotNil(t, agent)

	subNames := make(map[string]bool)
	for _, c := range agent.Commands() {
		subNames[c.Name()] = true
	}
	for _, want := range []string{"exec", "exec-status", "file-read", "file-write", "set-user-password"} {
		require.True(t, subNames[want], "expected agent sub-command %q", want)
	}
}

// --- agent exec-status ------------------------------------------------------

func TestQemuAgentExecStatus_Success(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/agent/exec-status", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"exited":   1,
			"exitcode": 0,
			"out-data": "file1.txt\nfile2.txt\n",
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "agent", "exec-status", "100", "--pid", "4242"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/agent/exec-status", gotPath)
	out := buf.String()
	require.Contains(t, out, "exited")
	require.Contains(t, out, "file1.txt")
}

// TestQemuAgent_RequiredFlags consolidates shape-1 (flag-required) cases for
// agent sub-commands that use run(). Each case omits one required flag and
// expects the flag name (lowercased) in the error. No HTTP handler is
// registered because MarkFlagRequired fires before any API call.
func TestQemuAgent_RequiredFlags(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantContain string // matched against strings.ToLower(err.Error())
	}{
		{
			name:        "exec-status missing pid",
			args:        []string{"agent", "exec-status", "100"},
			wantContain: "pid",
		},
		{
			name:        "file-read missing file",
			args:        []string{"agent", "file-read", "100"},
			wantContain: "file",
		},
		{
			name:        "file-write missing file",
			args:        []string{"agent", "file-write", "100", "--content", "data"},
			wantContain: "file",
		},
		{
			name:        "file-write missing content",
			args:        []string{"agent", "file-write", "100", "--file", "/tmp/x"},
			wantContain: "content",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ac := newFakeClient(t)
			deps := depsFor(t, ac, output.FormatTable, "pve1", false)

			var buf bytes.Buffer
			err := run(deps, &buf, tc.args...)
			require.Error(t, err)
			require.Contains(t, strings.ToLower(err.Error()), tc.wantContain)
		})
	}
}

func TestQemuAgentExecStatus_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/agent/exec-status", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "pid not found")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "agent", "exec-status", "100", "--pid", "9999")
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent exec-status for VM 100")
}

// --- agent file-read --------------------------------------------------------

func TestQemuAgentFileRead_Success(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/agent/file-read", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"content":   "hello world",
			"truncated": false,
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "agent", "file-read", "100", "--file", "/etc/hostname"))

	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/agent/file-read", gotPath)
	require.Contains(t, buf.String(), "hello world")
}

func TestQemuAgentFileRead_WithOffsetCount(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/agent/file-read", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{"content": "abc"})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "agent", "file-read", "100",
		"--file", "/etc/passwd", "--offset", "100", "--count", "512"))
	require.Contains(t, gotQuery, "offset")
	require.Contains(t, gotQuery, "count")
}

func TestQemuAgentFileRead_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/agent/file-read", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "access denied")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "agent", "file-read", "100", "--file", "/etc/shadow")
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent file-read for VM 100")
}

// --- agent file-write -------------------------------------------------------

func TestQemuAgentFileWrite_Success(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/file-write", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "agent", "file-write", "100",
		"--file", "/tmp/test.txt", "--content", "hello"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/agent/file-write", gotPath)
	require.Contains(t, body, "test.txt")
	require.Contains(t, body, "hello")
	require.Contains(t, buf.String(), "Wrote to")
}

func TestQemuAgentFileWrite_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/file-write", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "write failed")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "agent", "file-write", "100", "--file", "/tmp/x", "--content", "y")
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent file-write for VM 100")
}

// --- agent set-user-password ------------------------------------------------
// Password is read from stdin; tests inject it via runWithStdin / cmd.SetIn.

func TestQemuAgentSetUserPassword_Success(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/set-user-password", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, runWithStdin(deps, &buf, strings.NewReader("s3cret\n"),
		"agent", "set-user-password", "100", "--username", "alice", "--yes"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/agent/set-user-password", gotPath)
	// Password must be in the request body but must NOT appear in command output.
	require.Contains(t, body, "alice")
	require.NotContains(t, buf.String(), "s3cret")
	require.Contains(t, buf.String(), "alice")
}

// TestQemuAgentSetUserPassword_RequiredFlags consolidates shape-1 (flag-required)
// cases for set-user-password, which reads stdin and requires runWithStdin.
func TestQemuAgentSetUserPassword_RequiredFlags(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantContain string
	}{
		{
			name:        "missing username",
			args:        []string{"agent", "set-user-password", "100"},
			wantContain: "username",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ac := newFakeClient(t)
			deps := depsFor(t, ac, output.FormatTable, "pve1", false)

			var buf bytes.Buffer
			err := runWithStdin(deps, &buf, strings.NewReader("pass\n"), tc.args...)
			require.Error(t, err)
			require.Contains(t, strings.ToLower(err.Error()), tc.wantContain)
		})
	}
}

func TestQemuAgentSetUserPassword_EmptyStdin(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := runWithStdin(deps, &buf, strings.NewReader(""),
		"agent", "set-user-password", "100", "--username", "bob", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no password provided")
}

// TestQemuAgentSetUserPassword_RequiresYes verifies the command refuses to set a
// password without --yes and never reads stdin or issues the request.
func TestQemuAgentSetUserPassword_RequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)
	var called bool
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/set-user-password", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := runWithStdin(deps, &buf, strings.NewReader("s3cret\n"),
		"agent", "set-user-password", "100", "--username", "alice")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "set-user-password must not be issued without --yes")
}

func TestQemuAgentSetUserPassword_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/set-user-password", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "user not found")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := runWithStdin(deps, &buf, strings.NewReader("pass\n"),
		"agent", "set-user-password", "100", "--username", "alice", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent set-user-password for VM 100")
}
