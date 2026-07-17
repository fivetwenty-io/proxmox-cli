package lab

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
)

func TestClusterInit_SingleNodeLab_Errors(t *testing.T) {
	lab := multiNodeTestLab("solo", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"solo": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newClusterCmd())

	_, err := runGuestCmd(t, cmd, "init", "solo")
	require.Error(t, err)
	assert.ErrorContains(t, err, "single-node lab has no cluster")
	assert.Empty(t, fake.Calls)
}

func TestClusterInit_DryRun_NoRunnerCalls(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newClusterCmd())

	out, err := runGuestCmd(t, cmd, "init", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "pvecm create wayne --link0 10.10.1.10")
	assert.Empty(t, fake.Calls, "dry-run must never invoke the runner")
}

func TestClusterInit_AlreadyClustered_Skip(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newClusterCmd())
	fake = exec.Fake(exec.FakeResponse{Stdout: samplePvecmStatusQuorate3of3})
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "init", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "already clustered")
	require.Len(t, fake.Calls, 1, "only the probe, no pvecm create")
}

func TestClusterInit_HappyPath_CreatesAndVerifies(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())
	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1}, // probe: not clustered
		exec.FakeResponse{}, // pvecm create
		exec.FakeResponse{Stdout: `Cluster information
-------------------
Name:             wayne

Quorum information
------------------
Quorate:          Yes

Votequorum information
----------------------
Expected votes:   1
Highest expected: 1
Total votes:      1
Quorum:           1
Flags:            Quorate
`}, // verify
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "init", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "created on node 0")

	require.Len(t, fake.Calls, 3)
	assert.Contains(t, fake.Calls[1].Args, "pvecm create wayne --link0 10.10.1.10")
	assert.Contains(t, fake.Calls[1].Args, "root@10.10.1.10")
}

func TestClusterInit_DifferentClusterAlreadyPresent_Refuses(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())
	other := `Cluster information
-------------------
Name:             someoneelse

Quorum information
------------------
Quorate:          Yes
`
	fake := exec.Fake(exec.FakeResponse{Stdout: other})
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "init", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "DIFFERENT cluster")
	require.Len(t, fake.Calls, 1, "must refuse before any mutating call")
}

func TestClusterInit_PeppiGuardRefusesBeforeAnyRunnerCall(t *testing.T) {
	lab := multiNodeTestLab("dirty", 3, "")
	lab.Network.VnetID = "peppivn0"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"dirty": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newClusterCmd())

	_, err := runGuestCmd(t, cmd, "init", "dirty")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.Empty(t, fake.Calls)
}

func TestClusterJoin_RequiresNodeFlag(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newClusterCmd())

	_, err := runGuestCmd(t, cmd, "join", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "--node is required")
	assert.Empty(t, fake.Calls)
}

func TestClusterJoin_NodeZeroRejected(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newClusterCmd())

	_, err := runGuestCmd(t, cmd, "join", "wayne", "--node", "0")
	require.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
	assert.Empty(t, fake.Calls)
}

func TestClusterJoin_OutOfRangeForTopology(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newClusterCmd())

	_, err := runGuestCmd(t, cmd, "join", "wayne", "--node", "3")
	require.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
	assert.Empty(t, fake.Calls)
}

func TestClusterJoin_DryRun_NoRunnerCalls(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newClusterCmd())

	out, err := runGuestCmd(t, cmd, "join", "wayne", "--node", "1", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "pvecm add 10.10.1.10 --link0 10.10.1.11 --use_ssh")
	assert.Empty(t, fake.Calls)
}

func TestClusterJoin_AlreadyJoined_Skip(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())
	fake := exec.Fake(exec.FakeResponse{Stdout: samplePvecmStatusQuorate3of3})
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "join", "wayne", "--node", "1")
	require.NoError(t, err)
	assert.Contains(t, out, "already joined")
	require.Len(t, fake.Calls, 1)
}

func TestClusterJoin_HappyPath_JoinsAndPollsUntilQuorate(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())

	notYet := `Cluster information
-------------------
Name:             wayne

Quorum information
------------------
Quorate:          Yes

Votequorum information
----------------------
Expected votes:   1
Highest expected: 1
Total votes:      1
Quorum:           1
Flags:            Quorate
`
	quorate2of2 := `Cluster information
-------------------
Name:             wayne

Quorum information
------------------
Quorate:          Yes

Votequorum information
----------------------
Expected votes:   2
Highest expected: 2
Total votes:      2
Quorum:           2
Flags:            Quorate
`
	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1}, // probe on joining node: not yet joined
		exec.FakeResponse{Stdout: sampleQmListEmpty},                          // guest-free check: qm list (empty)
		exec.FakeResponse{Stdout: samplePctListEmpty},                         // guest-free check: pct list (empty)
		exec.FakeResponse{},                                   // pvecm add
		exec.FakeResponse{Stdout: notYet},                     // poll attempt 1: pvecm status (not yet 2/2)
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp}, // poll attempt 1: corosync-cfgtool -s
		exec.FakeResponse{Stdout: quorate2of2},                // poll attempt 2: pvecm status (2/2, quorate)
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp}, // poll attempt 2: corosync-cfgtool -s (all up)
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "join", "wayne", "--node", "1")
	require.NoError(t, err)
	assert.Contains(t, out, "joined cluster")
	assert.Contains(t, out, "2/2 votes quorate")

	require.Len(t, fake.Calls, 8)
	assert.Contains(t, fake.Calls[1].Args, "qm list")
	assert.Contains(t, fake.Calls[2].Args, "pct list")
	assert.Contains(t, fake.Calls[3].Args, "pvecm add 10.10.1.10 --link0 10.10.1.11 --use_ssh")
}

