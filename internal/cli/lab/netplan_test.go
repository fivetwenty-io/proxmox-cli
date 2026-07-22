package lab

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// planNetwork returns the coherent reference plan the mismatch cases below
// each break one field of: a /16 lab with a /24 management reservation.
func planNetwork() config.LabNetwork {
	return config.LabNetwork{
		CIDR: "10.253.0.0/16",
		Mgmt: config.LabMgmt{
			Subnet:  "10.253.0.0/24",
			HostIP:  "10.253.0.10",
			Gateway: "10.253.0.1",
		},
		BoshBloc: "10.253.16.0/20",
	}
}

func TestLabNetworkPlanIssues_CoherentPlanIsClean(t *testing.T) {
	assert.Empty(t, labNetworkPlanIssues(planNetwork()))
}

func TestLabNetworkPlanIssues_UnsetFieldsAreSkipped(t *testing.T) {
	// config add writes exactly this shape: cidr set, mgmt/bosh_bloc left
	// for the operator.
	n := config.LabNetwork{CIDR: "10.253.0.0/16"}
	assert.Empty(t, labNetworkPlanIssues(n))

	// No cidr at all: nothing to check against.
	assert.Empty(t, labNetworkPlanIssues(config.LabNetwork{}))
}

func TestLabNetworkPlanIssues_ContainmentViolations(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*config.LabNetwork)
		wantSub string
	}{
		{
			name:    "mgmt subnet outside cidr",
			mutate:  func(n *config.LabNetwork) { n.Mgmt.Subnet = "10.254.0.0/24" },
			wantSub: "network.mgmt.subnet 10.254.0.0/24 is not contained in network.cidr 10.253.0.0/16",
		},
		{
			name:    "mgmt subnet wider than cidr",
			mutate:  func(n *config.LabNetwork) { n.Mgmt.Subnet = "10.0.0.0/8" },
			wantSub: "network.mgmt.subnet 10.0.0.0/8 is not contained in network.cidr 10.253.0.0/16",
		},
		{
			name:    "host ip outside cidr",
			mutate:  func(n *config.LabNetwork) { n.Mgmt.HostIP = "192.168.1.10" },
			wantSub: "network.mgmt.host_ip 192.168.1.10 is not inside network.cidr 10.253.0.0/16",
		},
		{
			name:    "gateway outside cidr",
			mutate:  func(n *config.LabNetwork) { n.Mgmt.Gateway = "10.252.0.1" },
			wantSub: "network.mgmt.gateway 10.252.0.1 is not inside network.cidr 10.253.0.0/16",
		},
		{
			name:    "bosh bloc outside cidr",
			mutate:  func(n *config.LabNetwork) { n.BoshBloc = "10.108.16.0/20" },
			wantSub: "network.bosh_bloc 10.108.16.0/20 is not contained in network.cidr 10.253.0.0/16",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := planNetwork()
			tc.mutate(&n)
			issues := labNetworkPlanIssues(n)
			require.Len(t, issues, 1)
			assert.Contains(t, issues[0], tc.wantSub)
		})
	}
}

// planNetworkMultiVnet returns a coherent multi-vnet reference plan: the
// primary /16 with mgmt/bosh_bloc reservations (planNetwork's shape) plus a
// storage vnet (subnetted) and a workload vnet (pure L2, no subnet) and the
// 6-NIC HostNICs set the multi-node lab plan's pve-cpi example uses.
func planNetworkMultiVnet() config.LabNetwork {
	n := planNetwork()
	n.VnetID = "pvecpi"
	n.Vnets = []config.LabVnet{
		{ID: "pvecpist", Tag: 5011, CIDR: "10.253.32.0/24", Gateway: "10.253.32.1", Purpose: "storage"},
		{ID: "pvecpiwk", Tag: 5012, Purpose: "workload"},
	}
	n.HostNICs = []config.LabHostNIC{
		{Index: 1, VnetID: "pvecpi"},
		{Index: 2, VnetID: "pvecpist"},
		{Index: 3, VnetID: "pvecpist"},
		{Index: 4, VnetID: "pvecpiwk"},
		{Index: 5, VnetID: "pvecpiwk"},
	}
	return n
}

func TestLabNetworkPlanIssues_MultiVnetCoherentPlanIsClean(t *testing.T) {
	assert.Empty(t, labNetworkPlanIssues(planNetworkMultiVnet()))
}

