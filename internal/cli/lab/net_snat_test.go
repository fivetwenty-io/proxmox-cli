package lab

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// snatTestLab is a single-vnet IPv4-only lab on the default simple zone, so
// the only subnet an apply touches is the IPv4 one.
func snatTestLab() config.LabNetwork {
	off := false
	return config.LabNetwork{
		VnetID: "labwayne",
		CIDR:   "10.10.1.0/24",
		Mgmt:   config.LabMgmt{Subnet: "10.10.1.0/24", Gateway: "10.10.1.1"},
		IPv6:   &off,
	}
}

const snatTestSubnetRoute = "/api2/json/cluster/sdn/vnets/labwayne/subnets"

// TestEffectiveSnat pins the IPv4 masquerade default: on for a simple zone,
// off for any other zone type, and an explicit setting always wins.
func TestEffectiveSnat(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name     string
		zoneType string
		snat     *bool
		want     bool
	}{
		{"unset on the implicit simple zone", "", nil, true},
		{"unset on an explicit simple zone", "simple", nil, true},
		{"unset on a vxlan zone", "vxlan", nil, false},
		{"explicit false on a simple zone", "simple", &no, false},
		{"explicit true on a simple zone", "simple", &yes, true},
		{"explicit true on a vxlan zone", "vxlan", &yes, true},
		{"explicit false on a vxlan zone", "vxlan", &no, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := config.LabNetwork{ZoneType: tc.zoneType, Snat: tc.snat}
			assert.Equal(t, tc.want, n.EffectiveSnat())
		})
	}
}

// TestEnsureLabSdnVnets_Snat_SetOnCreate covers the headline fix: a lab left
// at its defaults creates its IPv4 subnet with masquerade.
func TestEnsureLabSdnVnets_Snat_SetOnCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	n := snatTestLab()

	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne", "zone": "labs"}})
	f.HandleJSON("GET "+snatTestSubnetRoute, []any{})
	var created []hostnetRecordedRequest
	hostnetRecord(f, &created, nil, "", "POST "+snatTestSubnetRoute, nil, 200)

	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	require.NoError(t, ensureLabSdnVnets(context.Background(), api, n, "simple"))

	require.Len(t, created, 1)
	assert.Equal(t, "10.10.1.0/24", created[0].body["subnet"])
	assert.Equal(t, "1", created[0].body["snat"], "the IPv4 subnet carries masquerade by default")
}

// TestEnsureLabSdnVnets_Snat_SetOnExtraVnetSubnet pins that network.vnets[]
// entries get the same IPv4 masquerade as the primary vnet.
func TestEnsureLabSdnVnets_Snat_SetOnExtraVnetSubnet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	n := snatTestLab()
	n.CIDR = ""
	n.Vnets = []config.LabVnet{{ID: "labst", CIDR: "10.20.0.0/24", Gateway: "10.20.0.1"}}

	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{
		map[string]any{"vnet": "labwayne", "zone": "labs"},
		map[string]any{"vnet": "labst", "zone": "labs"},
	})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labst/subnets", []any{})
	var created []hostnetRecordedRequest
	hostnetRecord(f, &created, nil, "", "POST /api2/json/cluster/sdn/vnets/labst/subnets", nil, 200)

	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	require.NoError(t, ensureLabSdnVnets(context.Background(), api, n, "simple"))

	require.Len(t, created, 1)
	assert.Equal(t, "1", created[0].body["snat"])
}

// TestEnsureLabSdnVnets_Snat_SetOnDriftedExistingSubnet pins the reconcile
// that makes re-running `lab net apply` repair a lab built before the flag
// defaulted on: an existing subnet without snat is updated, not skipped.
func TestEnsureLabSdnVnets_Snat_SetOnDriftedExistingSubnet(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	n := snatTestLab()

	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne", "zone": "labs"}})
	f.HandleJSON("GET "+snatTestSubnetRoute, []any{
		map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": "10.10.1.0/24", "gateway": "10.10.1.1"},
	})
	var updated []hostnetRecordedRequest
	hostnetRecord(f, &updated, nil, "", "PUT "+snatTestSubnetRoute+"/labwayne-10.10.1.0-24", nil, 200)

	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	require.NoError(t, ensureLabSdnVnets(context.Background(), api, n, "simple"))

	require.Len(t, updated, 1)
	assert.Equal(t, "1", updated[0].body["snat"])
	assert.NotContains(t, updated[0].body, "gateway", "the gateway has not drifted")
}

