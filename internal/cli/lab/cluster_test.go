package lab

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
)

// samplePvecmStatusQuorate1of1 returns pvecm status output for a
// freshly-created single-node cluster (node 0 immediately after `pvecm
// create`, before any other node has joined): quorate with exactly 1
// expected and 1 total vote, matching the shape ensureClusterInit's
// post-create verify step requires (clustered, quorate, expected=total=1).
// Kept in this file (rather than guestssh_test.go's shared fixture consts)
// since it is only consumed by this file's cluster-init tests.
func samplePvecmStatusQuorate1of1() string {
	return `Cluster information
-------------------
Name:             demo
Config Version:   1
Transport:        knet
Secure auth:      on

Quorum information
------------------
Date:             Thu Jul 16 12:00:00 2026
Quorum provider:  corosync_votequorum
Nodes:            1
Node ID:          0x00000001
Ring ID:          1.1
Quorate:          Yes

Votequorum information
----------------------
Expected votes:   1
Highest expected: 1
Total votes:      1
Quorum:           1
Flags:            Quorate

Membership information
----------------------
    Nodeid      Votes Name
0x00000001          1 10.10.1.10 (local)
`
}

// buildClusterCmdWithContext builds `pmx lab cluster` wired to fake (the
// caller's own ordered exec.FakeRunner) plus a config that already carries a
// resolvable lab-<name> lab (2-node topology, mgmt 10.10.1.0/24, so node 0's
// mgmt IP is 10.10.1.10) and a seeded lab-<name> pmx context (host
// 10.10.1.10 — inside that mgmt subnet — token auth, non-empty secret) so
// syncLabContext's existing-context reuse path can run without minting a new
// token. It returns the built command and its *cli.Deps for assertions.
func buildClusterCmdWithContext(t *testing.T, fake *exec.FakeRunner, name string) (*cobra.Command, *cli.Deps) {
	t.Helper()

	lab := multiNodeTestLab(name, 2, "")
	cfg := &config.Config{
		Labs: map[string]*config.Lab{name: lab},
		Contexts: map[string]*config.Context{
			"lab-" + name: {
				Host:     "10.10.1.10",
				Port:     8006,
				Protocol: "https",
				Product:  config.ProductPVE,
				Auth: config.AuthBlock{
					Type:     "token",
					Username: "pmx@pve",
					TokenID:  "pmx",
					Secret:   "keychain:pmx-lab-" + name + "/pmx@pve!pmx",
				},
			},
		},
	}
	path := writeConfig(t, cfg)

	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())
	deps := cli.GetDeps(cmd)
	deps.Runner = fake

	return cmd, deps
}

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
	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())
	fake := exec.Fake(exec.FakeResponse{Stdout: samplePvecmStatusQuorate3of3})
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
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1}, // 0: probe on joining node: not yet joined
		exec.FakeResponse{Stdout: sampleQmListEmpty},                          // 1: guest-free check: qm list (empty)
		exec.FakeResponse{Stdout: samplePctListEmpty},                         // 2: guest-free check: pct list (empty)
		exec.FakeResponse{}, // 3: trust seed: ensure root keypair (node 1)
		exec.FakeResponse{Stdout: sampleRootPubKey + "\n"}, // 4: trust seed: read public key (node 1)
		exec.FakeResponse{},                                   // 5: trust seed: append to node 0's authorized_keys
		exec.FakeResponse{},                                   // 6: trust seed: apiver preflight (node 1 -> node 0)
		exec.FakeResponse{},                                   // 7: pvecm add
		exec.FakeResponse{Stdout: quorate2of2},                // 8: join verification: pvecm status on node 1
		exec.FakeResponse{Stdout: notYet},                     // 9: poll attempt 1: pvecm status (not yet 2/2)
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp}, // 10: poll attempt 1: corosync-cfgtool -s
		exec.FakeResponse{Stdout: quorate2of2},                // 11: poll attempt 2: pvecm status (2/2, quorate)
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp}, // 12: poll attempt 2: corosync-cfgtool -s (all up)
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "join", "wayne", "--node", "1")
	require.NoError(t, err)
	assert.Contains(t, out, "joined cluster")
	assert.Contains(t, out, "2/2 votes quorate")

	require.Len(t, fake.Calls, 13)
	assert.Contains(t, fake.Calls[1].Args, "qm list")
	assert.Contains(t, fake.Calls[2].Args, "pct list")
	assert.Contains(t, fake.Calls[3].Args, clusterEnsureRootKeypairCmd)
	assert.Contains(t, fake.Calls[3].Args, "root@10.10.1.11")
	assert.Contains(t, fake.Calls[4].Args, "cat /root/.ssh/id_rsa.pub")
	assert.Contains(t, fake.Calls[4].Args, "root@10.10.1.11")
	assert.Contains(t, fake.Calls[5].Args, "root@10.10.1.10")
	require.NotEmpty(t, fake.Calls[5].Args)
	assert.Contains(t, fake.Calls[5].Args[len(fake.Calls[5].Args)-1], "authorized_keys",
		"trust seed step 5 must append the joining node's pubkey into node 0's authorized_keys")
	assert.Contains(t, fake.Calls[5].Args[len(fake.Calls[5].Args)-1], sampleRootPubKey)
	assert.Contains(t, fake.Calls[6].Args, "ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new root@10.10.1.10 pvecm apiver")
	assert.Contains(t, fake.Calls[6].Args, "root@10.10.1.11")
	assert.Contains(t, fake.Calls[7].Args, "pvecm add 10.10.1.10 --link0 10.10.1.11 --use_ssh")
	assert.Contains(t, fake.Calls[8].Args, "pvecm status")
	assert.Contains(t, fake.Calls[8].Args, "root@10.10.1.11", "join verification probes the JOINING node, not node 0")
}

