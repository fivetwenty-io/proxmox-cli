package lab

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestQdeviceParseNetplanList_EveryYAMLShape pins the decode of what
// `netplan get` prints for a list key: a block sequence, an inline flow
// sequence, and the literal "null" for a key that is not set.
func TestQdeviceParseNetplanList_EveryYAMLShape(t *testing.T) {
	assert.Equal(t, []string{"10.10.1.15/24", "fd10::f/48"},
		qdeviceParseNetplanList("- 10.10.1.15/24\n- fd10::f/48\n"))
	assert.Equal(t, []string{"10.10.1.15/24"}, qdeviceParseNetplanList(`["10.10.1.15/24"]`))
	assert.Empty(t, qdeviceParseNetplanList("null\n"))
	assert.Empty(t, qdeviceParseNetplanList(""))
}

// TestQdeviceIPv6Persisted_AnyStacksConfigFileCounts pins the stack-agnostic
// convergence marker: the probe's `grep -rl` phase naming ANY config file
// that holds the address is what counts, so an ifupdown drop-in and a
// netplan document are equally valid evidence — and a stray line of `ip`
// output is not.
func TestQdeviceIPv6Persisted_AnyStacksConfigFileCounts(t *testing.T) {
	assert.True(t, qdeviceIPv6Persisted("/etc/network/interfaces.d/lab-ipv6\n"))
	assert.True(t, qdeviceIPv6Persisted("/etc/netplan/70-netplan-set.yaml\n"))
	assert.False(t, qdeviceIPv6Persisted("    inet6 fd10::f/48 scope global\n"))
	assert.False(t, qdeviceIPv6Persisted(""))
}

// TestQdeviceAdd_NetplanGuest_PersistsThroughNetplan covers the gap this
// round closes on the QDevice side: the lab's own tmpl-qdevice image renders
// its network with netplan and never reads /etc/network/interfaces.d, so the
// old ifupdown-only drop-in left the address live but gone after a reboot —
// while the convergence probe, which grepped that same unread file, could
// never report converged either.
//
// The address list written back must carry the guest's EXISTING IPv4
// address, since `netplan set` replaces the key rather than merging into it.
func TestQdeviceAdd_NetplanGuest_PersistsThroughNetplan(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab, path := qdeviceIPv6TestLab(t, f)
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())

	addr6, err := labQdeviceMgmtIP6(lab.Network)
	require.NoError(t, err)
	gw6, err := labMgmtGateway6(lab.Network)
	require.NoError(t, err)

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice}, // cluster probe
		exec.FakeResponse{}, // qnetd already installed
		exec.FakeResponse{Stdout: qdeviceNetplanMarker + "\n"},                         // IPv6 probe: netplan guest, nothing converged
		exec.FakeResponse{Stdout: "2: ens18    inet 10.10.1.15/16 scope global ens18"}, // iface resolve
		exec.FakeResponse{}, // live ip -6 apply
		exec.FakeResponse{Stdout: "addresses:\n- 10.10.1.15/16\ndhcp4: false\n"}, // netplan get ethernets.ens18
		exec.FakeResponse{Stdout: "- 10.10.1.15/16\n"},                           // netplan get ...addresses
		exec.FakeResponse{Stdout: "null\n"},                                      // netplan get ...routes
		exec.FakeResponse{},                                                      // netplan set + generate
		exec.FakeResponse{},                                                      // node 0 package probe
		exec.FakeResponse{},                                                      // node 1 package probe
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "add", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "ensure IPv6")

	require.Len(t, fake.Calls, 11)
	liveArgs := fmt.Sprintf("%v", fake.Calls[4].Args)
	assert.Contains(t, liveArgs, fmt.Sprintf("ip -6 addr replace %s/48 dev ens18", addr6))
	assert.NotContains(t, liveArgs, "/etc/network/interfaces.d/lab-ipv6",
		"a netplan guest must never be handed the ifupdown drop-in")

	setArgs := fmt.Sprintf("%v", fake.Calls[8].Args)
	assert.Contains(t, setArgs,
		fmt.Sprintf(`netplan set 'ethernets.ens18.addresses=["10.10.1.15/16","%s/48"]'`, addr6),
		"the existing IPv4 address must be re-stated, since netplan set REPLACES the list")
	assert.Contains(t, setArgs, fmt.Sprintf(`"via": "%s"`, gw6), "the v6 default route is written too")
	assert.Contains(t, setArgs, "netplan generate", "the written config is validated, never applied")
	assert.NotContains(t, setArgs, "netplan apply",
		"applying would re-render the very interface this command is reached over")
}

