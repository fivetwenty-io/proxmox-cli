package lab

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// scaleTestLab returns a Lab definition fully populated for `pmx lab scale`
// tests: createTestLab's compute/storage/network fixture (needed by
// buildCreatePlan when scale grows a lab) plus a mgmt subnet (needed by
// labNodeMgmtIP, which createTestLab alone does not set) and the given
// topology.
func scaleTestLab(name string, nodes int, qdevice string) *config.Lab {
	lab := createTestLab(name)
	lab.Network.Mgmt.Subnet = "10.10.1.0/24"
	lab.Topology = config.LabTopology{Nodes: nodes, Qdevice: qdevice}
	return lab
}

// --- buildScalePlan (pure function) ----------------------------------------

func TestBuildScalePlan_WorkedTransitions(t *testing.T) {
	cases := []struct {
		name                  string
		currentN              int
		currentQdevicePresent bool
		targetN               int
		targetQdeviceRequired bool
		wantQdeviceRemove     bool
		wantGrow              []int
		wantShrink            []int
		wantQdeviceAdd        bool
	}{
		{"1->3", 1, false, 3, false, false, []int{1, 2}, nil, false},
		{"1->2+Q", 1, false, 2, true, false, []int{1}, nil, true},
		{"2+Q->3", 2, true, 3, false, true, []int{2}, nil, false},
		{"3->5", 3, false, 5, false, false, []int{3, 4}, nil, false},
		{"3->4+Q", 3, false, 4, true, false, []int{3}, nil, true},
		{"4+Q->5", 4, true, 5, false, true, []int{4}, nil, false},
		{"5->3", 5, false, 3, false, false, nil, []int{4, 3}, false},
		{"3->1", 3, false, 1, false, false, nil, []int{2, 1}, false},
		{"2+Q->1", 2, true, 1, false, true, nil, []int{1}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := buildScalePlan(tc.currentN, tc.currentQdevicePresent, tc.targetN, tc.targetQdeviceRequired)
			assert.Equal(t, tc.wantQdeviceRemove, p.qdeviceRemoveNeeded, "qdeviceRemoveNeeded")
			assert.Equal(t, tc.wantGrow, p.growIndices, "growIndices")
			assert.Equal(t, tc.wantShrink, p.shrinkIndices, "shrinkIndices")
			assert.Equal(t, tc.wantQdeviceAdd, p.qdeviceAddNeeded, "qdeviceAddNeeded")
		})
	}
}

func TestBuildScalePlan_QdeviceOnlyChangeNoNodeDelta(t *testing.T) {
	// nodes:4 unchanged, qdevice auto (not present) -> qdevice on: only
	// qdeviceAddNeeded, no grow/shrink.
	p := buildScalePlan(4, false, 4, true)
	assert.False(t, p.qdeviceRemoveNeeded)
	assert.Empty(t, p.growIndices)
	assert.Empty(t, p.shrinkIndices)
	assert.True(t, p.qdeviceAddNeeded)
}

// --- scaleCurrentNodeCount (VM-shell existence; dry-run estimate + "lab exists" precondition only) ---

func TestScaleCurrentNodeCount_Contiguous(t *testing.T) {
	classified := []classifiedLabVM{
		{VM: labVM{Name: "lab-wayne-0"}, Index: 0},
		{VM: labVM{Name: "lab-wayne-1"}, Index: 1},
		{VM: labVM{Name: "lab-wayne-2"}, Index: 2},
	}
	n, err := scaleCurrentNodeCount(classified)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
}

func TestScaleCurrentNodeCount_IgnoresQdeviceEntry(t *testing.T) {
	classified := []classifiedLabVM{
		{VM: labVM{Name: "lab-wayne-0"}, Index: 0},
		{VM: labVM{Name: "lab-wayne-q"}, IsQdevice: true},
	}
	n, err := scaleCurrentNodeCount(classified)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

func TestScaleCurrentNodeCount_EmptyReturnsZero(t *testing.T) {
	n, err := scaleCurrentNodeCount(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestScaleCurrentNodeCount_GapErrors(t *testing.T) {
	classified := []classifiedLabVM{
		{VM: labVM{Name: "lab-wayne-0"}, Index: 0},
		{VM: labVM{Name: "lab-wayne-2"}, Index: 2},
	}
	_, err := scaleCurrentNodeCount(classified)
	require.Error(t, err)
	assert.ErrorContains(t, err, "not contiguous")
}

// --- scaleCurrentMembership (M4-02: live-corosync ground truth) ------------

func TestScaleCurrentMembership_NotClusteredYet(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	lab.Name = "wayne"
	deps := &cli.Deps{
		Runner: exec.Fake(exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1}),
		Ctx:    &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}},
	}

	n, qdevicePresent, quorate, err := scaleCurrentMembership(deps, lab, "10.10.1.10")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.False(t, qdevicePresent)
	assert.True(t, quorate, "a not-yet-clustered lab has nothing to be non-quorate about")
}

func TestScaleCurrentMembership_DerivesFromLiveCorosyncNotShellExistence(t *testing.T) {
	// The core of the M4-02 fix: currentN/currentQdevicePresent must come
	// from live pvecm status, not VM-shell existence, so a re-run after a
	// deferred grow/qdevice-add correctly recomputes the remaining delta
	// instead of no-op'ing forever.
	lab := scaleTestLab("wayne", 1, "")
	lab.Name = "wayne"
	deps := &cli.Deps{
		Runner: exec.Fake(exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2}),
		Ctx:    &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}},
	}

	n, qdevicePresent, quorate, err := scaleCurrentMembership(deps, lab, "10.10.1.10")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.False(t, qdevicePresent)
	assert.True(t, quorate)
}

func TestScaleCurrentMembership_QdeviceRegistered(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	lab.Name = "wayne"
	deps := &cli.Deps{
		Runner: exec.Fake(exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice}),
		Ctx:    &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}},
	}

	n, qdevicePresent, quorate, err := scaleCurrentMembership(deps, lab, "10.10.1.10")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.True(t, qdevicePresent)
	assert.True(t, quorate)
}

func TestScaleCurrentMembership_NotQuorateRefuses(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	lab.Name = "wayne"
	notQuorate := strings.Replace(scaleClusteredNoQdevice2of2, "Quorate:          Yes", "Quorate:          No", 1)
	deps := &cli.Deps{
		Runner: exec.Fake(exec.FakeResponse{Stdout: notQuorate}),
		Ctx:    &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}},
	}

	_, _, quorate, err := scaleCurrentMembership(deps, lab, "10.10.1.10")
	require.NoError(t, err)
	assert.False(t, quorate)
}

func TestScaleCurrentMembership_DifferentClusterRefuses(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	lab.Name = "wayne"
	other := strings.Replace(scaleClusteredNoQdevice2of2, "Name:             wayne", "Name:             someoneelse", 1)
	deps := &cli.Deps{
		Runner: exec.Fake(exec.FakeResponse{Stdout: other}),
		Ctx:    &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}},
	}

	_, _, _, err := scaleCurrentMembership(deps, lab, "10.10.1.10")
	require.Error(t, err)
	assert.ErrorContains(t, err, "DIFFERENT cluster")
}

// --- scaleValidateNode (M4-05 / validator finding #6) -----------------------

const samplePvesmStatusAllActive = `Name             Type     Status           Total            Used       Available        %
local             dir     active       100000000        10000000        90000000    10.00%
nfs-images        nfs     active      1073741824               0      1073741824    0.00%
nfs-backup        nfs     active      1073741824               0      1073741824    0.00%
shared-iso        nfs     active      1073741824               0      1073741824    0.00%
`

const samplePvesmStatusOneInactive = `Name             Type     Status           Total            Used       Available        %
local             dir     active       100000000        10000000        90000000    10.00%
nfs-images        nfs   inactive               0               0               0    0.00%
`

func TestScaleValidateNode_HealthyWhenQuorateLinksUpStorageActive(t *testing.T) {
	deps := &cli.Deps{
		Runner: exec.Fake(
			exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
			exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
			exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
		),
		Ctx: &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}},
	}
	summary, healthy := scaleValidateNode(deps, "10.10.1.10")
	assert.True(t, healthy, summary)
}

