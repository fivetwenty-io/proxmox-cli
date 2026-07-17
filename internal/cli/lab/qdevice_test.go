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

func TestQdeviceAdd_RequiresTopologyQdevice(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "") // odd node count: qdevice never required
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newQdeviceCmd())

	_, err := runGuestCmd(t, cmd, "add", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "does not call for a QDevice")
	assert.Empty(t, fake.Calls)
}

func TestQdeviceAdd_DryRun_NoRunnerCalls(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "") // mandatory qdevice at 2 nodes
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newQdeviceCmd())

	out, err := runGuestCmd(t, cmd, "add", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "corosync-qnetd")
	assert.Contains(t, out, "corosync-qdevice")
	assert.Contains(t, out, "pvecm qdevice setup 10.10.1.15")
	assert.Empty(t, fake.Calls, "dry-run must never invoke the runner")
}

func TestQdeviceAdd_RequiresQdeviceVM(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f) // no VMs at all
	cmd, fake := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())

	_, err := runGuestCmd(t, cmd, "add", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "no QDevice VM found")
	assert.Empty(t, fake.Calls, "must fail before any ssh call")
}

func TestQdeviceAdd_RequiresQdeviceVMRunning(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f, map[string]any{
		"vmid": 9999, "node": "pve1", "pool": "lab-wayne", "status": "stopped", "type": "qemu", "name": "lab-wayne-q",
	})
	cmd, fake := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())

	_, err := runGuestCmd(t, cmd, "add", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not running")
	assert.Empty(t, fake.Calls)
}

func TestQdeviceAdd_RequiresClusterFormed(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f, map[string]any{
		"vmid": 9999, "node": "pve1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-q",
	})
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())
	fake := exec.Fake(exec.FakeResponse{Stdout: samplePvecmStatusNotClustered, ExitCode: 1})
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "add", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not yet clustered")
	require.Len(t, fake.Calls, 1, "only the cluster probe, no package install")
}

func TestQdeviceAdd_HappyPath_InstallsAndSetsUp(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f, map[string]any{
		"vmid": 9999, "node": "pve1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-q",
	})
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusQuorate3of3}, // cluster probe on node 0 (clustered, no qdevice yet)
		exec.FakeResponse{ExitCode: 1},                          // dpkg probe on QDevice VM: not installed
		exec.FakeResponse{},                                     // apt-get install corosync-qnetd
		exec.FakeResponse{},                                     // dpkg probe on node 0: already installed
		exec.FakeResponse{ExitCode: 1},                          // dpkg probe on node 1: not installed
		exec.FakeResponse{},                                     // apt-get install corosync-qdevice on node 1
		exec.FakeResponse{},                                     // pvecm qdevice setup on node 0
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "add", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "QDevice wired up")

	require.Len(t, fake.Calls, 7)
	assert.Contains(t, fake.Calls[0].Args, "pvecm status")
	assert.Contains(t, fake.Calls[1].Args, "dpkg -s corosync-qnetd")
	assert.Contains(t, fake.Calls[2].Args, "apt-get update && apt-get install -y corosync-qnetd")
	assert.Contains(t, fake.Calls[2].Args, "root@10.10.1.15")
	assert.Contains(t, fake.Calls[3].Args, "dpkg -s corosync-qdevice")
	assert.Contains(t, fake.Calls[3].Args, "root@10.10.1.10")
	assert.Contains(t, fake.Calls[4].Args, "dpkg -s corosync-qdevice")
	assert.Contains(t, fake.Calls[4].Args, "root@10.10.1.11")
	assert.Contains(t, fake.Calls[5].Args, "apt-get update && apt-get install -y corosync-qdevice")
	assert.Contains(t, fake.Calls[6].Args, "pvecm qdevice setup 10.10.1.15")
	assert.Contains(t, fake.Calls[6].Args, "root@10.10.1.10")
}

func TestQdeviceAdd_SkipsSetupWhenAlreadyRegistered(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	f := testhelper.NewFakePVE(t)
	handleClusterResources(f, map[string]any{
		"vmid": 9999, "node": "pve1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-q",
	})
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice}, // cluster probe: already has qdevice
		exec.FakeResponse{}, // dpkg probe QDevice VM: already installed
		exec.FakeResponse{}, // dpkg probe node 0: already installed
		exec.FakeResponse{}, // dpkg probe node 1: already installed
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "add", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "skip (already satisfied)")
	require.Len(t, fake.Calls, 4, "no setup call should run when already registered")
}

func TestQdeviceAdd_PeppiGuardRefusesBeforeAnyCall(t *testing.T) {
	lab := multiNodeTestLab("dirty", 2, "")
	lab.Network.VnetID = "peppivn0"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"dirty": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newQdeviceCmd())

	_, err := runGuestCmd(t, cmd, "add", "dirty")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.Empty(t, fake.Calls)
}

func TestQdeviceRemove_DryRun_NoRunnerCalls(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newQdeviceCmd())

	out, err := runGuestCmd(t, cmd, "remove", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "pvecm qdevice remove")
	assert.Empty(t, fake.Calls)
}

func TestQdeviceRemove_AlreadyAbsent_Skip(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newQdeviceCmd())
	fake := exec.Fake(exec.FakeResponse{Stdout: samplePvecmStatusQuorate3of3}) // no qdevice
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "remove", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "no registered QDevice")
	require.Len(t, fake.Calls, 1)
}

func TestQdeviceRemove_HappyPath(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newQdeviceCmd())
	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice},
		exec.FakeResponse{},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "remove", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "QDevice removed")
	require.Len(t, fake.Calls, 2)
	assert.Contains(t, fake.Calls[1].Args, "pvecm qdevice remove")
}

func TestQdeviceRemove_PeppiGuardRefusesBeforeAnyCall(t *testing.T) {
	lab := multiNodeTestLab("dirty", 2, "")
	lab.Network.VnetID = "peppivn0"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"dirty": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newQdeviceCmd())

	_, err := runGuestCmd(t, cmd, "remove", "dirty")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.Empty(t, fake.Calls)
}
