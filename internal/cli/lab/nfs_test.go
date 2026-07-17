package lab

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
)

func TestNfsAttach_DryRun_NoRunnerCalls(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	out, err := runGuestCmd(t, cmd, "attach", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "pvesm add nfs nfs-images --server 10.10.1.1 --export /tank/nfs/labs/wayne/images --content images --options vers=4.1")
	assert.Contains(t, out, "pvesm add nfs nfs-backup --server 10.10.1.1 --export /tank/nfs/labs/wayne/backup --content backup --options vers=4.1")
	assert.Contains(t, out, "pvesm add nfs shared-iso --server 10.10.1.1 --export /tank/nfs/shared/iso --content iso,vztmpl --options vers=4.1,ro,soft")
	assert.Empty(t, fake.Calls)
}

// TestNfsServerIP_RejectsInvalidGateway covers M3-R03: a malformed
// network.mgmt.gateway must never reach `pvesm add nfs --server <value>`
// unvalidated.
func TestNfsServerIP_RejectsInvalidGateway(t *testing.T) {
	n := config.LabNetwork{Mgmt: config.LabMgmt{Gateway: "not-an-ip; rm -rf /"}}
	_, err := nfsServerIP(n)
	require.Error(t, err)
	assert.ErrorContains(t, err, "not a valid IP address")
}

func TestNfsServerIP_ValidGatewayPassesThrough(t *testing.T) {
	n := config.LabNetwork{Mgmt: config.LabMgmt{Gateway: "10.10.1.1"}}
	ip, err := nfsServerIP(n)
	require.NoError(t, err)
	assert.Equal(t, "10.10.1.1", ip)
}

func TestNfsServerIP_FallsBackToDerivedGatewayWhenUnset(t *testing.T) {
	n := config.LabNetwork{Mgmt: config.LabMgmt{Subnet: "10.20.3.0/24"}}
	ip, err := nfsServerIP(n)
	require.NoError(t, err)
	assert.Equal(t, "10.20.3.1", ip)
}

func TestNfsAttach_RefusesInvalidGatewayBeforeAnyCall(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	lab.Network.Mgmt.Gateway = "not-an-ip"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	_, err := runGuestCmd(t, cmd, "attach", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not a valid IP address")
	assert.Empty(t, fake.Calls)
}

func TestNfsAttach_HappyPath_AttachesAll(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newNfsCmd())
	fake := exec.Fake(
		exec.FakeResponse{ExitCode: 1}, // probe nfs-images: not configured
		exec.FakeResponse{},            // add nfs-images
		exec.FakeResponse{ExitCode: 1}, // probe nfs-backup: not configured
		exec.FakeResponse{},            // add nfs-backup
		exec.FakeResponse{ExitCode: 1}, // probe shared-iso: not configured
		exec.FakeResponse{},            // add shared-iso
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "attach", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "attached")

	require.Len(t, fake.Calls, 6)
	assert.Contains(t, fake.Calls[1].Args, "pvesm add nfs nfs-images --server 10.10.1.1 --export /tank/nfs/labs/wayne/images --content images --options vers=4.1")
	assert.Contains(t, fake.Calls[3].Args, "pvesm add nfs nfs-backup --server 10.10.1.1 --export /tank/nfs/labs/wayne/backup --content backup --options vers=4.1")
	assert.Contains(t, fake.Calls[5].Args, "pvesm add nfs shared-iso --server 10.10.1.1 --export /tank/nfs/shared/iso --content iso,vztmpl --options vers=4.1,ro,soft")
}

func TestNfsAttach_SkipsAlreadyAttached(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newNfsCmd())
	fake := exec.Fake(
		exec.FakeResponse{Stdout: `{"storage":"nfs-images"}`},
		exec.FakeResponse{Stdout: `{"storage":"nfs-backup"}`},
		exec.FakeResponse{Stdout: `{"storage":"shared-iso"}`},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "attach", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "skip (already attached)")
	require.Len(t, fake.Calls, 3, "no add calls when every storage is already configured")
}

func TestNfsAttach_PeppiGuardRefusesBeforeAnyCall(t *testing.T) {
	lab := multiNodeTestLab("dirty", 1, "")
	lab.Network.VnetID = "peppivn0"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"dirty": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	_, err := runGuestCmd(t, cmd, "attach", "dirty")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.Empty(t, fake.Calls)
}

const samplePvesmStatus = `Name             Type     Status           Total            Used       Available        %
local             dir     active       100000000        10000000        90000000    10.00%
nfs-images        nfs     active      1073741824               0      1073741824    0.00%
shared-iso        nfs   inactive               0               0               0    0.00%
`

func TestNfsStatus_RendersConfiguredAndStatus(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newNfsCmd())
	fake := exec.Fake(exec.FakeResponse{Stdout: samplePvesmStatus})
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "status", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "nfs-images")
	assert.Contains(t, out, "active")
	assert.Contains(t, out, "shared-iso")
	assert.Contains(t, out, "inactive")
	assert.Contains(t, out, "nfs-backup")
	assert.Contains(t, out, "n/a", "nfs-backup is not in the sample pvesm status output, so it must render n/a")
}

func TestParsePvesmStatus_ParsesDataRows(t *testing.T) {
	statuses := parsePvesmStatus(samplePvesmStatus)
	assert.Equal(t, "active", statuses["nfs-images"])
	assert.Equal(t, "inactive", statuses["shared-iso"])
	assert.NotContains(t, statuses, "nfs-backup")
}

func TestNfsDetach_DryRun_NoRunnerCalls(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	out, err := runGuestCmd(t, cmd, "detach", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "pvesm remove nfs-images")
	assert.Contains(t, out, "pvesm remove nfs-backup")
	assert.Contains(t, out, "pvesm remove shared-iso")
	assert.Empty(t, fake.Calls)
}

func TestNfsDetach_RefusesWithoutYesNonInteractively(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())
	cmd.SetIn(strings.NewReader(""))

	out, err := runGuestCmd(t, cmd, "detach", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "Aborted")
	assert.Empty(t, fake.Calls, "must refuse to run without confirmation")
}

func TestNfsDetach_HappyPath(t *testing.T) {
	lab := multiNodeTestLab("wayne", 1, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newNfsCmd())
	fake := exec.Fake(
		exec.FakeResponse{Stdout: `{"storage":"nfs-images"}`}, // configured
		exec.FakeResponse{},                                   // remove
		exec.FakeResponse{ExitCode: 1},                        // nfs-backup: not configured
		exec.FakeResponse{Stdout: `{"storage":"shared-iso"}`}, // configured
		exec.FakeResponse{},                                   // remove
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "detach", "wayne", "--yes")
	require.NoError(t, err)
	assert.Contains(t, out, "detached")

	require.Len(t, fake.Calls, 5)
	assert.Contains(t, fake.Calls[1].Args, "pvesm remove nfs-images")
	assert.Contains(t, fake.Calls[4].Args, "pvesm remove shared-iso")
}

func TestNfsDetach_PeppiGuardRefusesBeforeAnyCall(t *testing.T) {
	lab := multiNodeTestLab("dirty", 1, "")
	lab.Network.VnetID = "peppivn0"
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"dirty": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newNfsCmd())

	_, err := runGuestCmd(t, cmd, "detach", "dirty", "--yes")
	require.Error(t, err)
	assert.ErrorContains(t, err, "peppi guard")
	assert.Empty(t, fake.Calls)
}
