package cephview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// captured loads a response captured from a live PVE 9.2 node running Ceph
// 20.2 (tentacle). These views exist because the payloads are Ceph's own and
// nest several levels deep, so a fixture written by hand would not exercise
// what the endpoints actually answer.
func captured(t *testing.T, name string) json.RawMessage {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return json.RawMessage(raw)
}

// cell returns the value of the named FIELD row of a two-column result.
func cell(t *testing.T, res output.Result, field string) string {
	t.Helper()

	for _, row := range res.Rows {
		if row[0] == field {
			return row[1]
		}
	}
	t.Fatalf("no %q row in %v", field, res.Rows)
	return ""
}

// column returns one column of every row.
func column(res output.Result, i int) []string {
	out := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		out = append(out, row[i])
	}
	return out
}

// TestStatus_SummarisesTheClusterInsteadOfFlatteningIt is the defect this
// package exists for: `pmx pve node ceph status` returned a tree the generic
// renderer wrote into one cell, 3.95 MB and 515,739 columns wide.
func TestStatus_SummarisesTheClusterInsteadOfFlatteningIt(t *testing.T) {
	res, err := Status(captured(t, "status.json"))
	require.NoError(t, err)

	assert.Equal(t, "HEALTH_OK", cell(t, res, "health"))
	assert.Equal(t, "6cc5bd83-cfe8-4fd5-88d4-63fabe93f424", cell(t, res, "fsid"))
	assert.Contains(t, cell(t, res, "mons"), "3, quorum lab-ceph-0, lab-ceph-1, lab-ceph-2")
	assert.Contains(t, cell(t, res, "mgr"), "lab-ceph-0 (active)")
	assert.Contains(t, cell(t, res, "mgr"), "standbys: lab-ceph-1, lab-ceph-2")
	assert.Equal(t, "6 up / 6 in / 6 total", cell(t, res, "osds"))
	assert.Equal(t, "97 (97 active+clean)", cell(t, res, "pgs"))
	assert.Equal(t, "4", cell(t, res, "pools"))
	assert.Contains(t, cell(t, res, "usage"), "GiB total")
	assert.NotNil(t, res.Raw, "-o json must still carry the whole payload")
}

