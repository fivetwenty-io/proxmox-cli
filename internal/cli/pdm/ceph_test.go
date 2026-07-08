package pdm

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestCephLs_SortsByClusterAndRendersOptionalFields asserts that `ceph ls`
// sorts entries by cluster and renders the optional numeric/state columns
// straight off the raw response, tolerant of fields absent on some entries.
func TestCephLs_SortsByClusterAndRendersOptionalFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/ceph/clusters", []map[string]any{
		{
			"cluster": "zeta", "display-name": "Zeta Cluster", "state": "ok",
			"health": "HEALTH_OK", "member-count": 3, "osds-up": 6, "osds-total": 6,
			"mons-in-quorum": 3, "mons-total": 3, "remote": "pve1", "node": "pve1",
		},
		{"cluster": "alpha", "display-name": "Alpha Cluster", "state": "ok", "member-count": 1},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newCephLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["cluster"])
	require.Equal(t, "zeta", got[1]["cluster"])
	require.Contains(t, buf.String(), "HEALTH_OK")
}

// TestCephStatus_SendsMaxAgeAndRendersRawJSON asserts that `ceph status`
// includes --max-age in the request path and renders the dynamic status
// blob as raw JSON, since the response shape has no fixed schema.
func TestCephStatus_SendsMaxAgeAndRendersRawJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/ceph/clusters/fsid1/status", &rec, map[string]any{"health": map[string]any{"status": "HEALTH_OK"}})

	var buf bytes.Buffer
	err := run(deps, &buf, newCephStatusCmd(), "status", "fsid1", "--max-age", "30")
	require.NoError(t, err)

	require.Contains(t, rec.path, "fsid1")
	require.Equal(t, "30", rec.query.Get("max-age"))
	require.Contains(t, buf.String(), "HEALTH_OK")
}

// TestCephSummary_SendsClusterInPathAndRendersSingle asserts that `ceph
// summary` addresses the given cluster and renders the typed summary fields.
func TestCephSummary_SendsClusterInPathAndRendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/ceph/clusters/fsid1/summary", &rec, map[string]any{
		"bytes-avail": 1000, "bytes-total": 2000, "bytes-used": 1000,
		"fsid": "fsid1", "health": "HEALTH_OK",
		"mons-in-quorum": 3, "mons-total": 3, "num-pgs": 64, "num-pools": 2,
		"osds-in": 6, "osds-total": 6, "osds-up": 6,
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newCephSummaryCmd(), "summary", "fsid1")
	require.NoError(t, err)

	require.Contains(t, rec.path, "fsid1")
	require.Contains(t, buf.String(), "HEALTH_OK")
}

// TestCephFlags_SortsByNameAndRendersValue asserts that `ceph flags` sorts
// by name and renders the boolean value column.
func TestCephFlags_SortsByNameAndRendersValue(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/ceph/clusters/fsid1/flags", []map[string]any{
		{"name": "noout", "value": true, "description": "OSDs will not be marked out"},
		{"name": "nobackfill", "value": false, "description": "backfill is disabled"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newCephFlagsCmd(), "flags", "fsid1")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "nobackfill", got[0]["name"])
	require.Equal(t, "noout", got[1]["name"])
}

// TestCephFs_SendsClusterInPath asserts that `ceph fs` addresses the given
// cluster in the request path.
func TestCephFs_SendsClusterInPath(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/ceph/clusters/fsid1/fs", &rec, []map[string]any{
		{"name": "cephfs", "data_pool": "cephfs_data", "metadata_pool": "cephfs_metadata", "metadata_pool_id": 2},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newCephFsCmd(), "fs", "fsid1")
	require.NoError(t, err)

	require.Contains(t, rec.path, "fsid1")
	require.Contains(t, buf.String(), "cephfs_data")
}

// TestCephMds_SortsByName asserts that `ceph mds` sorts entries by name.
func TestCephMds_SortsByName(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/ceph/clusters/fsid1/mds", []map[string]any{
		{"name": "mds-b", "state": "up:active"},
		{"name": "mds-a", "state": "up:standby"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newCephMdsCmd(), "mds", "fsid1")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "mds-a", got[0]["name"])
	require.Equal(t, "mds-b", got[1]["name"])
}

// TestCephMgr_SendsClusterInPath asserts that `ceph mgr` addresses the given cluster.
func TestCephMgr_SendsClusterInPath(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/ceph/clusters/fsid1/mgr", &rec, []map[string]any{
		{"name": "mgr-a", "state": "active"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newCephMgrCmd(), "mgr", "fsid1")
	require.NoError(t, err)
	require.Contains(t, rec.path, "fsid1")
}

// TestCephMon_RendersQuorumColumn asserts that `ceph mon` renders the
// optional quorum boolean column.
func TestCephMon_RendersQuorumColumn(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/ceph/clusters/fsid1/mon", []map[string]any{
		{"name": "mon-a", "state": "leader", "quorum": true, "rank": 0},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newCephMonCmd(), "mon", "fsid1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "true")
}

// TestCephOsdTree_RendersRawJSON asserts that `ceph osd-tree` addresses the
// given cluster and renders the dynamic CRUSH tree as raw JSON.
func TestCephOsdTree_RendersRawJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/ceph/clusters/fsid1/osd-tree", &rec, map[string]any{
		"nodes": []map[string]any{{"id": -1, "name": "default", "type": "root"}},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newCephOsdTreeCmd(), "osd-tree", "fsid1")
	require.NoError(t, err)

	require.Contains(t, rec.path, "fsid1")
	require.Contains(t, buf.String(), "default")
}

// TestCephPools_SortsByPoolName asserts that `ceph pools` sorts entries by
// pool name and renders the numeric columns.
func TestCephPools_SortsByPoolName(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/ceph/clusters/fsid1/pools", []map[string]any{
		{
			"pool": 2, "pool_name": "zpool", "type": "replicated", "size": 3, "min_size": 2, "pg_num": 32,
			"percent_used": 12.5, "bytes_used": 1000,
		},
		{"pool": 1, "pool_name": "apool", "type": "replicated", "size": 3, "min_size": 2, "pg_num": 32},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newCephPoolsCmd(), "pools", "fsid1")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "apool", got[0]["pool_name"])
	require.Equal(t, "zpool", got[1]["pool_name"])
}
