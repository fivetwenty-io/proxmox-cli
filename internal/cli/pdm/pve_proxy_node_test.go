package pdm

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// TestPveNodeLs_SortsByNode asserts that `pve node ls` sorts entries by node
// name and keeps each row's raw JSON paired through the sort.
func TestPveNodeLs_SortsByNode(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/nodes", []map[string]any{
		{"node": "pve2", "status": "online"},
		{"node": "pve1", "status": "online"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeLsCmd(), "ls", "cluster1")
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2)
	require.Equal(t, "pve1", got[0]["node"], "entries must sort by node name")
	require.Equal(t, "pve2", got[1]["node"])
}

// TestPveNodeStatus_RendersSingle asserts that `pve node status` renders
// the node's status fields.
func TestPveNodeStatus_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/nodes/pve1/status", map[string]any{
		"pveversion": "pve-manager/8.2", "uptime": 12345,
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeStatusCmd(), "status", "cluster1", "pve1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "pve-manager/8.2")
}

// TestPveNodeConfig_RendersFields asserts that `pve node config` renders
// the node's configuration fields.
func TestPveNodeConfig_RendersFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/nodes/pve1/config", map[string]any{
		"description": "primary node",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeConfigCmd(), "config", "cluster1", "pve1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "primary node")
}

// TestPveNodeNetwork_ListsInterfaces asserts that `pve node network` sends
// --interface-type and renders the interface list.
func TestPveNodeNetwork_ListsInterfaces(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pve/remotes/cluster1/nodes/pve1/network", &rec, []map[string]any{
		{"iface": "vmbr0", "type": "bridge", "active": true, "address": "10.0.0.5"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeNetworkCmd(), "network", "cluster1", "pve1", "--interface-type", "bridge")
	require.NoError(t, err)

	require.Equal(t, "bridge", rec.query.Get("interface-type"))
	require.Contains(t, buf.String(), "vmbr0")
}

// TestPveNodeRrddata_ValidatesTimeframe asserts that `pve node rrddata`
// validates --timeframe against the enum before issuing any request.
func TestPveNodeRrddata_ValidatesTimeframe(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeRrddataCmd(), "rrddata", "cluster1", "pve1", "--timeframe", "bogus")
	require.Error(t, err)
	require.ErrorContains(t, err, "--timeframe must be one of")
}

// TestPveNodeRrddata_ListsDataPoints asserts that `pve node rrddata`
// renders RRD data points in server order (not sorted).
func TestPveNodeRrddata_ListsDataPoints(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/nodes/pve1/rrddata", []map[string]any{
		{"time": 2000, "cpu-current": 0.4},
		{"time": 1000, "cpu-current": 0.1},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeRrddataCmd(), "rrddata", "cluster1", "pve1", "--timeframe", "hour")
	require.NoError(t, err)

	rows := buf.String()
	require.Less(t, strings.Index(rows, "2000"), strings.Index(rows, "1000"),
		"rrddata rows must preserve server order, not be sorted")
}

// TestPveNodeSubscription_RendersFields asserts that `pve node subscription`
// renders the node's real subscription fields (status/key/etc., not the
// node-status shape).
func TestPveNodeSubscription_RendersFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/nodes/pve1/subscription", map[string]any{
		"status": "active", "key": "abc-123",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeSubscriptionCmd(), "subscription", "cluster1", "pve1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "active")
	require.Contains(t, buf.String(), "abc-123")
}

// TestPveNodeAptUpdates_ListsPackages asserts that `pve node apt updates`
// decodes the PVE host's capitalized field names (Package/OldVersion/...).
func TestPveNodeAptUpdates_ListsPackages(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/nodes/pve1/apt/update", []map[string]any{
		{
			"Package": "pve-manager", "OldVersion": "8.2-1", "Version": "8.2-2",
			"Priority": "optional", "Section": "admin", "Origin": "proxmox",
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeAptUpdatesCmd(), "updates", "cluster1", "pve1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "pve-manager")
	require.Contains(t, buf.String(), "8.2-2")
}

// TestPveNodeAptUpdateDatabase_BlocksUntilRemoteTaskFinishes asserts that
// `pve node apt update-database` blocks until the remote task completes,
// polling the pve group's task-status endpoint (not PDM's local node tasks).
func TestPveNodeAptUpdateDatabase_BlocksUntilRemoteTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "POST /api2/json/pve/remotes/cluster1/nodes/pve1/apt/update", &rec, validUPID)
	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/tasks/"+validUPID+"/status", map[string]any{
		"id": "aptupdate", "node": "pve1", "pid": 100, "pstart": 1, "starttime": 1, "type": "aptupdate",
		"upid": validUPID, "user": "root@pam", "status": "stopped", "exitstatus": "OK",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeAptUpdateDatabaseCmd(), "update-database", "cluster1", "pve1")
	require.NoError(t, err)

	require.Equal(t, "POST", rec.method)
	require.Contains(t, buf.String(), `APT package index on node "pve1" of PVE remote "cluster1" refreshed.`)
}

