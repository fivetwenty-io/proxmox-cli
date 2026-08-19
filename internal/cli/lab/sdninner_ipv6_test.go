package lab

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
)

// TestSdnVlanApply_IPv6CreatesInnerSubnet covers the default-on IPv6 path
// for the nested vlan zone: with zone, vnet, and IPv4 subnet all already
// matching, `sdn vlan apply` must still ensure each vnet's carved IPv6 /64
// (subnet ID labV6SubnetInnerBase+i of the lab block, gatewayed at ::1) —
// creating it when the vnet's subnet list lacks it, then committing.
func TestSdnVlanApply_IPv6CreatesInnerSubnet(t *testing.T) {
	lab := clientVlanZoneTestLab("wayne", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newSdnCmd())

	cidr6, err := labInnerVnetCIDR6(lab.Network, 0)
	require.NoError(t, err)
	gw6, err := labV6Gateway(cidr6)
	require.NoError(t, err)

	v4List := `[{"subnet":"cli40-10.61.136.0-24","cidr":"10.61.136.0/24","gateway":"10.61.136.1"}]`
	fake := exec.Fake(
		exec.FakeResponse{Stdout: `{"bridge":"vmbr2"}`},                                  // probe zone: matches
		exec.FakeResponse{Stdout: `{"zone":"clivlan","tag":40,"alias":"client-vlan40"}`}, // probe vnet: matches
		exec.FakeResponse{Stdout: v4List},                                                // list subnets for the IPv4 ensure: matches
		exec.FakeResponse{Stdout: v4List},                                                // list subnets for the IPv6 ensure: v6 /64 absent
		exec.FakeResponse{},                                                              // create the IPv6 subnet
		exec.FakeResponse{},                                                              // commit
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "vlan", "apply", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "committed")

	require.Len(t, fake.Calls, 6)
	assert.Contains(t, fake.Calls[4].Args, fmt.Sprintf(
		"pvesh create /cluster/sdn/vnets/cli40/subnets --subnet %s --type subnet --gateway %s", cidr6, gw6))
}

// TestSdnVlanApply_IPv6AlreadyPresent_NoCommit pins idempotence: when the
// vnet's subnet list already holds the carved /64 with the right gateway,
// nothing is created or updated and the commit is skipped.
func TestSdnVlanApply_IPv6AlreadyPresent_NoCommit(t *testing.T) {
	lab := clientVlanZoneTestLab("wayne", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, _ := buildGuestSSHCmd(t, path, newSdnCmd())

	cidr6, err := labInnerVnetCIDR6(lab.Network, 0)
	require.NoError(t, err)
	gw6, err := labV6Gateway(cidr6)
	require.NoError(t, err)

	list := fmt.Sprintf(
		`[{"subnet":"cli40-10.61.136.0-24","cidr":"10.61.136.0/24","gateway":"10.61.136.1"},`+
			`{"subnet":"cli40-v6","cidr":%q,"gateway":%q}]`, cidr6, gw6)
	fake := exec.Fake(
		exec.FakeResponse{Stdout: `{"bridge":"vmbr2"}`},
		exec.FakeResponse{Stdout: `{"zone":"clivlan","tag":40,"alias":"client-vlan40"}`},
		exec.FakeResponse{Stdout: list}, // IPv4 ensure: matches
		exec.FakeResponse{Stdout: list}, // IPv6 ensure: matches
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "vlan", "apply", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "skip (no pending changes)")
	require.Len(t, fake.Calls, 4, "fully converged run must issue zero create/update/commit calls")
}

// TestSdnVlanApply_IPv6DryRun_PreviewsRow pins the --dry-run preview: the
// carved /64 must appear as its own would-run subnet row, with zero runner
// calls.
func TestSdnVlanApply_IPv6DryRun_PreviewsRow(t *testing.T) {
	lab := clientVlanZoneTestLab("wayne", 3)
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newSdnCmd())

	cidr6, err := labInnerVnetCIDR6(lab.Network, 0)
	require.NoError(t, err)

	out, err := runGuestCmd(t, cmd, "vlan", "apply", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, cidr6)
	assert.Empty(t, fake.Calls)
}
