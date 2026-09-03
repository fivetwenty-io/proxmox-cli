package cluster

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

const cephBulkUPID = "UPID:pve1:00001234:00000ABC:66D0F2A0:cephrestartbulk:mon:root@pam:"

// recordCephBulk answers the restart-bulk POST with a UPID, records the form,
// and reports the worker finished OK. Tests that need a different task outcome
// re-register the status route afterwards; HandleJSON replaces it.
func recordCephBulk(f *testhelper.FakePVE, form *string) {
	f.HandleFunc("POST /api2/json/cluster/ceph/restart-bulk", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		*form = r.Form.Encode()
		testhelper.WriteData(w, cephBulkUPID)
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+cephBulkUPID+"/status", map[string]any{
		"status": "stopped", "exitstatus": "OK", "upid": cephBulkUPID,
	})
}

func TestClusterCephRestartBulk_RequiresServiceType(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "restart-bulk", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "service-type")
}

func TestClusterCephRestartBulk_RejectsUnknownServiceType(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "restart-bulk", "--service-type", "rgw", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid --service-type")
}

func TestClusterCephRestartBulk_RefusesWithoutYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("POST /api2/json/cluster/ceph/restart-bulk", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, cephBulkUPID)
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "restart-bulk", "--service-type", "mon")
	require.Error(t, err)
	require.Contains(t, err.Error(),
		"refusing to rolling-restart cluster-wide Ceph mon daemons without confirmation")
	require.False(t, called, "restart-bulk must not issue a POST without --yes")
}

func TestClusterCephRestartBulk_ForwardsFlagsAndWaits(t *testing.T) {
	f, ac := newFakeClient(t)
	var form string
	recordCephBulk(f, &form)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "restart-bulk", "--service-type", "osd",
		"--force", "--only-outdated", "--timeout", "1200", "--yes")
	require.NoError(t, err)
	require.Contains(t, form, "service-type=osd")
	require.Contains(t, form, "force=1")
	require.Contains(t, form, "only-outdated=1")
	require.Contains(t, form, "timeout=1200")
	require.NotContains(t, form, "dry-run")
	require.Contains(t, buf.String(), "restarted")
}

func TestClusterCephRestartBulk_OnlyOutdatedRequiresOSD(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "restart-bulk", "--service-type", "mon", "--only-outdated", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--only-outdated")
}

func TestClusterCephRestartBulk_RejectsNegativeWaitTimeout(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "restart-bulk", "--service-type", "mon", "--wait-timeout=-5", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid --wait-timeout")
}

func TestClusterCephRestartBulk_RejectsTimeoutOutOfRange(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "restart-bulk", "--service-type", "mon", "--timeout", "2000", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid --timeout 2000: want 30 to 1800 seconds")
}

func TestClusterCephRestartBulk_AsyncPrintsUPID(t *testing.T) {
	f, ac := newFakeClient(t)
	var form string
	recordCephBulk(f, &form)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain, Async: true}

	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "restart-bulk", "--service-type", "mgr", "--yes")
	require.NoError(t, err)
	require.Contains(t, buf.String(), cephBulkUPID)
}

func TestClusterCephRestartBulk_DryRunPrintsTaskLog(t *testing.T) {
	f, ac := newFakeClient(t)
	var form string
	recordCephBulk(f, &form)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+cephBulkUPID+"/log", []map[string]any{
		{"n": 1, "t": "dry-run: mon.pve1, mon.pve2, mon.pve3"},
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "restart-bulk", "--service-type", "mon", "--dry-run")
	require.NoError(t, err)
	require.Contains(t, form, "dry-run=1")
	require.Contains(t, buf.String(), "mon.pve1, mon.pve2, mon.pve3")
}

func TestClusterCephRestartBulk_DryRunAsyncPrintsUPIDWithoutFetchingTheLog(t *testing.T) {
	f, ac := newFakeClient(t)
	var form string
	recordCephBulk(f, &form)
	logFetched := false
	f.HandleFunc("GET /api2/json/nodes/pve1/tasks/"+cephBulkUPID+"/log", func(w http.ResponseWriter, _ *http.Request) {
		logFetched = true
		testhelper.WriteData(w, []map[string]any{})
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain, Async: true}

	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "restart-bulk", "--service-type", "mon", "--dry-run")
	require.NoError(t, err)
	require.Contains(t, form, "dry-run=1")
	require.Contains(t, buf.String(), cephBulkUPID)
	require.False(t, logFetched, "--dry-run --async must return before fetching the log")
}

func TestClusterCephRestartBulk_FailedDryRunStillPrintsTheLog(t *testing.T) {
	f, ac := newFakeClient(t)
	var form string
	recordCephBulk(f, &form)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+cephBulkUPID+"/status", map[string]any{
		"status": "stopped", "exitstatus": "cluster is not healthy (HEALTH_WARN: MON_DOWN)", "upid": cephBulkUPID,
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+cephBulkUPID+"/log", []map[string]any{
		{"n": 1, "t": "HEALTH_WARN: MON_DOWN; refusing to plan without --force"},
		{"n": 2, "t": "TASK ERROR: cluster is not healthy (HEALTH_WARN: MON_DOWN)"},
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "restart-bulk", "--service-type", "mon", "--dry-run")
	require.Error(t, err)
	require.Contains(t, err.Error(), "ceph rolling restart dry run")
	require.Contains(t, buf.String(), "refusing to plan without --force", "the log is the point of a dry run")
}

func TestClusterCephRestartBulk_NoTaskHandleIsAnError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("POST /api2/json/cluster/ceph/restart-bulk", nil) // {"data": null}
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "restart-bulk", "--service-type", "mon", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "server returned no task handle")
	require.NotContains(t, buf.String(), "Ceph mon daemons restarted across the cluster.")
}

func TestClusterCephRestartBulk_WaitTimeoutSaysTheRollContinues(t *testing.T) {
	f, ac := newFakeClient(t)
	var form string
	recordCephBulk(f, &form)
	f.HandleJSON("GET /api2/json/nodes/pve1/tasks/"+cephBulkUPID+"/status", map[string]any{
		"status": "running", "upid": cephBulkUPID,
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "restart-bulk", "--service-type", "mon", "--wait-timeout", "1", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "stopped waiting after 1s")
	require.Contains(t, err.Error(), "still running")
	require.Contains(t, err.Error(), cephBulkUPID)
}

func TestClusterCephRestartBulk_SurfacesAPIError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/cluster/ceph/restart-bulk", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "HEALTH_ERR")
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "ceph", "restart-bulk", "--service-type", "mds", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "rolling-restart ceph mds daemons")
}