// TestStatus_EmptyBodyIsNotAHealthyCluster covers a response that decodes
// cleanly into all-zero fields, which would otherwise read as a healthy
// cluster with nothing in it.
func TestStatus_EmptyBodyIsNotAHealthyCluster(t *testing.T) {
	res, err := Status(json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Equal(t, "(no status reported)", cell(t, res, "health"))
}

// TestStatus_ReportsFailingHealthChecks covers what an operator reading
// HEALTH_WARN needs next: which check is failing, and why.
func TestStatus_ReportsFailingHealthChecks(t *testing.T) {
	res, err := Status(json.RawMessage(`{
		"health": {"status": "HEALTH_WARN", "checks": {
			"POOL_NO_REDUNDANCY": {"severity": "HEALTH_WARN",
				"summary": {"message": "1 pool(s) have no replicas configured"}}}}}`))
	require.NoError(t, err)

	assert.Equal(t, "HEALTH_WARN", cell(t, res, "health"))
	assert.Equal(t, "HEALTH_WARN: 1 pool(s) have no replicas configured",
		cell(t, res, "  POOL_NO_REDUNDANCY"))
}

// TestStatus_MdsRowIsOmittedWithoutAFilesystem covers a cluster with no
// CephFS, where an mds row would be a line of zeroes.
func TestStatus_MdsRowIsOmittedWithoutAFilesystem(t *testing.T) {
	res, err := Status(json.RawMessage(`{"health": {"status": "HEALTH_OK"}}`))
	require.NoError(t, err)

	for _, row := range res.Rows {
		assert.NotEqual(t, "mds", row[0])
	}
}

// TestOSDTree_KeepsTheHierarchy covers the tree the generic renderer showed
// as "root children=[{7 fields}]": every bucket and OSD becomes a row, and
// the nesting survives as an indent.
func TestOSDTree_KeepsTheHierarchy(t *testing.T) {
	res, err := OSDTree(captured(t, "osd_tree.json"))
	require.NoError(t, err)

	names := column(res, 0)
	assert.Equal(t, "default", names[0], "the root sits flush left")
	assert.Contains(t, names, "· lab-ceph-0", "a host is indented under its root")
	assert.Contains(t, names, "·· osd.0", "an OSD is indented under its host")

	// The indent uses a visible glyph because the renderer trims a cell, and
	// leading whitespace would flatten the tree back out.
	for _, n := range names {
		assert.False(t, strings.HasPrefix(n, " "), "%q must not lead with whitespace", n)
	}
}

// TestOSDTree_ReportsPerOSDStateAndCapacity covers the columns the command is
// actually read for.
func TestOSDTree_ReportsPerOSDStateAndCapacity(t *testing.T) {
	res, err := OSDTree(captured(t, "osd_tree.json"))
	require.NoError(t, err)

	var found bool
	for _, row := range res.Rows {
		if row[0] != "·· osd.0" {
			continue
		}
		found = true
		assert.Equal(t, "osd", row[1])
		assert.Equal(t, "0", row[2])
		assert.Equal(t, "ssd", row[3])
		assert.Equal(t, "up", row[4])
		assert.Equal(t, "in", row[5])
		assert.Equal(t, "100.0 GiB", row[10])
		assert.Equal(t, "20.2.2", row[12], "the version banner is trimmed to its number")
	}
	require.True(t, found, "osd.0 must be in the tree")
}

// TestOSDTree_BucketsLeaveOSDOnlyColumnsBlank covers the zeroes PVE sends for
// fields a bucket does not have, which read as data rather than as absence.
func TestOSDTree_BucketsLeaveOSDOnlyColumnsBlank(t *testing.T) {
	res, err := OSDTree(captured(t, "osd_tree.json"))
	require.NoError(t, err)

	for _, row := range res.Rows {
		if row[1] == "osd" {
			continue
		}
		assert.Empty(t, row[5], "%s must not report in/out", row[0])
		assert.Empty(t, row[6], "%s must not report a CRUSH weight", row[0])
		assert.Empty(t, row[8], "%s must not report a pg count", row[0])
	}
}

// TestPools_ReducesSeventeenColumnsToWhatIsRead covers a payload whose
// autoscale report alone carries twenty fields, and which the generic
// renderer spread over seventeen truncated columns.
func TestPools_ReducesSeventeenColumnsToWhatIsRead(t *testing.T) {
	res, err := Pools(captured(t, "pools.json"))
	require.NoError(t, err)

	require.Len(t, res.Rows, 4)
	assert.Equal(t, []string{".mgr", "labrbd", "cephfs_data", "cephfs_metadata"}, column(res, 0))

	for _, row := range res.Rows {
		if row[0] != "labrbd" {
			continue
		}
		assert.Equal(t, "replicated", row[2])
		assert.Equal(t, "3", row[3])
		assert.Equal(t, "2", row[4])
		assert.Equal(t, "32", row[5])
		assert.Equal(t, "on", row[6])
		assert.Equal(t, "replicated_rule", row[7])
		assert.Equal(t, "rbd", row[8], "the application map's key is the whole of the information")
	}
}

// TestPools_NamesAnAutoscalerTarget covers a pool the autoscaler is moving,
// where the current placement-group count alone is misleading.
func TestPools_NamesAnAutoscalerTarget(t *testing.T) {
	res, err := Pools(captured(t, "pools.json"))
	require.NoError(t, err)

	for _, row := range res.Rows {
		if row[0] == "cephfs_metadata" {
			assert.Equal(t, "32 → 16", row[5])
		}
	}
}

// TestOSDMetadata_PairsTheDaemonWithItsDevices covers what the command is
// asked for and what a nested object hid completely: the OSD and the disk
// underneath it.
func TestOSDMetadata_PairsTheDaemonWithItsDevices(t *testing.T) {
	res, err := OSDMetadata(captured(t, "osd_metadata.json"))
	require.NoError(t, err)

	assert.Equal(t, "osd.0", cell(t, res, "osd"))
	assert.Equal(t, "lab-ceph-0", cell(t, res, "host"))
	assert.Equal(t, "bluestore", cell(t, res, "objectstore"))
	assert.Equal(t, "no", cell(t, res, "encrypted"))
	assert.Contains(t, cell(t, res, "device block"), "/dev/dm-8")
	assert.Contains(t, cell(t, res, "device block"), "100.0 GiB")
}

// TestClusterMetadata_OneRowPerDaemon covers the endpoint that returns forty
// fields for each of a cluster's daemons, keyed by "name@host".
func TestClusterMetadata_OneRowPerDaemon(t *testing.T) {
	res, err := ClusterMetadata(captured(t, "cluster_metadata.json"))
	require.NoError(t, err)

	kinds := column(res, 0)
	assert.Contains(t, kinds, "mon")
	assert.Contains(t, kinds, "mgr")
	assert.Contains(t, kinds, "mds")
	assert.Contains(t, kinds, "osd")
	assert.Contains(t, kinds, "node")

	for _, row := range res.Rows {
		if row[0] == "osd" && row[1] == "osd.0" {
			assert.Equal(t, "lab-ceph-0", row[2])
			assert.Equal(t, "20.2.2", row[3], "the version banner is trimmed to its number")
			assert.Equal(t, "tentacle", row[4])
		}
	}
}

// TestBytesCell_ReadsAsCapacity pins the byte formatting every capacity
// column depends on. Ceph reports bytes, and 644219928576 tells nobody
// anything.
func TestBytesCell_ReadsAsCapacity(t *testing.T) {
	assert.Equal(t, "512 B", bytesCell(512))
	assert.Equal(t, "1.0 KiB", bytesCell(1024))
	assert.Equal(t, "100.0 GiB", bytesCell(107374182400))
	assert.Equal(t, "600.0 GiB", bytesCell(644219928576))
}

// TestAgeCell_IsCoarse pins the quorum age format, where the exact second has
// never mattered.
func TestAgeCell_IsCoarse(t *testing.T) {
	assert.Equal(t, "", ageCell(0))
	assert.Equal(t, "45s", ageCell(45))
	assert.Equal(t, "5m", ageCell(300))
	assert.Equal(t, "2h", ageCell(7200))
	assert.Equal(t, "3d", ageCell(259200))
}

// TestMonList_DropsTheRepeatedVersionBannerAndTheAllYesFlags covers the
// monitor table, which reported the version twice per row (the full banner
// beside the number it contains) and spent two more columns on flags that
// read "yes" on every daemon of a working cluster.
func TestMonList_DropsTheRepeatedVersionBannerAndTheAllYesFlags(t *testing.T) {
	res, err := MonList(captured(t, "mon_list.json"))
	require.NoError(t, err)

	assert.Equal(t, monHeaders, res.Headers)
	require.Len(t, res.Rows, 3)
	assert.Equal(t, []string{"lab-ceph-0", "lab-ceph-1", "lab-ceph-2"}, column(res, 0))
	assert.Equal(t, []string{"0", "1", "2"}, column(res, 3))
	assert.Equal(t, []string{"yes", "yes", "yes"}, column(res, 4))
	assert.Equal(t, []string{"20.2.2", "20.2.2", "20.2.2"}, column(res, 6))
	assert.Equal(t, []string{"", "", ""}, column(res, 7), "a healthy daemon has nothing to note")
	for _, row := range res.Rows {
		for _, c := range row {
			assert.NotContains(t, c, "ceph version", "the banner belongs in -o json")
		}
	}
	assert.NotNil(t, res.Raw)
}

// TestMdsList_NamesTheRankAndFilesystem checks that a standby, which holds
// rank -1, leaves the column blank rather than reporting a negative rank.
func TestMdsList_NamesTheRankAndFilesystem(t *testing.T) {
	res, err := MdsList(captured(t, "mds_list.json"))
	require.NoError(t, err)

	assert.Equal(t, mdsHeaders, res.Headers)
	require.Len(t, res.Rows, 2)
	assert.Equal(t, []string{"up:active", "up:standby"}, column(res, 2))
	assert.Equal(t, []string{"0", ""}, column(res, 3))
	assert.Equal(t, []string{"cephfs", ""}, column(res, 4))
}

// TestMgrList_ReportsWhichManagerIsActive covers the manager table, which
// carries neither a rank nor a quorum.
func TestMgrList_ReportsWhichManagerIsActive(t *testing.T) {
	res, err := MgrList(captured(t, "mgr_list.json"))
	require.NoError(t, err)

	assert.Equal(t, mgrHeaders, res.Headers)
	require.Len(t, res.Rows, 3)
	assert.Equal(t, []string{"active", "standby", "standby"}, column(res, 2))
	assert.Equal(t, []string{"20.2.2", "20.2.2", "20.2.2"}, column(res, 4))
}

// TestDaemonNotes_ReportsOnlyWhatIsWrong is why the flags are a notes column:
// they carry information exactly when they are false.
func TestDaemonNotes_ReportsOnlyWhatIsWrong(t *testing.T) {
	res, err := MonList(json.RawMessage(`[
		{"name": "pve1", "host": "pve1", "state": "running", "direxists": false, "service": false},
		{"name": "pve2", "host": "pve2", "state": "running", "direxists": true, "service": true}
	]`))
	require.NoError(t, err)

	require.Len(t, res.Rows, 2)
	assert.Equal(t, "no systemd unit, no data directory", res.Rows[0][7])
	assert.Empty(t, res.Rows[1][7])
}

// TestDaemonList_MissingFlagsAreNotFailures covers a payload that omits the
// flags entirely, which must not read as a daemon with nothing installed.
func TestDaemonList_MissingFlagsAreNotFailures(t *testing.T) {
	res, err := MonList(json.RawMessage(`[{"name": "pve1", "host": "pve1", "state": "running"}]`))
	require.NoError(t, err)

	require.Len(t, res.Rows, 1)
	assert.Empty(t, res.Rows[0][4], "an unreported quorum is blank, not a no")
	assert.Empty(t, res.Rows[0][7])
}

// TestFSList_PairsEachPoolWithItsID covers the filesystem table, where the
// data pool arrived as a scalar, as a list, and as a parallel array of ids.
func TestFSList_PairsEachPoolWithItsID(t *testing.T) {
	res, err := FSList(captured(t, "fs_list.json"))
	require.NoError(t, err)

	assert.Equal(t, fsHeaders, res.Headers)
	require.Len(t, res.Rows, 1)
	assert.Equal(t, []string{"cephfs", "cephfs_metadata (4)", "cephfs_data (3)"}, res.Rows[0])
}

// TestFSList_ExtraDataPoolWithoutAnID covers a filesystem whose id array is
// shorter than its pool list, which must still name every pool.
func TestFSList_ExtraDataPoolWithoutAnID(t *testing.T) {
	res, err := FSList(json.RawMessage(`[{
		"name": "cephfs", "metadata_pool": "meta", "metadata_pool_id": 4,
		"data_pools": ["a", "b"], "data_pool_ids": [3]
	}]`))
	require.NoError(t, err)

	require.Len(t, res.Rows, 1)
	assert.Equal(t, "a (3), b", res.Rows[0][2])
}

// TestDaemonList_EmptyListRendersAnEmptyTable covers a node that has no
// daemon of the kind at all.
func TestDaemonList_EmptyListRendersAnEmptyTable(t *testing.T) {
	res, err := MgrList(json.RawMessage(`[]`))
	require.NoError(t, err)

	assert.Equal(t, mgrHeaders, res.Headers)
	assert.Empty(t, res.Rows)
}
