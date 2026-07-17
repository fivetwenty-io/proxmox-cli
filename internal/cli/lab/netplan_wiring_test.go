package lab

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestCreateRefusesIncoherentNetworkPlan proves create fails fast — before
// any API read or mutation — when the lab's address plan is internally
// inconsistent. The SDN zone list is buildCreatePlan's first API call, so a
// trap there proves the plan never started building.
func TestCreateRefusesIncoherentNetworkPlan(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/cluster/sdn/zones", func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("an incoherent network plan must be rejected before any plan-building API call")
	})

	lab := createTestLab("wayne")
	lab.Network.Mgmt.HostIP = "192.168.1.10" // outside cleanLab's 10.10.1.0/24

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildCreateCmd(t, path, f, "node1")

	_, err := runCreateCmd(t, cmd, "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "network plan is incoherent")
	assert.ErrorContains(t, err, "network.mgmt.host_ip 192.168.1.10 is not inside network.cidr 10.10.1.0/24")
}

// TestStatus_NarrowGuestPrefixSurfacesNetworkWarning proves the live check
// end-to-end: a running lab whose guest agent reports an in-cidr interface
// with a narrower prefix than network.cidr gets a NETWORK_WARNING row.
func TestStatus_NarrowGuestPrefixSurfacesNetworkWarning(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f, map[string]any{
		"vmid":   100,
		"node":   "pve1",
		"pool":   "lab-alpha",
		"name":   "lab-alpha",
		"status": "running",
		"type":   "qemu",
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/status/current", map[string]any{
		"status": "running",
		"vmid":   100,
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/agent/network-get-interfaces", map[string]any{
		"result": []any{
			map[string]any{
				"name": "vmbr0",
				"ip-addresses": []any{
					map[string]any{"ip-address": "10.10.1.50", "ip-address-type": "ipv4", "prefix": 28},
				},
			},
		},
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/config", map[string]any{
		"cores": 4,
	})

	lab := cleanLab("alpha") // cidr 10.10.1.0/24; agent reports /28
	cfg := &config.Config{Labs: map[string]*config.Lab{"alpha": lab}}
	deps := newLifecycleDeps(t, cfg, ac)

	body, err := execLifecycle(newStatusCmd(), deps, "alpha")
	require.NoError(t, err)

	var table jsonTable
	require.NoError(t, json.Unmarshal([]byte(body), &table))
	require.Len(t, table.Rows, 2, "one node row plus a trailing summary row")
	row := table.Rows[0]
	assert.Equal(t, "10.10.1.50", row[4], "IP")
	warn := row[8]
	require.NotEmpty(t, warn)
	assert.Contains(t, warn, "10.10.1.50/28")
	assert.Contains(t, warn, "network.cidr 10.10.1.0/24")
}

// TestStatus_MatchingGuestPrefixHasNoWarning pins the quiet path: an agent
// reporting the cidr's own prefix adds no NETWORK_WARNING row.
func TestStatus_MatchingGuestPrefixHasNoWarning(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f, map[string]any{
		"vmid":   100,
		"node":   "pve1",
		"pool":   "lab-alpha",
		"name":   "lab-alpha",
		"status": "running",
		"type":   "qemu",
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/status/current", map[string]any{
		"status": "running",
		"vmid":   100,
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/agent/network-get-interfaces", map[string]any{
		"result": []any{
			map[string]any{
				"name": "vmbr0",
				"ip-addresses": []any{
					map[string]any{"ip-address": "10.10.1.50", "ip-address-type": "ipv4", "prefix": 24},
				},
			},
		},
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/config", map[string]any{
		"cores": 4,
	})

	lab := cleanLab("alpha")
	cfg := &config.Config{Labs: map[string]*config.Lab{"alpha": lab}}
	deps := newLifecycleDeps(t, cfg, ac)

	body, err := execLifecycle(newStatusCmd(), deps, "alpha")
	require.NoError(t, err)

	var table jsonTable
	require.NoError(t, json.Unmarshal([]byte(body), &table))
	require.Len(t, table.Rows, 2, "one node row plus a trailing summary row")
	assert.Empty(t, table.Rows[0][8], "WARNING column")
}
