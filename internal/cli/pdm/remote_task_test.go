package pdm

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestRemoteTaskLs_ListsTasks asserts that `remote task ls` renders the
// cached task list returned by GET /remotes/tasks/list.
func TestRemoteTaskLs_ListsTasks(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/remotes/tasks/list", []map[string]any{
		{
			"upid": validUPID, "node": "pdm-host", "pid": 100, "pstart": 1,
			"starttime": 1700000000, "status": "OK", "user": "root@pam",
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteTaskLsCmd(), "ls")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
}

// TestRemoteTaskLs_RejectsInvalidStatus asserts that `remote task ls`
// validates --status against the enum before issuing any request.
func TestRemoteTaskLs_RejectsInvalidStatus(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteTaskLsCmd(), "ls", "--status", "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, "--status must be one of")
}

// TestRemoteTaskRefresh_AsyncPrintsUPID asserts that with the root --async
// flag set, `remote task refresh` prints the UPID immediately and forwards
// the repeatable --remote flag as the "remotes" form field.
func TestRemoteTaskRefresh_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, true)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/remotes/tasks/refresh", &rec, validUPID)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteTaskRefreshCmd(), "refresh", "--remote", "alpha", "--remote", "beta")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.Equal(t, []string{"alpha", "beta"}, rec.form["remotes"])
}

// TestRemoteTaskRefresh_BlocksForCompletion asserts that without --async,
// `remote task refresh` blocks until the task completes and prints the
// success message instead of the raw UPID.
func TestRemoteTaskRefresh_BlocksForCompletion(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	handleTaskStatus(f, validUPID)

	f.HandleJSON("POST /api2/json/remotes/tasks/refresh", validUPID)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteTaskRefreshCmd(), "refresh")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "Remote task cache refreshed.")
	require.NotContains(t, buf.String(), validUPID)
}

// TestRemoteTaskStatistics_RendersSingle asserts that `remote task
// statistics` renders the by-remote/by-type breakdown.
func TestRemoteTaskStatistics_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/remotes/tasks/statistics", map[string]any{
		"by-remote": map[string]any{"alpha": map[string]any{"ok": 3}},
		"by-type":   map[string]any{"qmstart": map[string]any{"ok": 1}},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteTaskStatisticsCmd(), "statistics")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "by-remote")
	require.Contains(t, buf.String(), "by-type")
}

// TestRemoteUpdatesSummary_ListsPerRemoteRows asserts that `remote updates
// summary` decodes the nested remotes map into one typed row per remote,
// sorted by name, with NODES/UPDATES aggregated from each remote's nodes map.
func TestRemoteUpdatesSummary_ListsPerRemoteRows(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/remotes/updates/summary", map[string]any{
		"remotes": map[string]any{
			"zeta": map[string]any{
				"remote-type": "pve", "status": "unknown", "nodes": map[string]any{},
			},
			"alpha": map[string]any{
				"remote-type": "pve", "status": "success",
				"nodes": map[string]any{
					"pve1": map[string]any{"number-of-updates": 2, "last-refresh": 1752000000, "status": "success"},
					"pve2": map[string]any{"number-of-updates": 1, "last-refresh": 1752000001, "status": "success"},
				},
			},
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteUpdatesSummaryCmd(), "summary")
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "REMOTE")
	require.Contains(t, out, "TYPE")
	require.Contains(t, out, "STATUS")
	require.Contains(t, out, "NODES")
	require.Contains(t, out, "UPDATES")
	require.Contains(t, out, "MESSAGE")
	require.Contains(t, out, "alpha")
	require.Contains(t, out, "zeta")
	require.Less(t, strings.Index(out, "alpha"), strings.Index(out, "zeta"),
		"rows must sort by remote name")
	require.Contains(t, out, "3", "UPDATES must sum number-of-updates across alpha's nodes")
}