func TestClusterJoin_RefusesWhenJoiningNodeHostsVMs(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())
	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1}, // probe: not yet joined
		exec.FakeResponse{Stdout: sampleQmListNonEmpty},                       // qm list: one VM present
	)
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "join", "wayne", "--node", "1")
	require.Error(t, err)
	assert.ErrorContains(t, err, "hosts 1 VM(s)")
	assert.ErrorContains(t, err, "guest-free")
	require.Len(t, fake.Calls, 2, "must refuse before pvecm add ever runs")
}

func TestClusterJoin_RefusesWhenJoiningNodeHostsContainers(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())
	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1}, // probe: not yet joined
		exec.FakeResponse{Stdout: sampleQmListEmpty},                          // qm list: empty
		exec.FakeResponse{Stdout: samplePctListNonEmpty},                      // pct list: eleven containers present
	)
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "join", "wayne", "--node", "1")
	require.Error(t, err)
	assert.ErrorContains(t, err, "hosts 11 container(s)")
	assert.ErrorContains(t, err, "guest-free")
	require.Len(t, fake.Calls, 3, "must refuse before pvecm add ever runs")
}

func TestClusterJoin_PeppiGuardRefusesBeforeAnyCall(t *testing.T) {
	lab := multiNodeTestLab("dirty", 3, "")
	lab.Network.VnetID = "peppivn0"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"dirty": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newClusterCmd())

	_, err := runGuestCmd(t, cmd, "join", "dirty", "--node", "1")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.Empty(t, fake.Calls)
}

func TestClusterJoin_PollTimesOut(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())

	stuck := `Cluster information
-------------------
Name:             wayne

Quorum information
------------------
Quorate:          Yes

Votequorum information
----------------------
Expected votes:   1
Highest expected: 1
Total votes:      1
Quorum:           1
Flags:            Quorate
`
	responses := []exec.FakeResponse{
		{Stdout: samplePvecmStatusNotClustered, ExitCode: 1}, // probe: not yet joined
		{Stdout: sampleQmListEmpty},                          // guest-free check: qm list (empty)
		{Stdout: samplePctListEmpty},                         // guest-free check: pct list (empty)
		{},                                                   // pvecm add
	}
	for i := 0; i < clusterJoinPollAttempts; i++ {
		responses = append(responses, exec.FakeResponse{Stdout: stuck}, exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp})
	}
	fake := exec.Fake(responses...)
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "join", "wayne", "--node", "1")
	require.Error(t, err)
	assert.ErrorContains(t, err, "timed out")
}

// TestClusterWaitForJoin_DoesNotSleepAfterFinalAttempt covers M3-R04: the
// poll loop must not sleep one extra interval after its final,
// still-not-converged attempt before returning the timeout error — it
// should sleep exactly clusterJoinPollAttempts-1 times (between attempts),
// never clusterJoinPollAttempts times (which would include a wasted sleep
// after the last attempt nothing further waits on).
func TestClusterWaitForJoin_DoesNotSleepAfterFinalAttempt(t *testing.T) {
	sleepCalls := 0
	orig := clusterPollSleep
	clusterPollSleep = func(_ time.Duration) { sleepCalls++ }
	defer func() { clusterPollSleep = orig }()

	deps := &cli.Deps{Runner: exec.Fake(), Ctx: &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}}}

	err := clusterWaitForJoin(deps, "10.10.1.10", 3)
	require.Error(t, err)
	assert.ErrorContains(t, err, "timed out")
	assert.Equal(t, clusterJoinPollAttempts-1, sleepCalls,
		"must sleep only between attempts, never after the final one")
}

func TestClusterStatus_RendersFields(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())
	fake := exec.Fake(exec.FakeResponse{Stdout: samplePvecmStatusQuorate3of3}, exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp})
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "status", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "wayne")
	assert.Contains(t, out, "true")
	assert.Contains(t, out, "all links connected")
}

func TestClusterStatus_NotClustered(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())
	fake := exec.Fake(exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1})
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "status", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "n/a (not clustered)")
}
