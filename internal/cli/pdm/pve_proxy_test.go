package pdm

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestPveRemoteLs_SortsById asserts that `pve remote ls` sorts entries by
// remote ID and keeps each row's raw JSON paired through the sort.
func TestPveRemoteLs_SortsById(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes", []map[string]any{
		{"remote": "zeta"},
		{"remote": "alpha"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveRemoteLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0]["remote"], "entries must sort by remote ID")
	require.Equal(t, "zeta", got[1]["remote"])
}

// TestPveScan_SendsCredentialsAndStripsFromOutput asserts that `pve scan`
// sends --token/--authid on the wire but never renders them in output, even
// though the server echoes them back in the response.
func TestPveScan_SendsCredentialsAndStripsFromOutput(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/pve/scan", &rec, map[string]any{
		"id": "cluster1", "type": "pve", "authid": "root@pam",
		"nodes": []string{"10.0.0.5"}, "token": "s3cr3t-t0ken",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveScanCmd(), "scan",
		"--hostname", "10.0.0.5", "--authid", "root@pam", "--token", "s3cr3t-t0ken")
	require.NoError(t, err)

	require.Equal(t, "10.0.0.5", rec.form.Get("hostname"))
	require.Equal(t, "root@pam", rec.form.Get("authid"))
	require.Equal(t, "s3cr3t-t0ken", rec.form.Get("token"))

	require.NotContains(t, buf.String(), "s3cr3t-t0ken", "scan output must never render the credential")

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.NotContains(t, got, "token")
	require.Equal(t, "cluster1", got["id"])
}

// TestPveProbeTLS_SendsHostname asserts that `pve probe-tls` sends the
// required --hostname flag and reports success (the endpoint carries no
// response data of its own).
func TestPveProbeTLS_SendsHostname(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/pve/probe-tls", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveProbeTLSCmd(), "probe-tls", "--hostname", "10.0.0.5", "--fingerprint", "aa:bb")
	require.NoError(t, err)

	require.Equal(t, "10.0.0.5", rec.form.Get("hostname"))
	require.Equal(t, "aa:bb", rec.form.Get("fingerprint"))
	require.Contains(t, buf.String(), `TLS certificate of PVE host "10.0.0.5" probed.`)
}

// TestPveRealms_SortsByRealm asserts that `pve realms` sends --hostname and
// sorts entries by realm name.
func TestPveRealms_SortsByRealm(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pve/realms", &rec, []map[string]any{
		{"realm": "pve-zeta", "type": "pve"},
		{"realm": "pam", "type": "pam", "default": true},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveRealmsCmd(), "realms", "--hostname", "10.0.0.5")
	require.NoError(t, err)

	require.Equal(t, "10.0.0.5", rec.query.Get("hostname"))

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "pam", got[0]["realm"], "entries must sort by realm name")
	require.Equal(t, "pve-zeta", got[1]["realm"])
}

// TestPveOptions_RendersClusterOptions asserts that `pve options` renders
// the remote's cluster options.
func TestPveOptions_RendersClusterOptions(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/options", map[string]any{
		"keyboard": "en-us", "migration": "secure",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveOptionsCmd(), "options", "cluster1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "en-us")
	require.Contains(t, buf.String(), "secure")
}

