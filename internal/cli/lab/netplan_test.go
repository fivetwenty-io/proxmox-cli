package lab

import (
	"encoding/json"
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
