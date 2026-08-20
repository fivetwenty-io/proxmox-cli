package lab

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// qdeviceIPv6TestLab returns the standard 2-node qdevice fixture plus its
// pool's running QDevice VM registered on f, ready for `qdevice add` runs.
func qdeviceIPv6TestLab(t *testing.T, f *testhelper.FakePVE) (*config.Lab, string) {
	t.Helper()
	lab := multiNodeTestLab("wayne", 2, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	handleClusterResources(f, map[string]any{
		"vmid": 9999, "node": "pve1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-q",
	})
	return lab, path
}

// qdeviceIPv6ConvergedProbe returns the combined probe stdout of a FULLY
// converged ifupdown-managed QDevice VM: live address (ip -6 addr), default
// route (ip -6 route show default), and the persistence grep naming the
// ifupdown drop-in — the three markers qdeviceEnsureIPv6's single probe
// command checks before skipping.
func qdeviceIPv6ConvergedProbe(addr6, gw6 string) string {
	return fmt.Sprintf(
		"    inet6 %[1]s/48 scope global\n"+
			"default via %[2]s dev ens18 metric 1024 pref medium\n"+
			"/etc/network/interfaces.d/lab-ipv6\n",
		addr6, gw6)
}

// qdeviceIPv6ConvergedNetplanProbe is qdeviceIPv6ConvergedProbe's netplan
// counterpart: the persisted copy lives in a netplan document, and the
// stack marker is present.
func qdeviceIPv6ConvergedNetplanProbe(addr6, gw6 string) string {
	return fmt.Sprintf(
		"    inet6 %[1]s/48 scope global\n"+
			"default via %[2]s dev ens18 metric 1024 pref medium\n"+
			"/etc/netplan/70-netplan-set.yaml\n"+
			qdeviceNetplanMarker+"\n",
		addr6, gw6)
}

// TestQdeviceIPv6Converged_RequiresExactMarkers pins the probe markers to
// their exact shapes: the live address must carry the planned /48 prefix (a
// stale ::f/64 from an older run must read as NOT converged — `ip -6 addr
// replace` cannot remove it, but the state must never be reported as
// satisfied), and the route marker must be a DEFAULT route via the mgmt
// gateway, not any line that happens to contain "via".
func TestQdeviceIPv6Converged_RequiresExactMarkers(t *testing.T) {
	addr6, gw6 := "fd10:109:0:1::f", "fd10:109:0:1::1"
	full := qdeviceIPv6ConvergedProbe(addr6, gw6)
	assert.True(t, qdeviceIPv6Converged(full, addr6, gw6))

	stalePrefix := strings.Replace(full, "inet6 "+addr6+"/48", "inet6 "+addr6+"/64", 1)
	assert.False(t, qdeviceIPv6Converged(stalePrefix, addr6, gw6),
		"a live address with the wrong prefix must not read as converged")

	nonDefaultRoute := strings.Replace(full, "default via "+gw6, "fd10:109:0:2::/64 via "+gw6, 1)
	assert.False(t, qdeviceIPv6Converged(nonDefaultRoute, addr6, gw6),
		"a non-default route via the gateway must not satisfy the route marker")
}

// TestQdeviceAdd_IPv6Absent_AddsAddressToQdeviceVM covers the default-on
// IPv6 parity step: after the qnetd install, `qdevice add` must probe the
// QDevice VM for its planned management IPv6 state (live address, default
// route, AND the reboot-persistence drop-in, in one command), and — absent —
// resolve the VM's management interface from its IPv4 address, set the
// address with the /48 interface prefix, set the v6 default route via the
// mgmt gateway, and persist both for reboots.
func TestQdeviceAdd_IPv6Absent_AddsAddressToQdeviceVM(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab, path := qdeviceIPv6TestLab(t, f)
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())

	addr6, err := labQdeviceMgmtIP6(lab.Network)
	require.NoError(t, err)
	gw6, err := labMgmtGateway6(lab.Network)
	require.NoError(t, err)

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice}, // cluster probe: already has qdevice
		exec.FakeResponse{},           // dpkg probe QDevice VM: qnetd already installed
		exec.FakeResponse{Stdout: ""}, // IPv6 probe on QDevice VM: nothing converged yet
		exec.FakeResponse{Stdout: "2: ens18    inet 10.10.1.15/16 brd 10.10.255.255 scope global ens18"}, // iface resolve
		exec.FakeResponse{}, // IPv6 apply (addr + route + persist)
		exec.FakeResponse{}, // dpkg probe node 0: already installed
		exec.FakeResponse{}, // dpkg probe node 1: already installed
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "add", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "ensure IPv6")

	require.Len(t, fake.Calls, 7)
	probeArgs := fmt.Sprintf("%v", fake.Calls[2].Args)
	assert.Contains(t, probeArgs, fmt.Sprintf("ip -6 addr show to %s/128", addr6))
	assert.Contains(t, probeArgs, "ip -6 route show default", "the probe must check the default route too")
	assert.Contains(t, probeArgs, fmt.Sprintf("grep -rlsF -- '%s/48' /etc/network/interfaces.d /etc/netplan", addr6),
		"the probe must look for a persisted copy in BOTH stacks' config directories")
	assert.Contains(t, fake.Calls[2].Args, "root@10.10.1.15")
	assert.Contains(t, fake.Calls[3].Args, "ip -o -4 addr show to 10.10.1.15/32")
	applyArgs := fmt.Sprintf("%v", fake.Calls[4].Args)
	assert.Contains(t, applyArgs, fmt.Sprintf("ip -6 addr replace %s/48 dev ens18", addr6),
		"addr replace, not add: the apply must be idempotent when a half-applied run left the address live")
	assert.Contains(t, applyArgs, fmt.Sprintf("ip -6 route replace default via %s dev ens18", gw6))
	assert.Contains(t, applyArgs, "/etc/network/interfaces.d/lab-ipv6", "the address must persist across reboots")
}