// TestEnsureLabSdnVnets_Snat_NeverClearsExistingFlag pins the set-only rule:
// opting out with network.snat: false stops provisioning the flag, it does not
// strip one that is already applied.
func TestEnsureLabSdnVnets_Snat_NeverClearsExistingFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	n := snatTestLab()
	n.Snat = new(false)

	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne", "zone": "labs"}})
	f.HandleJSON("GET "+snatTestSubnetRoute, []any{
		map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": "10.10.1.0/24", "gateway": "10.10.1.1", "snat": 1},
	})
	var updated []hostnetRecordedRequest
	hostnetRecord(f, &updated, nil, "", "PUT "+snatTestSubnetRoute+"/labwayne-10.10.1.0-24", nil, 200)

	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	require.NoError(t, ensureLabSdnVnets(context.Background(), api, n, "simple"))
	assert.Empty(t, updated, "an already-masqueraded subnet is left alone when snat is off")
}

// TestEnsureLabSdnVnets_Snat_OptOutOmitsFlagOnCreate covers `snat: false`.
func TestEnsureLabSdnVnets_Snat_OptOutOmitsFlagOnCreate(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	n := snatTestLab()
	n.Snat = new(false)

	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne", "zone": "labs"}})
	f.HandleJSON("GET "+snatTestSubnetRoute, []any{})
	var created []hostnetRecordedRequest
	hostnetRecord(f, &created, nil, "", "POST "+snatTestSubnetRoute, nil, 200)

	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	require.NoError(t, ensureLabSdnVnets(context.Background(), api, n, "simple"))

	require.Len(t, created, 1)
	assert.NotContains(t, created[0].body, "snat")
}

// TestEnsureLabSdnVnets_Snat_DefaultsOffOnNonSimpleZone pins that an unset
// network.snat on a vxlan zone sends no flag, since PVE would render nothing
// from it there.
func TestEnsureLabSdnVnets_Snat_DefaultsOffOnNonSimpleZone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	n := snatTestLab()
	n.ZoneType = "vxlan"

	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne", "zone": "labs"}})
	f.HandleJSON("GET "+snatTestSubnetRoute, []any{})
	var created []hostnetRecordedRequest
	hostnetRecord(f, &created, nil, "", "POST "+snatTestSubnetRoute, nil, 200)

	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	require.NoError(t, ensureLabSdnVnets(context.Background(), api, n, "vxlan"))

	require.Len(t, created, 1)
	assert.NotContains(t, created[0].body, "snat")
}

// TestLabSnatPlanIssues pins validation: only an explicit snat: true on a
// non-simple zone is refused. Unset (which resolves to off there) and an
// explicit false are both coherent, as is any setting on a simple zone.
func TestLabSnatPlanIssues(t *testing.T) {
	yes, no := true, false

	vxlanTrue := config.LabNetwork{ZoneType: "vxlan", Snat: &yes}
	issues := labSnatPlanIssues(vxlanTrue)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], "network.snat: true")
	assert.Contains(t, issues[0], "vxlan")
	assert.Contains(t, issues[0], "simple")
	assert.Equal(t, issues, labNetworkPlanIssues(vxlanTrue), "the wider plan check surfaces it too")

	assert.Empty(t, labSnatPlanIssues(config.LabNetwork{ZoneType: "vxlan"}))
	assert.Empty(t, labSnatPlanIssues(config.LabNetwork{ZoneType: "vxlan", Snat: &no}))
	assert.Empty(t, labSnatPlanIssues(config.LabNetwork{Snat: &yes}))
	assert.Empty(t, labSnatPlanIssues(config.LabNetwork{ZoneType: "simple", Snat: &yes}))
	assert.Empty(t, labSnatPlanIssues(config.LabNetwork{Snat: &no}))
}