// TestRemoteUpdatesSummary_UndeclaredNodeFieldReachesRaw asserts that a
// field the typed per-node struct does not capture (e.g. versions) still
// reaches Raw, since Raw is decoded independently of the typed columns.
func TestRemoteUpdatesSummary_UndeclaredNodeFieldReachesRaw(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/remotes/updates/summary", map[string]any{
		"remotes": map[string]any{
			"alpha": map[string]any{
				"remote-type": "pve", "status": "success",
				"nodes": map[string]any{
					"pve1": map[string]any{
						"number-of-updates": 1, "last-refresh": 1752000000, "status": "success",
						"versions": []map[string]any{{"package": "pve-manager", "version": "8.2.4"}},
					},
				},
			},
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteUpdatesSummaryCmd(), "summary")
	require.NoError(t, err)

	var got map[string]map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	nodes, ok := got["alpha"]["nodes"].(map[string]any)
	require.True(t, ok)
	pve1, ok := nodes["pve1"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, pve1, "versions", "undeclared versions field must reach Raw")
}

// TestRemoteUpdatesRefresh_AsyncPrintsUPID asserts that with the root
// --async flag set, `remote updates refresh` prints the UPID immediately.
func TestRemoteUpdatesRefresh_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, true)

	f.HandleJSON("POST /api2/json/remotes/updates/refresh", validUPID)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteUpdatesRefreshCmd(), "refresh")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
}

// TestRemoteUpdatesRefresh_BlocksForCompletion asserts that without
// --async, `remote updates refresh` blocks until the task completes.
func TestRemoteUpdatesRefresh_BlocksForCompletion(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	handleTaskStatus(f, validUPID)

	f.HandleJSON("POST /api2/json/remotes/updates/refresh", validUPID)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteUpdatesRefreshCmd(), "refresh")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "Remote update summary refreshed.")
}

// TestRemoteMetricCollectionStatus_SortsByRemote asserts that
// `remote metric-collection status` sorts entries by remote name.
func TestRemoteMetricCollectionStatus_SortsByRemote(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/remotes/metric-collection/status", []map[string]any{
		{"remote": "zeta"},
		{"remote": "alpha", "last-collection": 1700000000},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteMetricCollectionStatusCmd(), "status")
	require.NoError(t, err)
	out := buf.String()
	require.Less(t, strings.Index(out, "alpha"), strings.Index(out, "zeta"),
		"rows must sort by remote name")
}

// TestRemoteMetricCollectionStatus_SortsByRemoteAndPairsRawWithRows asserts
// that `remote metric-collection status` sorts entries by remote name and
// keeps each row's raw JSON attached to its sorted row, including fields not
// captured by remoteMetricStatusEntry — mirroring the paired-sort convention
// used by every other discrete-entity ls in this package.
func TestRemoteMetricCollectionStatus_SortsByRemoteAndPairsRawWithRows(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/remotes/metric-collection/status", []map[string]any{
		{"remote": "zeta", "extra": "z-marker"},
		{"remote": "alpha", "last-collection": 1700000000, "extra": "a-marker"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteMetricCollectionStatusCmd(), "status")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["remote"], "entries must sort by remote name")
	require.Equal(t, "a-marker", got[0]["extra"], "raw entry must stay paired with its sorted row")
	require.Equal(t, "zeta", got[1]["remote"])
	require.Equal(t, "z-marker", got[1]["extra"])
}

// TestRemoteMetricCollectionTrigger_SendsOptionalRemote asserts that
// `remote metric-collection trigger` forwards --remote when set and reports
// a remote-scoped success message.
func TestRemoteMetricCollectionTrigger_SendsOptionalRemote(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/remotes/metric-collection/trigger", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteMetricCollectionTriggerCmd(), "trigger", "--remote", "alpha")
	require.NoError(t, err)
	require.Equal(t, "alpha", rec.form.Get("remote"))
	require.Contains(t, buf.String(), `Metric collection triggered for remote "alpha".`)
}

// TestRemoteMetricCollectionTrigger_NoRemote asserts that without --remote,
// `remote metric-collection trigger` sends no "remote" field and reports
// the all-remotes success message.
func TestRemoteMetricCollectionTrigger_NoRemote(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/remotes/metric-collection/trigger", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteMetricCollectionTriggerCmd(), "trigger")
	require.NoError(t, err)
	require.NotContains(t, rec.form, "remote")
	require.Contains(t, buf.String(), "Metric collection triggered for every managed remote.")
}
