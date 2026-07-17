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
