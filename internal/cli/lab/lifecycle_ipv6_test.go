package lab

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// statusIPv6Fixture registers a running node-0 VM whose guest agent reports
// the given IPv6 addresses on eth0, beside its IPv4 one.
func statusIPv6Fixture(t *testing.T, v6 []map[string]any) string {
	t.Helper()
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f, map[string]any{
		"vmid": 100, "node": "pve1", "pool": "lab-alpha", "name": "lab-alpha",
		"status": "running", "type": "qemu",
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/status/current", map[string]any{
		"status": "running", "vmid": 100,
	})
	addrs := []any{map[string]any{"ip-address": "10.10.1.50", "ip-address-type": "ipv4", "prefix": 24}}
	for _, a := range v6 {
		addrs = append(addrs, a)
	}
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/agent/network-get-interfaces", map[string]any{
		"result": []any{map[string]any{"name": "eth0", "ip-addresses": addrs}},
	})
	f.HandleJSON("GET /api2/json/nodes/pve1/qemu/100/config", map[string]any{"cores": 4})

	lab := cleanLab("alpha")
	deps := newLifecycleDeps(t, &config.Config{Labs: map[string]*config.Lab{"alpha": lab}}, ac)
	body, err := execLifecycle(newStatusCmd(), deps, "alpha")
	require.NoError(t, err)
	return body
}

// statusIP6Cell returns the node row's IP6 cell from a rendered status table.
func statusIP6Cell(t *testing.T, body string) string {
	t.Helper()
	var table jsonTable
	require.NoError(t, json.Unmarshal([]byte(body), &table))
	require.NotEmpty(t, table.Rows)
	return table.Rows[0][5]
}

// TestStatus_ReportsLiveGlobalIPv6 covers the verification surface the
// dual-stack rollout needs: `lab status` reports the address the node
// actually holds, so an operator can see convergence rather than infer it.
func TestStatus_ReportsLiveGlobalIPv6(t *testing.T) {
	body := statusIPv6Fixture(t, []map[string]any{
		{"ip-address": "fd15:8c8e:bba8:1::a", "ip-address-type": "ipv6", "prefix": 48},
	})
	assert.Equal(t, "fd15:8c8e:bba8:1::a", statusIP6Cell(t, body))
}

// TestStatus_SkipsLinkLocalIPv6 pins the address selection: every interface
// has an fe80:: address, and reporting it would tell the operator nothing
// about whether the lab's address plan was applied.
func TestStatus_SkipsLinkLocalIPv6(t *testing.T) {
	body := statusIPv6Fixture(t, []map[string]any{
		{"ip-address": "fe80::5054:ff:fe12:3456", "ip-address-type": "ipv6", "prefix": 64},
	})
	assert.Equal(t, "fd15:8c8e:bba8:1::a", statusIP6Cell(t, body),
		"a link-local-only guest falls back to the planned address, not fe80::")
}

// TestStatus_IPv4OnlyLabReportsNoIPv6 pins the opt-out's display half: a lab
// with `network.ipv6: false` shows "-", never an address its plan never
// asked for.
func TestStatus_IPv4OnlyLabReportsNoIPv6(t *testing.T) {
	f, ac := newLifecycleFakeClient(t)
	handleClusterResources(f)

	lab := cleanLab("beta")
	off := false
	lab.Network.IPv6 = &off
	deps := newLifecycleDeps(t, &config.Config{Labs: map[string]*config.Lab{"beta": lab}}, ac)

	body, err := execLifecycle(newStatusCmd(), deps, "beta")
	require.NoError(t, err)
	assert.Equal(t, "-", statusIP6Cell(t, body))
}