func TestScaleValidateNode_UnhealthyWhenNotQuorate(t *testing.T) {
	notQuorate := strings.Replace(scaleClusteredNoQdevice3of3, "Quorate:          Yes", "Quorate:          No", 1)
	deps := &cli.Deps{
		Runner: exec.Fake(
			exec.FakeResponse{Stdout: notQuorate},
			exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
			exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
		),
		Ctx: &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}},
	}
	_, healthy := scaleValidateNode(deps, "10.10.1.10")
	assert.False(t, healthy)
}

func TestScaleValidateNode_UnhealthyWhenLinksDegraded(t *testing.T) {
	deps := &cli.Deps{
		Runner: exec.Fake(
			exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
			exec.FakeResponse{Stdout: sampleCorosyncCfgtoolDegraded},
			exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
		),
		Ctx: &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}},
	}
	_, healthy := scaleValidateNode(deps, "10.10.1.10")
	assert.False(t, healthy)
}

func TestScaleValidateNode_UnhealthyWhenStorageInactive(t *testing.T) {
	deps := &cli.Deps{
		Runner: exec.Fake(
			exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
			exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
			exec.FakeResponse{Stdout: samplePvesmStatusOneInactive},
		),
		Ctx: &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}},
	}
	_, healthy := scaleValidateNode(deps, "10.10.1.10")
	assert.False(t, healthy)
}

func TestScaleValidateNode_UnreachableIsUnhealthyNotFatal(t *testing.T) {
	deps := &cli.Deps{
		Runner: exec.Fake(exec.FakeResponse{ExitCode: 255}),
		Ctx:    &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}},
	}
	summary, healthy := scaleValidateNode(deps, "10.10.1.99")
	assert.False(t, healthy)
	assert.Equal(t, "not reachable", summary)
}

// --- parseQmListVMIDs --------------------------------------------------------

func TestParseQmListVMIDs_SkipsHeaderAndBlankLines(t *testing.T) {
	out := "      VMID NAME                 STATUS     MEM(MB)    BOOTDISK(GB) PID\n" +
		"       100 test-vm              running        512              20 12345\n" +
		"       101 other-vm             stopped        256              10 0\n\n"
	assert.Equal(t, []string{"100", "101"}, parseQmListVMIDs(out))
}

func TestParseQmListVMIDs_EmptyList(t *testing.T) {
	out := "      VMID NAME                 STATUS     MEM(MB)    BOOTDISK(GB) PID\n"
	assert.Empty(t, parseQmListVMIDs(out))
}

// --- scaleProbeReachable / scaleNodeStillMember -----------------------------

func TestScaleProbeReachable_TrueOnSuccess(t *testing.T) {
	deps := &cli.Deps{Runner: exec.Fake(exec.FakeResponse{}), Ctx: &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}}}
	assert.True(t, scaleProbeReachable(deps, "10.10.1.11"))
}

func TestScaleProbeReachable_FalseOnFailure(t *testing.T) {
	deps := &cli.Deps{Runner: exec.Fake(exec.FakeResponse{ExitCode: 255}), Ctx: &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}}}
	assert.False(t, scaleProbeReachable(deps, "10.10.1.11"))
}

func TestScaleNodeStillMember_FoundInOutput(t *testing.T) {
	deps := &cli.Deps{
		Runner: exec.Fake(exec.FakeResponse{Stdout: samplePvecmStatusQuorate3of3}),
		Ctx:    &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}},
	}
	found, err := scaleNodeStillMember(deps, "10.10.1.10", "10.10.1.11")
	require.NoError(t, err)
	assert.True(t, found)
}

func TestScaleNodeStillMember_NotFound(t *testing.T) {
	deps := &cli.Deps{
		Runner: exec.Fake(exec.FakeResponse{Stdout: samplePvecmStatusQuorate3of3}),
		Ctx:    &config.Context{SSH: config.SSHBlock{User: "root", Port: 22}},
	}
	found, err := scaleNodeStillMember(deps, "10.10.1.10", "10.99.99.99")
	require.NoError(t, err)
	assert.False(t, found)
}

// --- scaleRenameLegacyNodeZero (decision D3 preflight safety net) ---------

func TestScaleRenameLegacyNodeZero_RenamesLegacyVM(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	lab.Name = "wayne"
	f := testhelper.NewFakePVE(t)
	var renamed map[string]any
	f.HandleFunc("PUT /api2/json/nodes/node1/qemu/9200/config", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		renamed = map[string]any{"name": r.PostForm.Get("name")}
		testhelper.WriteData(w, nil)
	})
	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	deps := &cli.Deps{API: api}

	classified := []classifiedLabVM{
		{VM: labVM{VMID: 9200, Node: "node1", Name: "lab-wayne"}, Index: 0}, // legacy bare name, no index suffix
	}

	msg, err := scaleRenameLegacyNodeZero(context.Background(), deps, lab, classified)
	require.NoError(t, err)
	assert.Contains(t, msg, "lab-wayne-0")
	require.NotNil(t, renamed)
	assert.Equal(t, "lab-wayne-0", renamed["name"])
}

func TestScaleRenameLegacyNodeZero_NoOpWhenAlreadyCorrectlyNamed(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	lab.Name = "wayne"
	f := testhelper.NewFakePVE(t)
	createForbid(f, t, "PUT /api2/json/nodes/node1/qemu/9200/config")
	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	deps := &cli.Deps{API: api}

	classified := []classifiedLabVM{
		{VM: labVM{VMID: 9200, Node: "node1", Name: "lab-wayne-0"}, Index: 0}, // already correctly named
	}

	msg, err := scaleRenameLegacyNodeZero(context.Background(), deps, lab, classified)
	require.NoError(t, err)
	assert.Empty(t, msg)
}

func TestScaleRenameLegacyNodeZero_NoOpWhenNoNodeZero(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	lab.Name = "wayne"
	deps := &cli.Deps{}

	msg, err := scaleRenameLegacyNodeZero(context.Background(), deps, lab, nil)
	require.NoError(t, err)
	assert.Empty(t, msg)
}

func TestScaleRenameLegacyNodeZero_PeppiGuardRefuses(t *testing.T) {
	lab := scaleTestLab("dirty", 1, "")
	lab.Name = "dirty"
	lab.Network.VnetID = "peppivn0"
	deps := &cli.Deps{}

	classified := []classifiedLabVM{
		{VM: labVM{VMID: 9200, Node: "node1", Name: "lab-dirty"}, Index: 0},
	}

	_, err := scaleRenameLegacyNodeZero(context.Background(), deps, lab, classified)
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
}

// --- preflight quorate check (plan §9 step 1) -------------------------------

// TestScale_PreflightNotQuorate_RefusesBeforeAnyMutation covers the
// command-level surface of scaleCurrentMembership's quorate check: a
// currently-clustered-but-not-quorate lab must refuse before anything else
// happens (no legacy-rename mutation call, no capacity gate, no
// confirmation prompt, no qdevice-remove).
func TestScale_PreflightNotQuorate_RefusesBeforeAnyMutation(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 9201, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-1"},
	)
	createForbid(f, t, "GET /api2/json/nodes/node1/storage/tank/status")

	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())
	notQuorate := strings.Replace(scaleClusteredNoQdevice2of2, "Quorate:          Yes", "Quorate:          No", 1)
	fake := exec.Fake(exec.FakeResponse{Stdout: notQuorate})
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "wayne", "--nodes", "3", "--node", "node1", "--yes")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not quorate")
	require.Len(t, fake.Calls, 1, "only the preflight membership probe; no further mutation may follow")
}

// The quorate refusal must precede EVERY mutation, including the D3
// legacy-rename safety net: a non-quorate lab with a surviving bare-named
// node-0 VM refuses without issuing the rename API call.
func TestScale_PreflightNotQuorate_RefusesBeforeLegacyRename(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne"},
	)
	createForbid(f, t, "PUT /api2/json/nodes/node1/qemu/9200/config")

	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())
	notQuorate := strings.Replace(scaleClusteredNoQdevice2of2, "Quorate:          Yes", "Quorate:          No", 1)
	fake := exec.Fake(exec.FakeResponse{Stdout: notQuorate})
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "wayne", "--nodes", "3", "--node", "node1", "--yes")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not quorate")
	require.Len(t, fake.Calls, 1, "only the preflight membership probe")
}

// --- scaleDestroyQdeviceVM (M4-04) ------------------------------------------