// TestNetApply_RefusesExplicitSnatOnNonSimpleZone pins that `net apply`
// refuses a hand-edited lab asking for snat on a vxlan zone, before any
// mutation.
func TestNetApply_RefusesExplicitSnatOnNonSimpleZone(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := netTestLab("wayne")
	lab.Network.Snat = new(true)
	lab.Network.ZoneType = "vxlan"

	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd := buildNetCmd(t, path, f, "node1")

	_, err := runNetCmd(t, cmd, "apply", "wayne")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "IPv4 plan is incoherent")
	assert.Contains(t, err.Error(), "network.snat: true")
}

// TestCreateSubnetSkip pins the create plan's skip rule for a subnet step: an
// existing subnet is skipped only when it already satisfies the requested
// snat, and a flagged subnet is never un-skipped by snat being off.
func TestCreateSubnetSkip(t *testing.T) {
	var flagged, bare sdnSubnetState
	require.NoError(t, json.Unmarshal([]byte(`{"snat":1}`), &flagged))

	assert.False(t, createSubnetSkip(false, true, bare), "absent subnet is created")
	assert.False(t, createSubnetSkip(false, false, bare), "absent subnet is created without snat too")
	assert.False(t, createSubnetSkip(true, true, bare), "existing subnet lacking requested snat is drift to repair")
	assert.True(t, createSubnetSkip(true, true, flagged), "existing subnet already carrying snat is converged")
	assert.True(t, createSubnetSkip(true, false, bare), "snat not requested, nothing to set")
	assert.True(t, createSubnetSkip(true, false, flagged), "snat is never cleared")
}

// TestCreateDryRun_IPv4SubnetStepFollowsSnatDrift runs the plan builder end
// to end: with every other resource already present, the IPv4 subnet step is
// planned (not skipped) while the live subnet lacks snat, and skipped once it
// carries it.
func TestCreateDryRun_IPv4SubnetStepFollowsSnatDrift(t *testing.T) {
	for _, tc := range []struct {
		name       string
		liveSnat   any
		optOut     bool
		wantStatus string
	}{
		{"drifted subnet is planned", nil, false, "would create"},
		{"converged subnet is skipped", 1, false, "skip"},
		{"opt-out leaves a bare subnet alone", nil, true, "skip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			lab := createTestLab("wayne")
			if tc.optOut {
				lab.Network.Snat = new(false)
			}
			row := map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": lab.Network.CIDR, "gateway": "10.10.1.1"}
			if tc.liveSnat != nil {
				row["snat"] = tc.liveSnat
			}

			createSharedResourcesExist(f, t, lab, "wayne", lab.Access.Pool)
			f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets", []any{row, createPrimaryV6SubnetRow(t, lab.Network)})
			f.HandleJSON("GET /api2/json/nodes/node1/qemu", []any{})
			createForbid(f, t, "GET /api2/json/cluster/nextid")
			createForbid(f, t, "PUT /api2/json/cluster/sdn/vnets/labwayne/subnets/labwayne-10.10.1.0-24")

			path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
			cmd := buildCreateCmd(t, path, f, "node1")

			out, err := runCreateCmd(t, cmd, "wayne", "--node", "node1", "--dry-run")
			require.NoError(t, err)

			var line string
			for l := range strings.SplitSeq(out, "\n") {
				if strings.Contains(l, `sdn subnet "`+lab.Network.CIDR+`" on vnet`) {
					line = l
				}
			}
			require.NotEmpty(t, line, "the IPv4 subnet step must be rendered:\n%s", out)
			assert.Contains(t, line, tc.wantStatus)
			if tc.wantStatus == "skip" {
				assert.NotContains(t, line, "would create")
			}
		})
	}
}
