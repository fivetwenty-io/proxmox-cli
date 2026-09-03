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

// TestNodeJournal_ForwardsFilterFlags asserts that `node journal` maps
// --priority, --service, --unit, and --kernel onto the query parameters of
// the typed (non-structured) request.
func TestNodeJournal_ForwardsFilterFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/nodes/pdm-host/journal", &rec, []string{"line"})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeJournalCmd(), "journal", "pdm-host",
		"--priority", "0..2", "--service", "proxmox-datacenter*", "--unit", "proxmox-datacenter-api", "--kernel")
	require.NoError(t, err)

	require.Equal(t, "0..2", rec.query.Get("priority"))
	require.Equal(t, "proxmox-datacenter*", rec.query.Get("service"))
	require.Equal(t, "proxmox-datacenter-api", rec.query.Get("unit"))
	require.Equal(t, "1", rec.query.Get("kernel"))
	require.Empty(t, rec.query.Get("structured"))
}

// TestNodeJournal_StructuredUsesRawTransport asserts that --structured routes
// the request through the raw transport and renders the decoded records as a
// table, skipping cursor markers.
func TestNodeJournal_StructuredUsesRawTransport(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/nodes/pdm-host/journal", &rec, []map[string]any{
		{"c": "s=85fd;i=1f2a53", "ty": "cursor"},
		{"id": "proxmox-datacenter-api", "msg": "ready", "p": 5, "pid": 900, "t": 1725000000123456},
		{"c": "s=85fd;i=1f2a54", "ty": "cursor"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeJournalCmd(), "journal", "pdm-host", "--structured", "--lastentries", "3")
	require.NoError(t, err)

	require.Equal(t, "1", rec.query.Get("structured"))
	require.Equal(t, "3", rec.query.Get("lastentries"))
	require.Contains(t, buf.String(), "TIMESTAMP")
	require.Contains(t, buf.String(), "2024-08-30T06:40:00Z")
	require.Contains(t, buf.String(), "ready")
	require.NotContains(t, buf.String(), "s=85fd", "cursor markers are not rows")
}

// TestNodeJournal_StructuredEscapesTheNodeSegment asserts that the node
// argument is escaped into the URL path so a node name carrying '#' cannot
// alter the endpoint.
func TestNodeJournal_StructuredEscapesTheNodeSegment(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	// The fake routes on the decoded r.URL.Path, so the pattern carries the
	// literal '#'. An unescaped '#' on the wire would be parsed as a fragment,
	// the request would arrive as GET /api2/json/nodes/pdm, and the fake would
	// answer 404.
	recordJSON(f, "GET /api2/json/nodes/pdm#x/journal", &rec, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeJournalCmd(), "journal", "pdm#x", "--structured")
	require.NoError(t, err)
	require.Equal(t, "1", rec.query.Get("structured"))
}

// TestNodeJournal_RejectsIdentifiersWithoutStructured asserts that
// --identifiers without --structured fails validation before issuing a
// request.
func TestNodeJournal_RejectsIdentifiersWithoutStructured(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeJournalCmd(), "journal", "pdm-host", "--identifiers")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--identifiers requires --structured")
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
