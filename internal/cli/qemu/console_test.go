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

func TestQemuConsole_VNCDefault(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/vncproxy", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"ticket": "PVEVNC:ABCDEF",
			"port":   5900,
			"user":   "root@pam",
			"cert":   "-----BEGIN-----",
			"upid":   validUPID,
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "console", "100"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/vncproxy", gotPath)
	out := buf.String()
	require.Contains(t, out, "ticket")
	require.Contains(t, out, "5900")
}

// TestQemuConsole_ByName verifies a guest name (no --node) is resolved to its
// VMID and node via cluster/resources, and the proxy POST lands on that node.
func TestQemuConsole_ByName(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"type": "qemu", "vmid": 100, "name": "peppi-cp", "node": "pve1"},
		})
	})
	var gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/vncproxy", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{"ticket": "T", "port": 5900})
	})
	// No node configured: it must be resolved from the guest name.
	deps := depsFor(t, ac, output.FormatTable, "", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "console", "peppi-cp"))
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/vncproxy", gotPath)
}

func TestQemuConsole_VNCWebsocket(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath, gotQuery, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/vncproxy", func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, map[string]any{"ticket": "T", "port": 5901})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "console", "100", "--type", "vnc", "--websocket"))

	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/vncproxy", gotPath)
	form := parseForm(t, gotQuery+"&"+body)
	require.NotEmpty(t, form.Get("websocket"))
}

func TestQemuConsole_Term(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath, gotQuery, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/termproxy", func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, map[string]any{"ticket": "TT", "port": 5902, "user": "root@pam"})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "console", "100", "--type", "term", "--serial", "serial0"))

	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/termproxy", gotPath)
	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "serial0", form.Get("serial"))
}

func TestQemuConsole_Spice(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath, gotQuery, body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/spiceproxy", func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		body = readBody(t, r)
		testhelper.WriteData(w, map[string]any{
			"password": "secret", "host": "pve1", "tls-port": 3128, "proxy": "https://pve1",
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "console", "100", "--type", "spice", "--proxy", "pve1.example"))

	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/spiceproxy", gotPath)
	form := parseForm(t, gotQuery+"&"+body)
	require.Equal(t, "pve1.example", form.Get("proxy"))
	// The SPICE ticket/password is the point of the command: it must be
	// surfaced to the caller's output (the raw-fetch workaround exists
	// precisely because the typed response struct drops it).
	require.Contains(t, buf.String(), "secret")
}

func TestQemuConsole_UnexpectedShape(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/vncproxy", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, []any{"not", "an", "object"})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "console", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected response shape")
}

func TestQemuConsole_InvalidType(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "console", "100", "--type", "bogus")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid console type")
}

func TestQemuConsole_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/vncproxy", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "console", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "open vnc console")
}

func TestQemuConsoleCommandTree(t *testing.T) {
	cmd := Group(nil)
	var console *cobra.Command
	for _, c := range cmd.Commands() {
		if c.Name() == "console" {
			console = c
			break
		}
	}
	require.NotNil(t, console, "console command should be registered")
	require.NotNil(t, console.Flags().Lookup("type"), "console should define --type")
}
