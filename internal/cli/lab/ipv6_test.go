package lab

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// ipv6TestNetwork is the fleet-conventional lab network shape the derivation
// helpers run against: a /16 lab block with the mgmt /24 at its base.
func ipv6TestNetwork() config.LabNetwork {
	return config.LabNetwork{
		VnetID: "krutten",
		CIDR:   "10.109.0.0/16",
		Mgmt: config.LabMgmt{
			Subnet:  "10.109.0.0/24",
			HostIP:  "10.109.0.10",
			Gateway: "10.109.0.1",
		},
		BoshBloc: "10.109.16.0/20",
	}
}

func TestLabULAPrefix_DerivedShape(t *testing.T) {
	p, err := labULAPrefix(ipv6TestNetwork())
	require.NoError(t, err)

	assert.Equal(t, 48, p.Bits(), "derived ULA block must be a /48")
	assert.True(t, strings.HasPrefix(p.Addr().String(), "fd"),
		"derived prefix %s must sit in the fd00::/8 ULA range", p)
	assert.Equal(t, p.Masked(), p, "derived prefix must be in masked (network-base) form")
}

func TestLabULAPrefix_DeterministicPerCIDR(t *testing.T) {
	a, err := labULAPrefix(ipv6TestNetwork())
	require.NoError(t, err)
	b, err := labULAPrefix(ipv6TestNetwork())
	require.NoError(t, err)
	assert.Equal(t, a, b, "same IPv4 CIDR must always derive the same ULA block")

	other := ipv6TestNetwork()
	other.CIDR = "10.108.0.0/16"
	c, err := labULAPrefix(other)
	require.NoError(t, err)
	assert.NotEqual(t, a, c, "different IPv4 CIDRs must derive different ULA blocks")
}

// TestLabULAPrefix_CanonicalizesCIDR pins that a CIDR authored with host
// bits set derives the same block as its masked network-base form, matching
// labMgmtCIDR's own canonicalize-before-use convention.
func TestLabULAPrefix_CanonicalizesCIDR(t *testing.T) {
	a, err := labULAPrefix(ipv6TestNetwork())
	require.NoError(t, err)

	sloppy := ipv6TestNetwork()
	sloppy.CIDR = "10.109.0.5/16"
	b, err := labULAPrefix(sloppy)
	require.NoError(t, err)
	assert.Equal(t, a, b)
}

func TestLabULAPrefix_CIDR6Override(t *testing.T) {
	n := ipv6TestNetwork()
	n.CIDR6 = "fd10:109::/48"

	p, err := labULAPrefix(n)
	require.NoError(t, err)
	assert.Equal(t, netip.MustParsePrefix("fd10:109::/48"), p)
}

func TestLabULAPrefix_CIDR6Errors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cidr6 string
	}{
		{"unparsable", "not-a-cidr"},
		{"ipv4", "10.0.0.0/8"},
		{"narrower than /48", "fd10:109:0:1::/64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := ipv6TestNetwork()
			n.CIDR6 = tc.cidr6
			_, err := labULAPrefix(n)
			assert.Error(t, err)
		})
	}
}

func TestLabULAPrefix_NoV4CIDRToDeriveFrom(t *testing.T) {
	n := ipv6TestNetwork()
	n.CIDR = ""
	_, err := labULAPrefix(n)
	assert.Error(t, err)
}

// TestLabMgmtCIDR6_IsSubnetOneOfBlock pins the carving rule: the management
// /64 is subnet ID 1 of the lab's block, so for an explicit cidr6 the full
// address plan is predictable from the lab file alone.
func TestLabMgmtCIDR6_IsSubnetOneOfBlock(t *testing.T) {
	n := ipv6TestNetwork()
	n.CIDR6 = "fd10:109::/48"

	cidr6, err := labMgmtCIDR6(n)
	require.NoError(t, err)
	assert.Equal(t, "fd10:109:0:1::/64", cidr6)
}

func TestLabMgmtGateway6_IsOffsetOne(t *testing.T) {
	n := ipv6TestNetwork()
	n.CIDR6 = "fd10:109::/48"

	gw, err := labMgmtGateway6(n)
	require.NoError(t, err)
	assert.Equal(t, "fd10:109:0:1::1", gw)
}

