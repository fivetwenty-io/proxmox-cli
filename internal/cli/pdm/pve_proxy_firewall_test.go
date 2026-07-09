package pdm

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestPveFirewallStatus_ListsEveryRemote asserts that `pve firewall status`
// renders every managed remote's summary row.
func TestPveFirewallStatus_ListsEveryRemote(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/pve/firewall/status", []map[string]any{
		{"remote": "cluster1", "status": "enabled", "nodes": []map[string]any{{"node": "pve1"}}},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveFirewallStatusCmd(), "status")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "cluster1")
	require.Contains(t, buf.String(), "enabled")
}

// TestPveFirewallShow_RendersSingle asserts that `pve firewall show` renders
// a specific remote's firewall status fields.
func TestPveFirewallShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/firewall/status", map[string]any{
		"remote": "cluster1", "status": "enabled", "nodes": []map[string]any{},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveFirewallShowCmd(), "show", "cluster1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "cluster1")
}

// TestPveFirewallOptionsShow_RendersSingle asserts that `pve firewall
// options show` renders the cluster firewall options.
func TestPveFirewallOptionsShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/firewall/options", map[string]any{
		"enable": 1, "policy_in": "DROP",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveFirewallOptionsShowCmd(), "show", "cluster1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "DROP")
}

// TestPveFirewallOptionsUpdate_RejectsNoFlags asserts the anyFlagChanged
// gate on `pve firewall options update` blocks the request when no flags
// are set.
func TestPveFirewallOptionsUpdate_RejectsNoFlags(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveFirewallOptionsUpdateCmd(), "update", "cluster1")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes requested: pass at least one flag")
}

// TestPveFirewallOptionsUpdate_SendsChangedFlags asserts that `pve firewall
// options update` sends only the flags explicitly set.
func TestPveFirewallOptionsUpdate_SendsChangedFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/pve/remotes/cluster1/firewall/options", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveFirewallOptionsUpdateCmd(), "update", "cluster1", "--policy-in", "ACCEPT")
	require.NoError(t, err)

	require.Equal(t, "PUT", rec.method)
	require.Equal(t, "ACCEPT", rec.form.Get("policy_in"))
	require.Empty(t, rec.form.Get("policy_out"))
	require.Contains(t, buf.String(), `Cluster firewall options on PVE remote "cluster1" updated.`)
}

// TestPveFirewallRules_PreservesServerOrder asserts that `pve firewall
// rules` renders rules in server (position) order, not sorted.
func TestPveFirewallRules_PreservesServerOrder(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/firewall/rules", []map[string]any{
		{"pos": 1, "type": "in", "action": "ACCEPT", "comment": "second rule listed first"},
		{"pos": 0, "type": "in", "action": "DROP", "comment": "first rule listed second"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveFirewallRulesCmd(), "rules", "cluster1")
	require.NoError(t, err)

	rows := buf.String()
	firstIdx := strings.Index(rows, "ACCEPT")
	secondIdx := strings.Index(rows, "DROP")
	require.GreaterOrEqual(t, firstIdx, 0)
	require.GreaterOrEqual(t, secondIdx, 0)
	require.Less(t, firstIdx, secondIdx, "firewall rules must preserve server order, not be sorted")
}
