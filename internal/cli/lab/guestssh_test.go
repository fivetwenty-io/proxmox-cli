package lab

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// --- shared test helpers for cluster/qdevice/sdn/nfs tests ----------------

// multiNodeTestLab returns a Lab configured for a nodes-node topology, with
// a fully-resolvable mgmt /24 (so labNodeMgmtIP/labQdeviceMgmtIP succeed):
// node 0 at .10, gateway at .1, matching the fixture CIDR every other lab
// test file in this package uses (10.10.1.0/24).
func multiNodeTestLab(name string, nodes int, qdevice string) *config.Lab {
	lab := cleanLab(name)
	lab.Network.Mgmt = config.LabMgmt{Subnet: "10.10.1.0/24", Gateway: "10.10.1.1"}
	lab.Topology = config.LabTopology{Nodes: nodes, Qdevice: qdevice}
	return lab
}

// buildGuestSSHCmd builds root (e.g. newClusterCmd()) wired to a *cli.Deps
// carrying configPath's loaded config, an exec.Fake() runner (returned so
// tests can inspect fake.Calls and pre-load responses), and an ssh
// context (host/user/port/identity) every guestssh.go helper needs. No
// *apiclient.APIClient is constructed: cluster/sdn/nfs never touch
// deps.API — only qdevice add does, via buildGuestSSHAndAPICmd instead.
func buildGuestSSHCmd(t *testing.T, configPath string, root *cobra.Command) (*cobra.Command, *exec.FakeRunner) {
	t.Helper()

	cfg, err := config.Load(configPath)
	require.NoError(t, err)

	fake := exec.Fake()

	deps := &cli.Deps{
		Cfg:        cfg,
		ConfigPath: configPath,
		Out:        output.New(),
		Format:     output.FormatPlain,
		Runner:     fake,
		Ctx: &config.Context{
			Host: "sm-0.lab.internal",
			SSH:  config.SSHBlock{User: "root", Port: 22},
		},
	}

	root.SetContext(cli.WithDeps(context.Background(), deps))
	return root, fake
}

// buildGuestSSHAndAPICmd is buildGuestSSHCmd plus a *apiclient.APIClient
// pointed at f, for the one command (qdevice add) that reads live VM state
// via the outer PVE API in addition to SSHing into lab guests.
func buildGuestSSHAndAPICmd(t *testing.T, configPath string, f *testhelper.FakePVE, root *cobra.Command) (*cobra.Command, *exec.FakeRunner) {
	t.Helper()

	root, fake := buildGuestSSHCmd(t, configPath, root)
	deps := cli.GetDeps(root)

	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	deps.API = api

	return root, fake
}

// runGuestCmd executes cmd with args, capturing combined stdout/stderr.
func runGuestCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)

	err := cmd.Execute()
	return buf.String(), err
}

func init() {
	// Every polling loop in this package (clusterWaitForJoin) must not
	// actually sleep during tests; clusterPollSleep is a package var exactly
	// so tests can neuter it package-wide.
	clusterPollSleep = func(_ time.Duration) {}
}

// --- pvecm status / corosync-cfgtool -s parsing ---------------------------

const samplePvecmStatusQuorate3of3 = `Cluster information
-------------------
Name:             wayne
Config Version:   3
Transport:        knet
Secure auth:      on

Quorum information
------------------
Date:             Thu Jul 16 12:00:00 2026
Quorum provider:  corosync_votequorum
Nodes:            3
Node ID:          0x00000001
Ring ID:          1.1b
Quorate:          Yes

Votequorum information
----------------------
Expected votes:   3
Highest expected: 3
Total votes:      3
Quorum:           2
Flags:            Quorate

Membership information
----------------------
    Nodeid      Votes Name
0x00000001          1 10.10.1.10 (local)
0x00000002          1 10.10.1.11
0x00000003          1 10.10.1.12
`

const samplePvecmStatusNotClustered = `Error: Corosync config '/etc/pve/corosync.conf' does not exist - is this node part of a cluster?
`

// sampleQmListEmpty and samplePctListEmpty are `qm list`/`pct list`'s
// real-shaped output on a guest-free node: a header row and nothing else.
const sampleQmListEmpty = `      VMID NAME                 STATUS     MEM(MB)    BOOTDISK(GB) PID
`
const samplePctListEmpty = `VMID       Status     Lock         Name
`

// sampleQmListNonEmpty and samplePctListNonEmpty carry one and eleven data
// rows respectively (used to exercise clusterCountGuestListRows'
// header-skipping on more than a single row).
const sampleQmListNonEmpty = `      VMID NAME                 STATUS     MEM(MB)    BOOTDISK(GB) PID
       100 test-vm              running        512              20 12345
`
const samplePctListNonEmpty = `VMID       Status     Lock         Name
101        running                 ct-1
102        running                 ct-2
103        running                 ct-3
104        running                 ct-4
105        running                 ct-5
106        running                 ct-6
107        running                 ct-7
108        running                 ct-8
109        running                 ct-9
110        running                 ct-10
111        running                 ct-11
`