func TestScaleDestroyQdeviceVM_DestroysWhenPresent(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	lab.Name = "wayne"
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 9299, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-q"},
	)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu/9299/status/current", map[string]any{"status": "running", "vmid": 9299})
	stopUPID := "UPID:node1:00000010:00000010:65000000:qmstop:9299:root@pam:"
	f.HandleFunc("POST /api2/json/nodes/node1/qemu/9299/status/stop", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, stopUPID)
	})
	destroyHandleTaskStatus(f, "node1", stopUPID)
	deleteUPID := "UPID:node1:00000011:00000011:65000000:qmdestroy:9299:root@pam:"
	f.HandleFunc("DELETE /api2/json/nodes/node1/qemu/9299", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, deleteUPID)
	})
	destroyHandleTaskStatus(f, "node1", deleteUPID)

	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	deps := &cli.Deps{API: api}

	rows, err := scaleDestroyQdeviceVM(context.Background(), deps, lab)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "destroy QDevice VM", rows[0][0])
	assert.Equal(t, "VM 9299 destroyed", rows[0][1])
}

func TestScaleDestroyQdeviceVM_AlreadyGone(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	lab.Name = "wayne"
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
	)
	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	deps := &cli.Deps{API: api}

	rows, err := scaleDestroyQdeviceVM(context.Background(), deps, lab)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "already gone", rows[0][1])
}

func TestScaleDestroyQdeviceVM_PeppiGuardRefuses(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	lab.Name = "wayne"
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f,
		map[string]any{"vmid": 50010, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-q"},
	)
	api, err := apiclient.NewAPIClient(f.Options)
	require.NoError(t, err)
	deps := &cli.Deps{API: api}

	_, err = scaleDestroyQdeviceVM(context.Background(), deps, lab)
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
}

// --- command-level validation ------------------------------------------------

func TestScale_RequiresNodesFlag(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newScaleCmd())

	_, err := runGuestCmd(t, cmd, "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "--nodes is required")
	assert.Empty(t, fake.Calls)
}

func TestScale_NodesOutOfRange(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newScaleCmd())

	_, err := runGuestCmd(t, cmd, "wayne", "--nodes", "6")
	require.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
	assert.Empty(t, fake.Calls)
}

func TestScale_InvalidTargetTopology_QdeviceNeverAtTwoNodes(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newScaleCmd())

	_, err := runGuestCmd(t, cmd, "wayne", "--nodes", "2", "--qdevice", "never")
	require.Error(t, err)
	assert.ErrorContains(t, err, "target topology is invalid")
	assert.Empty(t, fake.Calls)
}

func TestScale_LabNotCreatedYet_Errors(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f) // no VMs at all
	cmd, fake := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())

	_, err := runGuestCmd(t, cmd, "wayne", "--nodes", "3")
	require.Error(t, err)
	assert.ErrorContains(t, err, "no node 0 VM yet")
	assert.Empty(t, fake.Calls)
}

func TestScale_NonContiguousNodes_Errors(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 9202, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-2"},
	)
	cmd, fake := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())

	_, err := runGuestCmd(t, cmd, "wayne", "--nodes", "3")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not contiguous")
	assert.Empty(t, fake.Calls)
}

// TestScale_NoOp_AlreadyAtTarget covers M4-02(c): a genuine no-op fires only
// when LIVE MEMBERSHIP matches the target, not merely VM-shell existence —
// the no-op check here reads the (1-call) membership probe's result.
func TestScale_NoOp_AlreadyAtTarget(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 9201, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-1"},
		map[string]any{"vmid": 9202, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-2"},
	)
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())
	fake := exec.Fake(exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3}) // membership probe: already 3/3, no qdevice
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "wayne", "--nodes", "3")
	require.NoError(t, err)
	assert.Contains(t, out, "already at the target topology")
	require.Len(t, fake.Calls, 1, "the preflight membership probe, and nothing else")
}

func TestScale_DryRun_NoRunnerCalls(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
	)
	cmd, fake := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())

	out, err := runGuestCmd(t, cmd, "wayne", "--nodes", "3", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "create VM shell for node 1")
	assert.Contains(t, out, "join node 1")
	assert.Contains(t, out, "create VM shell for node 2")
	assert.Contains(t, out, "join node 2")
	assert.Contains(t, out, "reconcile inner sdn zone")
	assert.Contains(t, out, "reconcile NFS storage")
	assert.Empty(t, fake.Calls, "dry-run must never invoke the runner")
}

func TestScale_DryRun_ShowsQdeviceParitySequencing(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 9201, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-1"},
		map[string]any{"vmid": 9299, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-q"},
	)
	cmd, fake := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())

	out, err := runGuestCmd(t, cmd, "wayne", "--nodes", "3", "--dry-run")
	require.NoError(t, err)

	removeIdx := strings.Index(out, "remove QDevice")
	joinIdx := strings.Index(out, "join node 2")
	require.NotEqual(t, -1, removeIdx, "preview must mention removing the QDevice")
	require.NotEqual(t, -1, joinIdx, "preview must mention joining the new node")
	assert.Less(t, removeIdx, joinIdx, "QDevice removal must be sequenced before the join in the preview")
	assert.Empty(t, fake.Calls)
}

func TestScale_PeppiGuardRefusesBeforeAnyCall(t *testing.T) {
	lab := scaleTestLab("dirty", 1, "")
	lab.Network.VnetID = "peppivn0"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"dirty": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newScaleCmd())

	_, err := runGuestCmd(t, cmd, "dirty", "--nodes", "3")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.Empty(t, fake.Calls)
}

func TestScale_RefusesWithoutYesNonInteractively(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
	)
	f.HandleJSON("GET /api2/json/nodes/node1/storage/tank/status",
		map[string]any{"total": 10000000000000, "used": 100000000000})
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())
	fake := exec.Fake(exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1}) // preflight membership probe
	cli.GetDeps(cmd).Runner = fake
	cmd.SetIn(strings.NewReader(""))

	out, err := runGuestCmd(t, cmd, "wayne", "--nodes", "3", "--node", "node1")
	require.NoError(t, err)
	assert.Contains(t, out, "Aborted")
	require.Len(t, fake.Calls, 1, "must refuse to run without confirmation, after the preflight membership probe")
}

// TestScale_CapacityGateRefusesBeforeQdeviceRemove covers M4-03: a capacity
// refusal must happen before ANY mutation — in particular before `pvecm
// qdevice remove`, which would otherwise strand a 2+Q cluster witness-less.
func TestScale_CapacityGateRefusesBeforeQdeviceRemove(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 9201, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-1"},
		map[string]any{"vmid": 9299, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-q"},
	)
	// A realistic fleet-shaped zfspool storage nested under the base pool
	// (field finding F4, ignored by the capacity gate's resolver), and a
	// tiny real pool read via disks/zfs: reserved refquota (50G, cleanLab's
	// fixture) alone blows past the 85% refuse threshold against a ~1G pool.
	f.HandleJSON("GET /api2/json/storage", []any{map[string]any{
		"storage": "tank-lab-wayne", "type": "zfspool", "pool": "tank/labs/wayne",
	}})
	createHandleDisksZfs(f, "node1", "tank", 1000000000, 900000000)

	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())
	fake := exec.Fake(exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice}) // preflight membership probe: 2+Q
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "wayne", "--nodes", "3", "--node", "node1", "--yes")
	require.Error(t, err)
	assert.ErrorContains(t, err, "capacity gate")
	require.Len(t, fake.Calls, 1,
		"only the preflight membership probe; zero ssh mutations, in particular no pvecm qdevice remove")
}

// --- full grow + QDevice-remove-first integration (2+Q -> 3) ---------------

// scaleClusteredNoQdevice2of2, scaleClusteredNoQdevice3of3, and
// scaleClusteredNoQdevice1of1 are pvecm status fixtures for a lab already
// clustered as "wayne" post-QDevice-removal, at 2/2, 3/3, and 1/1 votes
// respectively (no QDevice registered) — each carries "Nodes:" so
// scaleCurrentMembership's live-membership-derived node count (M4-02) is
// exercised against a real-shaped fixture, not just the vote totals.
const scaleClusteredNoQdevice2of2 = `Cluster information
-------------------
Name:             wayne

Quorum information
------------------
Nodes:            2
Quorate:          Yes

Votequorum information
----------------------
Expected votes:   2
Highest expected: 2
Total votes:      2
Quorum:           2
Flags:            Quorate
`

