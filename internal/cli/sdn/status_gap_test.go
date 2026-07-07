package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// TestStatusCommandTree verifies the sdn status sub-group and all 12 endpoint
// commands are reachable from the root Group.
func TestStatusCommandTree(t *testing.T) {
	root := Group(nil)

	var statusCmd, zonesCmd, vnetsCmd, fabricsCmd interface {
		Commands() []interface{ Name() string }
	}
	_ = statusCmd
	_ = zonesCmd
	_ = vnetsCmd
	_ = fabricsCmd

	// Find status under sdn root.
	topNames := map[string]bool{}
	for _, c := range root.Commands() {
		topNames[c.Name()] = true
	}
	require.True(t, topNames["status"], "sdn must expose \"status\"")

	// Find status command.
	var status, zones, vnets, fabrics interface{ Commands() []interface{} }
	_ = status
	_ = zones
	_ = vnets
	_ = fabrics

	statusSub := map[string]bool{}
	for _, c := range root.Commands() {
		if c.Name() == "status" {
			for _, sc := range c.Commands() {
				statusSub[sc.Name()] = true
			}
		}
	}
	for _, want := range []string{"zones", "vnets", "fabrics"} {
		require.True(t, statusSub[want], "status must expose %q", want)
	}

	// Verify zones sub-commands.
	zonesSub := map[string]bool{}
	for _, c := range root.Commands() {
		if c.Name() == "status" {
			for _, sc := range c.Commands() {
				if sc.Name() == "zones" {
					for _, ssc := range sc.Commands() {
						zonesSub[ssc.Name()] = true
					}
				}
			}
		}
	}
	for _, want := range []string{"get", "bridges", "content", "ip-vrf"} {
		require.True(t, zonesSub[want], "status zones must expose %q", want)
	}

	// Verify vnets sub-commands. There is no `vnets get`: the per-vnet GET is
	// only a directory index, so mac-vrf is the vnet-level live view.
	vnetsSub := map[string]bool{}
	for _, c := range root.Commands() {
		if c.Name() == "status" {
			for _, sc := range c.Commands() {
				if sc.Name() == "vnets" {
					for _, ssc := range sc.Commands() {
						vnetsSub[ssc.Name()] = true
					}
				}
			}
		}
	}
	require.True(t, vnetsSub["mac-vrf"], "status vnets must expose mac-vrf")
	require.False(t, vnetsSub["get"],
		"status vnets get was removed: its endpoint is a directory index")

	// Verify fabrics sub-commands. There is no `fabrics get`: the per-fabric
	// GET is only a directory index of routes/neighbors/interfaces.
	fabricsSub := map[string]bool{}
	for _, c := range root.Commands() {
		if c.Name() == "status" {
			for _, sc := range c.Commands() {
				if sc.Name() == "fabrics" {
					for _, ssc := range sc.Commands() {
						fabricsSub[ssc.Name()] = true
					}
				}
			}
		}
	}
	for _, want := range []string{"interfaces", "neighbors", "routes"} {
		require.True(t, fabricsSub[want], "status fabrics must expose %q", want)
	}
	require.False(t, fabricsSub["get"],
		"status fabrics get was removed: its endpoint is a directory index")
}

// TestStatusRootShowsHelp verifies the bare `sdn status` group makes no API
// call: GET /nodes/{node}/sdn is only a directory index, so the group shows
// help and defers to its sub-commands.
func TestStatusRootShowsHelp(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn", []any{
		map[string]any{"id": "zone1", "type": "simple"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status")
	require.NoError(t, err)
	require.Contains(t, out, "zones", "help must list the zones sub-command")
	require.Empty(t, rec, "the status group itself must not call the API")
}

// TestStatusZonesList verifies `sdn status zones` calls GET /nodes/{node}/sdn/zones.
func TestStatusZonesList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/zones", []any{
		map[string]any{"zone": "pmxcli", "type": "simple"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "zones")
	require.NoError(t, err)
	require.Contains(t, out, "pmxcli")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/zones", rec[0].path)
}