func TestLabNetworkPlanIssues_VnetIDCharsetAndUniqueness(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*config.LabNetwork)
		wantSub string
	}{
		{
			name:    "empty vnet id",
			mutate:  func(n *config.LabNetwork) { n.Vnets[0].ID = "" },
			wantSub: "network.vnets[0].id is required",
		},
		{
			name:    "vnet id too long",
			mutate:  func(n *config.LabNetwork) { n.Vnets[0].ID = "toolongvnetid" },
			wantSub: `network.vnets[0].id "toolongvnetid" must be 1-8 alphanumeric characters with no hyphen`,
		},
		{
			name:    "vnet id has hyphen",
			mutate:  func(n *config.LabNetwork) { n.Vnets[0].ID = "st-vnet" },
			wantSub: `network.vnets[0].id "st-vnet" must be 1-8 alphanumeric characters with no hyphen`,
		},
		{
			name:    "vnet id collides with primary vnet_id",
			mutate:  func(n *config.LabNetwork) { n.Vnets[0].ID = n.VnetID },
			wantSub: `network.vnets[0].id "pvecpi" collides with the primary vnet_id or an earlier network.vnets[] entry`,
		},
		{
			name:    "vnet id collides with another vnets entry",
			mutate:  func(n *config.LabNetwork) { n.Vnets[1].ID = n.Vnets[0].ID },
			wantSub: `network.vnets[1].id "pvecpist" collides with the primary vnet_id or an earlier network.vnets[] entry`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := planNetworkMultiVnet()
			tc.mutate(&n)
			issues := labNetworkPlanIssues(n)
			require.NotEmpty(t, issues)
			found := false
			for _, issue := range issues {
				if strings.Contains(issue, tc.wantSub) {
					found = true
				}
			}
			assert.True(t, found, "issues %v do not contain %q", issues, tc.wantSub)
		})
	}
}

func TestLabNetworkPlanIssues_VnetCIDROverlaps(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*config.LabNetwork)
		wantSub string
	}{
		{
			name:    "vnet cidr not contained in primary cidr",
			mutate:  func(n *config.LabNetwork) { n.Vnets[0].CIDR = "10.254.32.0/24" },
			wantSub: "network.vnets[0].cidr 10.254.32.0/24 is not contained in network.cidr 10.253.0.0/16",
		},
		{
			name:    "vnet cidr overlaps mgmt subnet",
			mutate:  func(n *config.LabNetwork) { n.Vnets[0].CIDR = "10.253.0.0/25" },
			wantSub: "network.vnets[0].cidr 10.253.0.0/25 overlaps network.mgmt.subnet 10.253.0.0/24",
		},
		{
			name:    "vnet cidr overlaps bosh_bloc",
			mutate:  func(n *config.LabNetwork) { n.Vnets[0].CIDR = "10.253.16.0/24" },
			wantSub: "network.vnets[0].cidr 10.253.16.0/24 overlaps network.bosh_bloc 10.253.16.0/20",
		},
		{
			name:    "vnet cidr overlaps another vnets entry",
			mutate:  func(n *config.LabNetwork) { n.Vnets[1].CIDR = "10.253.32.0/25" },
			wantSub: "network.vnets[0].cidr 10.253.32.0/24 overlaps network.vnets[1].cidr 10.253.32.0/25",
		},
		{
			name:    "invalid vnet cidr reports itself",
			mutate:  func(n *config.LabNetwork) { n.Vnets[0].CIDR = "not-a-cidr" },
			wantSub: `network.vnets[0].cidr "not-a-cidr" is invalid`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := planNetworkMultiVnet()
			tc.mutate(&n)
			issues := labNetworkPlanIssues(n)
			require.NotEmpty(t, issues)
			found := false
			for _, issue := range issues {
				if strings.Contains(issue, tc.wantSub) {
					found = true
				}
			}
			assert.True(t, found, "issues %v do not contain %q", issues, tc.wantSub)
		})
	}
}

func TestLabNetworkPlanIssues_VnetWithNoCIDRIsPassthrough(t *testing.T) {
	// A workload vnet with no cidr is a pure L2 trunk: no containment or
	// overlap check applies to it at all.
	n := planNetworkMultiVnet()
	n.Vnets[1].CIDR = ""
	assert.Empty(t, labNetworkPlanIssues(n))
}

