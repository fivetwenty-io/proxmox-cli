package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// --- monitor ----------------------------------------------------------------

func TestQemuMonitor_Success(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/monitor", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = readBody(t, r)
		testhelper.WriteData(w, "OK")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "monitor", "100", "--command", "info status", "--yes"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/monitor", gotPath)
	// Body is URL-encoded; spaces become "+".
	require.Contains(t, body, "info")
	require.Contains(t, body, "command=")
}

func TestQemuMonitor_RequiresYes(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "monitor", "100", "--command", "info status")
	require.Error(t, err)
	require.Contains(t, err.Error(), "confirmation")
}

func TestQemuMonitor_RequiresCommand(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "monitor", "100", "--yes")
	require.Error(t, err)
	// MarkFlagRequired error mentions the flag name.
	require.Contains(t, err.Error(), "command")
}

func TestQemuMonitor_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/monitor", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "root only")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "monitor", "100", "--command", "info", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "monitor command for VM 100")
}

func TestQemuMonitor_RequiresNode(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "monitor", "100", "--command", "info", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node")
}

func TestQemuMonitor_CommandTree(t *testing.T) {
	root := newGroupCmd(nil)
	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["monitor"], "expected top-level sub-command 'monitor'")
}

// --- sendkey ----------------------------------------------------------------

func TestQemuSendkey_Success(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/sendkey", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "sendkey", "100", "--key", "ctrl-alt-delete"))

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/sendkey", gotPath)
	require.Contains(t, body, "ctrl-alt-delete")
	require.Contains(t, buf.String(), "sent")
}

func TestQemuSendkey_RequiresKey(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "sendkey", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "key")
}

func TestQemuSendkey_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/sendkey", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "sendkey", "100", "--key", "ret")
	require.Error(t, err)
	require.Contains(t, err.Error(), "sendkey")
}

func TestQemuSendkey_RequiresNode(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "sendkey", "100", "--key", "ret")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node")
}

func TestQemuSendkey_CommandTree(t *testing.T) {
	root := newGroupCmd(nil)
	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["sendkey"], "expected top-level sub-command 'sendkey'")
}