// TestStatusZonesGet verifies `sdn status zones get <zone>` filters the zones
// status list to the requested zone: the per-zone GET is only a directory
// index (content, bridges, ip-vrf).
func TestStatusZonesGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/zones", []any{
		map[string]any{"zone": "pmxcli", "status": "ok"},
		map[string]any{"zone": "other", "status": "error"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "zones", "get", "pmxcli")
	require.NoError(t, err)
	require.Contains(t, out, "pmxcli")
	require.NotContains(t, out, "other", "only the requested zone must be shown")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/zones", rec[0].path)
}

// TestStatusZonesGetNotFound verifies an unknown zone errors instead of
// rendering an empty table.
func TestStatusZonesGetNotFound(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/zones", []any{
		map[string]any{"zone": "other", "status": "ok"},
	}, 200)

	_, err := run(t, f, "", "--node", "pve1", "status", "zones", "get", "pmxcli")
	require.Error(t, err)
	require.ErrorContains(t, err, "not found")
}

// TestStatusZonesBridges verifies `sdn status zones bridges <zone>` calls
// GET /nodes/{node}/sdn/zones/{zone}/bridges.
func TestStatusZonesBridges(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/zones/pmxcli/bridges", []any{
		map[string]any{"iface": "vmbr0"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "zones", "bridges", "pmxcli")
	require.NoError(t, err)
	require.Contains(t, out, "vmbr0")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/zones/pmxcli/bridges", rec[0].path)
}

// TestStatusZonesContent verifies `sdn status zones content <zone>` calls
// GET /nodes/{node}/sdn/zones/{zone}/content.
func TestStatusZonesContent(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/zones/pmxcli/content", []any{
		map[string]any{"vnet": "pmxcli0"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "zones", "content", "pmxcli")
	require.NoError(t, err)
	require.Contains(t, out, "pmxcli0")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/zones/pmxcli/content", rec[0].path)
}

// TestStatusZonesIpVrf verifies `sdn status zones ip-vrf <zone>` calls
// GET /nodes/{node}/sdn/zones/{zone}/ip-vrf.
func TestStatusZonesIpVrf(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/zones/pmxcli/ip-vrf", []any{
		map[string]any{"name": "vrf1"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "zones", "ip-vrf", "pmxcli")
	require.NoError(t, err)
	require.Contains(t, out, "vrf1")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/zones/pmxcli/ip-vrf", rec[0].path)
}

// TestStatusVnetsMacVrf verifies `sdn status vnets mac-vrf <vnet>` calls
// GET /nodes/{node}/sdn/vnets/{vnet}/mac-vrf.
func TestStatusVnetsMacVrf(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/vnets/pmxcli0/mac-vrf", []any{
		map[string]any{"mac": "aa:bb:cc:dd:ee:ff"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "vnets", "mac-vrf", "pmxcli0")
	require.NoError(t, err)
	require.Contains(t, out, "aa:bb:cc:dd:ee:ff")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/vnets/pmxcli0/mac-vrf", rec[0].path)
}

// TestStatusFabricsInterfaces verifies `sdn status fabrics interfaces <fabric>` calls
// GET /nodes/{node}/sdn/fabrics/{fabric}/interfaces.
func TestStatusFabricsInterfaces(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/fabrics/fab1/interfaces", []any{
		map[string]any{"iface": "eth0"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "fabrics", "interfaces", "fab1")
	require.NoError(t, err)
	require.Contains(t, out, "eth0")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/fabrics/fab1/interfaces", rec[0].path)
}

// TestStatusFabricsNeighbors verifies `sdn status fabrics neighbors <fabric>` calls
// GET /nodes/{node}/sdn/fabrics/{fabric}/neighbors.
func TestStatusFabricsNeighbors(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/fabrics/fab1/neighbors", []any{
		map[string]any{"neighbor": "pve2"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "fabrics", "neighbors", "fab1")
	require.NoError(t, err)
	require.Contains(t, out, "pve2")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/fabrics/fab1/neighbors", rec[0].path)
}

// TestStatusFabricsRoutes verifies `sdn status fabrics routes <fabric>` calls
// GET /nodes/{node}/sdn/fabrics/{fabric}/routes.
func TestStatusFabricsRoutes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/fabrics/fab1/routes", []any{
		map[string]any{"prefix": "10.0.0.0/24"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "fabrics", "routes", "fab1")
	require.NoError(t, err)
	require.Contains(t, out, "10.0.0.0/24")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/fabrics/fab1/routes", rec[0].path)
}
