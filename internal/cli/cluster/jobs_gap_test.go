package cluster

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestJobsScheduleAnalyze_Success verifies `pmx cluster jobs schedule-analyze --schedule daily`
// queries GET /cluster/jobs/schedule-analyze with the schedule parameter and renders results.
func TestJobsScheduleAnalyze_Success(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath, gotQuery string
	f.HandleFunc("GET /api2/json/cluster/jobs/schedule-analyze", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		testhelper.WriteData(w, []any{
			map[string]any{"time": 1717200000, "utc": "2024-06-01T02:00:00Z"},
			map[string]any{"time": 1717286400, "utc": "2024-06-02T02:00:00Z"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "jobs", "schedule-analyze", "--schedule", "02:00"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/jobs/schedule-analyze", gotPath)
	require.Contains(t, gotQuery, "schedule=02%3A00")

	out := buf.String()
	require.Contains(t, out, "TIME")
}

// TestJobsScheduleAnalyze_RequiresSchedule verifies --schedule is required.
func TestJobsScheduleAnalyze_RequiresSchedule(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "jobs", "schedule-analyze")
	require.Error(t, err)
	require.Contains(t, err.Error(), "schedule")
}

// TestJobsScheduleAnalyze_OptionalIterationsOmitted verifies that when --iterations
// is not supplied, it is not sent in the query string.
func TestJobsScheduleAnalyze_OptionalIterationsOmitted(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotQuery string
	f.HandleFunc("GET /api2/json/cluster/jobs/schedule-analyze", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		testhelper.WriteData(w, []any{})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "jobs", "schedule-analyze", "--schedule", "daily"))

	require.NotContains(t, gotQuery, "iterations", "unset --iterations must not appear in query")
	require.NotContains(t, gotQuery, "starttime", "unset --starttime must not appear in query")
}

// TestJobsScheduleAnalyze_ServerError verifies a server error surfaces correctly.
func TestJobsScheduleAnalyze_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/jobs/schedule-analyze", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid schedule")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "jobs", "schedule-analyze", "--schedule", "badvalue"))
}

// TestJobsCommandTree_ScheduleAnalyze verifies schedule-analyze is registered.
func TestJobsCommandTree_ScheduleAnalyze(t *testing.T) {
	root := Group(&cli.Deps{})
	var jobsCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "jobs" {
			jobsCmd = c
		}
	}
	require.NotNil(t, jobsCmd)

	names := make(map[string]bool)
	for _, c := range jobsCmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["schedule-analyze"], "jobs must expose schedule-analyze sub-command")
}
