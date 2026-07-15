package pdm

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// TestPbsDatastoreLs_SortsByName asserts that `pbs datastore ls` sorts
// entries by datastore name and pairs each row's raw JSON through the sort.
func TestPbsDatastoreLs_SortsByName(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pbs/remotes/backup1/datastore", []map[string]any{
		{"name": "zeta-store", "path": "/mnt/zeta"},
		{"name": "alpha-store", "path": "/mnt/alpha", "comment": "primary"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsDatastoreLsCmd(), "ls", "backup1")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha-store", got[0]["name"], "entries must sort by datastore name")
	require.Equal(t, "primary", got[0]["comment"], "raw entry must stay paired with its sorted row")
	require.Equal(t, "zeta-store", got[1]["name"])
}

// TestPbsDatastoreNamespaces_SortsByNs asserts that `pbs datastore
// namespaces` sends --max-depth/--parent and sorts entries by ns.
func TestPbsDatastoreNamespaces_SortsByNs(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pbs/remotes/backup1/datastore/store1/namespaces", &rec, []map[string]any{
		{"ns": "zeta/child"},
		{"ns": "alpha"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsDatastoreNamespacesCmd(), "namespaces", "backup1", "store1",
		"--max-depth", "2", "--parent", "root")
	require.NoError(t, err)

	require.Equal(t, "2", rec.query.Get("max-depth"))
	require.Equal(t, "root", rec.query.Get("parent"))

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["ns"], "entries must sort by ns")
	require.Equal(t, "zeta/child", got[1]["ns"])
}

// TestPbsDatastoreSnapshots_SortsByCompositeKey asserts that `pbs datastore
// snapshots` sorts entries by (backup-type, backup-id, backup-time) and
// sends --ns.
func TestPbsDatastoreSnapshots_SortsByCompositeKey(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pbs/remotes/backup1/datastore/store1/snapshots", &rec, []map[string]any{
		{"backup-type": "vm", "backup-id": "200", "backup-time": 2000, "protected": false},
		{"backup-type": "ct", "backup-id": "100", "backup-time": 1000, "protected": true, "owner": "root@pam"},
		{"backup-type": "vm", "backup-id": "100", "backup-time": 3000, "protected": false},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsDatastoreSnapshotsCmd(), "snapshots", "backup1", "store1", "--ns", "prod")
	require.NoError(t, err)

	require.Equal(t, "prod", rec.query.Get("ns"))

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 3)
	require.Equal(t, "ct", got[0]["backup-type"], "entries must sort by backup-type first")
	require.Equal(t, "vm", got[1]["backup-type"])
	require.Equal(t, "100", got[1]["backup-id"], "then by backup-id")
	require.Equal(t, "vm", got[2]["backup-type"])
	require.Equal(t, "200", got[2]["backup-id"])
}

// TestPbsDatastoreRrddata_ValidatesConsolidation asserts that `pbs
// datastore rrddata` validates --cf against the enum before issuing any
// request.
func TestPbsDatastoreRrddata_ValidatesConsolidation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsDatastoreRrddataCmd(), "rrddata", "backup1", "store1",
		"--timeframe", "hour", "--cf", "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, "--cf must be one of")
}

// TestPbsDatastoreRrddata_ListsDataPoints asserts that `pbs datastore
// rrddata` renders the RRD data points as a table.
func TestPbsDatastoreRrddata_ListsDataPoints(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/pbs/remotes/backup1/datastore/store1/rrddata", []map[string]any{
		{"time": 1000, "disk-used": 500.0, "disk-total": 1000.0},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsDatastoreRrddataCmd(), "rrddata", "backup1", "store1", "--timeframe", "hour")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "1000")
	require.Contains(t, buf.String(), "500")
}
