package lxc

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestLxcConsole_VNCDefault(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/vncproxy", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"ticket": "PVEVNC:ABCDEF",
			"port":   5900,
			"user":   "root@pam",
			"cert":   "-----BEGIN-----",
		})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "console", "101")
	require.NoError(t, run())

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/vncproxy", gotPath)
	out := buf.String()
	require.Contains(t, out, "ticket")
	require.Contains(t, out, "5900")
}

func TestLxcConsole_VNCDimensions(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/vncproxy", func(w http.ResponseWriter, r *http.Request) {
		body = recordBody(t, r)
		testhelper.WriteData(w, map[string]any{"ticket": "T", "port": 5901})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "console", "101", "--websocket", "--width", "1024", "--height", "768")
	require.NoError(t, run())

	require.Equal(t, true, body["websocket"])
	require.EqualValues(t, 1024, body["width"])
	require.EqualValues(t, 768, body["height"])
}

func TestLxcConsole_Term(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/termproxy", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{"ticket": "TT", "port": 5902, "user": "root@pam"})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "console", "101", "--type", "term")
	require.NoError(t, run())

	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/termproxy", gotPath)
}

func TestLxcConsole_Spice(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotPath string
	var body map[string]any
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/spiceproxy", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body = recordBody(t, r)
		testhelper.WriteData(w, map[string]any{
			"password": "secret", "host": "pve1", "tls-port": 3128, "proxy": "https://pve1",
		})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "console", "101", "--type", "spice", "--proxy", "pve1.example")
	require.NoError(t, run())

	require.Equal(t, "/api2/json/nodes/pve1/lxc/101/spiceproxy", gotPath)
	require.Equal(t, "pve1.example", body["proxy"])
}

func TestLxcConsole_InvalidType(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "console", "101", "--type", "bogus")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid console type")
}

func TestLxcConsole_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/lxc/101/vncproxy", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "console", "101")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "open vnc console")
}

func TestLxcConsoleCommandTree(t *testing.T) {
	cmd := newGroupCmd(nil)
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