const scaleClusteredNoQdevice3of3 = `Cluster information
-------------------
Name:             wayne

Quorum information
------------------
Nodes:            3
Quorate:          Yes

Votequorum information
----------------------
Expected votes:   3
Highest expected: 3
Total votes:      3
Quorum:           2
Flags:            Quorate
`

const scaleClusteredNoQdevice1of1 = `Cluster information
-------------------
Name:             wayne

Quorum information
------------------
Nodes:            1
Quorate:          Yes

Votequorum information
----------------------
Expected votes:   1
Highest expected: 1
Total votes:      1
Quorum:           1
Flags:            Quorate
`

const sampleCorosyncCfgtoolSingleNode = `Local node ID 1, transport knet
LINK ID 0
	addr	= 10.10.1.10
	status:
		nodeid  1:	localhost
`

// scaleGrowFixture registers the buildCreatePlan resource-discovery routes
// (zone/vnet/subnet/storage/pool all already exist) shared by every grow
// integration test in this file, plus the capacity-gate storage-status
// route. existingMembers/existingQemus describe the pool/node-qemu-list
// members already present (so buildCreatePlan skips creating them).
func scaleGrowFixture(f *testhelper.FakePVE, lab *config.Lab, existingMembers []map[string]any, existingQemus []map[string]any) {
	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labs"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "labwayne"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/labwayne/subnets",
		[]any{map[string]any{"subnet": "labwayne-10.10.1.0-24", "cidr": lab.Network.CIDR}})
	// A realistic fleet-shaped zfspool storage: nested under the base pool
	// ("tank/labs/wayne"), not rooted at it (field finding F4: real hosts
	// register only per-lab storages). This backs the storage-provisioning
	// step's own existence check; the capacity gate never matches a nested
	// entry (createResolveCapacityDenominator) and falls through to the
	// disks/zfs mock below for the pool's real, refquota-independent size.
	f.HandleJSON("GET /api2/json/storage", []any{map[string]any{
		"storage": "tank-lab-wayne", "type": "zfspool", "pool": "tank/labs/wayne",
	}})
	f.HandleJSON("GET /api2/json/pools", []any{map[string]any{"poolid": "lab-wayne"}})
	members := make([]any, len(existingMembers))
	for i, m := range existingMembers {
		members[i] = m
	}
	f.HandleJSON("GET /api2/json/pools/lab-wayne", map[string]any{"members": members})
	qemus := make([]any, len(existingQemus))
	for i, q := range existingQemus {
		qemus[i] = q
	}
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", qemus)
	createHandleDisksZfs(f, "node1", "tank", 10000000000000, 100000000000)
}

func TestScale_Grow_RemovesStaleQdeviceBeforeJoin_2PlusQTo3(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)

	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 9201, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-1"},
		map[string]any{"vmid": 9299, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-q"},
	)
	scaleGrowFixture(f, lab,
		[]map[string]any{
			{"id": "qemu/9200", "node": "node1", "type": "qemu", "vmid": 9200, "name": "lab-wayne-0"},
			{"id": "qemu/9201", "node": "node1", "type": "qemu", "vmid": 9201, "name": "lab-wayne-1"},
		},
		[]map[string]any{
			{"vmid": 9200, "name": "lab-wayne-0"},
			{"vmid": 9201, "name": "lab-wayne-1"},
		},
	)
	f.HandleJSON("GET /api2/json/cluster/nextid", "9202")
	f.HandleJSON("POST /api2/json/nodes/node1/qemu", createTestUPID)
	createHandleTaskStatus(f)

	// QDevice VM destroy (M4-04), after the corosync-level removal.
	f.HandleJSON("GET /api2/json/nodes/node1/qemu/9299/status/current", map[string]any{"status": "stopped", "vmid": 9299})
	qdeviceDeleteUPID := "UPID:node1:00000099:00000099:65000000:qmdestroy:9299:root@pam:"
	f.HandleFunc("DELETE /api2/json/nodes/node1/qemu/9299", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, qdeviceDeleteUPID)
	})
	destroyHandleTaskStatus(f, "node1", qdeviceDeleteUPID)

	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())

	fake := exec.Fake(
		// 0: preflight membership probe (2+Q).
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice},
		// 1-2: qdevice remove: probe (has qdevice), then pvecm qdevice remove.
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice},
		exec.FakeResponse{},
		// 3: buildCreatePlan's zfs dataset existence probe (grow's node VM
		// shell(s) step) — dataset already exists.
		exec.FakeResponse{},
		// 4: ensure node 0 clustered: already clustered as wayne, 2/2, no qdevice.
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2},
		// 5-16: join node 2: reachable probe, not-yet-clustered probe,
		// guest-free (qm list, pct list), trust seed (ensure keypair, read
		// pubkey, append to node 0's authorized_keys, apiver preflight),
		// pvecm add, join verification probe, poll (pvecm status,
		// corosync-cfgtool).
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1},
		exec.FakeResponse{Stdout: sampleQmListEmpty},
		exec.FakeResponse{Stdout: samplePctListEmpty},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: sampleRootPubKey + "\n"},
		exec.FakeResponse{},
		exec.FakeResponse{},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		// 17-19: reconcile sdn: probe (missing), create, commit.
		exec.FakeResponse{ExitCode: 1},
		exec.FakeResponse{},
		exec.FakeResponse{},
		// 20-25: reconcile nfs: 3x (probe missing, add).
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		// 26-34: final validation, every target node (0, 1, 2): pvecm
		// status, corosync-cfgtool -s, pvesm status.
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "wayne", "--nodes", "3", "--node", "node1", "--yes")
	require.NoError(t, err)
	assert.Contains(t, out, "scale requested")
	assert.Contains(t, out, "VM 9299 destroyed", "the orphaned QDevice VM must be destroyed (M4-04)")
	assert.Contains(t, out, "PDM remote", "a node-count change must surface the PDM reminder row")

	require.Len(t, fake.Calls, 35)
	assert.Contains(t, fake.Calls[2].Args, "pvecm qdevice remove")
	assert.Contains(t, fake.Calls[13].Args, "pvecm add 10.10.1.10 --link0 10.10.1.12 --use_ssh")
}

// TestScale_Grow_ReRunJoinsAlreadyExistingShells covers M4-02(a): live
// membership reports only 2 nodes joined, while a VM shell for node 2
// already exists (created by a prior, deferred scale run). This run must
// still join node 2 — treating its existing shell as "nothing to do" would
// be the M4-02 defect.
func TestScale_Grow_ReRunJoinsAlreadyExistingShells(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)

	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 9201, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-1"},
		map[string]any{"vmid": 9202, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-2"},
	)
	// All three VM shells already exist -> buildCreatePlan must skip
	// creating any of them (nextid/qemu-create forbidden).
	scaleGrowFixture(f, lab,
		[]map[string]any{
			{"id": "qemu/9200", "node": "node1", "type": "qemu", "vmid": 9200, "name": "lab-wayne-0"},
			{"id": "qemu/9201", "node": "node1", "type": "qemu", "vmid": 9201, "name": "lab-wayne-1"},
			{"id": "qemu/9202", "node": "node1", "type": "qemu", "vmid": 9202, "name": "lab-wayne-2"},
		},
		[]map[string]any{
			{"vmid": 9200, "name": "lab-wayne-0"},
			{"vmid": 9201, "name": "lab-wayne-1"},
			{"vmid": 9202, "name": "lab-wayne-2"},
		},
	)
	createForbid(f, t, "GET /api2/json/cluster/nextid")
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu")

	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())

	fake := exec.Fake(
		// 0: preflight membership probe: LIVE membership is only 2/2 —
		// node 2's shell exists but was never joined.
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2},
		// 1: buildCreatePlan's zfs dataset existence probe (grow's node VM
		// shell(s) step) — dataset already exists.
		exec.FakeResponse{},
		// 2: ensure node 0 clustered: already clustered (idempotent skip).
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2},
		// 3-14: join node 2: reachable probe, not-yet-clustered probe,
		// guest-free (qm list, pct list), trust seed (ensure keypair, read
		// pubkey, append to node 0's authorized_keys, apiver preflight),
		// pvecm add, join verification probe, poll (pvecm status,
		// corosync-cfgtool).
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1},
		exec.FakeResponse{Stdout: sampleQmListEmpty},
		exec.FakeResponse{Stdout: samplePctListEmpty},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: sampleRootPubKey + "\n"},
		exec.FakeResponse{},
		exec.FakeResponse{},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		// 15-17: sdn.
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{}, exec.FakeResponse{},
		// 18-23: nfs.
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		// 24-32: final validation, 3 nodes.
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "wayne", "--nodes", "3", "--node", "node1", "--yes")
	require.NoError(t, err)
	assert.Contains(t, out, "node 2 (10.10.1.12) joined cluster")

	require.Len(t, fake.Calls, 33)
	assert.Contains(t, fake.Calls[11].Args, "pvecm add 10.10.1.10 --link0 10.10.1.12 --use_ssh")
}

