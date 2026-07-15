package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestMetricsServer_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/cluster/metrics/server", []any{
		map[string]any{"id": "graf", "type": "graphite", "server": "10.0.0.9", "port": 2003, "disable": 0},
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "metrics", "server", "list"))
	out := buf.String()
	require.Contains(t, out, "TYPE")
	require.Contains(t, out, "SERVER")
	require.Contains(t, out, "graphite")
	require.Contains(t, out, "10.0.0.9")
}

func TestMetricsServer_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/metrics/server/graf", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{"type": "graphite", "server": "10.0.0.9", "port": 2003})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "metrics", "server", "get", "graf"))
	require.Equal(t, "/api2/json/cluster/metrics/server/graf", gotPath)
	require.Contains(t, buf.String(), "10.0.0.9")
}

// TestMetricsServer_ListTokenNotEchoed verifies the InfluxDB access token is
// stripped from `metrics server list` output, including the Raw payload
// backing -o json/yaml (the table columns already omit it).
func TestMetricsServer_ListTokenNotEchoed(t *testing.T) {
	f, ac := newFakeClient(t)
	const secret = "s3cr3ttoken"
	f.HandleJSON("GET /api2/json/cluster/metrics/server", []any{
		map[string]any{"id": "influx", "type": "influxdb", "server": "10.0.0.9", "port": 8086, "token": secret},
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatJSON}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "metrics", "server", "list"))
	require.NotContains(t, buf.String(), secret, "token must never be echoed to output")
}

// TestMetricsServer_GetTokenNotEchoed verifies the InfluxDB access token
// returned by GET /cluster/metrics/server/{id} is stripped from `get` output.
func TestMetricsServer_GetTokenNotEchoed(t *testing.T) {
	f, ac := newFakeClient(t)
	const secret = "s3cr3ttoken"
	f.HandleFunc("GET /api2/json/cluster/metrics/server/influx", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"type": "influxdb", "server": "10.0.0.9", "port": 8086, "token": secret,
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatJSON}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "metrics", "server", "get", "influx"))
	require.NotContains(t, buf.String(), secret, "token must never be echoed to output")
}

func TestMetricsServer_CreateForwardsRequiredOmitsUnset(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	var gotMethod string
	f.HandleFunc("POST /api2/json/cluster/metrics/server/graf", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "metrics", "server", "create", "graf",
		"--type", "graphite", "--server", "10.0.0.9", "--port", "2003", "--proto", "udp"))
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "graphite", gotForm.Get("type"))
	require.Equal(t, "10.0.0.9", gotForm.Get("server"))
	require.Equal(t, "2003", gotForm.Get("port"))
	require.Equal(t, "udp", gotForm.Get("proto"))
	// Unset optionals must be omitted from the request body.
	require.NotContains(t, gotForm, "path")
	require.NotContains(t, gotForm, "mtu")
}

// TestMetricsServer_CreateTokenNotEchoed verifies the InfluxDB access token is
// forwarded to the API but never written to command output.
func TestMetricsServer_CreateTokenNotEchoed(t *testing.T) {
	f, ac := newFakeClient(t)
	const secret = "s3cr3ttoken"
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/metrics/server/influx", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "metrics", "server", "create", "influx",
		"--type", "influxdb", "--server", "10.0.0.9", "--port", "8086", "--token", secret))
	require.Equal(t, secret, gotForm.Get("token"), "token must reach the API")
	require.NotContains(t, buf.String(), secret, "token must never be echoed to output")
}

func TestMetricsServer_SetForwardsChanged(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	var gotMethod string
	f.HandleFunc("PUT /api2/json/cluster/metrics/server/graf", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "metrics", "server", "set", "graf",
		"--server", "10.0.0.9", "--port", "2003", "--disable"))
	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "10.0.0.9", gotForm.Get("server"))
	require.Equal(t, "1", gotForm.Get("disable"))
	require.NotContains(t, gotForm, "path")
}

func TestMetricsServer_DeleteRequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("DELETE /api2/json/cluster/metrics/server/graf", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "metrics", "server", "delete", "graf")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestMetricsServer_DeleteWithYes(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod string
	f.HandleFunc("DELETE /api2/json/cluster/metrics/server/graf", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "metrics", "server", "delete", "graf", "--yes"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Contains(t, buf.String(), "deleted")
}

func TestMetricsExport_QueryParams(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/cluster/metrics/export", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{"data": []any{
			map[string]any{"metric": "cpu", "id": "node/pve", "value": 0.1, "timestamp": 1700000000},
		}})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "metrics", "export", "--history", "--local-only"))
	require.Contains(t, gotQuery, "history=1")
	require.Contains(t, gotQuery, "local-only=1")
	out := buf.String()
	require.Contains(t, out, "METRIC")
	require.Contains(t, out, "cpu")
}

func TestMetricsExport_OmitsUnsetParams(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/cluster/metrics/export", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{"data": []any{}})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "metrics", "export"))
	require.NotContains(t, gotQuery, "history")
	require.NotContains(t, gotQuery, "start-time")
}

func TestMetricsCommandTree(t *testing.T) {
	root := Group(&cli.Deps{})
	metrics := childCommands(root)["metrics"]
	require.NotNil(t, metrics)
	mc := childCommands(metrics)
	server := mc["server"]
	require.NotNil(t, server)
	require.NotNil(t, mc["export"])
	sc := childCommands(server)
	for _, v := range []string{"list", "get", "create", "set", "delete"} {
		require.NotNil(t, sc[v], "metrics server must expose %q", v)
	}
}
