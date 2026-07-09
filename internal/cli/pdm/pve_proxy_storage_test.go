package pdm

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestPveStorageLs_SortsByStorage asserts that `pve storage ls` sorts
// entries by storage name and keeps each row's raw JSON paired through the
// sort.
func TestPveStorageLs_SortsByStorage(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/nodes/pve1/storage", []map[string]any{
		{"storage": "zeta-store", "type": "dir"},
		{"storage": "alpha-store", "type": "dir"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveStorageLsCmd(), "ls", "cluster1", "pve1")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha-store", got[0]["storage"], "entries must sort by storage name")
	require.Equal(t, "zeta-store", got[1]["storage"])
}

// TestPveStorageLs_ValidatesContent asserts that `pve storage ls` validates
// --content against the enum before issuing any request.
func TestPveStorageLs_ValidatesContent(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveStorageLsCmd(), "ls", "cluster1", "pve1", "--content", "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, "--content must be one of")
}

// TestPveStorageLs_SendsFilters asserts that `pve storage ls` forwards its
// filter flags on the wire.
func TestPveStorageLs_SendsFilters(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pve/remotes/cluster1/nodes/pve1/storage", &rec, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveStorageLsCmd(), "ls", "cluster1", "pve1",
		"--content", "images", "--enabled", "--storage", "local")
	require.NoError(t, err)

	require.Equal(t, []string{"images"}, rec.query["content"])
	require.Equal(t, "1", rec.query.Get("enabled"))
	require.Equal(t, "local", rec.query.Get("storage"))
}

// TestPveStorageStatus_RendersSingle asserts that `pve storage status`
// renders a specific storage's status fields.
func TestPveStorageStatus_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/nodes/pve1/storage/local/status", map[string]any{
		"type": "dir", "total": 1000000, "used": 500000,
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveStorageStatusCmd(), "status", "cluster1", "pve1", "local")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "500000")
}

// TestPveStorageRrddata_ValidatesTimeframe asserts that `pve storage
// rrddata` validates --timeframe against the enum before issuing any
// request.
func TestPveStorageRrddata_ValidatesTimeframe(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveStorageRrddataCmd(), "rrddata", "cluster1", "pve1", "local", "--timeframe", "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, "--timeframe must be one of")
}

// TestPveStorageRrddata_ListsDataPoints asserts that `pve storage rrddata`
// renders RRD data points in server order (not sorted).
func TestPveStorageRrddata_ListsDataPoints(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/nodes/pve1/storage/local/rrddata", []map[string]any{
		{"time": 2000, "disk-used": 400.0},
		{"time": 1000, "disk-used": 100.0},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveStorageRrddataCmd(), "rrddata", "cluster1", "pve1", "local", "--timeframe", "hour")
	require.NoError(t, err)

	rows := buf.String()
	require.Less(t, strings.Index(rows, "2000"), strings.Index(rows, "1000"),
		"rrddata rows must preserve server order, not be sorted")
}