// TestScale_QdeviceAdd_ReRunWiresAlreadyExistingShell covers M4-02(b): the
// QDevice VM shell already exists (created by a prior deferred run) but
// corosync never registered it — a re-run must still wire it up.
func TestScale_QdeviceAdd_ReRunWiresAlreadyExistingShell(t *testing.T) {
	lab := scaleTestLab("wayne", 2, "auto")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)

	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 9201, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-1"},
		map[string]any{"vmid": 9299, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-q"},
	)
	scaleGrowFixture(f, lab,
		[]map[string]any{
			{"id": "qemu/9200", "node": "node1", "type": "qemu", "vmid": 9200, "name": "lab-wayne-0"},
			{"id": "qemu/9201", "node": "node1", "type": "qemu", "vmid": 9201, "name": "lab-wayne-1"},
			{"id": "qemu/9299", "node": "node1", "type": "qemu", "vmid": 9299, "name": "lab-wayne-q"},
		},
		[]map[string]any{
			{"vmid": 9200, "name": "lab-wayne-0"},
			{"vmid": 9201, "name": "lab-wayne-1"},
			{"vmid": 9299, "name": "lab-wayne-q"},
		},
	)
	createForbid(f, t, "GET /api2/json/cluster/nextid")
	createForbid(f, t, "POST /api2/json/nodes/node1/qemu")

	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())

	fake := exec.Fake(
		// 0: preflight membership probe: clustered 2/2, corosync shows NO
		// registered qdevice (deferred on the previous run).
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2},
		// 1: buildCreatePlan's zfs dataset existence probe (QDevice VM
		// shell step) — dataset already exists.
		exec.FakeResponse{},
		// 2: qdevice reachable probe.
		exec.FakeResponse{},
		// 3: qdeviceAdd's own cluster-state probe.
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2},
		// 4-6: package probes (already installed on the shell + both nodes).
		exec.FakeResponse{},
		exec.FakeResponse{},
		exec.FakeResponse{},
		// 7: pvecm qdevice setup.
		exec.FakeResponse{},
		// 8-10: sdn.
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{}, exec.FakeResponse{},
		// 11-16: nfs.
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		// 17-22: final validation, 2 nodes.
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "wayne", "--nodes", "2", "--node", "node1", "--yes")
	require.NoError(t, err)
	assert.Contains(t, out, "pvecm qdevice setup")

	require.Len(t, fake.Calls, 23)
	assert.Contains(t, fake.Calls[7].Args, "pvecm qdevice setup 10.10.1.15")
}

// --- shrink integration (2 -> 1) --------------------------------------------

// TestScale_Shrink_EvacuatesDelnodesAndDestroys_2To1 uses a deliberately
// simplified live-state fixture (2 nodes, no QDevice) that config.
// ValidateTopology would reject as a TARGET (a bare 2-node cluster needs a
// QDevice) — this test isolates shrink-path mechanics (evacuate/delnode/
// destroy/per-VMID peppi guard) from QDevice-parity sequencing, which the
// 2+Q->3 grow test above already covers.
func TestScale_Shrink_EvacuatesDelnodesAndDestroys_2To1(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)

	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 9201, "node": "node1", "pool": "lab-wayne", "status": "stopped", "type": "qemu", "name": "lab-wayne-1"},
	)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu/9201/status/current", map[string]any{"status": "stopped", "vmid": 9201})
	deleteUPID := "UPID:node1:00000003:00000003:65000000:qmdestroy:9201:root@pam:"
	f.HandleFunc("DELETE /api2/json/nodes/node1/qemu/9201", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, deleteUPID)
	})
	destroyHandleTaskStatus(f, "node1", deleteUPID)

	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())

	fake := exec.Fake(
		// 0: preflight membership probe.
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2},
		// 1-2: shrink node 1: evacuate (qm list: empty), delnode.
		exec.FakeResponse{Stdout: sampleQmListEmpty},
		exec.FakeResponse{},
		// 3-8: reconcile nfs (target is now 1 node, so no sdn reconcile).
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		// 9-11: final validation, node 0 alone remains a quorate 1/1 cluster.
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice1of1},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolSingleNode},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "wayne", "--nodes", "1", "--yes")
	require.NoError(t, err)
	assert.Contains(t, out, "scale requested")
	assert.Contains(t, out, "VM 9201 destroyed")

	require.Len(t, fake.Calls, 12)
	assert.Contains(t, fake.Calls[1].Args, "qm list")
	assert.Contains(t, fake.Calls[1].Args, "root@10.10.1.11")
	assert.Contains(t, fake.Calls[2].Args, "pvecm delnode lab-wayne-1")
	assert.Contains(t, fake.Calls[2].Args, "root@10.10.1.10")
}

// TestScale_Shrink_EvacuatesGuestsToNodeZero covers M4-01: a guest actually
// running on the departing node must be migrated to NODE 0 — not to the
// departing node itself (the pre-fix bug: migrating a guest to the node it
// already runs on fails in real PVE, aborting every real shrink of a
// guest-hosting node).
func TestScale_Shrink_EvacuatesGuestsToNodeZero(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)

	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 9201, "node": "node1", "pool": "lab-wayne", "status": "stopped", "type": "qemu", "name": "lab-wayne-1"},
	)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu/9201/status/current", map[string]any{"status": "stopped", "vmid": 9201})
	deleteUPID := "UPID:node1:00000004:00000004:65000000:qmdestroy:9201:root@pam:"
	f.HandleFunc("DELETE /api2/json/nodes/node1/qemu/9201", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, deleteUPID)
	})
	destroyHandleTaskStatus(f, "node1", deleteUPID)

	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())

	fake := exec.Fake(
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2}, // preflight membership probe
		exec.FakeResponse{Stdout: sampleQmListNonEmpty},        // evacuate: qm list, one guest (vmid 100)
		exec.FakeResponse{},                                 // qm migrate 100 -> node 0
		exec.FakeResponse{},                                 // delnode
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{}, // nfs nfs-images
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{}, // nfs nfs-backup
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{}, // nfs shared-iso
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice1of1},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolSingleNode},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
	)
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "wayne", "--nodes", "1", "--yes")
	require.NoError(t, err)

	require.Len(t, fake.Calls, 13)
	migrateCall := fake.Calls[2]
	assert.Contains(t, migrateCall.Args, "qm migrate 100 lab-wayne-0 --online --with-local-disks",
		"the migrate target must be node 0 (lab-wayne-0), never the departing node itself")
	assert.Contains(t, migrateCall.Args, "root@10.10.1.11", "migrate must be issued FROM the departing node")
}

func TestScale_Shrink_PerVMIDPeppiGuardRefuses(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)

	// The departing node's live VMID (50010) matches a protected peppi
	// VMID; the guard must refuse before any stop/delete call.
	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 50010, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-1"},
	)

	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())
	fake := exec.Fake(
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2}, // preflight membership probe
		exec.FakeResponse{Stdout: sampleQmListEmpty},           // qm list on node 1: no guests
		exec.FakeResponse{}, // pvecm delnode
	)
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "wayne", "--nodes", "1", "--yes")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	require.Len(t, fake.Calls, 3, "guard must refuse after delnode but before any stop/delete API call")
}

// --- scaleEvacuateAndRemoveNode: delnode idempotency ------------------------