func TestLabNetworkPlanIssues_HostNICIndexIssues(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*config.LabNetwork)
		wantSub string
	}{
		{
			name:    "index zero",
			mutate:  func(n *config.LabNetwork) { n.HostNICs[0].Index = 0 },
			wantSub: "network.host_nics[0].index 0 must be >= 1 (net0 is reserved for the primary vnet)",
		},
		{
			name:    "negative index",
			mutate:  func(n *config.LabNetwork) { n.HostNICs[0].Index = -1 },
			wantSub: "network.host_nics[0].index -1 must be >= 1 (net0 is reserved for the primary vnet)",
		},
		{
			name:    "duplicate index",
			mutate:  func(n *config.LabNetwork) { n.HostNICs[2].Index = n.HostNICs[1].Index },
			wantSub: "network.host_nics[2].index 2 collides with network.host_nics[1].index (every netN index must be unique)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := planNetworkMultiVnet()
			tc.mutate(&n)
			issues := labNetworkPlanIssues(n)
			require.NotEmpty(t, issues)
			found := false
			for _, issue := range issues {
				if strings.Contains(issue, tc.wantSub) {
					found = true
				}
			}
			assert.True(t, found, "issues %v do not contain %q", issues, tc.wantSub)
		})
	}
}

func TestLabNetworkPlanIssues_HostNICVnetIDIssues(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*config.LabNetwork)
		wantSub string
	}{
		{
			name:    "empty vnet_id",
			mutate:  func(n *config.LabNetwork) { n.HostNICs[0].VnetID = "" },
			wantSub: "network.host_nics[0].vnet_id is required",
		},
		{
			name:   "unresolvable vnet_id",
			mutate: func(n *config.LabNetwork) { n.HostNICs[0].VnetID = "ghost" },
			wantSub: `network.host_nics[0].vnet_id "ghost" does not resolve to the primary vnet_id "pvecpi" ` +
				"or any network.vnets[] entry",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := planNetworkMultiVnet()
			tc.mutate(&n)
			issues := labNetworkPlanIssues(n)
			require.NotEmpty(t, issues)
			found := false
			for _, issue := range issues {
				if strings.Contains(issue, tc.wantSub) {
					found = true
				}
			}
			assert.True(t, found, "issues %v do not contain %q", issues, tc.wantSub)
		})
	}
}

func TestLabNestedNetworkPlanIssues_ZeroValueIsClean(t *testing.T) {
	assert.Empty(t, labNestedNetworkPlanIssues("pve-cpi", config.LabNestedNetwork{}))
}

func TestLabNestedNetworkPlanIssues_WrapsConfigValidateNestedNetwork(t *testing.T) {
	nn := config.LabNestedNetwork{
		Bonds: []config.LabNestedBond{
			{Name: "bond0", NICs: []string{"nic0", "nic1"}, Mode: config.NestedBondModeActiveBackup, Bridge: "vmbr0"},
		},
	}
	assert.Equal(t, config.ValidateNestedNetwork("pve-cpi", nn), labNestedNetworkPlanIssues("pve-cpi", nn))
}

func TestLabNestedNetworkPlanIssues_InvalidModeIsHardError(t *testing.T) {
	nn := config.LabNestedNetwork{
		Bonds: []config.LabNestedBond{
			{Name: "bond0", NICs: []string{"nic0", "nic1"}, Mode: "round-robin", Bridge: "vmbr0"},
		},
	}
	issues := labNestedNetworkPlanIssues("pve-cpi", nn)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], `mode "round-robin" must be "active-backup" or "802.3ad"`)
}

func TestLabNestedNetworkPlanIssues_VlanZoneBridgeWithNoMatchingVlanAwareBond(t *testing.T) {
	nn := config.LabNestedNetwork{
		Bonds: []config.LabNestedBond{
			{Name: "bond0", NICs: []string{"nic0", "nic1"}, Mode: config.NestedBondModeActiveBackup, Bridge: "vmbr0"},
		},
		VlanZone: &config.LabNestedVlanZone{
			Bridge:   "vmbr2",
			ZoneName: "clivlan",
			Vnets: []config.LabNestedVlanVnet{
				{ID: "cli40", Tag: 40, CIDR: "10.61.136.0/24"},
			},
		},
	}
	issues := labNestedNetworkPlanIssues("pve-cpi", nn)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0],
		`nested_network.vlan_zone.bridge "vmbr2" has no matching nested_network.bonds[] entry with that bridge and vlan_aware: true`)
}

