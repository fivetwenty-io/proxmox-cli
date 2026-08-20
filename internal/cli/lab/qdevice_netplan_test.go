package lab

import (
	"fmt"
	"testing"

	"github.com/goccy/go-yaml"

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

// TestQdeviceIPv6Persisted_OnlyTheGuestsOwnRendererCounts pins the
// convergence marker to the file the guest actually reads. The case that
// matters is the third one: a netplan guest carrying only the old
// ifupdown drop-in is precisely the state this round set out to fix, so it
// must never read as converged — that would restore the original bug, with
// the address live now and gone after a reboot.
func TestQdeviceIPv6Persisted_OnlyTheGuestsOwnRendererCounts(t *testing.T) {
	assert.True(t, qdeviceIPv6Persisted("/etc/network/interfaces.d/lab-ipv6\n", false))
	assert.True(t, qdeviceIPv6Persisted("/etc/netplan/70-netplan-set.yaml\n", true))
	assert.False(t, qdeviceIPv6Persisted("/etc/network/interfaces.d/lab-ipv6\n", true),
		"a netplan guest does not read /etc/network/interfaces.d")
	assert.False(t, qdeviceIPv6Persisted("/etc/netplan/60-lab-ipv6.yaml\n", false),
		"a guest with no netplan installed does not read /etc/netplan")
	assert.False(t, qdeviceIPv6Persisted("    inet6 fd10::f/48 scope global\n", false))
	assert.False(t, qdeviceIPv6Persisted("", false))
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
		fmt.Sprintf(`--origin-hint %s 'ethernets.ens18.addresses=["10.10.1.15/16","%s/48"]'`,
			qdeviceNetplanOriginHint, addr6),
		"the existing IPv4 address is re-stated, into a file of this command's own")
	assert.Contains(t, setArgs, fmt.Sprintf(`{"to":"::/0","via":"%s"}`, gw6), "the v6 default route is written too")
	assert.Contains(t, setArgs, "netplan generate", "the written config is validated, never applied")
	assert.NotContains(t, setArgs, "netplan apply",
		"applying would re-render the very interface this command is reached over")
}

// TestQdeviceAdd_NetplanGuest_ExistingRoutesAreMerged pins what happens to
// an interface that already declares routes: the IPv4 default route it
// carries is re-stated beside the new IPv6 one, so the guest keeps its v4
// gateway even on a netplan build where a later file's list replaces the
// earlier one rather than merging into it.
func TestQdeviceAdd_NetplanGuest_ExistingRoutesAreMerged(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	lab, path := qdeviceIPv6TestLab(t, f)
	cmd, _ := buildGuestSSHAndAPICmd(t, path, f, newQdeviceCmd())

	gw6, err := labMgmtGateway6(lab.Network)
	require.NoError(t, err)

	fake := exec.Fake(
		exec.FakeResponse{Stdout: samplePvecmStatusWithQdevice},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: qdeviceNetplanMarker + "\n"},
		exec.FakeResponse{Stdout: "2: ens18    inet 10.10.1.15/16 scope global ens18"},
		exec.FakeResponse{},
		exec.FakeResponse{Stdout: "addresses:\n- 10.10.1.15/16\n"},
		exec.FakeResponse{Stdout: "- 10.10.1.15/16\n"},
		exec.FakeResponse{Stdout: "- to: default\n  via: 10.10.1.1\n"}, // routes already declared
		exec.FakeResponse{}, // netplan set + generate
		exec.FakeResponse{},
		exec.FakeResponse{},
	)
	cli.GetDeps(cmd).Runner = fake

	_, err = runGuestCmd(t, cmd, "add", "wayne")
	require.NoError(t, err)

	setArgs := fmt.Sprintf("%v", fake.Calls[8].Args)
	assert.Contains(t, setArgs, `{"to":"default","via":"10.10.1.1"}`,
		"the guest's IPv4 default route survives into the written list")
	assert.Contains(t, setArgs, fmt.Sprintf(`{"to":"::/0","via":"%s"}`, gw6))
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

// TestQdeviceAdd_NetplanGuestWithOnlyIfupdownDropIn_Rewrites covers the
// upgrade path a lab built before the netplan writer is actually in: the
// guest is live and routed, and /etc/network/interfaces.d/lab-ipv6 holds
// the address — but netplan renders this guest and never reads that file,
// so the address is gone at the next boot. The run must repair it through
// netplan rather than reading the stale drop-in as proof of convergence.
func TestQdeviceAdd_NetplanGuestWithOnlyIfupdownDropIn_Rewrites(t *testing.T) {
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
		// Live, routed, and persisted — but only in the file a netplan
		// guest ignores.
		exec.FakeResponse{Stdout: qdeviceIPv6ConvergedProbe(addr6, gw6) + qdeviceNetplanMarker + "\n"},
		exec.FakeResponse{Stdout: "2: ens18    inet 10.10.1.15/16 scope global ens18"},
		exec.FakeResponse{}, // live ip -6 apply
		exec.FakeResponse{Stdout: "addresses:\n- 10.10.1.15/16\n"},
		exec.FakeResponse{Stdout: "- 10.10.1.15/16\n"},
		exec.FakeResponse{Stdout: "null\n"},
		exec.FakeResponse{}, // netplan set + generate
		exec.FakeResponse{},
		exec.FakeResponse{},
	)
	cli.GetDeps(cmd).Runner = fake

	out, err := runGuestCmd(t, cmd, "add", "wayne")
	require.NoError(t, err)
	assert.Regexp(t, `ensure IPv6[^\n]*done`, out,
		"an ifupdown drop-in is not persistence on a netplan guest, so the row must not skip")

	require.Len(t, fake.Calls, 11)
	setArgs := fmt.Sprintf("%v", fake.Calls[8].Args)
	assert.Contains(t, setArgs, fmt.Sprintf(`%s/48`, addr6))
	assert.Contains(t, setArgs, "netplan set")
}

