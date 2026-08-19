package lab

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// netSubnetCreates filters rec down to the POST subnet-create bodies, in
// order, so a test can assert on exactly which subnets were provisioned.
func netSubnetCreates(rec []netRecordedRequest) []map[string]any {
	var creates []map[string]any
	for _, r := range rec {
		if r.method == http.MethodPost {
			creates = append(creates, r.body)
		}
	}
	return creates
}

// TestNetApplyFreshCreatesIPv6Subnets covers the default-on IPv6 path: a lab
// with no ipv6 keys at all gets, per vnet with an IPv4 subnet, an IPv6
// subnet too — the whole derived ULA /48 (gatewayed at the mgmt /64's ::1)
// on the primary vnet, mirroring the v4 shape (whole network.cidr, mgmt
// gateway), and the carved /64 (gatewayed at its own ::1) on each extra
// vnet.
func TestNetApplyFreshCreatesIPv6Subnets(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := netTestLab("wayne")
	lab.Network.Vnets = []config.LabVnet{
		{ID: "waynest", Tag: 2, CIDR: "10.10.1.128/25", Gateway: "10.10.1.129"},
	}
	// netTestLab's mgmt has only a gateway; the /25 vnet above needs a CIDR
	// carve-out that stays inside cleanLab's /24, so the primary CIDR is
	// narrowed to the front /25.
	lab.Network.CIDR = "10.10.1.0/25"
	lab.Network.Mgmt.Gateway = "10.10.1.1"

	var discard, subnetRec, extraSubnetRec []netRecordedRequest
	netRecord(f, &discard, nil, "", "GET /api2/json/cluster/sdn/zones", []any{}, 200)
	netRecord(f, &discard, nil, "", "POST /api2/json/cluster/sdn/zones", map[string]any{}, 200)
	netRecord(f, &discard, nil, "", "GET /api2/json/cluster/sdn/vnets", []any{}, 200)
	netRecord(f, &discard, nil, "", "POST /api2/json/cluster/sdn/vnets", map[string]any{}, 200)
	netRecord(f, &subnetRec, nil, "", "GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{}, 200)
	netRecord(f, &subnetRec, nil, "", "POST /api2/json/cluster/sdn/vnets/labwayne/subnets", map[string]any{}, 200)
	netRecord(f, &extraSubnetRec, nil, "", "GET /api2/json/cluster/sdn/vnets/waynest/subnets", []any{}, 200)
	netRecord(f, &extraSubnetRec, nil, "", "POST /api2/json/cluster/sdn/vnets/waynest/subnets", map[string]any{}, 200)
	netRecord(f, &discard, nil, "", "GET /api2/json/cluster/sdn/dry-run",
		map[string]any{"frr-diff": "+x", "interfaces-diff": "+y"}, 200)
	netRecord(f, &discard, nil, "", "PUT /api2/json/cluster/sdn", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildNetCmd(t, path, f, "node1")

	_, err := runNetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)

	block, err := labULAPrefix(lab.Network)
	require.NoError(t, err)
	gw6, err := labMgmtGateway6(lab.Network)
	require.NoError(t, err)

	creates := netSubnetCreates(subnetRec)
	require.Len(t, creates, 2, "primary vnet must get one IPv4 and one IPv6 subnet create")
	assert.Equal(t, "10.10.1.0/25", creates[0]["subnet"])
	assert.Equal(t, "10.10.1.1", creates[0]["gateway"])
	assert.Equal(t, block.String(), creates[1]["subnet"])
	assert.Equal(t, gw6, creates[1]["gateway"])

	vnet6, err := labVnetCIDR6(lab.Network, 0)
	require.NoError(t, err)
	vnetGw6, err := labV6Gateway(vnet6)
	require.NoError(t, err)

	extraCreates := netSubnetCreates(extraSubnetRec)
	require.Len(t, extraCreates, 2, "extra vnet must get one IPv4 and one IPv6 subnet create")
	assert.Equal(t, "10.10.1.128/25", extraCreates[0]["subnet"])
	assert.Equal(t, vnet6, extraCreates[1]["subnet"])
	assert.Equal(t, vnetGw6, extraCreates[1]["gateway"])
}

// TestNetApplyIPv6DisabledCreatesNoV6Subnet pins the opt-out: with
// network.ipv6: false, net apply provisions exactly what it did before the
// IPv6 feature existed — one IPv4 subnet, no v6 anything.
func TestNetApplyIPv6DisabledCreatesNoV6Subnet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := netTestLab("wayne")
	off := false
	lab.Network.IPv6 = &off

	var discard, subnetRec []netRecordedRequest
	netRecord(f, &discard, nil, "", "GET /api2/json/cluster/sdn/zones", []any{}, 200)
	netRecord(f, &discard, nil, "", "POST /api2/json/cluster/sdn/zones", map[string]any{}, 200)
	netRecord(f, &discard, nil, "", "GET /api2/json/cluster/sdn/vnets", []any{}, 200)
	netRecord(f, &discard, nil, "", "POST /api2/json/cluster/sdn/vnets", map[string]any{}, 200)
	netRecord(f, &subnetRec, nil, "", "GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{}, 200)
	netRecord(f, &subnetRec, nil, "", "POST /api2/json/cluster/sdn/vnets/labwayne/subnets", map[string]any{}, 200)
	netRecord(f, &discard, nil, "", "GET /api2/json/cluster/sdn/dry-run",
		map[string]any{"frr-diff": "+x", "interfaces-diff": "+y"}, 200)
	netRecord(f, &discard, nil, "", "PUT /api2/json/cluster/sdn", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildNetCmd(t, path, f, "node1")

	_, err := runNetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)

	creates := netSubnetCreates(subnetRec)
	require.Len(t, creates, 1, "ipv6: false must leave the plan IPv4-only")
	assert.Equal(t, "10.10.1.0/24", creates[0]["subnet"])
}

// TestNetApplyIPv6SubnetAlreadyPresentSkipsCreate pins idempotence for the
// IPv6 half specifically: when the vnet already carries the lab's v6 subnet
// at the right gateway, a re-apply issues no subnet create at all.
func TestNetApplyIPv6SubnetAlreadyPresentSkipsCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := netTestLab("wayne")

	block, err := labULAPrefix(lab.Network)
	require.NoError(t, err)
	gw6, err := labMgmtGateway6(lab.Network)
	require.NoError(t, err)

	existingSubnets := []any{
		map[string]any{"subnet": "labs-10.10.1.0-24", "cidr": "10.10.1.0/24", "gateway": "10.10.1.1"},
		map[string]any{"subnet": "labs-" + block.Addr().String() + "-48", "cidr": block.String(), "gateway": gw6},
	}

	var discard, subnetRec []netRecordedRequest
	netRecord(f, &discard, nil, "", "GET /api2/json/cluster/sdn/zones", []any{
		map[string]any{"zone": "labs", "type": "simple", "nodes": "node1", "mtu": 1450},
	}, 200)
	netRecord(f, &discard, nil, "", "GET /api2/json/cluster/sdn/vnets", []any{
		map[string]any{"vnet": "labwayne", "zone": "labs", "alias": "lab-wayne"},
	}, 200)
	netRecord(f, &subnetRec, nil, "", "GET /api2/json/cluster/sdn/vnets/labwayne/subnets", existingSubnets, 200)
	netRecord(f, &subnetRec, nil, "", "POST /api2/json/cluster/sdn/vnets/labwayne/subnets", map[string]any{}, 200)
	netRecord(f, &discard, nil, "", "GET /api2/json/cluster/sdn/dry-run", map[string]any{}, 200)
	netRecord(f, &discard, nil, "", "PUT /api2/json/cluster/sdn", nil, 200)

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildNetCmd(t, path, f, "node1")

	_, err = runNetCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)

	assert.Empty(t, netSubnetCreates(subnetRec), "nothing drifted; no subnet create may be issued")
}