// TestPveUpdates_SortsNodeRowsAndPairsRaw asserts that `pve updates` decodes
// the RemoteUpdateSummary's nodes map into one row per node, sorted by node
// name, and that each node's raw map (including an undeclared field the
// typed entry does not capture) stays paired with its row in Raw.
func TestPveUpdates_SortsNodeRowsAndPairsRaw(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/updates", map[string]any{
		"remote-type": "pve",
		"status":      "success",
		"nodes": map[string]any{
			"zeta": map[string]any{
				"number-of-updates": 1, "last-refresh": 1752000001, "status": "success",
				"repository-status": "ok",
			},
			"alpha": map[string]any{
				"number-of-updates": 3, "last-refresh": 1752000000, "status": "success",
				"repository-status": "non-production-ready",
				"versions": []map[string]any{
					{"package": "pve-manager", "version": "8.2.4"},
				},
			},
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveUpdatesCmd(), "updates", "cluster1")
	require.NoError(t, err)
	out := buf.String()
	require.Less(t, strings.Index(out, "alpha"), strings.Index(out, "zeta"), "rows must sort by node name")

	var got struct {
		Nodes map[string]map[string]any `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Contains(t, got.Nodes, "alpha")
	require.Contains(t, got.Nodes, "zeta")

	alphaVersions, ok := got.Nodes["alpha"]["versions"].([]any)
	require.True(t, ok, "alpha's raw node must keep the undeclared versions field")
	require.Len(t, alphaVersions, 1)
	require.NotContains(t, got.Nodes["zeta"], "versions", "zeta's raw node must not gain alpha's versions field")
}

// TestPveUpdates_RendersTableColumns asserts that the table format renders
// the documented per-node columns.
func TestPveUpdates_RendersTableColumns(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/updates", map[string]any{
		"remote-type": "pve",
		"status":      "success",
		"nodes": map[string]any{
			"pve-node-1": map[string]any{
				"number-of-updates": 3, "last-refresh": 1752000000, "status": "success",
				"repository-status": "non-production-ready",
			},
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveUpdatesCmd(), "updates", "cluster1")
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "NODE")
	require.Contains(t, out, "UPDATES")
	require.Contains(t, out, "LAST - REFRESH")
	require.Contains(t, out, "STATUS")
	require.Contains(t, out, "REPO - STATUS")
	require.Contains(t, out, "pve-node-1")
	require.Contains(t, out, "3")
	require.Contains(t, out, "1752000000")
	require.Contains(t, out, "non-production-ready")
}

// TestPveUpdates_EmptyNodesFallsBackToSingle asserts that when a remote has
// never been polled (or errored), the nodes map is empty and the command
// renders Single with the remote-level status instead of an empty table.
func TestPveUpdates_EmptyNodesFallsBackToSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/updates", map[string]any{
		"remote-type":    "pve",
		"status":         "error",
		"status-message": "connection refused",
		"nodes":          map[string]any{},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveUpdatesCmd(), "updates", "cluster1")
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "remote-type")
	require.Contains(t, out, "pve")
	require.Contains(t, out, "status")
	require.Contains(t, out, "error")
	require.Contains(t, out, "status-message")
	require.Contains(t, out, "connection refused")
}

// TestPveClusterStatus_PreservesServerOrder asserts that `pve cluster
// status` renders the mixed cluster/node listing in API order.
func TestPveClusterStatus_PreservesServerOrder(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pve/remotes/cluster1/cluster-status", &rec, []map[string]any{
		{"id": "cluster", "name": "cluster1", "type": "cluster", "nodes": 2, "quorate": true},
		{"id": "node/pve1", "name": "pve1", "type": "node", "online": true, "local": true},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveClusterStatusCmd(), "status", "cluster1", "--target-endpoint", "pve1")
	require.NoError(t, err)

	require.Equal(t, "pve1", rec.query.Get("target-endpoint"))
	require.Contains(t, buf.String(), "cluster1")
	require.Contains(t, buf.String(), "pve1")
}

// TestPveClusterNextID_RendersVMID asserts that `pve cluster next-id`
// renders the next free VMID.
func TestPveClusterNextID_RendersVMID(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pve/remotes/cluster1/cluster-nextid", &rec, "104")

	var buf bytes.Buffer
	err := run(deps, &buf, newPveClusterNextIDCmd(), "next-id", "cluster1", "--target-endpoint", "cluster2")
	require.NoError(t, err)

	require.Equal(t, "cluster2", rec.query.Get("target-endpoint"))
	require.Contains(t, buf.String(), "104")
}

// TestPveClusterResources_ValidatesKind asserts that `pve cluster resources`
// validates --kind against the enum before issuing any request.
func TestPveClusterResources_ValidatesKind(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveClusterResourcesCmd(), "resources", "cluster1", "--kind", "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, "--kind must be one of")
}

// TestPveClusterResources_InfersTypeFromID asserts that `pve cluster
// resources` sends --kind and infers TYPE from the resource id's
// "<type>/<name>" prefix.
func TestPveClusterResources_InfersTypeFromID(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pve/remotes/cluster1/resources", &rec, []map[string]any{
		{"id": "qemu/104", "node": "pve1", "name": "web01", "status": "running", "vmid": 104},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveClusterResourcesCmd(), "resources", "cluster1", "--kind", "vm")
	require.NoError(t, err)

	require.Equal(t, "vm", rec.query.Get("kind"))
	require.Contains(t, buf.String(), "qemu")
	require.Contains(t, buf.String(), "web01")
}