func TestScaleEvacuateAndRemoveNode_DelnodeIdempotentWhenAlreadyGone(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	lab.Name = "wayne" // called directly below, bypassing config.ResolveLabs' map-key defaulting.
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
	)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())
	deps := cli.GetDeps(cmd)

	fake := exec.Fake(
		exec.FakeResponse{Stdout: sampleQmListEmpty},           // qm list: no guests
		exec.FakeResponse{ExitCode: 1},                         // delnode fails ("could not kill node")
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice1of1}, // re-probe: node 1's IP absent -> already gone
	)
	deps.Runner = fake

	rows, err := scaleEvacuateAndRemoveNode(context.Background(), deps, lab, 1, "10.10.1.10")
	require.NoError(t, err)

	found := false
	for _, r := range rows {
		if strings.Contains(r[0], "delnode") {
			found = true
			assert.Equal(t, "removed from cluster membership", r[1])
		}
		if strings.Contains(r[0], "destroy") {
			assert.Equal(t, "already gone", r[1])
		}
	}
	assert.True(t, found, "expected a delnode row")
	require.Len(t, fake.Calls, 3)
}

// --- M4-05: exit code reflects convergence, not just "steps ran" -----------

// TestScale_NonDeferredNonQuorateFinalState_ReturnsError covers M4-05: a
// transition that completed (not deferred) but ends non-quorate must
// return a non-zero error, while still rendering the full report.
func TestScale_NonDeferredNonQuorateFinalState_ReturnsError(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)

	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 9201, "node": "node1", "pool": "lab-wayne", "status": "stopped", "type": "qemu", "name": "lab-wayne-1"},
	)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu/9201/status/current", map[string]any{"status": "stopped", "vmid": 9201})
	deleteUPID := "UPID:node1:00000005:00000005:65000000:qmdestroy:9201:root@pam:"
	f.HandleFunc("DELETE /api2/json/nodes/node1/qemu/9201", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, deleteUPID)
	})
	destroyHandleTaskStatus(f, "node1", deleteUPID)

	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())

	notQuorate1of1 := strings.Replace(scaleClusteredNoQdevice1of1, "Quorate:          Yes", "Quorate:          No", 1)
	fake := exec.Fake(
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2}, // preflight membership probe
		exec.FakeResponse{Stdout: sampleQmListEmpty},           // evacuate
		exec.FakeResponse{},                                 // delnode
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{}, // nfs
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{Stdout: notQuorate1of1}, // final validation: NOT quorate
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolSingleNode},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "wayne", "--nodes", "1", "--yes")
	require.Error(t, err)
	assert.ErrorContains(t, err, "non-converged state")
	// The full report must still render despite the error.
	assert.Contains(t, out, "final validation node 0")
	assert.Contains(t, out, "VM 9201 destroyed")
}

// TestScale_DeferredGrow_ExitsZeroDespiteIncompleteFinalValidation covers
// the other half of M4-05: a run that legitimately DEFERRED (waiting on
// manual OS provisioning of a new node) must exit 0 even though final
// validation finds unreachable/incomplete nodes — that is expected
// partial progress, not a failure.
func TestScale_DeferredGrow_ExitsZeroDespiteIncompleteFinalValidation(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)

	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
	)
	scaleGrowFixture(f, lab,
		[]map[string]any{{"id": "qemu/9200", "node": "node1", "type": "qemu", "vmid": 9200, "name": "lab-wayne-0"}},
		[]map[string]any{{"vmid": 9200, "name": "lab-wayne-0"}},
	)
	f.HandleJSON("GET /api2/json/cluster/nextid", "9201")
	f.HandleJSON("POST /api2/json/nodes/node1/qemu", createTestUPID)
	createHandleTaskStatus(f)

	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())

	fake := exec.Fake(
		// 0: preflight membership probe: fresh, not clustered.
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1},
		// 1-3: cluster init: probe, create, verify.
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice1of1},
		// 4: node 1 reachability probe FAILS -> deferred, loop breaks
		// before node 2 is even probed.
		exec.FakeResponse{ExitCode: 255},
		// 5-7: sdn (target is 3 nodes, so this still reconciles).
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{}, exec.FakeResponse{},
		// 8-13: nfs.
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		// 14-16: final validation node 0 (healthy, self-consistent at 1/1).
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice1of1},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolSingleNode},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
		// 17: final validation node 1: unreachable.
		exec.FakeResponse{ExitCode: 255},
		// 18: final validation node 2: unreachable (its VM shell was
		// created, but it was never even probed for join reachability).
		exec.FakeResponse{ExitCode: 255},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "wayne", "--nodes", "3", "--node", "node1", "--yes")
	require.NoError(t, err, "a deferred (not-yet-complete) transition must exit 0")
	assert.Contains(t, out, "deferred:")
	assert.Contains(t, out, "final validation node 1")
	assert.Contains(t, out, "not reachable")
}

// TestScale_Grow_RefreshesContextFingerprintAfterClusterInit covers the fix
// for executeScalePlan's grow branch, which calls ensureClusterInit (the
// same `pvecm create` cert reissue `cluster init` performs) but, before this
// fix, never called refreshLabContextAfterClusterInit afterward — so a
// pinned lab-<name> context's TLS fingerprint went stale the moment a scale
// actually formed the cluster, and every later `pmx -c lab-<name>` command
// failed TLS silently. This is a near-duplicate of
// TestScale_DeferredGrow_ExitsZeroDespiteIncompleteFinalValidation's fixture
// (fresh 1-node lab growing to 3, deferred at node 1's ssh reachability),
// with a seeded lab-wayne context added and the refresh's own ssh calls
// (ensure-user, ensure-ACL, fingerprint, hostname — same shape as
// cluster_test.go's TestClusterInit_RefreshesExistingContextFingerprint)
// inserted right after cluster init's create+verify.
//
// Non-vacuousness: if the fix (the refreshLabContextAfterClusterInit call in
// scale.go's grow branch) is reverted, refreshLabContextAfterClusterInit is
// never invoked, so deps.Cfg.Contexts["lab-wayne"].TLS.Fingerprint stays at
// its seeded zero value ("") and the fingerprint assertion below fails
// regardless of how the now-misaligned fake ssh call queue plays out
// downstream (the 4 queued refresh responses would instead be misread as
// the node-1 reachability probe and whatever follows it, very likely
// failing require.NoError or require.Len first).
func TestScale_Grow_RefreshesContextFingerprintAfterClusterInit(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	cfg := &config.Config{
		Labs: map[string]*config.Lab{"wayne": lab},
		Contexts: map[string]*config.Context{
			"lab-wayne": {
				Host:     "10.10.1.10",
				Port:     8006,
				Protocol: "https",
				Product:  config.ProductPVE,
				Auth: config.AuthBlock{
					Type:     "token",
					Username: "pmx@pve",
					TokenID:  "pmx",
					Secret:   "keychain:pmx-lab-wayne/pmx@pve!pmx",
				},
			},
		},
	}
	path := writeConfig(t, cfg)
	f := testhelper.NewFakePVE(t)

	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
	)
	scaleGrowFixture(f, lab,
		[]map[string]any{{"id": "qemu/9200", "node": "node1", "type": "qemu", "vmid": 9200, "name": "lab-wayne-0"}},
		[]map[string]any{{"vmid": 9200, "name": "lab-wayne-0"}},
	)
	f.HandleJSON("GET /api2/json/cluster/nextid", "9201")
	f.HandleJSON("POST /api2/json/nodes/node1/qemu", createTestUPID)
	createHandleTaskStatus(f)

	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())
	deps := cli.GetDeps(cmd)

	// Reuse-secret path: the probe stub returns nil (valid), so refresh never
	// mints a new token over ssh, matching cluster_test.go's own
	// TestClusterInit_RefreshesExistingContextFingerprint fixture.
	origProbe := labProbeContextVersion
	labProbeContextVersion = func(*cobra.Command, *cli.Deps, string) error { return nil }
	t.Cleanup(func() { labProbeContextVersion = origProbe })

	fp := "11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00"
	fake := exec.Fake(
		// 0: preflight membership probe: fresh, not clustered.
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1},
		// 1-3: cluster init: probe, create, verify.
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice1of1},
		// 4-7: context refresh: ensure user, ensure ACL, fingerprint, hostname.
		exec.FakeResponse{}, exec.FakeResponse{},
		exec.FakeResponse{Stdout: "sha256 Fingerprint=" + fp + "\n"},
		exec.FakeResponse{Stdout: "lab-wayne-0\n"},
		// 8: node 1 reachability probe FAILS -> deferred, loop breaks before
		// node 2 is even probed.
		exec.FakeResponse{ExitCode: 255},
		// 9-11: sdn (target is 3 nodes, so this still reconciles).
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{}, exec.FakeResponse{},
		// 12-17: nfs.
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		// 18-20: final validation node 0 (healthy, self-consistent at 1/1).
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice1of1},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolSingleNode},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
		// 21: final validation node 1: unreachable.
		exec.FakeResponse{ExitCode: 255},
		// 22: final validation node 2: unreachable (its VM shell was
		// created, but it was never even probed for join reachability).
		exec.FakeResponse{ExitCode: 255},
	)
	deps.Runner = fake

	out, err := runGuestCmd(t, cmd, "wayne", "--nodes", "3", "--node", "node1", "--yes")
	require.NoError(t, err, "a deferred (not-yet-complete) transition must exit 0")
	assert.Contains(t, out, "deferred:")
	assert.Contains(t, out, "context lab-wayne refreshed")

	require.Len(t, fake.Calls, 23)
	assert.Equal(t, fp, deps.Cfg.Contexts["lab-wayne"].TLS.Fingerprint,
		"the grow path's ensureClusterInit reissues node 0's API cert; the pinned lab-wayne context's "+
			"fingerprint must be refreshed afterward, exactly like `pmx lab cluster init` already does")
}