// TestQdeviceAdd_IPv6Present_SkipsApply pins idempotence: when the probe
// reports full convergence (live address, default route, persistence
// drop-in), no interface resolve and no apply command may run — only the
// probe.
func TestQdeviceAdd_IPv6Present_SkipsApply(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab, path := qdeviceIPv6TestLab(t, f)
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())

	addr6, err := labQdeviceMgmtIP6(lab.Network)
	require.NoError(t, err)
	gw6, err := labMgmtGateway6(lab.Network)
	require.NoError(t, err)

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice},
		exec.FakeResponse{}, // dpkg probe QDevice VM: qnetd already installed
		exec.FakeResponse{Stdout: qdeviceIPv6ConvergedProbe(addr6, gw6)}, // IPv6 probe: fully converged
		exec.FakeResponse{}, // dpkg probe node 0
		exec.FakeResponse{}, // dpkg probe node 1
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "add", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "skip (already satisfied)")
	require.Len(t, fake.Calls, 5, "full convergence must cost exactly one probe, no apply")
}

// TestQdeviceAdd_IPv6PartiallyApplied_RerunsApply pins self-repair of a
// half-applied state: a previous run's `ip -6 addr` succeeded but the chain
// died before route/persistence (transient failure, dropped ssh). The live
// address alone must NOT read as converged — the rerun must resolve the
// interface and apply again, or the VM keeps a routeless address that
// vanishes on reboot while every rerun reports skip.
func TestQdeviceAdd_IPv6PartiallyApplied_RerunsApply(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab, path := qdeviceIPv6TestLab(t, f)
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())

	addr6, err := labQdeviceMgmtIP6(lab.Network)
	require.NoError(t, err)

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice},
		exec.FakeResponse{}, // dpkg probe QDevice VM
		// IPv6 probe: live address present, but no default route and no
		// persistence drop-in in the combined output.
		exec.FakeResponse{Stdout: fmt.Sprintf("    inet6 %s/48 scope global", addr6)},
		exec.FakeResponse{Stdout: "2: ens18    inet 10.10.1.15/16 scope global ens18"}, // iface resolve
		exec.FakeResponse{}, // IPv6 apply
		exec.FakeResponse{}, // dpkg probe node 0
		exec.FakeResponse{}, // dpkg probe node 1
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "add", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "ensure IPv6")

	require.Len(t, fake.Calls, 7, "a partial state must trigger iface resolve + apply, never a skip")
	applyArgs := fmt.Sprintf("%v", fake.Calls[4].Args)
	assert.Contains(t, applyArgs, fmt.Sprintf("ip -6 addr replace %s/48 dev ens18", addr6))
}

