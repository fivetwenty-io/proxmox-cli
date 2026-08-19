package lab

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/api/nodes"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// hostnetVmbr0Row returns a live vmbr0 list entry shaped like a real nested
// node's installer-written management bridge: IPv4-addressed, ports on the
// mgmt NIC, autostarted — and carrying whatever IPv6 fields extra holds.
// The addressing triple mirrors what PVE actually reports: cidr, address,
// AND netmask are always all populated (Interfaces.pm derives each from the
// others on every read), and netmask comes back as a prefix-length string
// ("24"), never a dotted quad.
func hostnetVmbr0Row(extra map[string]any) map[string]any {
	row := map[string]any{
		"iface": "vmbr0", "type": "bridge", "bridge_ports": "ens18",
		"autostart": 1, "cidr": "10.10.1.10/24", "address": "10.10.1.10",
		"netmask": "24", "gateway": "10.10.1.1",
	}
	maps.Copy(row, extra)
	return row
}

// TestHostnetApply_IPv6NoBonds_AddsV6ToVmbr0 covers the default-on IPv6
// path on a lab with no bonds at all: hostnet apply must stage the node's
// management IPv6 address (mgmt /64 offset 10+i, /48 interface prefix,
// mirroring the IPv4 host_ip/16-style convention) and gateway onto vmbr0 —
// carrying the existing IPv4 addressing and bridge_ports forward unchanged —
// then reload the node's staged changes exactly once.
func TestHostnetApply_IPv6NoBonds_AddsV6ToVmbr0(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := cleanLab("wayne")

	var listRec, updateRec, applyRec []hostnetRecordedRequest
	hostnetRecord(f, &listRec, nil, "", "GET /api2/json/nodes/lab-wayne-0/network",
		[]any{hostnetVmbr0Row(nil)}, 200)
	hostnetRecord(f, &updateRec, nil, "", "PUT /api2/json/nodes/lab-wayne-0/network/vmbr0", nil, 200)
	hostnetRecord(f, &applyRec, nil, "", "PUT /api2/json/nodes/lab-wayne-0/network", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake()
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)

	addr6, err := labNodeMgmtIP6(lab.Network, 0)
	require.NoError(t, err)
	gw6, err := labMgmtGateway6(lab.Network)
	require.NoError(t, err)

	require.Len(t, updateRec, 1, "vmbr0 must receive exactly one staged update")
	body := updateRec[0].body
	assert.Equal(t, "bridge", body["type"])
	assert.Equal(t, addr6+"/48", body["cidr6"])
	assert.Equal(t, gw6, body["gateway6"])
	assert.Equal(t, "10.10.1.10/24", body["cidr"], "existing IPv4 addressing must be carried forward")
	assert.Equal(t, "10.10.1.1", body["gateway"], "existing IPv4 gateway must be carried forward")
	assert.Equal(t, "ens18", body["bridge_ports"], "existing bridge_ports must be carried forward")

	require.Len(t, applyRec, 1, "staged changes must be reloaded exactly once")
	assert.Contains(t, out, "applied")
	assert.Empty(t, fake.Calls, "an IPv6-only apply has no NIC-naming phase, so no ssh at all")
}

// TestHostnetApply_IPv6PUT_CidrOnlyAddressing pins the exact addressing form
// every vmbr0 PUT may use: cidr/cidr6 ONLY. PVE's update_network runs
// $map_cidr_to_address_netmask before anything else and hard-rejects a body
// carrying both spellings ("address conflicts with cidr" / "address6
// conflicts with cidr6"), and its list endpoint ALWAYS reports all three
// IPv4 fields (deriving the missing ones on read) with netmask as a
// prefix-length string — which the netmask param's ipv4mask format rejects.
// So a PUT that blindly echoes list output back is rejected by every real
// PVE; the fixture here is exactly that PVE-shaped worst case, including an
// existing IPv6 triple with a drifted gateway6 to force the drift-repair
// path the second live failure rode on.
func TestHostnetApply_IPv6PUT_CidrOnlyAddressing(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := cleanLab("wayne")

	addr6, err := labNodeMgmtIP6(lab.Network, 0)
	require.NoError(t, err)
	gw6, err := labMgmtGateway6(lab.Network)
	require.NoError(t, err)

	var updateRec []hostnetRecordedRequest
	hostnetRecord(f, nil, nil, "", "GET /api2/json/nodes/lab-wayne-0/network",
		[]any{hostnetVmbr0Row(map[string]any{
			"cidr6": addr6 + "/48", "address6": addr6, "netmask6": "48", "gateway6": "fd00:dead::1",
		})}, 200)
	hostnetRecord(f, &updateRec, nil, "", "PUT /api2/json/nodes/lab-wayne-0/network/vmbr0", nil, 200)
	hostnetRecord(f, nil, nil, "", "PUT /api2/json/nodes/lab-wayne-0/network", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", exec.Fake())

	_, err = runHostnetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)

	require.Len(t, updateRec, 1, "drifted gateway6 must trigger exactly one staged update")
	body := updateRec[0].body
	assert.Equal(t, "10.10.1.10/24", body["cidr"])
	assert.Equal(t, addr6+"/48", body["cidr6"])
	assert.Equal(t, gw6, body["gateway6"])
	assert.NotContains(t, body, "address", "address alongside cidr is rejected by PVE")
	assert.NotContains(t, body, "netmask", "netmask alongside cidr is rejected by PVE")
	assert.NotContains(t, body, "address6", "address6 alongside cidr6 is rejected by PVE")
	assert.NotContains(t, body, "netmask6", "netmask6 alongside cidr6 is rejected by PVE")
}

