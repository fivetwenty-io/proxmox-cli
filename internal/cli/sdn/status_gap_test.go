package sdn

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
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

	// Verify vnets sub-commands.
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
	for _, want := range []string{"get", "mac-vrf"} {
		require.True(t, vnetsSub[want], "status vnets must expose %q", want)
	}

	// Verify fabrics sub-commands.
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
	for _, want := range []string{"get", "interfaces", "neighbors", "routes"} {
		require.True(t, fabricsSub[want], "status fabrics must expose %q", want)
	}
}

// TestStatusListSdn verifies `sdn status` calls GET /nodes/{node}/sdn.
func TestStatusListSdn(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn", []any{
		map[string]any{"id": "zone1", "type": "simple"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status")
	require.NoError(t, err)
	require.Contains(t, out, "zone1")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn", rec[0].path)
}

// TestStatusListSdnRequiresNode verifies an error is returned when no node is set.
func TestStatusListSdnRequiresNode(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn", []any{}, 200)

	_, err := run(t, f, "", "status")
	require.Error(t, err)
	require.ErrorContains(t, err, "no node specified")
	require.Empty(t, rec)
}

// TestStatusZonesList verifies `sdn status zones` calls GET /nodes/{node}/sdn/zones.
func TestStatusZonesList(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/zones", []any{
		map[string]any{"zone": "pvecli", "type": "simple"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "zones")
	require.NoError(t, err)
	require.Contains(t, out, "pvecli")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/zones", rec[0].path)
}

// TestStatusZonesGet verifies `sdn status zones get <zone>` calls GET /nodes/{node}/sdn/zones/{zone}.
func TestStatusZonesGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/zones/pvecli", []any{
		map[string]any{"zone": "pvecli", "status": "ok"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "zones", "get", "pvecli")
	require.NoError(t, err)
	require.Contains(t, out, "pvecli")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/zones/pvecli", rec[0].path)
}

// TestStatusZonesBridges verifies `sdn status zones bridges <zone>` calls
// GET /nodes/{node}/sdn/zones/{zone}/bridges.
func TestStatusZonesBridges(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/zones/pvecli/bridges", []any{
		map[string]any{"iface": "vmbr0"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "zones", "bridges", "pvecli")
	require.NoError(t, err)
	require.Contains(t, out, "vmbr0")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/zones/pvecli/bridges", rec[0].path)
}

// TestStatusZonesContent verifies `sdn status zones content <zone>` calls
// GET /nodes/{node}/sdn/zones/{zone}/content.
func TestStatusZonesContent(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/zones/pvecli/content", []any{
		map[string]any{"vnet": "pvecli0"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "zones", "content", "pvecli")
	require.NoError(t, err)
	require.Contains(t, out, "pvecli0")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/zones/pvecli/content", rec[0].path)
}

// TestStatusZonesIpVrf verifies `sdn status zones ip-vrf <zone>` calls
// GET /nodes/{node}/sdn/zones/{zone}/ip-vrf.
func TestStatusZonesIpVrf(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/zones/pvecli/ip-vrf", []any{
		map[string]any{"name": "vrf1"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "zones", "ip-vrf", "pvecli")
	require.NoError(t, err)
	require.Contains(t, out, "vrf1")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/zones/pvecli/ip-vrf", rec[0].path)
}

// TestStatusVnetsGet verifies `sdn status vnets get <vnet>` calls
// GET /nodes/{node}/sdn/vnets/{vnet}.
func TestStatusVnetsGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/vnets/pvecli0", []any{
		map[string]any{"vnet": "pvecli0", "status": "ok"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "vnets", "get", "pvecli0")
	require.NoError(t, err)
	require.Contains(t, out, "pvecli0")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/vnets/pvecli0", rec[0].path)
}

// TestStatusVnetsMacVrf verifies `sdn status vnets mac-vrf <vnet>` calls
// GET /nodes/{node}/sdn/vnets/{vnet}/mac-vrf.
func TestStatusVnetsMacVrf(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/vnets/pvecli0/mac-vrf", []any{
		map[string]any{"mac": "aa:bb:cc:dd:ee:ff"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "vnets", "mac-vrf", "pvecli0")
	require.NoError(t, err)
	require.Contains(t, out, "aa:bb:cc:dd:ee:ff")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/vnets/pvecli0/mac-vrf", rec[0].path)
}

// TestStatusFabricsGet verifies `sdn status fabrics get <fabric>` calls
// GET /nodes/{node}/sdn/fabrics/{fabric}.
func TestStatusFabricsGet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/nodes/pve1/sdn/fabrics/fab1", []any{
		map[string]any{"id": "fab1", "status": "up"},
	}, 200)

	out, err := run(t, f, "", "--node", "pve1", "status", "fabrics", "get", "fab1")
	require.NoError(t, err)
	require.Contains(t, out, "fab1")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodGet, rec[0].method)
	require.Equal(t, "/api2/json/nodes/pve1/sdn/fabrics/fab1", rec[0].path)
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
