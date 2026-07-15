package pdm

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// TestNodeNetworkLs_SortsByNameAndPairsRawWithRows asserts that `node
// network ls` sorts entries by interface name and keeps the Raw/table row
// for each entry paired together after sorting.
func TestNodeNetworkLs_SortsByNameAndPairsRawWithRows(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/network", []map[string]any{
		{"name": "vmbr1", "type": "bridge", "active": 1, "autostart": 1, "cidr": "10.0.1.1/24"},
		{"name": "eth0", "type": "eth", "active": 1, "autostart": 1, "cidr": "10.0.0.1/24"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeNetworkLsCmd(), "ls", "pdm-host")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "eth0", got[0]["name"], "entries must sort by name")
	require.Equal(t, "10.0.0.1/24", got[0]["cidr"], "raw entry must stay paired with its sorted row")
	require.Equal(t, "vmbr1", got[1]["name"])
	require.Equal(t, "10.0.1.1/24", got[1]["cidr"])
}

// TestNodeNetworkShow_RendersSingle asserts that `node network show`
// renders a single interface's configuration.
func TestNodeNetworkShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/network/eth0", map[string]any{
		"name": "eth0", "type": "eth", "active": 1, "autostart": 1,
		"options": []string{}, "options6": []string{},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeNetworkShowCmd(), "show", "pdm-host", "eth0")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "eth0")
}

// TestNodeNetworkCreate_SendsParams asserts that `node network create`
// encodes every flag onto the expected form field names.
func TestNodeNetworkCreate_SendsParams(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/nodes/pdm-host/network", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeNetworkCreateCmd(), "create", "pdm-host", "vmbr2",
		"--type", "bridge", "--cidr", "10.0.2.1/24", "--autostart")
	require.NoError(t, err)

	require.Equal(t, "vmbr2", rec.form.Get("iface"))
	require.Equal(t, "bridge", rec.form.Get("type"))
	require.Equal(t, "10.0.2.1/24", rec.form.Get("cidr"))
	require.Equal(t, "1", rec.form.Get("autostart"))
	require.Contains(t, buf.String(), `Network interface "vmbr2" created on node "pdm-host".`)
}

// TestNodeNetworkUpdate_RejectsNoChanges asserts that `node network update`
// refuses to issue a request when no flag was explicitly set.
func TestNodeNetworkUpdate_RejectsNoChanges(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeNetworkUpdateCmd(), "update", "pdm-host", "eth0")
	require.Error(t, err)
	require.ErrorContains(t, err, `update network interface "eth0" on node "pdm-host": no changes requested: pass at least one flag`)
}

// TestNodeNetworkUpdate_SendsChangedFlagsOnly asserts that `node network
// update` sends only the flags explicitly set.
func TestNodeNetworkUpdate_SendsChangedFlagsOnly(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/nodes/pdm-host/network/eth0", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeNetworkUpdateCmd(), "update", "pdm-host", "eth0", "--mtu", "9000")
	require.NoError(t, err)

	require.Equal(t, "9000", rec.form.Get("mtu"))
	require.NotContains(t, rec.form, "cidr")
	require.Contains(t, buf.String(), `Network interface "eth0" on node "pdm-host" updated.`)
}

// TestNodeNetworkDelete_RefusesWithoutConfirmation asserts the --yes/-y gate
// on `node network delete` blocks the request entirely when unset.
func TestNodeNetworkDelete_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeNetworkDeleteCmd(), "delete", "pdm-host", "eth0")
	require.Error(t, err)
	require.ErrorContains(t, err, `refusing to remove network interface "eth0" on node "pdm-host" without confirmation: pass --yes/-y`)
}

// TestNodeNetworkDelete_SendsRequestWithConfirmation asserts that passing
// --yes issues the delete request.
func TestNodeNetworkDelete_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/nodes/pdm-host/network/eth0", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeNetworkDeleteCmd(), "delete", "pdm-host", "eth0", "--yes")
	require.NoError(t, err)

	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), `Network interface "eth0" on node "pdm-host" deleted.`)
}

// TestNodeNetworkRevert_RefusesWithoutConfirmation asserts the --yes/-y gate
// on `node network revert` blocks the request entirely when unset.
func TestNodeNetworkRevert_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeNetworkRevertCmd(), "revert", "pdm-host")
	require.Error(t, err)
	require.ErrorContains(t, err, `refusing to revert staged network configuration on node "pdm-host" without confirmation: pass --yes/-y`)
}

// TestNodeNetworkRevert_SendsRequestWithConfirmation asserts that passing
// --yes issues the revert request.
func TestNodeNetworkRevert_SendsRequestWithConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "DELETE /api2/json/nodes/pdm-host/network", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeNetworkRevertCmd(), "revert", "pdm-host", "--yes")
	require.NoError(t, err)

	require.Equal(t, "DELETE", rec.method)
	require.Contains(t, buf.String(), `Staged network configuration on node "pdm-host" reverted.`)
}

// TestNodeNetworkApply_BlocksUntilTaskFinishes asserts that `node network
// apply` blocks until the reload task completes.
func TestNodeNetworkApply_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/nodes/pdm-host/network", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeNetworkApplyCmd(), "apply", "pdm-host")
	require.NoError(t, err)

	require.Equal(t, "PUT", rec.method)
	require.Contains(t, buf.String(), `Network configuration on node "pdm-host" reloaded.`)
}

// TestNodeNetworkApply_Async asserts that `node network apply` prints the
// UPID immediately when --async is set.
func TestNodeNetworkApply_Async(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("PUT /api2/json/nodes/pdm-host/network", validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeNetworkApplyCmd(), "apply", "pdm-host")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "reloaded")
}