// TestHostnetApply_IPv6HandWrittenAddress6_CarriedForwardAsCidr6 pins the
// fallback carry: an interface whose inet6 config was written by hand as
// address6+netmask6 with no cidr6 (PVE derives cidr6 on read, but a stale or
// minimal listing may not carry it) must survive an unrelated PUT as the
// joined cidr6 form — never as the conflicting address6+netmask6 pair.
func TestHostnetApply_IPv6HandWrittenAddress6_CarriedForwardAsCidr6(t *testing.T) {
	cur := hostnetIfaceState{
		Type: "bridge", Cidr: "10.10.1.10/24", Address: "10.10.1.10", Netmask: "24",
		Gateway: "10.10.1.1", Address6: "fd10:109:0:1::a", Netmask6: "48", Gateway6: "fd10:109:0:1::1",
	}
	params := &nodes.UpdateNetwork2Params{Type: "bridge"}
	hostnetPreserveUntouchedBridgeFields(cur, params)

	require.NotNil(t, params.Cidr)
	assert.Equal(t, "10.10.1.10/24", *params.Cidr)
	assert.Nil(t, params.Address)
	assert.Nil(t, params.Netmask)
	require.NotNil(t, params.Cidr6)
	assert.Equal(t, "fd10:109:0:1::a/48", *params.Cidr6)
	assert.Nil(t, params.Address6)
	assert.Nil(t, params.Netmask6)
	require.NotNil(t, params.Gateway6)
	assert.Equal(t, "fd10:109:0:1::1", *params.Gateway6)
}

// TestHostnetApply_IPv6AlreadyMatches_NoUpdateNoReload pins idempotence:
// when vmbr0 already carries the exact IPv6 address and gateway the plan
// wants, no update and no reload may be issued.
func TestHostnetApply_IPv6AlreadyMatches_NoUpdateNoReload(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := cleanLab("wayne")

	addr6, err := labNodeMgmtIP6(lab.Network, 0)
	require.NoError(t, err)
	gw6, err := labMgmtGateway6(lab.Network)
	require.NoError(t, err)

	var updateRec, applyRec []hostnetRecordedRequest
	hostnetRecord(f, nil, nil, "", "GET /api2/json/nodes/lab-wayne-0/network",
		[]any{hostnetVmbr0Row(map[string]any{"cidr6": addr6 + "/48", "gateway6": gw6})}, 200)
	hostnetRecord(f, &updateRec, nil, "", "PUT /api2/json/nodes/lab-wayne-0/network/vmbr0", nil, 200)
	hostnetRecord(f, &applyRec, nil, "", "PUT /api2/json/nodes/lab-wayne-0/network", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", exec.Fake())

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)

	assert.Empty(t, updateRec, "nothing drifted; vmbr0 must not be updated")
	assert.Empty(t, applyRec, "nothing staged; the reload must be skipped")
	assert.Contains(t, out, "already matches")
}

// hostnetIPv6Off opts lab out of the IPv6 phase, so a bond/bridge-focused
// test's fixtures and assertions stay exactly about bonds and bridges. The
// IPv6 phase has its own coverage in this file; the disabled-everything
// no-op shape is covered by TestHostnetApply_NoBondsConfigured_NoOp.
func hostnetIPv6Off(lab *config.Lab) *config.Lab {
	off := false
	lab.Network.IPv6 = &off
	return lab
}

// TestHostnetApply_IPv6Vmbr0Absent_SkipsWithNotice covers the one shape
// where the IPv6 phase cannot act: the node reports no vmbr0 at all (e.g. a
// bond retrofit whose management bridge is created in this same staged
// batch). The phase must report a visible skip row — never a hard error and
// never a blind update against a bridge the list did not return — and a
// re-run picks the bridge up once it exists.
func TestHostnetApply_IPv6Vmbr0Absent_SkipsWithNotice(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := cleanLab("wayne")

	var updateRec []hostnetRecordedRequest
	hostnetRecord(f, nil, nil, "", "GET /api2/json/nodes/lab-wayne-0/network", []any{}, 200)
	hostnetRecord(f, &updateRec, nil, "", "PUT /api2/json/nodes/lab-wayne-0/network/vmbr0", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", exec.Fake())

	out, err := runHostnetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)
	assert.Empty(t, updateRec)
	assert.Contains(t, out, "skip")
	assert.Contains(t, out, "vmbr0")
}

// TestHostnetApply_IPv6DryRun_PreviewsRow pins the --dry-run preview: the
// IPv6 phase must appear as a would-run step, with zero API and ssh calls
// (the fake PVE has no handlers registered, so any call would 404 the run).
func TestHostnetApply_IPv6DryRun_PreviewsRow(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := cleanLab("wayne")

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	fake := exec.Fake()
	cmd := buildHostnetCmd(t, path, f, "lab-wayne", fake)

	out, err := runHostnetCmd(t, cmd, "apply", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "IPv6")
	assert.Contains(t, out, "vmbr0")
	assert.Contains(t, out, "would run")
	assert.Empty(t, fake.Calls)
}