func TestLabNetworkPlanIssues_MalformedFields(t *testing.T) {
	n := planNetwork()
	n.CIDR = "not-a-cidr"
	issues := labNetworkPlanIssues(n)
	require.Len(t, issues, 1, "an unparseable cidr reports itself, not cascade errors")
	assert.Contains(t, issues[0], `network.cidr "not-a-cidr" is invalid`)

	n = planNetwork()
	n.Mgmt.Subnet = "10.253.0.0"
	n.Mgmt.HostIP = "nope"
	n.BoshBloc = "10.253.16.0"
	issues = labNetworkPlanIssues(n)
	require.Len(t, issues, 3)
	assert.Contains(t, issues[0], `network.mgmt.subnet "10.253.0.0" is invalid`)
	assert.Contains(t, issues[1], `network.mgmt.host_ip "nope" is not a valid IP address`)
	assert.Contains(t, issues[2], `network.bosh_bloc "10.253.16.0" is invalid`)
}

// agentResult builds an agentNetworkInterfaces from the guest agent's wire
// shape, so tests exercise the real JSON field names including "prefix".
func agentResult(t *testing.T, raw string) *agentNetworkInterfaces {
	t.Helper()
	var parsed agentNetworkInterfaces
	require.NoError(t, json.Unmarshal([]byte(raw), &parsed))
	return &parsed
}

func TestGuestPrefixWarning_NarrowerPrefixWarns(t *testing.T) {
	// The lab-pmx incident shape: node interface installed as /24 inside a
	// /16 lab. Replies to guests in the wider /16 hairpin via the gateway.
	ifaces := agentResult(t, `{"result": [
		{"name": "lo", "ip-addresses": [{"ip-address": "127.0.0.1", "ip-address-type": "ipv4", "prefix": 8}]},
		{"name": "vmbr0", "ip-addresses": [{"ip-address": "10.253.0.10", "ip-address-type": "ipv4", "prefix": 24}]}
	]}`)

	warn, ok := guestPrefixWarning(ifaces, "10.253.0.0/16")
	require.True(t, ok)
	assert.Contains(t, warn, "vmbr0")
	assert.Contains(t, warn, "10.253.0.10/24")
	assert.Contains(t, warn, "network.cidr 10.253.0.0/16")
	assert.Contains(t, warn, "/16 prefix")
}

func TestGuestPrefixWarning_MatchingPrefixIsClean(t *testing.T) {
	ifaces := agentResult(t, `{"result": [
		{"name": "vmbr0", "ip-addresses": [{"ip-address": "10.253.0.10", "ip-address-type": "ipv4", "prefix": 16}]}
	]}`)

	_, ok := guestPrefixWarning(ifaces, "10.253.0.0/16")
	assert.False(t, ok)
}

func TestGuestPrefixWarning_OutOfCidrAddressesAreIgnored(t *testing.T) {
	// A tailscale interface outside the lab cidr must not trigger or mask
	// the check; the in-cidr interface behind it still gets inspected.
	ifaces := agentResult(t, `{"result": [
		{"name": "tailscale0", "ip-addresses": [{"ip-address": "100.81.200.105", "ip-address-type": "ipv4", "prefix": 32}]},
		{"name": "vmbr0", "ip-addresses": [{"ip-address": "10.253.0.10", "ip-address-type": "ipv4", "prefix": 24}]}
	]}`)

	warn, ok := guestPrefixWarning(ifaces, "10.253.0.0/16")
	require.True(t, ok)
	assert.Contains(t, warn, "vmbr0")
}

func TestGuestPrefixWarning_MissingPrefixOrCidrIsSilent(t *testing.T) {
	noPrefix := agentResult(t, `{"result": [
		{"name": "vmbr0", "ip-addresses": [{"ip-address": "10.253.0.10", "ip-address-type": "ipv4"}]}
	]}`)
	_, ok := guestPrefixWarning(noPrefix, "10.253.0.0/16")
	assert.False(t, ok, "an agent that omits prefix must not produce a warning")

	narrow := agentResult(t, `{"result": [
		{"name": "vmbr0", "ip-addresses": [{"ip-address": "10.253.0.10", "ip-address-type": "ipv4", "prefix": 24}]}
	]}`)
	_, ok = guestPrefixWarning(narrow, "")
	assert.False(t, ok, "no cidr configured means nothing to compare against")

	_, ok = guestPrefixWarning(narrow, "bogus")
	assert.False(t, ok, "an invalid cidr is a config problem, not a status warning")
}
