package pdm

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestNodeJournal_MapsFlagsToQueryParams asserts that `node journal` maps
// its flags onto the query parameters of the raw-transport GET request, and
// decodes the recovered array of lines.
func TestNodeJournal_MapsFlagsToQueryParams(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	f.HandleFunc("GET /api2/json/nodes/pdm-host/journal", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.query = r.URL.Query()
		testhelper.WriteData(w, []string{"line one", "line two"})
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeJournalCmd(), "journal", "pdm-host", "--lastentries", "50", "--since", "1000")
	require.NoError(t, err)

	require.Equal(t, "50", rec.query.Get("lastentries"))
	require.Equal(t, "1000", rec.query.Get("since"))
	require.Contains(t, buf.String(), "line one")
	require.Contains(t, buf.String(), "line two")
}

// TestNodeSyslog_MapsFlagsToQueryParams asserts that `node syslog` maps its
// flags onto the wire request and renders decoded lines.
func TestNodeSyslog_MapsFlagsToQueryParams(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/nodes/pdm-host/syslog", &rec, []map[string]any{
		{"n": 1, "t": "log line"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeSyslogCmd(), "syslog", "pdm-host", "--service", "pdm-api", "--limit", "10")
	require.NoError(t, err)

	require.Equal(t, "pdm-api", rec.query.Get("service"))
	require.Equal(t, "10", rec.query.Get("limit"))
	require.Contains(t, buf.String(), "log line")
}

// TestNodeReport_RendersText asserts that `node report` decodes and prints
// the report text.
func TestNodeReport_RendersText(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/report", "report text")

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeReportCmd(), "report", "pdm-host")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "report text")
}

// TestNodeRrddata_ValidatesTimeframe asserts that `node rrddata` validates
// --timeframe against the enum before issuing any request.
func TestNodeRrddata_ValidatesTimeframe(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeRrddataCmd(), "rrddata", "pdm-host", "--timeframe", "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, "--timeframe must be one of")
}

// TestNodeRrddata_ValidatesConsolidation asserts that `node rrddata`
// validates --cf against the enum before issuing any request.
func TestNodeRrddata_ValidatesConsolidation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeRrddataCmd(), "rrddata", "pdm-host", "--timeframe", "hour", "--cf", "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, "--cf must be one of")
}

// TestNodeRrddata_ListsDataPoints asserts that `node rrddata` renders the
// RRD data points as a table and preserves every field in Raw.
func TestNodeRrddata_ListsDataPoints(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/rrddata", []map[string]any{
		{"time": 1000, "cpu-current": 0.25, "mem-used": 512, "mem-total": 1024},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeRrddataCmd(), "rrddata", "pdm-host", "--timeframe", "hour")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "1000")
	require.Contains(t, buf.String(), "0.25")
	require.Contains(t, buf.String(), "mem-total")
}
