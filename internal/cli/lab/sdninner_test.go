package lab

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
)

func TestSdnApply_SingleNodeLab_NoOp(t *testing.T) {
	lab := multiNodeTestLab("solo", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"solo": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newSdnCmd())

	out, err := runGuestCmd(t, cmd, "apply", "solo")
	require.NoError(t, err)
	assert.Contains(t, out, "no inner cluster")
	assert.Empty(t, fake.Calls)
}

func TestSdnApply_DryRun_NoRunnerCalls(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newSdnCmd())

	out, err := runGuestCmd(t, cmd, "apply", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "labvx")
	assert.Contains(t, out, "10.10.1.10,10.10.1.11,10.10.1.12")
	assert.Empty(t, fake.Calls)
}

func TestSdnApply_CreatesZoneWhenMissing(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newSdnCmd())
	fake := exec.Fake(
		exec.FakeResponse{ExitCode: 1}, // probe: zone does not exist
		exec.FakeResponse{},            // create
		exec.FakeResponse{},            // commit
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "committed")

	require.Len(t, fake.Calls, 3)
	assert.Contains(t, fake.Calls[1].Args, `pvesh create /cluster/sdn/zones --zone labvx --type vxlan --peers "10.10.1.10,10.10.1.11,10.10.1.12" --mtu 1450`)
	assert.Contains(t, fake.Calls[2].Args, "pvesh set /cluster/sdn")
}

func TestSdnApply_UpdatesPeersWhenDrifted(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newSdnCmd())
	fake := exec.Fake(
		// Real PVE `pvesh get .../zones/<z>` shape: peers comma-separated,
		// missing node 2 here (the drift this run must repair).
		exec.FakeResponse{Stdout: `{"peers":"10.10.1.10,10.10.1.11"}`},
		exec.FakeResponse{}, // update
		exec.FakeResponse{}, // commit
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "committed")

	require.Len(t, fake.Calls, 3)
	assert.Contains(t, fake.Calls[1].Args, `pvesh set /cluster/sdn/zones/labvx --peers "10.10.1.10,10.10.1.11,10.10.1.12"`)
}

// TestSdnApply_SkipsWhenUnchanged_CommaSeparated covers M3-R01: real PVE
// (`pvesh get .../zones/<z>`, and zones.cfg) reports the vxlan zone's peers
// COMMA-separated, not space-separated. Before the fix this fixture (the
// format this command actually writes and PVE actually returns) made the
// raw-string comparison false-positive on drift every single run, issuing a
// spurious pvesh set + SDN commit against an already-converged zone.
func TestSdnApply_SkipsWhenUnchanged_CommaSeparated(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newSdnCmd())
	fake := exec.Fake(
		exec.FakeResponse{Stdout: `{"peers":"10.10.1.10,10.10.1.11,10.10.1.12"}`}, // already matches
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "already matches")
	assert.Contains(t, out, "no pending changes")

	require.Len(t, fake.Calls, 1, "no update or commit call should run when nothing changed")
}

// TestSdnApply_SkipsWhenUnchanged_MixedSeparatorsAndOrder covers the same
// convergence guarantee against a peers value using a different separator
// mix and a different address order than this command would itself write —
// e.g. a hand-edited zones.cfg, or a future PVE version — proving the
// comparison is genuinely set-based, not merely "accepts commas too".
func TestSdnApply_SkipsWhenUnchanged_MixedSeparatorsAndOrder(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newSdnCmd())
	fake := exec.Fake(
		exec.FakeResponse{Stdout: `{"peers":"10.10.1.12  10.10.1.10,10.10.1.11"}`}, // reordered, mixed separators
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "apply", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "already matches")

	require.Len(t, fake.Calls, 1, "no update or commit call should run when the peer SET is unchanged")
}

func TestPeersEqual_CommaVsSpaceVsMixed(t *testing.T) {
	assert.True(t, peersEqual("a,b,c", "a b c"))
	assert.True(t, peersEqual("a,b,c", "c,a,b"))
	assert.True(t, peersEqual("a, b ,c", "a;b;c"))
	assert.False(t, peersEqual("a,b,c", "a,b"))
	assert.False(t, peersEqual("a,b,c", "a,b,d"))
}

func TestNormalizePeers_DropsEmptyTokens(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, normalizePeers(",a,,b,,"))
	assert.Empty(t, normalizePeers(""))
}

func TestSdnApply_PeppiGuardRefusesBeforeAnyCall(t *testing.T) {
	lab := multiNodeTestLab("dirty", 3, "")
	lab.Network.VnetID = "peppivn0"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"dirty": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newSdnCmd())

	_, err := runGuestCmd(t, cmd, "apply", "dirty")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.Empty(t, fake.Calls)
}

// --- pmx lab sdn vlan apply -------------------------------------------------

// clientVlanZoneTestLab returns a multi-node lab with one nested vlan-zone
// vnet configured (id "cli40", tag 40, CIDR 10.61.136.0/24) — a one-vnet
// slice of the multi-AZ topology plan §1 example's four-vnet "clivlan" zone,
// enough to exercise zone+vnet+subnet reconciliation without four near-
// identical fixtures.
func clientVlanZoneTestLab(name string, nodes int) *config.Lab {
	lab := multiNodeTestLab(name, nodes, "")
	lab.Network.NestedNetwork = config.LabNestedNetwork{
		Bonds: []config.LabNestedBond{
			{Name: "bond2", NICs: []string{"nic4", "nic5"}, Mode: config.NestedBondModeActiveBackup, Bridge: "vmbr2", VlanAware: true},
		},
		VlanZone: &config.LabNestedVlanZone{
			Bridge:   "vmbr2",
			ZoneName: "clivlan",
			Vnets: []config.LabNestedVlanVnet{
				{ID: "cli40", Alias: "client-vlan40", Tag: 40, CIDR: "10.61.136.0/24", Gateway: "10.61.136.1"},
			},
		},
	}
	return lab
}

