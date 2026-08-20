package lab

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
)

func TestLabCephInstall_UnknownLab_Errors(t *testing.T) {
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	_, err := runGuestCmd(t, cmd, "install", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "not found")
	assert.Empty(t, fake.Calls)
}

func TestLabCephInstall_TwoNodeLab_Refuses(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	_, err := runGuestCmd(t, cmd, "install", "wayne")
	require.Error(t, err)
	assert.ErrorContains(t, err, "at least a 3-node lab")
	assert.Empty(t, fake.Calls)
}

func TestLabCephInstall_DryRun_NoRunnerCalls(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newCephCmd())

	out, err := runGuestCmd(t, cmd, "install", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "[dry-run]")
	require.Empty(t, fake.Calls, "dry-run must never invoke the runner")
}

func TestLabCephInstall_HappyPath_InstallsOnAllThree(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())

	fake := exec.Fake(
		exec.FakeResponse{Stdout: "absent"}, // 0: probe node 0
		exec.FakeResponse{},                 // 1: install node 0
		exec.FakeResponse{Stdout: "absent"}, // 2: probe node 1
		exec.FakeResponse{},                 // 3: install node 1
		exec.FakeResponse{Stdout: "absent"}, // 4: probe node 2
		exec.FakeResponse{},                 // 5: install node 2
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "install", "wayne")
	require.NoError(t, err)

	assert.Contains(t, out, "install node 0")
	assert.Contains(t, out, "install node 1")
	assert.Contains(t, out, "install node 2")
	assert.Contains(t, out, "installed")

	require.Len(t, fake.Calls, 6)
	installCmd := fake.Calls[1].Args[len(fake.Calls[1].Args)-1]
	assert.True(t, strings.HasSuffix(installCmd, "pveceph install --repository no-subscription -y"),
		"the install command must end with the mandatory -y flag, got %q", installCmd)
}

func TestLabCephInstall_AlreadyInstalled_Skips(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())

	fake := exec.Fake(
		exec.FakeResponse{Stdout: "installed"}, // 0: probe node 0 -> already installed, no install call
		exec.FakeResponse{Stdout: "installed"}, // 1: probe node 1
		exec.FakeResponse{Stdout: "installed"}, // 2: probe node 2
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "install", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "already installed")

	require.Len(t, fake.Calls, 3, "no second (install) call must run per node once already installed")
}

func TestLabCephInstall_TransportFailure_Aborts(t *testing.T) {
	lab := multiNodeTestLab("wayne", 3, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newCephCmd())

	fake := exec.Fake(
		exec.FakeResponse{Err: errors.New("ssh: connect to host 10.10.1.10 port 22: no route to host")},
	)
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "install", "wayne")
	require.Error(t, err)
	require.Len(t, fake.Calls, 1)
}
