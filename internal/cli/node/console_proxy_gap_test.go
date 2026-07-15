package node_test

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// ---------------------------------------------------------------------------
// Command-tree registration
// ---------------------------------------------------------------------------

// TestNodeConsoleProxy_CommandTree verifies that termproxy, vncshell, and
// spiceshell are all registered under `pmx node`.
func TestNodeConsoleProxy_CommandTree(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	_ = prefix

	var nodeCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "node" {
			nodeCmd = c
			break
		}
	}
	require.NotNil(t, nodeCmd, "node command group must be registered")

	names := make(map[string]bool)
	for _, c := range nodeCmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["termproxy"], "node must expose termproxy")
	require.True(t, names["vncshell"], "node must expose vncshell")
	require.True(t, names["spiceshell"], "node must expose spiceshell")
}

// ---------------------------------------------------------------------------
// termproxy
// ---------------------------------------------------------------------------

func TestNodeTermproxy_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/termproxy", &rec, map[string]any{
		"ticket": "PVETERM:deadbeef",
		"port":   5900,
		"user":   "root@pam",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "termproxy"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/termproxy", rec.path)
	out := buf.String()
	require.Contains(t, out, "ticket")
	require.Contains(t, out, "PVETERM:deadbeef")
	require.Contains(t, out, "port")
}

func TestNodeTermproxy_CmdFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/termproxy", &rec, map[string]any{
		"ticket": "T", "port": 5901, "user": "root@pam",
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "termproxy",
		"--cmd", "bash",
		"--cmd-opts", "-l",
	))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "cmd=bash")
	require.Contains(t, rec.query, "cmd-opts=-l")
}

func TestNodeTermproxy_CmdOmittedWhenNotSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/termproxy", &rec, map[string]any{
		"ticket": "T", "port": 5902, "user": "root@pam",
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "termproxy"))

	require.NoError(t, root.Execute())
	require.NotContains(t, rec.query, "cmd=")
	require.NotContains(t, rec.query, "cmd-opts=")
}

func TestNodeTermproxy_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "termproxy"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeTermproxy_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/termproxy", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "termproxy boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "termproxy"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "create termproxy on node")
}

// ---------------------------------------------------------------------------
// vncshell
// ---------------------------------------------------------------------------

func TestNodeVncshell_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/vncshell", &rec, map[string]any{
		"ticket": "PVEVNC:aabbcc",
		"port":   5903,
		"user":   "root@pam",
		"cert":   "-----BEGIN CERTIFICATE-----",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "vncshell"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/vncshell", rec.path)
	out := buf.String()
	require.Contains(t, out, "PVEVNC:aabbcc")
	require.Contains(t, out, "port")
}

func TestNodeVncshell_Flags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/vncshell", &rec, map[string]any{
		"ticket": "T", "port": 5904, "user": "root@pam",
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "vncshell",
		"--height", "768",
		"--width", "1024",
		"--websocket",
		"--cmd", "top",
		"--cmd-opts", "-b",
	))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "height=768")
	require.Contains(t, rec.query, "width=1024")
	// PVE API encodes booleans as 1/0.
	require.Contains(t, rec.query, "websocket=1")
	require.Contains(t, rec.query, "cmd=top")
	require.Contains(t, rec.query, "cmd-opts=-b")
}

func TestNodeVncshell_OptionalFlagsOmitted(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/vncshell", &rec, map[string]any{
		"ticket": "T", "port": 5905, "user": "root@pam",
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "vncshell"))

	require.NoError(t, root.Execute())
	require.NotContains(t, rec.query, "height=")
	require.NotContains(t, rec.query, "width=")
	require.NotContains(t, rec.query, "websocket=")
	require.NotContains(t, rec.query, "cmd=")
}

func TestNodeVncshell_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "vncshell"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeVncshell_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/vncshell", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "vncshell boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "vncshell"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "create vncshell on node")
}

// ---------------------------------------------------------------------------
// spiceshell
// ---------------------------------------------------------------------------

func TestNodeSpiceshell_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordOn(f, "POST /api2/json/nodes/pve1/spiceshell", &rec, map[string]any{
		"type":         "spice",
		"password":     "ticket123",
		"host":         "pve1.example.com",
		"tls-port":     3128,
		"host-subject": "CN=pve1.example.com",
		"proxy":        "http://pve1.example.com",
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "spiceshell"))

	require.NoError(t, root.Execute())
	require.Equal(t, "POST", rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/spiceshell", rec.path)

	out := buf.String()
	require.Contains(t, out, "ticket123")
	require.Contains(t, out, "pve1.example.com")
	require.Contains(t, out, "vv-file")

	// vv-file path must point to a real file with [virt-viewer] header.
	vvPath := extractVVPath(t, out)
	require.NotEmpty(t, vvPath, "vv-file path must be non-empty")
	contents, err := os.ReadFile(vvPath)
	require.NoError(t, err)
	require.Contains(t, string(contents), "[virt-viewer]")
	require.Contains(t, string(contents), "password=ticket123")
	require.Contains(t, string(contents), "tls-port=3128")
	require.Contains(t, string(contents), "delete-this-file=1")
	// Clean up the .vv file the test wrote.
	_ = os.Remove(vvPath)
}

func TestNodeSpiceshell_ProxyFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/spiceshell", &rec, map[string]any{
		"type": "spice", "password": "p", "host": "h", "tls-port": 3128,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "spiceshell",
		"--proxy", "http://proxy.example.com",
	))

	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "proxy=http%3A%2F%2Fproxy.example.com")

	// Clean up any .vv file created.
	if vvPath := extractVVPath(t, buf.String()); vvPath != "" {
		_ = os.Remove(vvPath)
	}
}

func TestNodeSpiceshell_ProxyOmittedWhenNotSet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "POST /api2/json/nodes/pve1/spiceshell", &rec, map[string]any{
		"type": "spice", "password": "p", "host": "h", "tls-port": 3128,
	})

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "spiceshell"))

	require.NoError(t, root.Execute())
	require.NotContains(t, rec.query, "proxy=")

	if vvPath := extractVVPath(t, buf.String()); vvPath != "" {
		_ = os.Remove(vvPath)
	}
}

func TestNodeSpiceshell_RequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "node", "spiceshell"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "no node specified")
}

func TestNodeSpiceshell_APIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/spiceshell", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "spice boom")
	})

	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "spiceshell"))

	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "create spiceshell on node")
}

// extractVVPath scans the rendered output for a vv-file row and returns the
// file path value. Returns empty string when not found.
func extractVVPath(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "vv-file") {
			// Table format: "vv-file   /tmp/pve-spice-*.vv"
			// Single format: key = value
			parts := strings.Fields(line)
			for i, p := range parts {
				if strings.Contains(p, "vv-file") && i+1 < len(parts) {
					candidate := parts[i+1]
					if strings.HasSuffix(candidate, ".vv") {
						return candidate
					}
				}
				if strings.HasSuffix(p, ".vv") {
					return p
				}
			}
		}
	}
	return ""
}