// TestQdeviceAdd_IPv6IfaceLinkSuffix_Stripped pins interface-name hygiene:
// for some link kinds `ip -o -4 addr show` prints the name as "eth0@if5";
// only the part before '@' is a real interface name usable in `ip ... dev`.
func TestQdeviceAdd_IPv6IfaceLinkSuffix_Stripped(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab, path := qdeviceIPv6TestLab(t, f)
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())

	addr6, err := labQdeviceMgmtIP6(lab.Network)
	require.NoError(t, err)

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice},
		exec.FakeResponse{},           // dpkg probe QDevice VM
		exec.FakeResponse{Stdout: ""}, // IPv6 probe: absent
		exec.FakeResponse{Stdout: "2: eth0@if5    inet 10.10.1.15/16 scope global eth0"}, // iface resolve
		exec.FakeResponse{}, // IPv6 apply
		exec.FakeResponse{}, // dpkg probe node 0
		exec.FakeResponse{}, // dpkg probe node 1
	)
	cli.GetDeps(cmd).Runner = fake

	_, err = runGuestCmd(t, cmd, "add", "wayne")
	require.NoError(t, err)

	require.Len(t, fake.Calls, 7)
	applyArgs := fmt.Sprintf("%v", fake.Calls[4].Args)
	assert.Contains(t, applyArgs, fmt.Sprintf("ip -6 addr replace %s/48 dev eth0 ", addr6))
	assert.NotContains(t, applyArgs, "eth0@if5", "the @link suffix must never reach the remote shell")
}

// TestQdeviceAdd_IPv6MalformedIfaceName_Errors pins the guard on the
// remote-derived interface name: a value that is not a plain interface name
// (here: shell metacharacters) must fail the step with an error, never be
// interpolated into the apply command line.
func TestQdeviceAdd_IPv6MalformedIfaceName_Errors(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, path := qdeviceIPv6TestLab(t, f)
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice},
		exec.FakeResponse{},           // dpkg probe QDevice VM
		exec.FakeResponse{Stdout: ""}, // IPv6 probe: absent
		exec.FakeResponse{Stdout: "2: $(reboot)    inet 10.10.1.15/16 scope global"}, // iface resolve: hostile
	)
	cli.GetDeps(cmd).Runner = fake

	_, err := runGuestCmd(t, cmd, "add", "wayne")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interface")
	require.Len(t, fake.Calls, 4, "the malformed name must stop the flow before any apply command")
}

// TestQdeviceAdd_IPv6Disabled_NoIPv6Calls pins the opt-out: with
// `network.ipv6: false` the add flow must be byte-identical to the pre-IPv6
// behavior — no probe, no apply, no IPv6 row.
func TestQdeviceAdd_IPv6Disabled_NoIPv6Calls(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab := multiNodeTestLab("wayne", 2, "")
	off := false
	lab.Network.IPv6 = &off
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	handleClusterResources(f, map[string]any{
		"vmid": 9999, "node": "pve1", "pool": "lab-wayne", "status": "running", "type": "qemu", "name": "lab-wayne-q",
	})
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice},
		exec.FakeResponse{}, // dpkg probe QDevice VM
		exec.FakeResponse{}, // dpkg probe node 0
		exec.FakeResponse{}, // dpkg probe node 1
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "add", "wayne")
	require.NoError(t, err)
	assert.NotContains(t, out, "IPv6")
	require.Len(t, fake.Calls, 4)
}

// TestQdeviceAdd_IPv6DryRun_PreviewsRow pins the --dry-run preview: the
// IPv6 ensure must appear as a would-run step with zero runner calls.
func TestQdeviceAdd_IPv6DryRun_PreviewsRow(t *testing.T) {
	lab := multiNodeTestLab("wayne", 2, "")
	path := writeConfig(t, &config.Config{Labs: map[string]*config.Lab{"wayne": lab}})
	cmd, fake := buildGuestSSHCmd(t, path, newQdeviceCmd())

	addr6, err := labQdeviceMgmtIP6(lab.Network)
	require.NoError(t, err)

	out, err := runGuestCmd(t, cmd, "add", "wayne", "--dry-run")
	require.NoError(t, err)
	assert.Contains(t, out, "ensure IPv6")
	assert.Contains(t, out, addr6)
	assert.Empty(t, fake.Calls, "dry-run must never invoke the runner")
}
