package pdm

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestNodeLs_ListsNodes asserts that `node ls` decodes the compatibility
// cluster-node listing recovered via the raw-transport bypass.
func TestNodeLs_ListsNodes(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/nodes", []map[string]any{
		{"node": "pdm-host"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 1)
	require.Equal(t, "pdm-host", got[0]["node"])
}

// TestNodeLs_SortsByNameAndPairsRawWithRows asserts that `node ls` sorts
// entries by node name and keeps each row's raw JSON attached to its sorted
// row, mirroring the paired-sort convention used by every other
// discrete-entity ls in this package (remote.go, node_network.go).
func TestNodeLs_SortsByNameAndPairsRawWithRows(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/nodes", []map[string]any{
		{"node": "zeta-host", "extra": "z-marker"},
		{"node": "alpha-host", "extra": "a-marker"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeLsCmd(), "ls")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "alpha-host", got[0]["node"], "entries must sort by node name")
	require.Equal(t, "a-marker", got[0]["extra"], "raw entry must stay paired with its sorted row")
	require.Equal(t, "zeta-host", got[1]["node"])
	require.Equal(t, "z-marker", got[1]["extra"])

	var tableBuf bytes.Buffer
	tableDeps := depsFor(t, pc, output.FormatTable, false)
	err = run(tableDeps, &tableBuf, newNodeLsCmd(), "ls")
	require.NoError(t, err)
	rows := tableBuf.String()
	require.Less(t, strings.Index(rows, "alpha-host"), strings.Index(rows, "zeta-host"),
		"table rows must also be sorted by node name")
}

// TestNodeStatus_RendersSingle asserts that `node status` renders the
// node's status fields.
func TestNodeStatus_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/status", map[string]any{
		"uptime": 12345, "kversion": "Linux 6.8", "cpu": 0.1, "wait": 0.0,
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeStatusCmd(), "status", "pdm-host")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "12345")
}

// TestNodeReboot_RefusesWithoutConfirmation asserts the --yes/-y gate on
// `node reboot` blocks the request entirely when unset.
func TestNodeReboot_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodePowerCmd("reboot", "Reboot the node"), "reboot", "pdm-host")
	require.Error(t, err)
	require.ErrorContains(t, err, `refusing to reboot node "pdm-host" without confirmation: pass --yes/-y`)
}

// TestNodeShutdown_RefusesWithoutConfirmation asserts the --yes/-y gate on
// `node shutdown` blocks the request entirely when unset.
func TestNodeShutdown_RefusesWithoutConfirmation(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodePowerCmd("shutdown", "Shut down the node"), "shutdown", "pdm-host")
	require.Error(t, err)
	require.ErrorContains(t, err, `refusing to shutdown node "pdm-host" without confirmation: pass --yes/-y`)
}

// TestNodeReboot_SendsCommandAndRunsSynchronously asserts that `node reboot
// --yes` sends command=reboot and completes without waiting for a task
// (CreateStatus returns no UPID).
func TestNodeReboot_SendsCommandAndRunsSynchronously(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/nodes/pdm-host/status", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodePowerCmd("reboot", "Reboot the node"), "reboot", "pdm-host", "--yes")
	require.NoError(t, err)

	require.Equal(t, "POST", rec.method)
	require.Equal(t, "reboot", rec.form.Get("command"))
	require.Contains(t, buf.String(), `Node "pdm-host" reboot initiated.`)
}

// TestNodeConfigShow_RendersDefaults asserts that `node config show
// --defaults` lists options absent from the live response as "(unset)".
func TestNodeConfigShow_RendersDefaults(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/config", map[string]any{
		"email-from": "root@pdm-host",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeConfigShowCmd(), "show", "pdm-host", "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "default-lang")
	require.Contains(t, buf.String(), "(unset)")
}

// TestNodeConfigUpdate_RejectsNoChanges asserts that `node config update`
// refuses to issue a request when no flag was explicitly set.
func TestNodeConfigUpdate_RejectsNoChanges(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeConfigUpdateCmd(), "update", "pdm-host")
	require.Error(t, err)
	require.ErrorContains(t, err, `update config on node "pdm-host": no changes requested: pass at least one flag`)
}

// TestNodeConfigUpdate_SendsChangedFlagsOnly asserts that `node config
// update` sends only the flags explicitly set.
func TestNodeConfigUpdate_SendsChangedFlagsOnly(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/nodes/pdm-host/config", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeConfigUpdateCmd(), "update", "pdm-host", "--email-from", "root@pdm-host")
	require.NoError(t, err)

	require.Equal(t, "PUT", rec.method)
	require.Equal(t, "root@pdm-host", rec.form.Get("email-from"))
	require.NotContains(t, rec.form, "http-proxy")
	require.Contains(t, buf.String(), `Configuration for node "pdm-host" updated.`)
}

// TestNodeDNSShow_RendersSingle asserts that `node dns show` renders the
// node's DNS settings.
func TestNodeDNSShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/dns", map[string]any{
		"digest": "abc123", "dns1": "1.1.1.1",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeDNSShowCmd(), "show", "pdm-host")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "1.1.1.1")
}

// TestNodeDNSUpdate_RejectsNoChanges asserts that `node dns update` refuses
// to issue a request when no flag was explicitly set.
func TestNodeDNSUpdate_RejectsNoChanges(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeDNSUpdateCmd(), "update", "pdm-host")
	require.Error(t, err)
	require.ErrorContains(t, err, `update dns on node "pdm-host": no changes requested: pass at least one flag`)
}

// TestNodeDNSUpdate_SendsChangedFlagsOnly asserts that `node dns update`
// sends only the flags explicitly set.
func TestNodeDNSUpdate_SendsChangedFlagsOnly(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/nodes/pdm-host/dns", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeDNSUpdateCmd(), "update", "pdm-host", "--dns1", "9.9.9.9")
	require.NoError(t, err)

	require.Equal(t, "9.9.9.9", rec.form.Get("dns1"))
	require.NotContains(t, rec.form, "dns2")
	require.Contains(t, buf.String(), `DNS settings for node "pdm-host" updated.`)
}

// TestNodeTimeShow_RendersSingle asserts that `node time show` renders the
// node's time zone settings.
func TestNodeTimeShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/nodes/pdm-host/time", map[string]any{
		"timezone": "UTC", "time": 1000, "localtime": 1000,
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeTimeShowCmd(), "show", "pdm-host")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "UTC")
}

// TestNodeTimeUpdate_SendsTimezone asserts that `node time update` sends
// the required --timezone field.
func TestNodeTimeUpdate_SendsTimezone(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/nodes/pdm-host/time", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeTimeUpdateCmd(), "update", "pdm-host", "--timezone", "America/New_York")
	require.NoError(t, err)

	require.Equal(t, "America/New_York", rec.form.Get("timezone"))
	require.Contains(t, buf.String(), `Time zone for node "pdm-host" set to "America/New_York".`)
}
