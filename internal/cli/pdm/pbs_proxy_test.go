package pdm

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// TestPbsRemoteLs_SortsById asserts that `pbs remote ls` sorts entries by
// remote ID and keeps each row's raw JSON paired through the sort.
func TestPbsRemoteLs_SortsById(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pbs/remotes", []map[string]any{
		{"remote": "zeta"},
		{"remote": "alpha"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsRemoteLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["remote"], "entries must sort by remote ID")
	require.Equal(t, "zeta", got[1]["remote"])
}

// TestPbsScan_SendsCredentialsAndStripsFromOutput asserts that `pbs scan`
// sends --token/--authid on the wire but never renders them in output, even
// though the server echoes them back in the response.
func TestPbsScan_SendsCredentialsAndStripsFromOutput(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/pbs/scan", &rec, map[string]any{
		"id": "backup1", "type": "pbs", "authid": "root@pam",
		"nodes": []string{"10.0.0.5"}, "token": "s3cr3t-t0ken",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsScanCmd(), "scan",
		"--hostname", "10.0.0.5", "--authid", "root@pam", "--token", "s3cr3t-t0ken")
	require.NoError(t, err)

	// The wire request must carry the credential.
	require.Equal(t, "10.0.0.5", rec.form.Get("hostname"))
	require.Equal(t, "root@pam", rec.form.Get("authid"))
	require.Equal(t, "s3cr3t-t0ken", rec.form.Get("token"))

	// The rendered output must never contain the token, even though the
	// fake server echoed it back in the response body.
	require.NotContains(t, buf.String(), "s3cr3t-t0ken", "scan output must never render the credential")

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.NotContains(t, got, "token")
	require.Equal(t, "backup1", got["id"])
}

// TestPbsProbeTLS_SendsHostname asserts that `pbs probe-tls` sends the
// required --hostname flag and reports success (the endpoint carries no
// response data of its own).
func TestPbsProbeTLS_SendsHostname(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/pbs/probe-tls", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsProbeTLSCmd(), "probe-tls", "--hostname", "10.0.0.5", "--fingerprint", "aa:bb")
	require.NoError(t, err)

	require.Equal(t, "10.0.0.5", rec.form.Get("hostname"))
	require.Equal(t, "aa:bb", rec.form.Get("fingerprint"))
	require.Contains(t, buf.String(), `TLS certificate of PBS host "10.0.0.5" probed.`)
}

// TestPbsRealms_SortsByRealm asserts that `pbs realms` sends --hostname and
// sorts entries by realm name.
func TestPbsRealms_SortsByRealm(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pbs/realms", &rec, []map[string]any{
		{"realm": "pbs-zeta", "type": "pbs"},
		{"realm": "pam", "type": "pam", "default": true},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsRealmsCmd(), "realms", "--hostname", "10.0.0.5")
	require.NoError(t, err)

	require.Equal(t, "10.0.0.5", rec.query.Get("hostname"))

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "pam", got[0]["realm"], "entries must sort by realm name")
	require.Equal(t, "pbs-zeta", got[1]["realm"])
}

// TestPbsStatus_RendersSingle asserts that `pbs status` renders a PBS
// remote's status fields.
func TestPbsStatus_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pbs/remotes/backup1/status", map[string]any{
		"kversion": "Linux 6.8", "cpu": 0.05, "wait": 0.0, "uptime": 5000,
		"loadavg": []string{"0.1", "0.2", "0.3"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsStatusCmd(), "status", "backup1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "5000")
}

// TestPbsRrddata_ValidatesTimeframe asserts that `pbs rrddata` validates
// --timeframe against the enum before issuing any request.
func TestPbsRrddata_ValidatesTimeframe(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsRrddataCmd(), "rrddata", "backup1", "--timeframe", "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, "--timeframe must be one of")
}

// TestPbsRrddata_ListsDataPoints asserts that `pbs rrddata` renders the RRD
// data points as a table, in server order (not sorted).
func TestPbsRrddata_ListsDataPoints(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/pbs/remotes/backup1/rrddata", []map[string]any{
		{"time": 2000, "cpu-current": 0.4},
		{"time": 1000, "cpu-current": 0.1},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsRrddataCmd(), "rrddata", "backup1", "--timeframe", "hour")
	require.NoError(t, err)

	rows := buf.String()
	require.Less(t, strings.Index(rows, "2000"), strings.Index(rows, "1000"),
		"rrddata rows must preserve server order, not be sorted")
}

