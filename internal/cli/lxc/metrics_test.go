package lxc

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestLxcMetrics_Success verifies the metrics command reaches the rrddata
// endpoint with the required --timeframe query parameter and renders the
// returned data points.
func TestLxcMetrics_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotMethod, gotPath, gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/200/rrddata", func(w http.ResponseWriter, r *http.Request) {
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
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "metrics", "200", "--timeframe", "hour")
	require.NoError(t, run())

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/nodes/pve1/lxc/200/rrddata", gotPath)
	require.Contains(t, gotQuery, "timeframe=hour")
	require.Contains(t, buf.String(), "1700000000")
}

// TestLxcMetrics_LargeNumberRendersAsInteger pins the fix for the RRD table
// renderer: a decoded JSON number above six digits used to switch fmt's %v to
// exponent notation (e.g. "5.36870912e+08" for a 512 MiB memory reading).
// The fix routes each cell through cli.StringifyValue, which prints a whole
// float64 as a plain integer.
func TestLxcMetrics_LargeNumberRendersAsInteger(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/200/rrddata", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{
				"time":    1700000000,
				"maxmem":  536870912,
				"maxdisk": 8589934592,
			},
		})
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "metrics", "200", "--timeframe", "hour")
	require.NoError(t, run())

	out := buf.String()
	require.Contains(t, out, "536870912")
	require.Contains(t, out, "8589934592")
	require.NotContains(t, out, "e+08")
	require.NotContains(t, out, "e+09")
}

func TestLxcMetrics_MissingTimeframe(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "metrics", "200")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeframe")
}

func TestLxcMetrics_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/lxc/200/rrddata", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})
	deps := newDeps(t, f, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	run := newTestCmd(t, deps, &buf, "metrics", "200", "--timeframe", "hour")
	err := run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "metrics for container 200")
}
