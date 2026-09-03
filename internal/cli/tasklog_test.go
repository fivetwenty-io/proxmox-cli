package cli_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

const taskLogTestUPID = "UPID:pve1:00001234:00000ABC:66D0F2A0:cephrestartbulk:osd:root@pam:"

func TestWaitOptionsFor_SetsOnlyTheTimeout(t *testing.T) {
	opts := cli.WaitOptionsFor(3600)
	require.NotNil(t, opts)
	require.Equal(t, 3600, opts.TimeoutSeconds)
	require.Zero(t, opts.IntervalMillis, "polling cadence stays at the SDK default")
}

func TestWaitOptionsFor_NonPositiveMeansUnbounded(t *testing.T) {
	for _, in := range []int64{0, -1} {
		opts := cli.WaitOptionsFor(in)
		require.NotNil(t, opts, "a nil would let the SDK fall back to its 300 s default")
		require.Equal(t, 7*24*3600, opts.TimeoutSeconds)
		require.Zero(t, opts.IntervalMillis)
	}
}

func TestTaskLogResult_NilParamsRequestsTheFullLog(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotLimit, gotStart string
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+taskLogTestUPID+"/log",
		func(w http.ResponseWriter, r *http.Request) {
			gotLimit = r.URL.Query().Get("limit")
			gotStart = r.URL.Query().Get("start")
			testhelper.WriteData(w, []map[string]any{
				{"n": 1, "t": "plan: osd.0, osd.1"},
				{"n": 2, "t": "TASK OK"},
			})
		})
	ac := newCLITestClient(t, f)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	res, err := cli.TaskLogResult(context.Background(), deps, "pve1", taskLogTestUPID, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"N", "T"}, res.Headers)
	require.Equal(t, [][]string{{"1", "plan: osd.0, osd.1"}, {"2", "TASK OK"}}, res.Rows)
	require.Equal(t, "5000", gotLimit)
	require.Empty(t, gotStart)
	lines, ok := res.Raw.([]cli.TaskLogLine)
	require.True(t, ok)
	require.Len(t, lines, 2)
}

func TestTaskLogResult_ForwardsCallerParams(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotLimit, gotStart string
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+taskLogTestUPID+"/log",
		func(w http.ResponseWriter, r *http.Request) {
			gotLimit = r.URL.Query().Get("limit")
			gotStart = r.URL.Query().Get("start")
			testhelper.WriteData(w, []map[string]any{})
		})
	ac := newCLITestClient(t, f)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	limit, start := int64(100), int64(5)
	params := &nodes.ListTasksLogParams{Limit: &limit, Start: &start}
	res, err := cli.TaskLogResult(context.Background(), deps, "pve1", taskLogTestUPID, params)
	require.NoError(t, err)
	require.Equal(t, "100", gotLimit)
	require.Equal(t, "5", gotStart)
	require.Empty(t, res.Rows)
	lines, ok := res.Raw.([]cli.TaskLogLine)
	require.True(t, ok)
	require.Empty(t, lines)
}

func TestTaskLogResult_EmptyParamsSendsNoLimit(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotQuery string
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+taskLogTestUPID+"/log",
		func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			testhelper.WriteData(w, []map[string]any{})
		})
	ac := newCLITestClient(t, f)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	_, err := cli.TaskLogResult(context.Background(), deps, "pve1", taskLogTestUPID, &nodes.ListTasksLogParams{})
	require.NoError(t, err)
	require.NotContains(t, gotQuery, "limit=", "an explicit empty params leaves paging to the server")
}

func TestTaskLogResult_SurfacesAPIError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+taskLogTestUPID+"/log",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteError(w, http.StatusInternalServerError, "no such task")
		})
	ac := newCLITestClient(t, f)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	_, err := cli.TaskLogResult(context.Background(), deps, "pve1", taskLogTestUPID, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get log for task")
}

func TestTaskLogResult_RejectsMalformedLine(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+taskLogTestUPID+"/log",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, []any{map[string]any{"n": 1, "t": "ok"}, "not a line"})
		})
	ac := newCLITestClient(t, f)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	_, err := cli.TaskLogResult(context.Background(), deps, "pve1", taskLogTestUPID, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode task log line 1")
}