// TestPveNodeAptUpdateDatabase_Async asserts that `pve node apt
// update-database` prints the UPID immediately when --async is set.
func TestPveNodeAptUpdateDatabase_Async(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("POST /api2/json/pve/remotes/cluster1/nodes/pve1/apt/update", validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeAptUpdateDatabaseCmd(), "update-database", "cluster1", "pve1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "refreshed")
}

// TestPveNodeAptRepositories_RendersSummaryAndRaw asserts that `pve node apt
// repositories` renders summary counts in Single while preserving the full
// structure in Raw.
func TestPveNodeAptRepositories_RendersSummaryAndRaw(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/nodes/pve1/apt/repositories", map[string]any{
		"digest": "def456",
		"files":  []map[string]any{{"path": "/etc/apt/sources.list"}},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeAptRepositoriesCmd(), "repositories", "cluster1", "pve1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "def456")
	require.Contains(t, buf.String(), "files")
}

// TestPveNodeAptChangelog_RendersText asserts that `pve node apt changelog`
// takes the package name as a positional argument and decodes the
// changelog text.
func TestPveNodeAptChangelog_RendersText(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/pve/remotes/cluster1/nodes/pve1/apt/changelog", &rec, "changelog text")

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeAptChangelogCmd(), "changelog", "cluster1", "pve1", "pve-manager")
	require.NoError(t, err)

	require.Equal(t, "pve-manager", rec.query.Get("name"))
	require.Contains(t, buf.String(), "changelog text")
}

// TestPveNodeFirewallOptionsShow_RendersSingle asserts that `pve node
// firewall options show` renders the node firewall options.
func TestPveNodeFirewallOptionsShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/nodes/pve1/firewall/options", map[string]any{
		"enable": true, "log_level_in": "info",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeFirewallOptionsShowCmd(), "show", "cluster1", "pve1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "info")
}

// TestPveNodeFirewallOptionsUpdate_RejectsNoFlags asserts the
// anyFlagChanged gate on `pve node firewall options update` blocks the
// request when no flags are set.
func TestPveNodeFirewallOptionsUpdate_RejectsNoFlags(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeFirewallOptionsUpdateCmd(), "update", "cluster1", "pve1")
	require.Error(t, err)
	require.ErrorContains(t, err, "no changes requested: pass at least one flag")
}

// TestPveNodeFirewallOptionsUpdate_SendsChangedFlags asserts that `pve node
// firewall options update` sends only the flags explicitly set.
func TestPveNodeFirewallOptionsUpdate_SendsChangedFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "PUT /api2/json/pve/remotes/cluster1/nodes/pve1/firewall/options", &rec, nil)

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeFirewallOptionsUpdateCmd(), "update", "cluster1", "pve1", "--enable")
	require.NoError(t, err)

	require.Equal(t, "PUT", rec.method)
	require.Equal(t, "1", rec.form.Get("enable"))
	require.Empty(t, rec.form.Get("nftables"))
	require.Contains(t, buf.String(), `Firewall options on node "pve1" of PVE remote "cluster1" updated.`)
}

// TestPveNodeFirewallRules_PreservesServerOrder asserts that `pve node
// firewall rules` renders rules in server (position) order, not sorted.
func TestPveNodeFirewallRules_PreservesServerOrder(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/nodes/pve1/firewall/rules", []map[string]any{
		{"pos": 1, "type": "in", "action": "ACCEPT"},
		{"pos": 0, "type": "in", "action": "DROP"},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeFirewallRulesCmd(), "rules", "cluster1", "pve1")
	require.NoError(t, err)

	rows := buf.String()
	require.Less(t, strings.Index(rows, "ACCEPT"), strings.Index(rows, "DROP"),
		"node firewall rules must preserve server order, not be sorted")
}

// TestPveNodeFirewallStatus_RendersSingle asserts that `pve node firewall
// status` renders the node's firewall status fields.
func TestPveNodeFirewallStatus_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/nodes/pve1/firewall/status", map[string]any{
		"node": "pve1", "status": "enabled", "guests": []map[string]any{},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeFirewallStatusCmd(), "status", "cluster1", "pve1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "pve1")
}

// TestPveNodeSdnVnetMacVrf_RendersSingle asserts that `pve node sdn vnet
// mac-vrf` decodes the MAC-VRF entry fields.
func TestPveNodeSdnVnetMacVrf_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/nodes/pve1/sdn/vnets/vnet1/mac-vrf", map[string]any{
		"ip": "10.0.0.5", "mac": "aa:bb:cc:dd:ee:ff", "nexthop": "10.0.0.1",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeSdnVnetMacVrfCmd(), "mac-vrf", "cluster1", "pve1", "vnet1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "aa:bb:cc:dd:ee:ff")
}

// TestPveNodeSdnZoneIPVrf_RendersSingle asserts that `pve node sdn zone
// ip-vrf` decodes the IP-VRF entry fields.
func TestPveNodeSdnZoneIPVrf_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	f.HandleJSON("GET /api2/json/pve/remotes/cluster1/nodes/pve1/sdn/zones/zone1/ip-vrf", map[string]any{
		"ip": "10.0.0.0/24", "metric": 100, "nexthops": []string{"10.0.0.1"}, "protocol": "bgp",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPveNodeSdnZoneIPVrfCmd(), "ip-vrf", "cluster1", "pve1", "zone1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "bgp")
}
