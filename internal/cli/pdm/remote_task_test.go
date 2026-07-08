package pdm

import (
	"bytes"
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
// summary` decodes the nested remotes map into one row per remote, sorted
// by name.
func TestRemoteUpdatesSummary_ListsPerRemoteRows(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/remotes/updates/summary", map[string]any{
		"remotes": map[string]any{
			"zeta":  map[string]any{"total": 0},
			"alpha": map[string]any{"total": 3},
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newRemoteUpdatesSummaryCmd(), "summary")
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "alpha")
	require.Contains(t, out, "zeta")
	require.Less(t, strings.Index(out, "alpha"), strings.Index(out, "zeta"),
		"rows must sort by remote name")
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