// TestClusterJoin_TrustSeedPreflightFails_AbortsBeforeJoin covers
// clusterSeedJoinTrust's final verification step: even after the keypair is
// ensured and the public key pushed to node 0, the non-interactive ssh
// preflight itself can still fail (e.g. sshd misconfiguration, a stale
// authorized_keys mount) — this must abort with an error naming both nodes,
// and `pvecm add` must never run.
func TestClusterJoin_TrustSeedPreflightFails_AbortsBeforeJoin(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1}, // probe: not yet joined
		exec.FakeResponse{Stdout: sampleQmListEmpty},                          // guest-free: qm list
		exec.FakeResponse{Stdout: samplePctListEmpty},                         // guest-free: pct list
		exec.FakeResponse{}, // trust seed: ensure root keypair
		exec.FakeResponse{Stdout: sampleRootPubKey + "\n"}, // trust seed: read public key
		exec.FakeResponse{},              // trust seed: append to authorized_keys
		exec.FakeResponse{ExitCode: 255}, // trust seed: apiver preflight FAILS
	)
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "join", "wayne", "--node", "1")
	require.Error(t, err)
	assert.ErrorContains(t, err, "10.10.1.11", "error must name the joining node")
	assert.ErrorContains(t, err, "10.10.1.10", "error must name node 0")
	assert.ErrorContains(t, err, "trust")
	require.Len(t, fake.Calls, 7, "pvecm add must never be invoked after the trust preflight fails")
}

// TestClusterJoin_ExitZeroButNotJoined_ErrorsWithPvecmAddOutput is the
// regression test for the exact live failure this change fixes: `pvecm add`
// exits 0 (and would otherwise be treated as success) while its stderr
// carries the real failure ("unable to copy ssh ID: exit code 1") and the
// joining node never actually joined. The post-add re-probe must catch this,
// surface the captured pvecm add stdout/stderr verbatim in the error, and
// never enter clusterWaitForJoin (node 0 is never polled).
func TestClusterJoin_ExitZeroButNotJoined_ErrorsWithPvecmAddOutput(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1}, // probe: not yet joined
		exec.FakeResponse{Stdout: sampleQmListEmpty},                          // guest-free: qm list
		exec.FakeResponse{Stdout: samplePctListEmpty},                         // guest-free: pct list
		exec.FakeResponse{}, // trust seed: ensure root keypair
		exec.FakeResponse{Stdout: sampleRootPubKey + "\n"}, // trust seed: read public key
		exec.FakeResponse{}, // trust seed: append to authorized_keys
		exec.FakeResponse{}, // trust seed: apiver preflight OK
		exec.FakeResponse{Stderr: "unable to copy ssh ID: exit code 1\n"},     // pvecm add: exit 0, but warns on stderr
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1}, // join verification: still not clustered
	)
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "join", "wayne", "--node", "1")
	require.Error(t, err)
	assert.ErrorContains(t, err, "unable to copy ssh ID: exit code 1")
	assert.ErrorContains(t, err, "did not actually join")
	require.Len(t, fake.Calls, 9, "clusterWaitForJoin must never be entered when the post-add re-probe shows unclustered")
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

