package pdm

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// TestNodeSdnVnetMacVrf_SendsRemoteAndRendersSingle asserts that `node sdn
// vnet mac-vrf` sends the required --remote query parameter and renders the
// MAC-VRF entry.
func TestNodeSdnVnetMacVrf_SendsRemoteAndRendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/nodes/pdm-host/sdn/vnets/vnet1/mac-vrf", &rec, map[string]any{
		"ip": "10.0.0.5", "mac": "aa:bb:cc:dd:ee:ff", "nexthop": "10.0.0.1",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeSdnVnetMacVrfCmd(), "mac-vrf", "pdm-host", "vnet1", "--remote", "alpha")
	require.NoError(t, err)

	require.Equal(t, "alpha", rec.query.Get("remote"))
	require.Contains(t, buf.String(), "aa:bb:cc:dd:ee:ff")
}

// TestNodeSdnVnetMacVrf_RequiresRemote asserts that --remote is required.
func TestNodeSdnVnetMacVrf_RequiresRemote(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeSdnVnetMacVrfCmd(), "mac-vrf", "pdm-host", "vnet1")
	require.Error(t, err)
}

// TestNodeSdnZoneIPVrf_SendsRemoteAndRendersSingle asserts that `node sdn
// zone ip-vrf` sends the required --remote query parameter and renders the
// IP-VRF entry.
func TestNodeSdnZoneIPVrf_SendsRemoteAndRendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/nodes/pdm-host/sdn/zones/zone1/ip-vrf", &rec, map[string]any{
		"ip": "10.0.0.0/24", "metric": 100, "nexthops": []string{"10.0.0.1"}, "protocol": "bgp",
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeSdnZoneIPVrfCmd(), "ip-vrf", "pdm-host", "zone1", "--remote", "alpha")
	require.NoError(t, err)

	require.Equal(t, "alpha", rec.query.Get("remote"))
	require.Contains(t, buf.String(), "bgp")
}

// TestNodeSdnZoneIPVrf_RequiresRemote asserts that --remote is required.
func TestNodeSdnZoneIPVrf_RequiresRemote(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var buf bytes.Buffer
	err := run(deps, &buf, newNodeSdnZoneIPVrfCmd(), "ip-vrf", "pdm-host", "zone1")
	require.Error(t, err)
}