// TestFinishPveRemoteAsync_AsyncPrintsUPID asserts that with deps.Async=true,
// finishPveRemoteAsync prints the UPID immediately without polling the remote.
func TestFinishPveRemoteAsync_AsyncPrintsUPID(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, true)

	cmd := &cobra.Command{Use: "test"}
	upidMsg := mustJSONString(t, validUPID)

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := finishPveRemoteAsync(cmd, deps, "cluster1", upidMsg, "Test message")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
}

// TestFinishPveRemoteAsync_WaitsForRemoteTaskAndPrintsMessage asserts that
// with deps.Async=false, finishPveRemoteAsync polls the pve group's
// ListRemotesTasksStatus (not PDM's local node-task endpoint) until the task
// stops, then prints the provided message. Uses a lowercase "ok" exit status
// to prove the unified waitRemoteTask's success-criteria fix (the
// pre-unification PBS-only check omitted this case).
func TestFinishPveRemoteAsync_WaitsForRemoteTaskAndPrintsMessage(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/tasks/"+validUPID+"/status", map[string]any{
		"id": "aptupdate", "node": "pdm-host", "pid": 100, "pstart": 1, "starttime": 1, "type": "aptupdate",
		"upid": validUPID, "user": "root@pam", "status": "stopped", "exitstatus": "ok",
	})

	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(context.Background())
	upidMsg := mustJSONString(t, validUPID)
	expectedMsg := "Remote task completed"

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := finishPveRemoteAsync(cmd, deps, "cluster1", upidMsg, expectedMsg)
	require.NoError(t, err)
	require.Contains(t, buf.String(), expectedMsg)
}

// TestFinishPveRemoteAsync_FailsOnBadExitStatus asserts that
// finishPveRemoteAsync returns an error when the remote task stops with a
// non-OK exit status.
func TestFinishPveRemoteAsync_FailsOnBadExitStatus(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/tasks/"+validUPID+"/status", map[string]any{
		"id": "aptupdate", "node": "pdm-host", "pid": 100, "pstart": 1, "starttime": 1, "type": "aptupdate",
		"upid": validUPID, "user": "root@pam", "status": "stopped", "exitstatus": "unable to connect",
	})

	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(context.Background())
	upidMsg := mustJSONString(t, validUPID)

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := finishPveRemoteAsync(cmd, deps, "cluster1", upidMsg, "should not print")
	require.Error(t, err)
	require.ErrorContains(t, err, "unable to connect")
}

// TestFinishPveRemoteAsync_RejectsNonUPIDResponse asserts that
// finishPveRemoteAsync rejects responses that don't parse as UPID strings.
func TestFinishPveRemoteAsync_RejectsNonUPIDResponse(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, true)

	cmd := &cobra.Command{Use: "test"}
	invalidMsg := json.RawMessage(`{}`)

	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := finishPveRemoteAsync(cmd, deps, "cluster1", invalidMsg, "Test message")
	require.Error(t, err)
}
