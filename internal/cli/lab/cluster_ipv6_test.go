package lab

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// clusterJoinQuorate2of2 is the joining node's post-join `pvecm status`, the
// shape clusterWaitForJoin waits for.
const clusterJoinQuorate2of2 = `Cluster information
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

// TestClusterJoin_ReconcilesJoinedNodeIPv6 covers the manual counterpart of
// the scale gap: a node joined by hand used to end up a cluster member with
// no management IPv6, because that address is written through the nested
// cluster's own API while `cluster join` runs over ssh from the outer
// context. The join must now converge it, and say so in its message.
func TestClusterJoin_ReconcilesJoinedNodeIPv6(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())

	f := testhelper.NewFakePVE(t)
	var updateRec, applyRec []hostnetRecordedRequest
	hostnetRecord(f, nil, nil, "", "GET /api2/json/nodes/lab-wayne-1/network",
		[]any{hostnetVmbr0Row(nil)}, 200)
	hostnetRecord(f, &updateRec, nil, "", "PUT /api2/json/nodes/lab-wayne-1/network/vmbr0", nil, 200)
	hostnetRecord(f, &applyRec, nil, "", "PUT /api2/json/nodes/lab-wayne-1/network", nil, 200)
	stubInnerAPIClient(t, f)

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1}, // probe: not joined yet
		exec.FakeResponse{Stdout: sampleQmListEmpty},
		exec.FakeResponse{Stdout: samplePctListEmpty},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: sampleRootPubKey + "\n"},
		exec.FakeResponse{},
		exec.FakeResponse{},
		exec.FakeResponse{}, // pvecm add
		exec.FakeResponse{Stdout: clusterJoinQuorate2of2},     // join verification
		exec.FakeResponse{Stdout: clusterJoinQuorate2of2},     // quorum poll
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp}, // links poll
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "join", "wayne", "--node", "1")
	require.NoError(t, err)
	assert.Contains(t, out, "joined cluster")

	require.Len(t, updateRec, 1, "the joined node's vmbr0 gets its planned IPv6")
	addr6, err := labNodeMgmtIP6(lab.Network, 1)
	require.NoError(t, err)
	assert.Contains(t, updateRec[0].body["cidr6"], addr6)
	require.Len(t, applyRec, 1)
	assert.Contains(t, out, "lab-wayne-1")
}

// TestClusterJoin_ReconcileFailureDoesNotFailTheJoin pins the ordering
// guarantee: the node IS a member by the time the reconcile runs, so nothing
// the reconcile hits may turn a successful join into a command failure.
func TestClusterJoin_ReconcileFailureDoesNotFailTheJoin(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newClusterCmd())

	f := testhelper.NewFakePVE(t)
	hostnetRecord(f, nil, nil, "", "GET /api2/json/nodes/lab-wayne-1/network", nil, 500)
	stubInnerAPIClient(t, f)

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1},
		exec.FakeResponse{Stdout: sampleQmListEmpty},
		exec.FakeResponse{Stdout: samplePctListEmpty},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: sampleRootPubKey + "\n"},
		exec.FakeResponse{},
		exec.FakeResponse{},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: clusterJoinQuorate2of2},
		exec.FakeResponse{Stdout: clusterJoinQuorate2of2},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "join", "wayne", "--node", "1")
	require.NoError(t, err, "a reconcile failure must never fail a join that succeeded")
	assert.Contains(t, out, "joined cluster")
	assert.Contains(t, out, "deferred")
}

// TestClusterJoin_DryRun_PreviewsIPv6 pins the dry-run half: the preview
// names the address the join would give the node, with no runner calls.
func TestClusterJoin_DryRun_PreviewsIPv6(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newClusterCmd())

	addr6, err := labNodeMgmtIP6(lab.Network, 1)
	require.NoError(t, err)

	out, err := runGuestCmd(t, cmd, "join", "wayne", "--node", "1", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, addr6)
	assert.Empty(t, fake.Calls, "dry-run must never invoke the runner")
}
