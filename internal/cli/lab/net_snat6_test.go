package lab

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// snat6TestLab is a single-vnet lab with IPv6 egress requested.
func snat6TestLab() config.LabNetwork {
	return config.LabNetwork{
		VnetID: "labwayne",
		CIDR:   "10.10.1.0/24",
		Mgmt:   config.LabMgmt{Subnet: "10.10.1.0/24", Gateway: "10.10.1.1"},
		Snat6:  true,
	}
}

// TestEnsureLabSdnVnets_Snat6_SetOnIPv6SubnetOnly covers the SNAT66 render:
// with network.snat6, the lab's IPv6 subnet is created with masquerade —
// and the IPv4 subnet beside it is NOT, since a lab's IPv4 egress belongs to
// the outer platform, not to this command.
func TestEnsureLabSdnVnets_Snat6_SetOnIPv6SubnetOnly(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	n := snat6TestLab()

	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne", "zone": "labs"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{})
	var created []hostnetRecordedRequest
	hostnetRecord(f, &created, nil, "", "POST /api2/json/cluster/sdn/vnets/labwayne/subnets", nil, 200)

	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	require.NoError(t, ensureLabSdnVnets(context.Background(), api, n, "simple"))

	require.Len(t, created, 2, "the IPv4 subnet, then the IPv6 one")
	assert.NotContains(t, created[0].body, "snat", "the IPv4 subnet is never masqueraded by this command")
	assert.Equal(t, "1", created[1].body["snat"], "the IPv6 subnet carries the requested masquerade")

	cidr6, _, err := labPrimaryV6Subnet(n)
	require.NoError(t, err)
	assert.Equal(t, cidr6, created[1].body["subnet"])
}

// TestEnsureLabSdnVnets_Snat6_SetOnDriftedExistingSubnet pins the
// reconcile: a lab that gains network.snat6 after its subnet already exists
// must have the flag set on the next apply, not silently skipped as
// "subnet already exists".
func TestEnsureLabSdnVnets_Snat6_SetOnDriftedExistingSubnet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	n := snat6TestLab()
	cidr6, gw6, err := labPrimaryV6Subnet(n)
	require.NoError(t, err)

	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne", "zone": "labs"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{
		map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": "10.10.1.0/24", "gateway": "10.10.1.1"},
		map[string]any{"subnet": "labwayne-v6", "cidr": cidr6, "gateway": gw6},
	})
	var updated []hostnetRecordedRequest
	hostnetRecord(f, &updated, nil, "", "PUT /api2/json/cluster/sdn/vnets/labwayne/subnets/labwayne-v6", nil, 200)

	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	require.NoError(t, ensureLabSdnVnets(context.Background(), api, n, "simple"))

	require.Len(t, updated, 1, "only the IPv6 subnet needs the update")
	assert.Equal(t, "1", updated[0].body["snat"])
}

// TestEnsureLabSdnVnets_Snat6_NeverClearsExistingFlag pins the
// never-tear-down rule the whole IPv6 feature follows: switching snat6 back
// off stops provisioning it, it does not strip what is already applied.
func TestEnsureLabSdnVnets_Snat6_NeverClearsExistingFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	n := snat6TestLab()
	n.Snat6 = false
	cidr6, gw6, err := labPrimaryV6Subnet(n)
	require.NoError(t, err)

	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne", "zone": "labs"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{
		map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": "10.10.1.0/24", "gateway": "10.10.1.1"},
		map[string]any{"subnet": "labwayne-v6", "cidr": cidr6, "gateway": gw6, "snat": 1},
	})
	var updated []hostnetRecordedRequest
	hostnetRecord(f, &updated, nil, "", "PUT /api2/json/cluster/sdn/vnets/labwayne/subnets/labwayne-v6", nil, 200)

	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	require.NoError(t, ensureLabSdnVnets(context.Background(), api, n, "simple"))
	assert.Empty(t, updated, "an already-masqueraded subnet is left alone when snat6 is off")
}

