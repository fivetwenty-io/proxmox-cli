package qemu

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// --- metrics ----------------------------------------------------------------

func TestQemuMetrics_Success(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/rrddata", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		testhelper.WriteData(w, []any{
			map[string]any{
				"time":   1700000000,
				"cpu":    0.25,
				"mem":    1073741824,
				"maxmem": 4294967296,
				"netin":  1234,
				"netout": 5678,
			},
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "metrics", "100", "--timeframe", "hour"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/rrddata", gotPath)
	require.Contains(t, gotQuery, "timeframe=hour")
	out := buf.String()
	require.Contains(t, out, "1700000000")
}

func TestQemuMetrics_WithCF(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/rrddata", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []any{})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "metrics", "100", "--timeframe", "day", "--cf", "MAX"))
	require.Contains(t, gotQuery, "cf=MAX")
}

func TestQemuMetrics_OmitCFWhenUnset(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/rrddata", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []any{})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "metrics", "100", "--timeframe", "hour"))
	require.NotContains(t, gotQuery, "cf=")
}

// TestQemuMetricsRrd_RequiredFlags consolidates shape-1 (flag-required) cases
// for the metrics and rrd commands. Each case omits a required flag and expects
// the flag name (lowercased) in the error. No HTTP handler is registered.
func TestQemuMetricsRrd_RequiredFlags(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantContain string // matched via strings.ToLower(err.Error())
	}{
		{
			name:        "metrics missing timeframe",
			args:        []string{"metrics", "100"},
			wantContain: "timeframe",
		},
		{
			name:        "rrd missing ds",
			args:        []string{"rrd", "100", "--timeframe", "hour"},
			wantContain: "ds",
		},
		{
			name:        "rrd missing timeframe",
			args:        []string{"rrd", "100", "--ds", "cpu"},
			wantContain: "timeframe",
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

func TestQemuMetrics_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/rrddata", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "metrics", "100", "--timeframe", "hour")
	require.Error(t, err)
	require.Contains(t, err.Error(), "metrics for VM 100")
}

// TestQemuMetrics_RequiresNode consolidates shape-3 (node-required) cases for
// the metrics command. Each case runs with an empty node and expects "no node"
// in the error; no HTTP handler is registered.
func TestQemuMetrics_RequiresNode(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{
			name: "metrics",
			args: []string{"metrics", "100", "--timeframe", "hour"},
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

func TestQemuMetrics_CommandTree(t *testing.T) {
	root := Group(nil)
	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["metrics"], "expected top-level sub-command 'metrics'")
}

// --- rrd --------------------------------------------------------------------

func TestQemuRrd_Success(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod, gotPath, gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/rrd", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{
			"filename": "/var/lib/rrdcached/db/pve2-node/100/cpu.png",
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "rrd", "100", "--ds", "cpu", "--timeframe", "hour"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/rrd", gotPath)
	require.Contains(t, gotQuery, "ds=cpu")
	require.Contains(t, gotQuery, "timeframe=hour")
	require.Contains(t, buf.String(), "filename")
}

func TestQemuRrd_WithCF(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/rrd", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, map[string]any{"filename": "/tmp/x.png"})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "rrd", "100", "--ds", "cpu", "--timeframe", "day", "--cf", "AVERAGE"))
	require.Contains(t, gotQuery, "cf=AVERAGE")
}

func TestQemuRrd_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/rrd", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	err := run(deps, &buf, "rrd", "100", "--ds", "cpu", "--timeframe", "hour")
	require.Error(t, err)
	require.Contains(t, err.Error(), "rrd for VM 100")
}

func TestQemuRrd_CommandTree(t *testing.T) {
	root := Group(nil)
	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["rrd"], "expected top-level sub-command 'rrd'")
}