// TestFinishRemoteAsync_AsyncPrintsUPID asserts that with deps.Async=true,
// finishRemoteAsync prints the UPID immediately without polling the remote.
func TestFinishRemoteAsync_AsyncPrintsUPID(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, true)

	cmd := &cobra.Command{Use: "test"}
	upidMsg := mustJSONString(t, validUPID)

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := finishRemoteAsync(cmd, deps, "backup1", upidMsg, "Test message")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
}

// TestFinishRemoteAsync_WaitsForRemoteTaskAndPrintsMessage asserts that with
// deps.Async=false, finishRemoteAsync polls the pbs group's
// ListRemotesTasksStatus (not PDM's local node-task endpoint) until the task
// stops, then prints the provided message.
func TestFinishRemoteAsync_WaitsForRemoteTaskAndPrintsMessage(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pbs/remotes/backup1/tasks/"+remoteUpid("backup1", validUPID)+"/status", map[string]any{
		"node": "pdm-host", "pid": 100, "pstart": 1, "starttime": 1, "type": "aptupdate",
		"upid": validUPID, "user": "root@pam", "status": "stopped", "exitstatus": "OK",
	})

	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(context.Background())
	upidMsg := mustJSONString(t, validUPID)
	expectedMsg := "Remote task completed"

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := finishRemoteAsync(cmd, deps, "backup1", upidMsg, expectedMsg)
	require.NoError(t, err)
	require.Contains(t, buf.String(), expectedMsg)
}

// TestFinishRemoteAsync_FailsOnBadExitStatus asserts that finishRemoteAsync
// returns an error when the remote task stops with a non-OK exit status.
func TestFinishRemoteAsync_FailsOnBadExitStatus(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pbs/remotes/backup1/tasks/"+remoteUpid("backup1", validUPID)+"/status", map[string]any{
		"node": "pdm-host", "pid": 100, "pstart": 1, "starttime": 1, "type": "aptupdate",
		"upid": validUPID, "user": "root@pam", "status": "stopped", "exitstatus": "unable to connect",
	})

	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(context.Background())
	upidMsg := mustJSONString(t, validUPID)

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := finishRemoteAsync(cmd, deps, "backup1", upidMsg, "should not print")
	require.Error(t, err)
	require.ErrorContains(t, err, "unable to connect")
}

// TestFinishRemoteAsync_RejectsNonUPIDResponse asserts that finishRemoteAsync
// rejects responses that don't parse as UPID strings.
func TestFinishRemoteAsync_RejectsNonUPIDResponse(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, true)

	cmd := &cobra.Command{Use: "test"}
	invalidMsg := json.RawMessage(`{}`)

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := finishRemoteAsync(cmd, deps, "backup1", invalidMsg, "Test message")
	require.Error(t, err)
}

// TestWaitRemoteTask_UsesTheOperatorsWaitTimeout pins the bound on the ten
// PDM proxy verbs to --wait-timeout. Those verbs poll the managed remote's
// own task endpoint rather than one of the three wait funnels, so nothing
// else makes them honour the operator's bound, and they carried a hardcoded
// five minutes of their own before.
//
// Only elapsed time separates the operator's bound from a hardcoded one, so
// the test lets a real one-second bound expire against a poller that never
// stops reporting "running". testhelper.ExpiringContext would end the wait
// faster, but it ends it whatever the bound is, so it cannot tell the two
// apart. The watchdog turns a regression into a failure in seconds rather
// than a wait for the old five minutes.
func TestWaitRemoteTask_UsesTheOperatorsWaitTimeout(t *testing.T) {
	t.Cleanup(func() { apiclient.SetDefaultWaitTimeout(0) })
	apiclient.SetDefaultWaitTimeout(1)

	// The poll itself takes a moment, which puts the loop's next poll safely
	// behind the one-second deadline so the wait always ends on the deadline
	// rather than racing it.
	poll := func(_ context.Context, _, _ string) (*remoteTaskStatus, error) {
		time.Sleep(50 * time.Millisecond)

		return &remoteTaskStatus{Status: "running"}, nil
	}

	done := make(chan error, 1)
	go func() {
		done <- waitRemoteTask(context.Background(), "PBS", "backup1", validUPID, poll)
	}()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.ErrorContains(t, err, "backup1")
	case <-time.After(20 * time.Second):
		t.Fatal("the remote-task wait ignored the operator's one-second bound")
	}
}