// TestQdeviceNetplanFallbackFile_CarriesFullAddressAndRouteLists pins the
// shape of the file written when `netplan set` is unavailable: its keys win
// the merge on a netplan build that replaces rather than appends, so it must
// carry every address and every route the interface had, not only the IPv6
// ones.
func TestQdeviceNetplanFallbackFile_CarriesFullAddressAndRouteLists(t *testing.T) {
	routes := `[{"to":"default","via":"10.10.1.1"},{"to":"::/0","via":"fd10::1"}]`
	doc, err := qdeviceNetplanFallbackFile("ens18", []string{"10.10.1.15/16", "fd10::f/48"}, routes)
	require.NoError(t, err)

	var parsed struct {
		Network struct {
			Version   int `yaml:"version"`
			Ethernets map[string]struct {
				Addresses []string            `yaml:"addresses"`
				Routes    []map[string]string `yaml:"routes"`
			} `yaml:"ethernets"`
		} `yaml:"network"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(doc), &parsed))
	assert.Equal(t, 2, parsed.Network.Version)
	eth := parsed.Network.Ethernets["ens18"]
	assert.Equal(t, []string{"10.10.1.15/16", "fd10::f/48"}, eth.Addresses)
	assert.Equal(t, []map[string]string{
		{"to": "default", "via": "10.10.1.1"},
		{"to": "::/0", "via": "fd10::1"},
	}, eth.Routes)

	noRoutes, err := qdeviceNetplanFallbackFile("ens18", []string{"10.10.1.15/16"}, "")
	require.NoError(t, err)
	assert.NotContains(t, noRoutes, "routes:")
}

// TestQdeviceNetplanRoutesValue_MergesRatherThanReplaces covers the routes
// half of the write. The guest's existing IPv4 default route has to survive
// into the value written, since a netplan build that lets a later file
// replace the list would otherwise leave the guest with no IPv4 gateway
// after a reboot.
func TestQdeviceNetplanRoutesValue_MergesRatherThanReplaces(t *testing.T) {
	value, note := qdeviceNetplanRoutesValue("- to: \"default\"\n  via: \"10.10.1.1\"\n", "fd10::1", "ens18")
	assert.Empty(t, note)
	assert.JSONEq(t, `[{"to":"default","via":"10.10.1.1"},{"to":"::/0","via":"fd10::1"}]`, value)

	unset, note := qdeviceNetplanRoutesValue("null\n", "fd10::1", "ens18")
	assert.Empty(t, note)
	assert.JSONEq(t, `[{"to":"::/0","via":"fd10::1"}]`, unset)
}

// TestQdeviceNetplanRoutesValue_AlreadyDeclaredIsNotDuplicated pins
// idempotence: a second run must not append the same ::/0 route again.
func TestQdeviceNetplanRoutesValue_AlreadyDeclaredIsNotDuplicated(t *testing.T) {
	in := "- to: \"default\"\n  via: \"10.10.1.1\"\n- to: \"::/0\"\n  via: \"fd10::1\"\n"
	value, note := qdeviceNetplanRoutesValue(in, "fd10::1", "ens18")
	assert.Empty(t, note)
	assert.JSONEq(t, `[{"to":"default","via":"10.10.1.1"},{"to":"::/0","via":"fd10::1"}]`, value)
}

// TestQdeviceNetplanRoutesValue_UnparseableIsLeftAlone pins the refusal: a
// routes list this code cannot read, or one carrying a quote that would end
// the shell argument it travels in, is never rewritten — the operator is
// told to add the route instead.
func TestQdeviceNetplanRoutesValue_UnparseableIsLeftAlone(t *testing.T) {
	value, note := qdeviceNetplanRoutesValue("- to: \"default\"\n   via: [unbalanced\n", "fd10::1", "ens18")
	assert.Empty(t, value)
	assert.Contains(t, note, "by hand")

	value, note = qdeviceNetplanRoutesValue("- to: \"default\"\n  via: \"10.10.1.1'x\"\n", "fd10::1", "ens18")
	assert.Empty(t, value)
	assert.Contains(t, note, "by hand")
}
