package sdn

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestZoneCreateFullSurface verifies every type-specific zone attribute is
// forwarded by `zone create`, not only the base bridge/nodes/ipam set.
func TestZoneCreateFullSurface(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/zones", map[string]any{}, 200)

	_, err := run(t, f, "", "zone", "create", "evpnz",
		"--type", "evpn",
		"--controller", "evpnctl",
		"--vrf-vxlan", "10000",
		"--mac", "0c:73:18:00:00:01",
		"--exitnodes", "pve1,pve2",
		"--exitnodes-primary", "pve1",
		"--exitnodes-local-routing",
		"--advertise-subnets",
		"--rt-import", "65000:10000",
		"--fabric", "fab0",
		"--peers", "10.0.0.1,10.0.0.2",
		"--tag", "200",
		"--vlan-protocol", "802.1ad",
		"--vxlan-port", "4789",
		"--mtu", "1450",
		"--dhcp", "dnsmasq",
		"--dns", "dnsapi",
		"--dnszone", "example.com",
		"--reversedns", "revapi",
		"--bridge-disable-mac-learning",
		"--disable-arp-nd-suppression",
		"--dp-id", "7",
		"--secondary-controller", "ctl2",
		"--lock-token", "tok123",
	)
	require.NoError(t, err)
	require.Len(t, rec, 1)
	b := rec[0].body
	require.Equal(t, "evpn", b["type"])
	require.Equal(t, "evpnctl", b["controller"])
	require.Equal(t, "10000", b["vrf-vxlan"])
	require.Equal(t, "0c:73:18:00:00:01", b["mac"])
	require.Equal(t, "pve1,pve2", b["exitnodes"])
	require.Equal(t, "pve1", b["exitnodes-primary"])
	require.Equal(t, "1", b["exitnodes-local-routing"])
	require.Equal(t, "1", b["advertise-subnets"])
	require.Equal(t, "65000:10000", b["rt-import"])
	require.Equal(t, "fab0", b["fabric"])
	require.Equal(t, "10.0.0.1,10.0.0.2", b["peers"])
	require.Equal(t, "200", b["tag"])
	require.Equal(t, "802.1ad", b["vlan-protocol"])
	require.Equal(t, "4789", b["vxlan-port"])
	require.Equal(t, "1450", b["mtu"])
	require.Equal(t, "dnsmasq", b["dhcp"])
	require.Equal(t, "dnsapi", b["dns"])
	require.Equal(t, "example.com", b["dnszone"])
	require.Equal(t, "revapi", b["reversedns"])
	require.Equal(t, "1", b["bridge-disable-mac-learning"])
	require.Equal(t, "1", b["disable-arp-nd-suppression"])
	require.Equal(t, "7", b["dp-id"])
	require.Equal(t, "ctl2", b["secondary-controllers"])
	require.Equal(t, "tok123", b["lock-token"])
}

// TestZoneCreateOmitsTypeSpecific verifies a minimal zone create does not leak
// any of the newly wired optional attributes.
func TestZoneCreateOmitsTypeSpecific(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/zones", map[string]any{}, 200)

	_, err := run(t, f, "", "zone", "create", "simplez")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	for _, k := range []string{"controller", "vrf-vxlan", "mac", "exitnodes", "peers", "fabric", "lock-token"} {
		require.NotContains(t, rec[0].body, k, "unset optional %q must not be sent", k)
	}
}

func TestZoneListFilters(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/zones", []any{}, 200)

	_, err := run(t, f, "", "zone", "list", "--pending", "--running", "--type", "evpn")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "1", rec[0].query.Get("pending"))
	require.Equal(t, "1", rec[0].query.Get("running"))
	require.Equal(t, "evpn", rec[0].query.Get("type"))
}

func TestZoneDeleteLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/zones/pmxcli", map[string]any{}, 200)

	_, err := run(t, f, "", "zone", "delete", "pmxcli", "--yes", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "tok", rec[0].query.Get("lock-token"))
}

func TestVnetCreateNewFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/vnets", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "create", "pmxcli0", "--zone", "pmxcli",
		"--vlanaware", "--isolate-ports", "--type", "vnet", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "1", rec[0].body["vlanaware"])
	require.Equal(t, "1", rec[0].body["isolate-ports"])
	require.Equal(t, "vnet", rec[0].body["type"])
	require.Equal(t, "tok", rec[0].body["lock-token"])
}

func TestVnetListFilters(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets", []any{}, 200)

	_, err := run(t, f, "", "vnet", "list", "--pending", "--running")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "1", rec[0].query.Get("pending"))
	require.Equal(t, "1", rec[0].query.Get("running"))
}

func TestVnetDeleteLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/vnets/pmxcli0", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "delete", "pmxcli0", "--yes", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "tok", rec[0].query.Get("lock-token"))
}

func TestSubnetCreateNewFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/vnets/pmxcli0/subnets", map[string]any{}, 200)

	_, err := run(t, f, "", "subnet", "create", "pmxcli0", "10.241.0.0/24",
		"--dhcp-dns-server", "10.241.0.2",
		"--dhcp-range", "start-address=10.241.0.10,end-address=10.241.0.20",
		"--dnszoneprefix", "adm",
		"--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "10.241.0.2", rec[0].body["dhcp-dns-server"])
	require.Equal(t, "start-address=10.241.0.10,end-address=10.241.0.20", rec[0].body["dhcp-range"])
	require.Equal(t, "adm", rec[0].body["dnszoneprefix"])
	require.Equal(t, "tok", rec[0].body["lock-token"])
}

func TestSubnetListFilters(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "GET /api2/json/cluster/sdn/vnets/pmxcli0/subnets", []any{}, 200)

	_, err := run(t, f, "", "subnet", "list", "pmxcli0", "--pending", "--running")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "1", rec[0].query.Get("pending"))
	require.Equal(t, "1", rec[0].query.Get("running"))
}

func TestSubnetDeleteLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/vnets/pmxcli0/subnets/sub0", map[string]any{}, 200)

	_, err := run(t, f, "", "subnet", "delete", "pmxcli0", "sub0", "--yes", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "tok", rec[0].query.Get("lock-token"))
}

func TestApplyLockFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn", nil, 200)

	out, err := run(t, f, "", "apply", "--lock-token", "tok", "--release-lock")
	require.NoError(t, err)
	require.Contains(t, out, "applied")
	require.Len(t, rec, 1)
	require.Equal(t, http.MethodPut, rec[0].method)
	require.Equal(t, "tok", rec[0].body["lock-token"])
	require.Equal(t, "1", rec[0].body["release-lock"])
}

func TestControllerCreateLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/controllers", map[string]any{}, 200)

	_, err := run(t, f, "", "controller", "create", "ctl0", "--type", "evpn",
		"--asn", "65000", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "evpn", rec[0].body["type"])
	require.Equal(t, "65000", rec[0].body["asn"])
	require.Equal(t, "tok", rec[0].body["lock-token"])
}

func TestControllerDeleteLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/controllers/ctl0", map[string]any{}, 200)

	_, err := run(t, f, "", "controller", "delete", "ctl0", "--yes", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "tok", rec[0].query.Get("lock-token"))
}

func TestIpamCreateLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/ipams", map[string]any{}, 200)

	_, err := run(t, f, "", "ipam", "create", "ipam0", "--type", "pve", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "pve", rec[0].body["type"])
	require.Equal(t, "tok", rec[0].body["lock-token"])
}

func TestIpamDeleteLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/ipams/ipam0", map[string]any{}, 200)

	_, err := run(t, f, "", "ipam", "delete", "ipam0", "--yes", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "tok", rec[0].query.Get("lock-token"))
}

func TestDnsCreateLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/dns", map[string]any{}, 200)

	_, err := run(t, f, "", "dns", "create", "dns0",
		"--type", "powerdns", "--url", "https://pdns.example.com", "--key", "secret",
		"--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "powerdns", rec[0].body["type"])
	require.Equal(t, "tok", rec[0].body["lock-token"])
}

func TestDnsDeleteLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/dns/dns0", map[string]any{}, 200)

	_, err := run(t, f, "", "dns", "delete", "dns0", "--yes", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "tok", rec[0].query.Get("lock-token"))
}

func TestVnetSetLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/vnets/pmxcli0", map[string]any{}, 200)

	_, err := run(t, f, "", "vnet", "set", "pmxcli0", "--alias", "e2e", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "e2e", rec[0].body["alias"])
	require.Equal(t, "tok", rec[0].body["lock-token"])
}

func TestControllerSetLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/controllers/ctl0", map[string]any{}, 200)

	_, err := run(t, f, "", "controller", "set", "ctl0", "--asn", "65001", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "65001", rec[0].body["asn"])
	require.Equal(t, "tok", rec[0].body["lock-token"])
}

func TestIpamSetLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/ipams/ipam0", map[string]any{}, 200)

	_, err := run(t, f, "", "ipam", "set", "ipam0",
		"--url", "https://ipam.example.com", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "https://ipam.example.com", rec[0].body["url"])
	require.Equal(t, "tok", rec[0].body["lock-token"])
}

func TestDnsSetLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/dns/dns0", map[string]any{}, 200)

	_, err := run(t, f, "", "dns", "set", "dns0", "--ttl", "600", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "600", rec[0].body["ttl"])
	require.Equal(t, "tok", rec[0].body["lock-token"])
}

func TestPrefixListDeleteLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/prefix-lists/pl1", map[string]any{}, 200)

	_, err := run(t, f, "", "prefix-list", "delete", "pl1", "--yes", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "tok", rec[0].query.Get("lock-token"))
}

func TestPrefixListEntryDeleteLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/prefix-lists/pl1/entries/10", map[string]any{}, 200)

	_, err := run(t, f, "", "prefix-list", "entry", "delete", "pl1", "10", "--yes", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "tok", rec[0].query.Get("lock-token"))
}

func TestRouteMapEntryDeleteLockToken(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "DELETE /api2/json/cluster/sdn/route-maps/entries/rm1/entry/10", map[string]any{}, 200)

	_, err := run(t, f, "", "route-map", "entry", "delete", "rm1", "10", "--yes", "--lock-token", "tok")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.Equal(t, "tok", rec[0].query.Get("lock-token"))
}

// --- M1: --redistribute on fabric create / set ---

// TestFabricCreateRedistribute verifies --redistribute values reach the
// `redistribute` API parameter and that multiple occurrences are forwarded.
func TestFabricCreateRedistribute(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/fabrics/fabric", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "create", "fab1",
		"--protocol", "ospf",
		"--redistribute", "connected",
		"--redistribute", "static")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	// The API expects redistribute as a JSON array of the repeated values.
	var got []string
	require.NoError(t, json.Unmarshal([]byte(rec[0].body["redistribute"].(string)), &got))
	require.Equal(t, []string{"connected", "static"}, got,
		"every --redistribute value must reach API key \"redistribute\"")
}

// TestFabricCreateRedistributeAbsent verifies that when --redistribute is not
// passed on create, the `redistribute` key is absent from the request.
func TestFabricCreateRedistributeAbsent(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/fabrics/fabric", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "create", "fab1", "--protocol", "ospf")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.NotContains(t, rec[0].body, "redistribute",
		"omitted --redistribute must not appear in the request body")
}

// TestFabricSetRedistribute verifies --redistribute is forwarded on set when
// explicitly passed and gated (absent when not passed).
func TestFabricSetRedistribute(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/fabrics/fabric/fab1", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "set", "fab1",
		"--protocol", "ospf",
		"--redistribute", "bgp")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	var got []string
	require.NoError(t, json.Unmarshal([]byte(rec[0].body["redistribute"].(string)), &got))
	require.Equal(t, []string{"bgp"}, got,
		"--redistribute value must reach API key \"redistribute\" on set")
}

// TestFabricSetRedistributeAbsent verifies that when --redistribute is not
// passed on set, the `redistribute` key is absent (gated behind Changed).
func TestFabricSetRedistributeAbsent(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/fabrics/fabric/fab1", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "set", "fab1", "--protocol", "ospf", "--area", "0.0.0.1")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.NotContains(t, rec[0].body, "redistribute",
		"omitted --redistribute must not appear in the set request body")
}

// --- M2: --interface on fabric node create / set ---

// TestFabricNodeCreateInterface verifies --interface values reach the
// `interfaces` API parameter (singular flag → plural API key).
func TestFabricNodeCreateInterface(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/fabrics/node/fab1", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "node", "create", "fab1", "n1",
		"--protocol", "openfabric",
		"--interface", "eth0",
		"--interface", "eth1")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	var got []string
	require.NoError(t, json.Unmarshal([]byte(rec[0].body["interfaces"].(string)), &got))
	require.Equal(t, []string{"eth0", "eth1"}, got,
		"every --interface value must reach API key \"interfaces\"")
}

// TestFabricNodeCreateInterfaceAbsent verifies that when --interface is not
// passed on create, the `interfaces` key is absent from the request.
func TestFabricNodeCreateInterfaceAbsent(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "POST /api2/json/cluster/sdn/fabrics/node/fab1", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "node", "create", "fab1", "n1",
		"--protocol", "openfabric", "--ip", "10.0.0.1")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.NotContains(t, rec[0].body, "interfaces",
		"omitted --interface must not appear in the request body")
}

// TestFabricNodeSetInterface verifies --interface is forwarded on set when
// explicitly passed.
func TestFabricNodeSetInterface(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/fabrics/node/fab1/n1", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "node", "set", "fab1", "n1",
		"--protocol", "openfabric",
		"--interface", "bond0")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	var got []string
	require.NoError(t, json.Unmarshal([]byte(rec[0].body["interfaces"].(string)), &got))
	require.Equal(t, []string{"bond0"}, got,
		"--interface value must reach API key \"interfaces\" on set")
}

// TestFabricNodeSetInterfaceAbsent verifies that when --interface is not
// passed on set, the `interfaces` key is absent (gated behind Changed).
func TestFabricNodeSetInterfaceAbsent(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec []recordedRequest
	record(f, &rec, "PUT /api2/json/cluster/sdn/fabrics/node/fab1/n1", map[string]any{}, 200)

	_, err := run(t, f, "", "fabric", "node", "set", "fab1", "n1",
		"--protocol", "openfabric", "--ip", "10.0.0.2")
	require.NoError(t, err)
	require.Len(t, rec, 1)
	require.NotContains(t, rec[0].body, "interfaces",
		"omitted --interface must not appear in the set request body")
}