func TestSdnVlanApply_NoVlanZoneConfigured_NoOp(t *testing.T) {
	lab := multiNodeTestLab("solo", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"solo": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newSdnCmd())

	out, err := runGuestCmd(t, cmd, "vlan", "apply", "solo")
	require.NoError(t, err)
	assert.Contains(t, out, "nothing to do")
	assert.Empty(t, fake.Calls)
}

func TestSdnVlanApply_DryRun_NoRunnerCalls(t *testing.T) {
	lab := clientVlanZoneTestLab("wayne", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newSdnCmd())

	out, err := runGuestCmd(t, cmd, "vlan", "apply", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "clivlan")
	assert.Contains(t, out, "cli40")
	assert.Contains(t, out, "10.61.136.0/24")
	assert.Empty(t, fake.Calls)
}

// TestSdnVlanApply_CreatesZoneVnetSubnetWhenMissing covers the zone-absent
// case: every probe reports "not found" (non-zero exit, no reachability
// failure), so zone, vnet, and subnet are each created in turn, then
// committed once.
func TestSdnVlanApply_CreatesZoneVnetSubnetWhenMissing(t *testing.T) {
	lab := clientVlanZoneTestLab("wayne", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newSdnCmd())
	fake := exec.Fake(
		exec.FakeResponse{ExitCode: 1}, // probe zone: absent
		exec.FakeResponse{},            // create zone
		exec.FakeResponse{ExitCode: 1}, // probe vnet: absent
		exec.FakeResponse{},            // create vnet
		exec.FakeResponse{ExitCode: 1}, // list subnets on vnet: none yet
		exec.FakeResponse{},            // create subnet
		exec.FakeResponse{},            // commit
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "vlan", "apply", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "committed")

	require.Len(t, fake.Calls, 7)
	assert.Contains(t, fake.Calls[1].Args, "pvesh create /cluster/sdn/zones --zone clivlan --type vlan --bridge vmbr2")
	assert.Contains(t, fake.Calls[3].Args, "pvesh create /cluster/sdn/vnets --vnet cli40 --zone clivlan --tag 40 --alias client-vlan40")
	assert.Contains(t, fake.Calls[5].Args, "pvesh create /cluster/sdn/vnets/cli40/subnets --subnet 10.61.136.0/24 --type subnet --gateway 10.61.136.1")
	assert.Contains(t, fake.Calls[6].Args, "pvesh set /cluster/sdn")
}

// TestSdnVlanApply_UpdatesDriftedVnetAndSubnet covers the drift case: the
// zone already matches, but the vnet's tag and the subnet's gateway have
// both drifted, so both get an update (not a create) before the commit.
func TestSdnVlanApply_UpdatesDriftedVnetAndSubnet(t *testing.T) {
	lab := clientVlanZoneTestLab("wayne", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newSdnCmd())
	fake := exec.Fake(
		exec.FakeResponse{Stdout: `{"bridge":"vmbr2"}`},                                  // probe zone: matches
		exec.FakeResponse{Stdout: `{"zone":"clivlan","tag":99,"alias":"client-vlan40"}`}, // probe vnet: tag drifted
		exec.FakeResponse{}, // update vnet
		exec.FakeResponse{Stdout: `[{"subnet":"cli40-10.61.136.0-24","cidr":"10.61.136.0/24","gateway":"10.61.136.99"}]`}, // list subnets: gateway drifted
		exec.FakeResponse{}, // update subnet
		exec.FakeResponse{}, // commit
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "vlan", "apply", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "committed")

	require.Len(t, fake.Calls, 6)
	assert.Contains(t, fake.Calls[2].Args, "pvesh set /cluster/sdn/vnets/cli40 --zone clivlan --tag 40 --alias client-vlan40")
	assert.Contains(t, fake.Calls[4].Args, "pvesh set /cluster/sdn/vnets/cli40/subnets/cli40-10.61.136.0-24 --gateway 10.61.136.1")
}

// TestSdnVlanApply_FullyConverged_NoOp covers the fully-converged case: zone,
// vnet, and subnet all already match, so no create/update/commit call runs
// — only the read-only probe/list calls.
func TestSdnVlanApply_FullyConverged_NoOp(t *testing.T) {
	lab := clientVlanZoneTestLab("wayne", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newSdnCmd())
	fake := exec.Fake(
		exec.FakeResponse{Stdout: `{"bridge":"vmbr2"}`},
		exec.FakeResponse{Stdout: `{"zone":"clivlan","tag":40,"alias":"client-vlan40"}`},
		exec.FakeResponse{Stdout: `[{"subnet":"cli40-10.61.136.0-24","cidr":"10.61.136.0/24","gateway":"10.61.136.1"}]`},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "vlan", "apply", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "skip")

	require.Len(t, fake.Calls, 3, "no create, update, or commit call should run when nothing changed")
}

func TestSdnVlanApply_PeppiGuardRefusesBeforeAnyCall(t *testing.T) {
	lab := clientVlanZoneTestLab("dirty", 3)
	lab.Network.VnetID = "peppivn0"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"dirty": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newSdnCmd())

	_, err := runGuestCmd(t, cmd, "vlan", "apply", "dirty")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.Empty(t, fake.Calls)
}
