package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// nodeDefaultName is the default --node value ("localhost", since PBS is
// single-node) used across every node_*_test.go file.
const nodeDefaultName = "localhost"

// nodeAPIBase is the /api2/json base path for /nodes/{node}/... endpoints
// with the default node name, shared across every node_*_test.go file.
const nodeAPIBase = "/api2/json/nodes/" + nodeDefaultName

func TestNodeCmd_Tree(t *testing.T) {
	cmd := newNodeCmd()
	require.Equal(t, "node", cmd.Use)

	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}

	for _, want := range []string{
		"ls", "status", "reboot", "shutdown", "rrd", "report", "syslog", "journal",
		"dns", "time", "config", "subscription", "identity", "tasks", "services",
		"apt", "disks", "network", "certificates",
	} {
		require.True(t, names[want], "expected node sub-command %q to be registered", want)
	}
}

func TestNodeLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET /api2/json/nodes", &rec, []map[string]any{{"node": nodeDefaultName}})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/nodes", rec.path)
	require.Contains(t, buf.String(), nodeDefaultName)
}

func TestNodeLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list nodes")
}

func TestNodeStatus_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/status", &rec, map[string]any{
		"cpu": 0.5, "uptime": 12345, "kversion": "Linux 6.8", "wait": 0.1,
		"loadavg": []any{"0.1", "0.2", "0.3"},
		"memory":  map[string]any{"total": 1000, "used": 500},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "status")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, nodeAPIBase+"/status", rec.path)
	require.Contains(t, buf.String(), "12345")
}

func TestNodeStatus_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+nodeAPIBase+"/status", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "status")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get status for node")
}

func TestNodeReboot_RequiresYes(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "reboot")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes/-y")
}

func TestNodeReboot_SendsCommand(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+nodeAPIBase+"/status", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "reboot", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, "reboot", rec.form.Get("command"))
	require.Contains(t, buf.String(), "reboot initiated")
}

func TestNodeShutdown_SendsCommand(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+nodeAPIBase+"/status", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "shutdown", "-y")
	require.NoError(t, err)

	require.Equal(t, "shutdown", rec.form.Get("command"))
}

func TestNodeReboot_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+nodeAPIBase+"/status", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "reboot", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "reboot node")
}

func TestNodeRrd_RequiresValidTimeframe(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "rrd", "--timeframe", "bogus")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--timeframe")
}

func TestNodeRrd_RendersRaw(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/rrd", &rec, map[string]any{"x": []int{1, 2, 3}})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "rrd", "--timeframe", "hour")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "hour", rec.query.Get("timeframe"))
	require.Equal(t, "AVERAGE", rec.query.Get("cf"))
}

func TestNodeReport_RendersText(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/report", &rec, "system report text")

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "report")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Contains(t, buf.String(), "system report text")
}

func TestNodeCmd_HonoursNodeFlag(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET /api2/json/nodes/pbs2/status", &rec, map[string]any{
		"cpu": 0.1, "uptime": 1, "kversion": "x", "wait": 0,
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "--node", "pbs2", "status")
	require.NoError(t, err)
	require.Equal(t, "/api2/json/nodes/pbs2/status", rec.path)
}
