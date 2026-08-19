package lab

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

func TestLabNetworkPlanIssues_IPv6DefaultsClean(t *testing.T) {
	assert.Empty(t, labNetworkPlanIssues(ipv6TestNetwork()),
		"a fleet-conventional lab with no ipv6 keys at all must pass cleanly")
}

func TestLabNetworkPlanIssues_IPv6DisabledWithCIDR6(t *testing.T) {
	off := false
	n := ipv6TestNetwork()
	n.IPv6 = &off
	n.CIDR6 = "fd10:109::/48"

	issues := labNetworkPlanIssues(n)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], "ipv6: false")
	assert.Contains(t, issues[0], "cidr6")
}

func TestLabNetworkPlanIssues_IPv6DisabledWithVnetCIDR6(t *testing.T) {
	off := false
	n := ipv6TestNetwork()
	n.IPv6 = &off
	n.Vnets = []config.LabVnet{
		{ID: "storage", Tag: 1, CIDR: "10.109.32.0/24", CIDR6: "fd10:109:0:10::/64"},
	}

	issues := labNetworkPlanIssues(n)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], "vnets[0].cidr6")
}

func TestLabNetworkPlanIssues_InvalidCIDR6(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cidr6 string
	}{
		{"unparsable", "not-a-cidr"},
		{"ipv4 block", "10.0.0.0/8"},
		{"narrower than /48", "fd10:109:0:1::/64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := ipv6TestNetwork()
			n.CIDR6 = tc.cidr6
			issues := labNetworkPlanIssues(n)
			require.NotEmpty(t, issues)
			assert.Contains(t, issues[0], "cidr6")
		})
	}
}

func TestLabNetworkPlanIssues_VnetCIDR6Checks(t *testing.T) {
	t.Run("invalid override", func(t *testing.T) {
		n := ipv6TestNetwork()
		n.Vnets = []config.LabVnet{
			{ID: "storage", Tag: 1, CIDR: "10.109.32.0/24", CIDR6: "bogus"},
		}
		issues := labNetworkPlanIssues(n)
		require.Len(t, issues, 1)
		assert.Contains(t, issues[0], "vnets[0].cidr6")
	})

	t.Run("ipv4 override", func(t *testing.T) {
		n := ipv6TestNetwork()
		n.Vnets = []config.LabVnet{
			{ID: "storage", Tag: 1, CIDR: "10.109.32.0/24", CIDR6: "10.109.34.0/24"},
		}
		issues := labNetworkPlanIssues(n)
		require.Len(t, issues, 1)
		assert.Contains(t, issues[0], "vnets[0].cidr6")
	})

	t.Run("override on a pure L2 vnet", func(t *testing.T) {
		n := ipv6TestNetwork()
		n.Vnets = []config.LabVnet{
			{ID: "workload", Tag: 2, CIDR6: "fd10:109:0:20::/64"},
		}
		issues := labNetworkPlanIssues(n)
		require.Len(t, issues, 1)
		assert.Contains(t, issues[0], "vnets[0].cidr6")
		assert.Contains(t, issues[0], "no cidr")
	})

	t.Run("override colliding with the carved mgmt /64", func(t *testing.T) {
		n := ipv6TestNetwork()
		n.CIDR6 = "fd10:109::/48"
		n.Vnets = []config.LabVnet{
			{ID: "storage", Tag: 1, CIDR: "10.109.32.0/24", CIDR6: "fd10:109:0:1::/64"},
		}
		issues := labNetworkPlanIssues(n)
		require.Len(t, issues, 1)
		assert.Contains(t, issues[0], "vnets[0].cidr6")
	})

	t.Run("override narrower than /112", func(t *testing.T) {
		n := ipv6TestNetwork()
		n.Vnets = []config.LabVnet{
			{ID: "storage", Tag: 1, CIDR: "10.109.32.0/24", CIDR6: "fd99::100/120"},
		}
		issues := labNetworkPlanIssues(n)
		require.Len(t, issues, 1)
		assert.Contains(t, issues[0], "vnets[0].cidr6")
		assert.Contains(t, issues[0], "/112",
			"a subnet narrower than /112 cannot hold the plan's 16-bit host offsets — its derived ::1 "+
				"gateway would land outside it and PVE would reject the subnet at apply time")
	})

	t.Run("override colliding with a LATER vnet's carved /64", func(t *testing.T) {
		n := ipv6TestNetwork()
		n.CIDR6 = "fd10:109::/48"
		n.Vnets = []config.LabVnet{
			// The /64 that will be carved for vnets[1] (subnet ID 17, hex 11).
			{ID: "storage", Tag: 1, CIDR: "10.109.32.0/24", CIDR6: "fd10:109:0:11::/64"},
			{ID: "other", Tag: 2, CIDR: "10.109.33.0/24"},
		}
		issues := labNetworkPlanIssues(n)
		require.Len(t, issues, 1,
			"the cross-check must run against the FULL effective set, not just subnets seen before the override")
		assert.Contains(t, issues[0], "vnets[0].cidr6")
	})

	t.Run("two overrides colliding", func(t *testing.T) {
		n := ipv6TestNetwork()
		n.Vnets = []config.LabVnet{
			{ID: "storage", Tag: 1, CIDR: "10.109.32.0/24", CIDR6: "fd99::/64"},
			{ID: "other", Tag: 2, CIDR: "10.109.33.0/24", CIDR6: "fd99::/64"},
		}
		issues := labNetworkPlanIssues(n)
		require.Len(t, issues, 1)
		assert.Contains(t, issues[0], "vnets[1].cidr6")
	})

	t.Run("distinct overrides pass", func(t *testing.T) {
		n := ipv6TestNetwork()
		n.Vnets = []config.LabVnet{
			{ID: "storage", Tag: 1, CIDR: "10.109.32.0/24", CIDR6: "fd99::/64"},
			{ID: "other", Tag: 2, CIDR: "10.109.33.0/24", CIDR6: "fd99:0:0:1::/64"},
		}
		assert.Empty(t, labNetworkPlanIssues(n))
	})
}

// TestLabNetworkPlanIssues_TooManyVnetsForIPv6 pins the carving-space bound:
// outer vnets take subnet IDs 16+i and the inner vlan-zone vnets take 32+i,
// so a 17th outer vnet (index 16 → subnet ID 32) would share its /64 with
// inner vnet 0 — two different bridges, two different ::1 gateways, one
// subnet. With IPv6 enabled the plan must refuse more than 16 vnets instead
// of silently deriving that collision; 16 exactly still passes.
func TestLabNetworkPlanIssues_TooManyVnetsForIPv6(t *testing.T) {
	vnets := func(count int) []config.LabVnet {
		out := make([]config.LabVnet, count)
		for i := range out {
			out[i] = config.LabVnet{
				ID:   fmt.Sprintf("vn%d", i),
				Tag:  i + 1,
				CIDR: fmt.Sprintf("10.109.%d.0/24", 32+i),
			}
		}
		return out
	}

	n := ipv6TestNetwork()
	n.Vnets = vnets(16)
	assert.Empty(t, labNetworkPlanIssues(n), "16 vnets exactly must still pass")

	n.Vnets = vnets(17)
	issues := labNetworkPlanIssues(n)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], "16")
	assert.Contains(t, issues[0], "vnets")

	off := false
	n.IPv6 = &off
	assert.Empty(t, labNetworkPlanIssues(n), "with IPv6 disabled no /64s are carved, so the bound does not apply")
}