// TestQdeviceAdd_NetplanGuest_ExistingRoutesAreNotRewritten pins the
// deliberate limit: netplan routes are a list of mappings this code does not
// round-trip, so an interface that already declares routes keeps them and
// the operator is told to add the v6 default route by hand — far better than
// silently rewriting a working IPv4 default route.
func TestQdeviceAdd_NetplanGuest_ExistingRoutesAreNotRewritten(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, path := qdeviceIPv6TestLab(t, f)
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: qdeviceNetplanMarker + "\n"},
		exec.FakeResponse{Stdout: "2: ens18    inet 10.10.1.15/16 scope global ens18"},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: "addresses:\n- 10.10.1.15/16\n"},
		exec.FakeResponse{Stdout: "- 10.10.1.15/16\n"},
		exec.FakeResponse{Stdout: "- to: default\n  via: 10.10.1.1\n"}, // routes already declared
		exec.FakeResponse{}, // netplan set (addresses only) + generate
		exec.FakeResponse{},
		exec.FakeResponse{},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "add", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "add the ::/0 route")

	setArgs := fmt.Sprintf("%v", fake.Calls[8].Args)
	assert.NotContains(t, setArgs, "routes=", "an existing routes list must never be rewritten")
}

// TestQdeviceAdd_NetplanInstalledButUnmanagedIface_FallsBackToIfupdown pins
// the guard against taking an interface away from whatever manages it: a
// netplan installation that has no stanza for the management interface must
// not be given one — that would hand the interface to networkd at the next
// boot. Such a guest gets the ifupdown drop-in it actually reads, plus a
// note saying so.
func TestQdeviceAdd_NetplanInstalledButUnmanagedIface_FallsBackToIfupdown(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab, path := qdeviceIPv6TestLab(t, f)
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())

	addr6, err := labQdeviceMgmtIP6(lab.Network)
	require.NoError(t, err)

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: qdeviceNetplanMarker + "\n"},
		exec.FakeResponse{Stdout: "2: ens18    inet 10.10.1.15/16 scope global ens18"},
		exec.FakeResponse{},                 // live apply
		exec.FakeResponse{Stdout: "null\n"}, // netplan get ethernets.ens18: not managed here
		exec.FakeResponse{},                 // ifupdown drop-in write
		exec.FakeResponse{},
		exec.FakeResponse{},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "add", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "does not manage ens18")

	writeArgs := fmt.Sprintf("%v", fake.Calls[6].Args)
	assert.Contains(t, writeArgs, qdeviceIPv6PersistPath)
	assert.Contains(t, writeArgs, fmt.Sprintf("address %s/48", addr6))
}

// TestQdeviceAdd_NetplanGuest_ConvergedSkips pins idempotence for the
// netplan stack: a guest whose address is live, routed, and present in a
// netplan document costs one probe and nothing else.
func TestQdeviceAdd_NetplanGuest_ConvergedSkips(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab, path := qdeviceIPv6TestLab(t, f)
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())

	addr6, err := labQdeviceMgmtIP6(lab.Network)
	require.NoError(t, err)
	gw6, err := labMgmtGateway6(lab.Network)
	require.NoError(t, err)

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: qdeviceIPv6ConvergedNetplanProbe(addr6, gw6)},
		exec.FakeResponse{},
		exec.FakeResponse{},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "add", "wayne")
	require.NoError(t, err)
	assert.Contains(t, out, "skip (already satisfied)")
	require.Len(t, fake.Calls, 5, "a converged netplan guest costs exactly one IPv6 probe")
}

// TestQdeviceNetplanFallbackFile_CarriesFullAddressList pins the shape of
// the file written when `netplan set` is unavailable: its keys win the merge
// against the image's own file, so it must carry every address, not only the
// IPv6 one.
func TestQdeviceNetplanFallbackFile_CarriesFullAddressList(t *testing.T) {
	doc := qdeviceNetplanFallbackFile("ens18", []string{"10.10.1.15/16", "fd10::f/48"}, "fd10::1", true)
	assert.Contains(t, doc, "    ens18:")
	assert.Contains(t, doc, "        - 10.10.1.15/16")
	assert.Contains(t, doc, "        - fd10::f/48")
	assert.Contains(t, doc, `        - to: "::/0"`)
	assert.Contains(t, doc, `          via: "fd10::1"`)

	noRoute := qdeviceNetplanFallbackFile("ens18", []string{"10.10.1.15/16"}, "fd10::1", false)
	assert.NotContains(t, noRoute, "routes:")
}