// TestCluster_TwoIndependentLabs is P1-T9's regression coverage for the
// "2 HA clusters == 2 independent `pmx lab` config entries" design (P1 plan
// §0): two differently-named labs, each with its own mgmt subnet (mirroring
// the real pve-cpi/pve-cpi-az2 shape, 10.254.0.0/24 vs 10.255.0.0/24), must
// never produce colliding `pvecm create`/`pvecm add` command strings or
// colliding node mgmt IPs when cluster init/join is run against each in
// turn — confirming runClusterInit/runClusterJoin derive every value from
// the single resolved lab argument, with zero state shared between two
// labs resolved out of the same config.
func TestCluster_TwoIndependentLabs(t *testing.T) {
	az1 := cleanLab("pve-cpi")
	az1.Network.Mgmt = config.LabMgmt{Subnet: "10.254.0.0/24", Gateway: "10.254.0.1"}
	az1.Topology = config.LabTopology{Nodes: 3}

	az2 := cleanLab("pve-cpi-az2")
	az2.Network.Mgmt = config.LabMgmt{Subnet: "10.255.0.0/24", Gateway: "10.255.0.1"}
	az2.Topology = config.LabTopology{Nodes: 3}

	cfg := &config.Config{Labs: map[string]*config.Lab{
		"pve-cpi":     az1,
		"pve-cpi-az2": az2,
	}}
	path := writeConfig(t, cfg)

	// cluster init --dry-run against both labs: distinct cluster-init
	// command strings, each carrying its own lab name and node 0 mgmt IP.
	initCmd1, fake1 := buildGuestSSHCmd(t, path, newClusterCmd())
	initOut1, err := runGuestCmd(t, initCmd1, "init", "pve-cpi", "--dry-run")
	require.NoError(t, err)
	assert.Empty(t, fake1.Calls, "dry-run must never invoke the runner")

	initCmd2, fake2 := buildGuestSSHCmd(t, path, newClusterCmd())
	initOut2, err := runGuestCmd(t, initCmd2, "init", "pve-cpi-az2", "--dry-run")
	require.NoError(t, err)
	assert.Empty(t, fake2.Calls, "dry-run must never invoke the runner")

	assert.Contains(t, initOut1, "pvecm create pve-cpi --link0 10.254.0.10")
	assert.Contains(t, initOut2, "pvecm create pve-cpi-az2 --link0 10.255.0.10")
	assert.NotEqual(t, initOut1, initOut2,
		"two independently-named labs must never produce identical cluster-init commands")

	// cluster join --dry-run against both labs: distinct join command
	// strings, each carrying its own node 0 and joining-node mgmt IPs.
	joinCmd1, fake3 := buildGuestSSHCmd(t, path, newClusterCmd())
	joinOut1, err := runGuestCmd(t, joinCmd1, "join", "pve-cpi", "--node", "1", "--dry-run")
	require.NoError(t, err)
	assert.Empty(t, fake3.Calls, "dry-run must never invoke the runner")

	joinCmd2, fake4 := buildGuestSSHCmd(t, path, newClusterCmd())
	joinOut2, err := runGuestCmd(t, joinCmd2, "join", "pve-cpi-az2", "--node", "1", "--dry-run")
	require.NoError(t, err)
	assert.Empty(t, fake4.Calls, "dry-run must never invoke the runner")

	assert.Contains(t, joinOut1, "pvecm add 10.254.0.10 --link0 10.254.0.11 --use_ssh")
	assert.Contains(t, joinOut2, "pvecm add 10.255.0.10 --link0 10.255.0.11 --use_ssh")
	assert.NotEqual(t, joinOut1, joinOut2,
		"two independently-named labs must never produce identical cluster-join commands")

	// Belt-and-suspenders: assert the underlying derived mgmt IPs themselves
	// never collide, independent of the rendered command string's exact
	// formatting.
	node0AZ1, err := labNodeMgmtIP(az1.Network, 0)
	require.NoError(t, err)
	node0AZ2, err := labNodeMgmtIP(az2.Network, 0)
	require.NoError(t, err)
	assert.NotEqual(t, node0AZ1, node0AZ2, "two labs' node 0 mgmt IPs must never collide")

	node1AZ1, err := labNodeMgmtIP(az1.Network, 1)
	require.NoError(t, err)
	node1AZ2, err := labNodeMgmtIP(az2.Network, 1)
	require.NoError(t, err)
	assert.NotEqual(t, node1AZ1, node1AZ2, "two labs' node 1 mgmt IPs must never collide")
}

// TestClusterInit_RefreshesExistingContextFingerprint covers cluster init's
// best-effort context refresh: `pvecm create` reissues node 0's API cert
// under a new cluster CA, invalidating any previously pinned TLS fingerprint,
// so `cluster init` must refresh the existing lab-<name> context afterward.
func TestClusterInit_RefreshesExistingContextFingerprint(t *testing.T) {
	// pvecm status (not clustered) -> pvecm create -> pvecm status (quorate),
	// then the context refresh's ensure-user/ensure-ACL/fingerprint/hostname
	// calls (the reuse-probe below is stubbed and issues no ssh call, so no
	// token remove/add happens).
	fp := "11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00"
	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1}, // probe
		exec.FakeResponse{}, // pvecm create
		exec.FakeResponse{Stdout: samplePvecmStatusQuorate1of1()}, // verify
		exec.FakeResponse{}, exec.FakeResponse{}, // ensure user, ACL
		exec.FakeResponse{Stdout: "sha256 Fingerprint=" + fp + "\n"}, // fingerprint
		exec.FakeResponse{Stdout: "lab-demo-0\n"},                    // hostname
	)
	cmd, deps := buildClusterCmdWithContext(t, fake, "demo")

	// Reuse-secret path: probe returns valid so no token add is issued.
	origProbe := labProbeContextVersion
	labProbeContextVersion = func(*cobra.Command, *cli.Deps, string) error { return nil }
	t.Cleanup(func() { labProbeContextVersion = origProbe })

	out, err := runGuestCmd(t, cmd, "init", "demo")
	require.NoError(t, err)
	assert.Equal(t, fp, deps.Cfg.Contexts["lab-demo"].TLS.Fingerprint)
	assert.Contains(t, out, "context lab-demo refreshed")
	require.Len(t, fake.Calls, 7)
}