// TestLabNodeMgmtIP6_MirrorsV4Offsets pins node addressing parity with
// labNodeMgmtIP: node i sits at offset 10+i (rendered hex: ::a..::e), and
// the QDevice at offset 15 (::f), inside the mgmt /64.
func TestLabNodeMgmtIP6_MirrorsV4Offsets(t *testing.T) {
	n := ipv6TestNetwork()
	n.CIDR6 = "fd10:109::/48"

	for i, want := range []string{
		"fd10:109:0:1::a", "fd10:109:0:1::b", "fd10:109:0:1::c", "fd10:109:0:1::d", "fd10:109:0:1::e",
	} {
		ip, err := labNodeMgmtIP6(n, i)
		require.NoError(t, err)
		assert.Equal(t, want, ip, "node %d", i)
	}

	qdev, err := labQdeviceMgmtIP6(n)
	require.NoError(t, err)
	assert.Equal(t, "fd10:109:0:1::f", qdev)
}

func TestLabNodeMgmtIP6_DerivedBlockContainsNodes(t *testing.T) {
	n := ipv6TestNetwork()

	block, err := labULAPrefix(n)
	require.NoError(t, err)

	ip, err := labNodeMgmtIP6(n, 0)
	require.NoError(t, err)
	assert.True(t, block.Contains(netip.MustParseAddr(ip)),
		"node 0 address %s must sit inside the derived block %s", ip, block)
}

// TestLabVnetCIDR6 pins extra-vnet carving: network.vnets[i] gets subnet ID
// 16+i of the lab block, an explicit per-vnet cidr6 overrides that, and a
// pure L2 vnet (no IPv4 CIDR) gets no IPv6 subnet at all — even when it
// carries a (validation-rejected) cidr6 override.
func TestLabVnetCIDR6(t *testing.T) {
	n := ipv6TestNetwork()
	n.CIDR6 = "fd10:109::/48"
	n.Vnets = []config.LabVnet{
		{ID: "storage", Tag: 1, CIDR: "10.109.32.0/24"},
		{ID: "workload", Tag: 2},
		{ID: "special", Tag: 3, CIDR: "10.109.33.0/24", CIDR6: "fd10:109:0:99::/64"},
		{ID: "l2tagged", Tag: 4, CIDR6: "fd10:109:0:88::/64"},
	}

	cidr6, err := labVnetCIDR6(n, 0)
	require.NoError(t, err)
	assert.Equal(t, "fd10:109:0:10::/64", cidr6, "vnet 0 is subnet ID 16 (hex 10)")

	cidr6, err = labVnetCIDR6(n, 1)
	require.NoError(t, err)
	assert.Empty(t, cidr6, "a pure L2 vnet must get no IPv6 subnet")

	cidr6, err = labVnetCIDR6(n, 2)
	require.NoError(t, err)
	assert.Equal(t, "fd10:109:0:99::/64", cidr6, "explicit cidr6 must win over the carved /64")

	cidr6, err = labVnetCIDR6(n, 3)
	require.NoError(t, err)
	assert.Empty(t, cidr6,
		"a pure L2 vnet stays subnet-less even with a cidr6 override — labIPv6PlanIssues rejects that "+
			"contradiction; this helper must not resolve it to a subnet")
}

// TestLabV6OffsetIP_RejectsPrefixNarrowerThan112 pins the host-offset
// contract: offsets live in the address's low 16 bits, so a subnet narrower
// than /112 cannot hold them — the base-with-low-bits-overwritten arithmetic
// would produce an address OUTSIDE the subnet (e.g. fd00::100/120's "::1"),
// which PVE then rejects as a gateway not in its own subnet. Must error,
// never mis-derive.
func TestLabV6OffsetIP_RejectsPrefixNarrowerThan112(t *testing.T) {
	_, err := labV6OffsetIP("fd10:109::100/120", 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/112")

	ip, err := labV6OffsetIP("fd10:109::/112", 1)
	require.NoError(t, err)
	assert.Equal(t, "fd10:109::1", ip, "/112 exactly still holds every 16-bit offset")
}

// TestLabNodeMgmtIP6_RejectsOutOfRangeIndex pins parity with labVnetNodeIP's
// bound: node indexes outside [0, maxLabNodeIndex] are a caller error. The
// IPv4 twin refuses them loudly; without this guard, index 5 would silently
// derive offset 15 — the QDevice's own ::f — as a duplicate address.
func TestLabNodeMgmtIP6_RejectsOutOfRangeIndex(t *testing.T) {
	n := ipv6TestNetwork()
	for _, i := range []int{-1, maxLabNodeIndex + 1} {
		_, err := labNodeMgmtIP6(n, i)
		assert.Error(t, err, "index %d must be rejected", i)
	}
}

func TestLabV6Gateway_IsOffsetOneOfSubnet(t *testing.T) {
	gw, err := labV6Gateway("fd10:109:0:10::/64")
	require.NoError(t, err)
	assert.Equal(t, "fd10:109:0:10::1", gw)

	_, err = labV6Gateway("not-a-cidr")
	assert.Error(t, err)
}