// TestSdnSubnetState_SnatDecodesEveryPVEEncoding pins the tolerant decode:
// PVE spells its boolean flags as 1/0, "1"/"0", or true/false depending on
// version and endpoint, and reading any of them as "not set" would make the
// reconcile re-issue the same update forever.
func TestSdnSubnetState_SnatDecodesEveryPVEEncoding(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`{"snat":1}`, true},
		{`{"snat":"1"}`, true},
		{`{"snat":true}`, true},
		{`{"snat":0}`, false},
		{`{"snat":"0"}`, false},
		{`{}`, false},
	} {
		var s sdnSubnetState
		require.NoError(t, json.Unmarshal([]byte(tc.raw), &s), tc.raw)
		assert.Equal(t, tc.want, s.Snat.Bool(), tc.raw)
	}
}

// TestLabIPv6PlanIssues_Snat6Contradictions pins validation: snat6 is
// refused with IPv6 off (a contradiction), and on any zone type but simple,
// where PVE accepts the flag and then renders nothing from it.
func TestLabIPv6PlanIssues_Snat6Contradictions(t *testing.T) {
	off := false
	ipv4Only := snat6TestLab()
	ipv4Only.IPv6 = &off
	issues := labIPv6PlanIssues(ipv4Only)
	require.NotEmpty(t, issues)
	assert.Contains(t, issues[0], "snat6")
	assert.Contains(t, issues[0], "ipv6: false")

	vxlan := snat6TestLab()
	vxlan.ZoneType = "vxlan"
	issues = labIPv6PlanIssues(vxlan)
	require.NotEmpty(t, issues)
	assert.Contains(t, issues[0], "snat6")
	assert.Contains(t, issues[0], "vxlan")

	assert.Empty(t, labIPv6PlanIssues(snat6TestLab()), "a simple-zone dual-stack lab with snat6 is valid")
}

// TestLabSnat6EgressWarning_HostWithoutIPv6Route covers what a live run
// turned up: PVE accepts the subnet's snat flag and then renders no
// ip6tables rule at all when it cannot resolve an IPv6 egress interface, so
// a lab on a host with no IPv6 route gets a flag that masquerades nothing.
// The apply still succeeds — the warning is the whole point.
func TestLabSnat6EgressWarning_HostWithoutIPv6Route(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/sm-0/network", []any{
		map[string]any{"iface": "vmbr0", "type": "bridge", "gateway": "10.0.0.1"},
	})
	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)

	warning := labSnat6EgressWarning(context.Background(), api, snat6TestLab(), "sm-0")
	assert.Contains(t, warning, "no interface on node \"sm-0\" declares an IPv6 gateway")
}

// TestLabSnat6EgressWarning_SilentWhenNotApplicable pins the three cases
// that must stay quiet: a host that does declare an IPv6 gateway, a lab that
// never asked for snat6, and a lab with IPv6 switched off.
func TestLabSnat6EgressWarning_SilentWhenNotApplicable(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleJSON("GET /api2/json/nodes/sm-0/network", []any{
		map[string]any{"iface": "vmbr0", "type": "bridge", "gateway6": "2001:db8::1"},
	})
	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	ctx := context.Background()

	assert.Empty(t, labSnat6EgressWarning(ctx, api, snat6TestLab(), "sm-0"))

	noSnat := snat6TestLab()
	noSnat.Snat6 = false
	assert.Empty(t, labSnat6EgressWarning(ctx, api, noSnat, "sm-0"))

	off := false
	ipv4Only := snat6TestLab()
	ipv4Only.IPv6 = &off
	assert.Empty(t, labSnat6EgressWarning(ctx, api, ipv4Only, "sm-0"))
}

// TestNetApply_RefusesIncoherentIPv6Plan pins where the snat6 contradiction
// is caught. A hand-edited labs.d file never passes through `lab config add`
// or `lab create`, so `net apply` — the verb that renders the IPv6 subnets —
// has to refuse it itself rather than write a flag PVE renders nothing from.
func TestNetApply_RefusesIncoherentIPv6Plan(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := netTestLab("wayne")
	lab.Network.Snat6 = true
	lab.Network.ZoneType = "vxlan"

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildNetCmd(t, path, f, "node1")

	_, err := runNetCmd(t, cmd, "apply", "wayne")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "IPv6 plan is incoherent")
	assert.Contains(t, err.Error(), "vxlan")
}
