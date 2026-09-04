package cli_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

const renderTestUPID = "UPID:pve1:00001234:00000ABC:66D0F2A0:cephrestartbulk:mon:root@pam:"

// renderTestCmd builds a bare *cobra.Command suitable for calling cli.RenderUPID
// and cli.RenderDryRunLog directly, without going through Execute: a context
// (both helpers call cmd.Context()) and an output writer.
func renderTestCmd(buf *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(buf)
	return cmd
}

func TestRenderUPID_PrintsTheHandle(t *testing.T) {
	deps := &cli.Deps{Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := cli.RenderUPID(renderTestCmd(&buf), deps, renderTestUPID)
	require.NoError(t, err)
	require.Contains(t, buf.String(), renderTestUPID)
}

// TestRenderTaskWait_AsyncPrintsTheUPIDWithoutPolling covers the --async
// short circuit: it must return before ever asking for the task's status.
func TestRenderTaskWait_AsyncPrintsTheUPIDWithoutPolling(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	statusFetched := false
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+renderTestUPID+"/status",
		func(w http.ResponseWriter, _ *http.Request) {
			statusFetched = true
			testhelper.WriteData(w, map[string]any{"status": "stopped", "exitstatus": "OK", "upid": renderTestUPID})
		})
	ac := newCLITestClient(t, f)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain, Async: true}

	var buf bytes.Buffer
	err := cli.RenderTaskWait(renderTestCmd(&buf), deps, renderTestUPID, "the task is done", nil)
	require.NoError(t, err)
	require.Contains(t, buf.String(), renderTestUPID)
	require.False(t, statusFetched, "async must return before polling task status")
}

// TestRenderTaskWait_SuccessPrintsTheDoneMessage covers the ordinary wait
// path: the task finishes OK, and doneMsg, not the UPID, reaches the buffer.
func TestRenderTaskWait_SuccessPrintsTheDoneMessage(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+renderTestUPID+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": renderTestUPID,
	})
	ac := newCLITestClient(t, f)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := cli.RenderTaskWait(renderTestCmd(&buf), deps, renderTestUPID, "the task is done", cli.WaitOptionsFor(5))
	require.NoError(t, err)
	require.Contains(t, buf.String(), "the task is done")
	require.NotContains(t, buf.String(), renderTestUPID)
}

// TestRenderTaskWait_FailedTaskReturnsTheErrorAndPrintsNothing covers a task
// that finishes with a failure: the wait error is returned unwrapped for the
// caller to prefix, and nothing, not even doneMsg, is rendered.
func TestRenderTaskWait_FailedTaskReturnsTheErrorAndPrintsNothing(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+renderTestUPID+"/status", map[string]any{
		"status": "stopped", "exitstatus": "cluster is not healthy (HEALTH_WARN: MON_DOWN)", "upid": renderTestUPID,
	})
	ac := newCLITestClient(t, f)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := cli.RenderTaskWait(renderTestCmd(&buf), deps, renderTestUPID, "the task is done", cli.WaitOptionsFor(5))
	require.Error(t, err)
	require.Empty(t, buf.String())
}

// TestRenderDryRunLog_AsyncPrintsUPIDWithoutFetchingTheLog covers the --async
// short circuit: it must return before ever asking for the task's status or
// its log.
func TestRenderDryRunLog_AsyncPrintsUPIDWithoutFetchingTheLog(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	logFetched := false
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+renderTestUPID+"/log", func(w http.ResponseWriter, _ *http.Request) {
		logFetched = true
		testhelper.WriteData(w, []map[string]any{})
	})
	ac := newCLITestClient(t, f)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain, Async: true}

	var buf bytes.Buffer
	err := cli.RenderDryRunLog(renderTestCmd(&buf), deps, renderTestUPID, nil, "test dry run")
	require.NoError(t, err)
	require.Contains(t, buf.String(), renderTestUPID)
	require.False(t, logFetched, "async must return before fetching the log")
}

// TestRenderDryRunLog_SuccessRendersTheLogAndReturnsNil covers the ordinary
// dry-run path: the worker finishes OK, and the log it wrote is rendered.
func TestRenderDryRunLog_SuccessRendersTheLogAndReturnsNil(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+renderTestUPID+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": renderTestUPID,
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+renderTestUPID+"/log", []map[string]any{
		{"n": 1, "t": "plan: mon.pve1, mon.pve2"},
	})
	ac := newCLITestClient(t, f)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := cli.RenderDryRunLog(renderTestCmd(&buf), deps, renderTestUPID, nil, "test dry run")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "plan: mon.pve1, mon.pve2")
}

// TestRenderDryRunLog_FailedWaitStillRendersTheLogAndReturnsTheJoinedError
// covers the worker-refused path: the log, which is where the refusal
// reason lives, must still reach the buffer, and the returned error must
// carry the wait failure rather than being swallowed.
func TestRenderDryRunLog_FailedWaitStillRendersTheLogAndReturnsTheJoinedError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+renderTestUPID+"/status", map[string]any{
		"status": "stopped", "exitstatus": "cluster is not healthy (HEALTH_WARN: MON_DOWN)", "upid": renderTestUPID,
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+renderTestUPID+"/log", []map[string]any{
		{"n": 1, "t": "HEALTH_WARN: MON_DOWN; refusing to plan without --force"},
	})
	ac := newCLITestClient(t, f)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := cli.RenderDryRunLog(renderTestCmd(&buf), deps, renderTestUPID, nil, "test dry run")
	require.Error(t, err)
	require.Contains(t, err.Error(), "test dry run")
	require.Contains(t, buf.String(), "refusing to plan without --force",
		"the log is the point of a dry run even when the worker refuses")
}

// TestRenderDryRunLog_UnparsableUPIDReportsBothErrors covers a UPID the
// server never issued: WaitTask fails on it before any HTTP request, and the
// caller's own tasks.ParseUPID call fails the same way, so the returned
// error must carry both failures and nothing must be rendered or fetched.
func TestRenderDryRunLog_UnparsableUPIDReportsBothErrors(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("/", func(_ http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request for an unparsable UPID: %s %s", r.Method, r.URL.Path)
	})
	ac := newCLITestClient(t, f)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := cli.RenderDryRunLog(renderTestCmd(&buf), deps, "not-a-upid", nil, "test dry run")
	require.Error(t, err)
	require.Contains(t, err.Error(), "test dry run")
	require.Contains(t, err.Error(), "unexpected field count",
		"the joined error must mention why the UPID itself would not parse")
	require.Empty(t, buf.String(), "nothing can be rendered for a UPID that was never a task")
}