const samplePvecmStatusWithQdevice = `Cluster information
-------------------
Name:             wayne

Quorum information
------------------
Quorate:          Yes

Votequorum information
----------------------
Expected votes:   3
Highest expected: 3
Total votes:      3
Quorum:           2
Flags:            Quorate Qdevice
`

const sampleCorosyncCfgtoolAllUp = `Local node ID 1, transport knet
LINK ID 0
	addr	= 10.10.1.10
	status:
		nodeid  1:	localhost
		nodeid  2:	connected
		nodeid  3:	connected
`

const sampleCorosyncCfgtoolDegraded = `Local node ID 1, transport knet
LINK ID 0
	addr	= 10.10.1.10
	status:
		nodeid  1:	localhost
		nodeid  2:	connected
		nodeid  3:	disconnected
`

func TestParsePvecmStatus_NotClustered(t *testing.T) {
	st := parsePvecmStatus(samplePvecmStatusNotClustered)
	assert.False(t, st.Clustered)
	assert.False(t, st.Quorate)
	assert.Equal(t, "", st.ClusterName)
}

func TestParsePvecmStatus_Quorate3of3(t *testing.T) {
	st := parsePvecmStatus(samplePvecmStatusQuorate3of3)
	assert.True(t, st.Clustered)
	assert.Equal(t, "wayne", st.ClusterName)
	assert.True(t, st.Quorate)
	assert.Equal(t, 3, st.ExpectedVotes)
	assert.Equal(t, 3, st.TotalVotes)
	assert.False(t, st.HasQdevice)
}

func TestParsePvecmStatus_WithQdevice(t *testing.T) {
	st := parsePvecmStatus(samplePvecmStatusWithQdevice)
	assert.True(t, st.HasQdevice)
}

func TestParseCorosyncLinks_AllUp(t *testing.T) {
	allUp, statuses := parseCorosyncLinks(sampleCorosyncCfgtoolAllUp)
	assert.True(t, allUp)
	assert.Equal(t, []string{"localhost", "connected", "connected"}, statuses)
}

func TestParseCorosyncLinks_Degraded(t *testing.T) {
	allUp, statuses := parseCorosyncLinks(sampleCorosyncCfgtoolDegraded)
	assert.False(t, allUp)
	assert.Contains(t, statuses, "disconnected")
}

func TestParseCorosyncLinks_UnparsableOutput(t *testing.T) {
	allUp, statuses := parseCorosyncLinks("garbage, not corosync-cfgtool output at all")
	assert.False(t, allUp, "unparsable output must never be treated as vacuously all-up")
	assert.Nil(t, statuses)
}

// --- runGuestSSH / labGuestSSHArgs -----------------------------------------

func TestLabGuestSSHArgs_IncludesBatchModeAndAcceptNewHostKey(t *testing.T) {
	deps := &cli.Deps{Ctx: &config.Context{SSH: config.SSHBlock{User: "root", Port: 2222, Identity: "/id"}}}
	f, err := labGuestSSHFlags(deps)
	require.NoError(t, err)

	args := labGuestSSHArgs(f, "10.10.1.10")
	assert.Contains(t, args, "BatchMode=yes")
	assert.Contains(t, args, "StrictHostKeyChecking=accept-new")
	assert.Contains(t, args, "-i")
	assert.Contains(t, args, "/id")
	assert.Equal(t, "root@10.10.1.10", args[len(args)-1])
}

func TestLabGuestSSHFlags_RequiresContext(t *testing.T) {
	deps := &cli.Deps{}
	_, err := labGuestSSHFlags(deps)
	require.Error(t, err)
	assert.ErrorContains(t, err, "active pmx context")
}

func TestRunGuestSSH_SuccessCapturesStdout(t *testing.T) {
	fake := exec.Fake(exec.FakeResponse{Stdout: samplePvecmStatusQuorate3of3})
	deps := &cli.Deps{Runner: fake, Ctx: &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}}}

	res, err := runGuestSSH(deps, "10.10.1.10", "pvecm status")
	require.NoError(t, err)
	assert.Equal(t, samplePvecmStatusQuorate3of3, res.Stdout)

	require.Len(t, fake.Calls, 1)
	assert.Equal(t, "ssh", fake.Calls[0].Name)
	assert.Contains(t, fake.Calls[0].Args, "pvecm status")
	assert.Contains(t, fake.Calls[0].Args, "root@10.10.1.10")
}

func TestRunGuestSSH_NonZeroExitReturnsErrorAndExitCode(t *testing.T) {
	fake := exec.Fake(exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1})
	deps := &cli.Deps{Runner: fake, Ctx: &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}}}

	res, err := runGuestSSH(deps, "10.10.1.10", "pvecm status")
	require.Error(t, err)
	assert.Equal(t, 1, res.ExitCode)
	assert.False(t, guestCommandTransportFailed(err), "a plain non-zero exit is not a transport failure")
}

func TestGuestCommandTransportFailed_NonExitError(t *testing.T) {
	assert.True(t, guestCommandTransportFailed(assert.AnError), "a non-ExitError must be treated as a transport failure")
	assert.False(t, guestCommandTransportFailed(nil))
}