// --- Multi-AZ topology plan P1-T6: scale's grow path passes HostNICs/Vnets
// through unmodified ------------------------------------------------------

// scaleMultiNICLab returns createMultiNICLab's "pve-cpi"-shaped fixture (2
// extra network.vnets[] entries — a storage vnet with a subnet, a workload
// vnet without one — and 5 network.host_nics[] entries spanning both,
// multi-AZ topology plan §1) named "wayne" so this file's existing
// "wayne"-clustered pvecm-status fixtures (samplePvecmStatusWithQdevice,
// scaleClusteredNoQdevice*) still apply unmodified — the multi-NIC network
// shape and the cluster-name fixtures are independent axes, no new pvecm
// output needs inventing for this test. topology.nodes is forced to nodes;
// qdevice is left unset (the caller's exec.Fake sequence drives whichever
// qdevice state is needed).
func scaleMultiNICLab(nodes int) *config.Lab {
	lab := createMultiNICLab("wayne")
	lab.Topology = config.LabTopology{Nodes: nodes}
	return lab
}

// scaleGrowMultiNICFixture registers the buildCreatePlan resource-discovery
// routes for a scaleMultiNICLab-shaped grow: the primary vnet ("pvecpi")
// already exists (so its zone/vnet/subnet steps skip, isolating this test to
// the grow-specific delta), while both extra network.vnets[] entries
// ("pvecpist", "pvecpiwk") do NOT exist yet, so buildCreatePlan's own extra-
// vnet steps actually create them — this is what proves the "and SDN vnet
// steps" half of P1-T6's acceptance, not just the per-node NIC map half.
// existingMembers/existingQemus describe the pool/node-qemu-list members
// already present, so buildCreatePlan skips creating their VM shells.
func scaleGrowMultiNICFixture(f *testhelper.FakePVE, poolID string, existingMembers, existingQemus []map[string]any) {
	f.HandleJSON("GET /api2/json/cluster/sdn/zones", []any{map[string]any{"zone": "labs"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets", []any{map[string]any{"vnet": "pvecpi"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/pvecpi/subnets",
		[]any{map[string]any{"subnet": "pvecpi-10.254.0.0-16", "cidr": "10.254.0.0/16"}})
	f.HandleJSON("GET /api2/json/cluster/sdn/vnets/pvecpist/subnets", []any{})
	f.HandleJSON("GET /api2/json/storage", []any{map[string]any{
		"storage": "tank-" + poolID, "type": "zfspool", "pool": "tank/labs/wayne",
	}})
	f.HandleJSON("GET /api2/json/pools", []any{map[string]any{"poolid": poolID}})
	members := make([]any, len(existingMembers))
	for i, m := range existingMembers {
		members[i] = m
	}
	f.HandleJSON("GET /api2/json/pools/"+poolID, map[string]any{"members": members})
	qemus := make([]any, len(existingQemus))
	for i, q := range existingQemus {
		qemus[i] = q
	}
	f.HandleJSON("GET /api2/json/nodes/node1/qemu", qemus)
	createHandleDisksZfs(f, "node1", "tank", 10*1024*1024*1024*1024, 100*1024*1024*1024)

	// Every already-existing target (node 0, node 1) gets its own
	// NIC-reconciliation createStep once network.host_nics is non-empty
	// (create.go's planCreateTarget, multi-AZ topology plan §2) — both
	// report a live config that already matches every configured entry
	// exactly, so no PUT is ever issued for them; this isolates node 2 (the
	// only genuinely new target) as the sole source of a qemu-create/
	// UpdateQemuConfig call in this test.
	convergedNet := map[string]any{
		"net0": "virtio=BC:24:11:00:00:00,bridge=pvecpi,mtu=1450",
		"net1": "virtio=BC:24:11:00:00:01,bridge=pvecpi,mtu=1450",
		"net2": "virtio=BC:24:11:00:00:02,bridge=pvecpist,mtu=1450",
		"net3": "virtio=BC:24:11:00:00:03,bridge=pvecpist,mtu=1450",
		"net4": "virtio=BC:24:11:00:00:04,bridge=pvecpiwk,mtu=1450",
		"net5": "virtio=BC:24:11:00:00:05,bridge=pvecpiwk,mtu=1450",
	}
	f.HandleJSON("GET /api2/json/nodes/node1/qemu/9500/config", convergedNet)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu/9501/config", convergedNet)
}

// TestScaleGrow_MultiNIC covers P1-T6: `pmx lab scale`'s grow path
// (executeScalePlan's growIndices branch, scale.go unmodified per P1-T5's
// own finding) reuses create.go's buildCreatePlan as-is, so a lab
// configuring network.vnets[]/network.host_nics[] (the multi-AZ topology
// plan §1 shape) growing a NEW node gets the exact same full multi-NIC Net
// map create.go's own TestCreateFreshLab_MultiNIC already proves for `pmx
// lab create`, plus the same extra-SDN-vnet-ensure steps — with ZERO
// scale.go code touched to make it so (scaleCopyLabWithTopology is a plain
// struct copy; Network.Vnets/HostNICs ride along for free).
//
// Scenario: lab "wayne" is currently a 2-node + QDevice cluster (mandatory
// at 2 nodes, §3.1) growing to 3 nodes — QDevice removal (unrelated to this
// test's HostNICs focus) exercises the SAME qdevice-parity-first sequencing
// TestScale_Grow_RemovesStaleQdeviceBeforeJoin_2PlusQTo3 already covers, so
// this test's own fake exec sequence is a direct copy of that one; the only
// new thing under test is node 2's create-time Net map and the extra vnet
// steps, both asserted from the recorded API request bodies.
func TestScaleGrow_MultiNIC(t *testing.T) {
	lab := scaleMultiNICLab(2)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)
	poolID := lab.Access.Pool // "lab-wayne"

	handleClusterResources(f,
		map[string]any{"vmid": 9500, "node": "node1", "pool": poolID, "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 9501, "node": "node1", "pool": poolID, "status": "running", "type": "qemu", "name": "lab-wayne-1"},
		map[string]any{"vmid": 9599, "node": "node1", "pool": poolID, "status": "running", "type": "qemu", "name": "lab-wayne-q"},
	)
	scaleGrowMultiNICFixture(f, poolID,
		[]map[string]any{
			{"id": "qemu/9500", "node": "node1", "type": "qemu", "vmid": 9500, "name": "lab-wayne-0"},
			{"id": "qemu/9501", "node": "node1", "type": "qemu", "vmid": 9501, "name": "lab-wayne-1"},
		},
		[]map[string]any{
			{"vmid": 9500, "name": "lab-wayne-0"},
			{"vmid": 9501, "name": "lab-wayne-1"},
		},
	)

	// Extra SDN vnets (pvecpist, pvecpiwk) and pvecpist's subnet: both
	// missing (scaleGrowMultiNICFixture), so buildCreatePlan's own extra-
	// vnet steps issue exactly these creates.
	var vnetCreateRec []createRecordedRequest
	createRecord(f, &vnetCreateRec, nil, "sdn-vnet-create", "POST /api2/json/cluster/sdn/vnets", map[string]any{}, 200)
	var subnetCreateRec []createRecordedRequest
	createRecord(f, &subnetCreateRec, nil, "sdn-subnet-create", "POST /api2/json/cluster/sdn/vnets/pvecpist/subnets", map[string]any{}, 200)

	// Node 2's VM shell: brand new (only node 0/1 already exist), so
	// createVM builds its Net map from scratch and issues one qemu-create.
	f.HandleJSON("GET /api2/json/cluster/nextid", "9502")
	var qemuCreateRec []createRecordedRequest
	createRecord(f, &qemuCreateRec, nil, "qemu-create", "POST /api2/json/nodes/node1/qemu", createTestUPID, 200)
	createHandleTaskStatus(f)

	// QDevice VM destroy (M4-04), after the corosync-level removal below.
	f.HandleJSON("GET /api2/json/nodes/node1/qemu/9599/status/current", map[string]any{"status": "stopped", "vmid": 9599})
	qdeviceDeleteUPID := "UPID:node1:00000099:00000099:65000000:qmdestroy:9599:root@pam:"
	f.HandleFunc("DELETE /api2/json/nodes/node1/qemu/9599", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, qdeviceDeleteUPID)
	})
	destroyHandleTaskStatus(f, "node1", qdeviceDeleteUPID)

	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())

	fake := exec.Fake(
		// 0: preflight membership probe (2+Q).
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice},
		// 1-2: qdevice remove: probe (has qdevice), then pvecm qdevice remove.
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice},
		exec.FakeResponse{},
		// 3: buildCreatePlan's zfs dataset existence probe (grow's node VM
		// shell(s) step) — dataset already exists.
		exec.FakeResponse{},
		// 4: ensure node 0 clustered: already clustered as wayne, 2/2, no qdevice.
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2},
		// 5-16: join node 2: reachable probe, not-yet-clustered probe,
		// guest-free (qm list, pct list), trust seed (ensure keypair, read
		// pubkey, append to node 0's authorized_keys, apiver preflight),
		// pvecm add, join verification probe, poll (pvecm status,
		// corosync-cfgtool).
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1},
		exec.FakeResponse{Stdout: sampleQmListEmpty},
		exec.FakeResponse{Stdout: samplePctListEmpty},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: sampleRootPubKey + "\n"},
		exec.FakeResponse{},
		exec.FakeResponse{},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		// 17-19: reconcile sdn: probe (missing), create, commit.
		exec.FakeResponse{ExitCode: 1},
		exec.FakeResponse{},
		exec.FakeResponse{},
		// 20-25: reconcile nfs: 3x (probe missing, add).
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		// 26-34: final validation, every target node (0, 1, 2): pvecm
		// status, corosync-cfgtool -s, pvesm status.
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice3of3},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolAllUp},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "wayne", "--nodes", "3", "--node", "node1", "--yes")
	require.NoError(t, err)
	assert.Contains(t, out, "scale requested")
	require.Len(t, fake.Calls, 35)

	// --- SDN vnet steps: both extra vnets created, storage vnet's subnet
	// created, workload vnet's subnet never even attempted (empty CIDR). ---
	require.Len(t, vnetCreateRec, 2, "both extra network.vnets[] entries (pvecpist, pvecpiwk) must be created")
	byVnet := make(map[string]createRecordedRequest, len(vnetCreateRec))
	for _, rec := range vnetCreateRec {
		byVnet[fmt.Sprint(rec.body["vnet"])] = rec
	}
	assert.Equal(t, "labs", byVnet["pvecpist"].body["zone"])
	assert.Equal(t, "5011", byVnet["pvecpist"].body["tag"])
	assert.Equal(t, "labs", byVnet["pvecpiwk"].body["zone"])
	assert.Equal(t, "5012", byVnet["pvecpiwk"].body["tag"])
	require.Len(t, subnetCreateRec, 1, "only the storage vnet (CIDR set) gets a subnet ensure; the workload vnet (no CIDR) never does")
	assert.Equal(t, "10.254.32.0/24", subnetCreateRec[0].body["subnet"])
	assert.Equal(t, "10.254.32.1", subnetCreateRec[0].body["gateway"])

	// --- Node 2's Net map: net0 (primary) plus all 5 configured
	// network.host_nics[] entries (net1..net5), each resolved to its target
	// vnet's bridge name and the lab's own net0 MTU (multi-AZ topology plan
	// §2, task P1-T5) — proving scale's grow path renders the SAME full
	// multi-NIC map `pmx lab create` renders for a fresh lab, via the
	// shared buildCreatePlan. ---
	require.Len(t, qemuCreateRec, 1, "exactly one qemu-create call, for node 2 (nodes 0/1 already exist)")
	body := qemuCreateRec[0].body
	assert.Equal(t, "virtio,bridge=pvecpi,mtu=1450", body["net0"])
	assert.Equal(t, "virtio,bridge=pvecpi,mtu=1450", body["net1"])
	assert.Equal(t, "virtio,bridge=pvecpist,mtu=1450", body["net2"])
	assert.Equal(t, "virtio,bridge=pvecpist,mtu=1450", body["net3"])
	assert.Equal(t, "virtio,bridge=pvecpiwk,mtu=1450", body["net4"])
	assert.Equal(t, "virtio,bridge=pvecpiwk,mtu=1450", body["net5"])
	assert.Equal(t, "lab-wayne-2", body["name"])
}

