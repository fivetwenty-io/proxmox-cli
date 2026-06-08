package qemu

import (
	"bytes"
	"net/http"
	"os"
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
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "agent", "exec", "100", "--command", "ls -la"))

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
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "agent", "exec", "100", "--command", "cat", "--input-data", "hello"))
	require.Contains(t, body, "hello")
}

func TestQemuAgentExec_MissingCommand(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "agent", "exec", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--command is required")
}

func TestQemuAgentExec_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/exec", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "agent not running")
	})
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "agent", "exec", "100", "--command", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent exec for VM 100")
}

func TestQemuAgentExec_RequiresNode(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "", false)

	var buf bytes.Buffer
	err := run(&buf, "agent", "exec", "100", "--command", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node")
}

func TestQemuAgentExec_CommandTree(t *testing.T) {
	group := newGroupCmd(nil)
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
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "agent", "exec-status", "100", "--pid", "4242"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/agent/exec-status", gotPath)
	out := buf.String()
	require.Contains(t, out, "exited")
	require.Contains(t, out, "file1.txt")
}

func TestQemuAgentExecStatus_RequiresPID(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "agent", "exec-status", "100")
	require.Error(t, err)
	// MarkFlagRequired produces a cobra error containing the flag name.
	require.Contains(t, strings.ToLower(err.Error()), "pid")
}

func TestQemuAgentExecStatus_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/agent/exec-status", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "pid not found")
	})
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "agent", "exec-status", "100", "--pid", "9999")
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
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "agent", "file-read", "100", "--file", "/etc/hostname"))

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
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "agent", "file-read", "100",
		"--file", "/etc/passwd", "--offset", "100", "--count", "512"))
	require.Contains(t, gotQuery, "offset")
	require.Contains(t, gotQuery, "count")
}

func TestQemuAgentFileRead_RequiresFile(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "agent", "file-read", "100")
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "file")
}

func TestQemuAgentFileRead_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/agent/file-read", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "access denied")
	})
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "agent", "file-read", "100", "--file", "/etc/shadow")
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
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "agent", "file-write", "100",
		"--file", "/tmp/test.txt", "--content", "hello"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/agent/file-write", gotPath)
	require.Contains(t, body, "test.txt")
	require.Contains(t, body, "hello")
	require.Contains(t, buf.String(), "Wrote to")
}

func TestQemuAgentFileWrite_RequiresFile(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "agent", "file-write", "100", "--content", "data")
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "file")
}

func TestQemuAgentFileWrite_RequiresContent(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "agent", "file-write", "100", "--file", "/tmp/x")
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "content")
}

func TestQemuAgentFileWrite_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/file-write", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "write failed")
	})
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "agent", "file-write", "100", "--file", "/tmp/x", "--content", "y")
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent file-write for VM 100")
}

// --- agent set-user-password ------------------------------------------------
// Password is read from stdin; tests inject it via cmd.SetIn.

// withFakeStdin replaces os.Stdin with a pipe seeded with content and restores
// it when the returned cleanup function is called. This is the only reliable
// way to inject stdin for commands that call cmd.InOrStdin() (which falls back
// to os.Stdin when no reader is set on the cobra command).
func withFakeStdin(t *testing.T, content string) func() {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, err = w.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	old := os.Stdin
	os.Stdin = r
	return func() {
		os.Stdin = old
		require.NoError(t, r.Close())
	}
}

func TestQemuAgentSetUserPassword_Success(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/set-user-password", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	depsFor(t, ac, output.FormatTable, "pve1", false)
	defer withFakeStdin(t, "s3cret\n")()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "agent", "set-user-password", "100", "--username", "alice", "--yes"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/agent/set-user-password", gotPath)
	// Password must be in the request body but must NOT appear in command output.
	require.Contains(t, body, "alice")
	require.NotContains(t, buf.String(), "s3cret")
	require.Contains(t, buf.String(), "alice")
}

func TestQemuAgentSetUserPassword_RequiresUsername(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)
	defer withFakeStdin(t, "pass\n")()

	var buf bytes.Buffer
	err := run(&buf, "agent", "set-user-password", "100")
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "username")
}

func TestQemuAgentSetUserPassword_EmptyStdin(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)
	defer withFakeStdin(t, "")()

	var buf bytes.Buffer
	err := run(&buf, "agent", "set-user-password", "100", "--username", "bob", "--yes")
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
	depsFor(t, ac, output.FormatTable, "pve1", false)
	defer withFakeStdin(t, "s3cret\n")()

	var buf bytes.Buffer
	err := run(&buf, "agent", "set-user-password", "100", "--username", "alice")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "set-user-password must not be issued without --yes")
}

func TestQemuAgentSetUserPassword_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/set-user-password", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "user not found")
	})
	depsFor(t, ac, output.FormatTable, "pve1", false)
	defer withFakeStdin(t, "pass\n")()

	var buf bytes.Buffer
	err := run(&buf, "agent", "set-user-password", "100", "--username", "alice", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent set-user-password for VM 100")
}
