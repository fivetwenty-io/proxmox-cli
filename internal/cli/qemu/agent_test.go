package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestQemuAgentPing(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/ping", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, nil)
	})
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "agent", "100", "ping"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/agent/ping", gotPath)
	// Ping returns no payload; the empty-body branch reports acknowledgement.
	require.Contains(t, buf.String(), "acknowledged")
}

func TestQemuAgentGetHostName(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/agent/get-host-name", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{"result": map[string]any{"host-name": "vmguest"}})
	})
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "agent", "100", "get-host-name"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/agent/get-host-name", gotPath)
	// The object response is flattened; the nested host name survives as JSON.
	require.Contains(t, buf.String(), "vmguest")
}

func TestQemuAgentGetFsinfo_ArrayResult(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/agent/get-fsinfo", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"result": []any{map[string]any{"name": "sda1", "mountpoint": "/"}},
		})
	})
	depsFor(t, ac, output.FormatJSON, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "agent", "100", "get-fsinfo"))
	require.Contains(t, buf.String(), "mountpoint")
}

func TestQemuAgentMutateFstrim(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/fstrim", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		testhelper.WriteData(w, nil)
	})
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "agent", "100", "fstrim"))
	require.Equal(t, http.MethodPost, gotMethod)
}

func TestQemuAgentUnknownCommand(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "agent", "100", "no-such-cmd")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown agent command")
}

func TestQemuAgentInvalidVMID(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "agent", "abc", "ping")
	require.Error(t, err)
}

func TestQemuAgentRequiresNode(t *testing.T) {
	_, ac := newFakeClient(t)
	depsFor(t, ac, output.FormatTable, "", false)

	var buf bytes.Buffer
	err := run(&buf, "agent", "100", "ping")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestQemuAgentServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/ping", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "no agent")
	})
	depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(&buf, "agent", "100", "ping")
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent ping for VM 100")
}

func TestQemuAgentCommandTree(t *testing.T) {
	cmd := newGroupCmd(nil)
	var agent *cobra.Command
	for _, c := range cmd.Commands() {
		if c.Name() == "agent" {
			agent = c
			break
		}
	}
	require.NotNil(t, agent, "agent command should be registered")
	require.Error(t, agent.Args(agent, []string{"100"}), "agent should require <vmid> <command>")
	require.NoError(t, agent.Args(agent, []string{"100", "ping"}))
}