// TestScale_NfsReconcile_AliasedExportUsesOwnerPaths covers the scale
// orchestrator's NFS reconcile step for a lab whose storage.nfs_export
// aliases another lab's export tree: the pvesm add commands must point at
// the OWNER's export paths (tank/nfs/labs/<owner>/...), exactly as
// `pmx lab nfs attach` would build them — never at the aliasing lab's own
// name, which has no dataset on the server at all.
func TestScale_NfsReconcile_AliasedExportUsesOwnerPaths(t *testing.T) {
	lab := scaleTestLab("wayne", 1, "")
	lab.Storage.NFSExport = "pvecpi"
	owner := scaleTestLab("pvecpi", 1, "")
	owner.Network.Mgmt.Subnet = "10.20.1.0/24"
	owner.Network.VxlanTag = 5002
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab, "pvecpi": owner}})
	f := testhelper.NewFakePVE(t)

	handleClusterResources(f,
		map[string]any{"vmid": 9200, "node": "node1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-0"},
		map[string]any{"vmid": 9201, "node": "node1", "pool": "lab-wayne", "status": "stopped", "type": "qemu", "name": "lab-wayne-1"},
	)
	f.HandleJSON("GET /api2/json/nodes/node1/qemu/9201/status/current", map[string]any{"status": "stopped", "vmid": 9201})
	deleteUPID := "UPID:node1:00000003:00000003:65000000:qmdestroy:9201:root@pam:"
	f.HandleFunc("DELETE /api2/json/nodes/node1/qemu/9201", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, deleteUPID)
	})
	destroyHandleTaskStatus(f, "node1", deleteUPID)

	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newScaleCmd())

	fake := exec.Fake(
		// 0: preflight membership probe.
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice2of2},
		// 1-2: shrink node 1: evacuate (qm list: empty), delnode.
		exec.FakeResponse{Stdout: sampleQmListEmpty},
		exec.FakeResponse{},
		// 3-8: reconcile nfs: 3x (probe missing, add).
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		exec.FakeResponse{ExitCode: 1}, exec.FakeResponse{},
		// 9-11: final validation, node 0 alone.
		exec.FakeResponse{Stdout: scaleClusteredNoQdevice1of1},
		exec.FakeResponse{Stdout: sampleCorosyncCfgtoolSingleNode},
		exec.FakeResponse{Stdout: samplePvesmStatusAllActive},
	)
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "wayne", "--nodes", "1", "--yes")
	require.NoError(t, err)

	sawOwnerImages := false
	for _, c := range fake.Calls {
		joined := strings.Join(c.Args, " ")
		assert.NotContains(t, joined, "/tank/nfs/labs/wayne/",
			"aliased lab must never mount its own (nonexistent) export tree")
		if strings.Contains(joined, "pvesm add nfs nfs-images") {
			assert.Contains(t, joined, "/tank/nfs/labs/pvecpi/images")
			sawOwnerImages = true
		}
	}
	assert.True(t, sawOwnerImages, "expected a pvesm add for the owner's images export")
}
