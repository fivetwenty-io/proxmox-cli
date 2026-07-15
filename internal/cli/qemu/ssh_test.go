package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// ifaceData returns a guest-agent network-get-interfaces payload with a loopback
// device first and an eth0 carrying the given IPv4 address, exercising the
// loopback-skipping path.
func ifaceData(ipv4 string) map[string]any {
	return map[string]any{
		"result": []any{
			map[string]any{
				"name": "lo",
				"ip-addresses": []any{
					map[string]any{"ip-address-type": "ipv4", "ip-address": "127.0.0.1"},
				},
			},
			map[string]any{
				"name": "eth0",
				"ip-addresses": []any{
					map[string]any{"ip-address-type": "ipv6", "ip-address": "fe80::1"},
					map[string]any{"ip-address-type": "ipv4", "ip-address": ipv4},
				},
			},
		},
	}
}

// lastSSHCall returns the single recorded interactive ssh invocation.
func lastSSHCall(t *testing.T, fr *exec.FakeRunner) exec.Call {
	t.Helper()
	require.Len(t, fr.Calls, 1, "expected exactly one ssh invocation")
	c := fr.Calls[0]
	require.Equal(t, "ssh", c.Name)
	require.True(t, c.Interactive)
	return c
}

// TestQemuSSH_ByName resolves a guest name to its VMID and node via
// cluster/resources, discovers the IP via the guest agent, and connects as root.
func TestQemuSSH_ByName(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"type": "qemu", "vmid": 100, "name": "web-01", "node": "pve1"},
		})
	})
	var agentPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/agent/network-get-interfaces",
		func(w http.ResponseWriter, r *http.Request) {
			agentPath = r.URL.Path
			testhelper.WriteData(w, ifaceData("10.0.0.20"))
		})

	fr := exec.Fake()
	deps := depsFor(t, ac, output.FormatTable, "", false)
	deps.Runner = fr

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ssh", "web-01"))

	require.Equal(t, "/api2/json/nodes/pve1/qemu/100/agent/network-get-interfaces", agentPath)
	c := lastSSHCall(t, fr)
	require.Equal(t, []string{"-p", "22", "root@10.0.0.20"}, c.Args)
}

// TestQemuSSH_NumericVMID connects using a numeric VMID with a configured node,
// taking the resolve fast path (no cluster/resources call needed).
func TestQemuSSH_NumericVMID(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/agent/network-get-interfaces",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, ifaceData("10.0.0.21"))
		})

	fr := exec.Fake()
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	deps.Runner = fr

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ssh", "100"))

	c := lastSSHCall(t, fr)
	require.Equal(t, []string{"-p", "22", "root@10.0.0.21"}, c.Args)
}

// TestQemuSSH_HostOverride verifies --host bypasses the guest agent entirely.
func TestQemuSSH_HostOverride(t *testing.T) {
	f, ac := newFakeClient(t)
	agentCalled := false
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/agent/network-get-interfaces",
		func(w http.ResponseWriter, _ *http.Request) {
			agentCalled = true
			testhelper.WriteData(w, ifaceData("10.0.0.99"))
		})

	fr := exec.Fake()
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	deps.Runner = fr

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ssh", "100", "--host", "10.0.0.9"))

	require.False(t, agentCalled, "guest agent must not be queried when --host is set")
	c := lastSSHCall(t, fr)
	require.Equal(t, []string{"-p", "22", "root@10.0.0.9"}, c.Args)
}

// TestQemuSSH_DuplicateName errors with the ambiguous nodes and never invokes ssh.
func TestQemuSSH_DuplicateName(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"type": "qemu", "vmid": 100, "name": "dup", "node": "pve1"},
			map[string]any{"type": "qemu", "vmid": 200, "name": "dup", "node": "pve2"},
		})
	})

	fr := exec.Fake()
	deps := depsFor(t, ac, output.FormatTable, "", false)
	deps.Runner = fr

	var buf bytes.Buffer
	err := run(deps, &buf, "ssh", "dup")
	require.Error(t, err)
	require.Contains(t, err.Error(), "ambiguous")
	require.Contains(t, err.Error(), "pve1")
	require.Contains(t, err.Error(), "pve2")
	require.Empty(t, fr.Calls, "ssh must not be invoked on an ambiguous target")
}

// TestQemuSSH_FlagParity verifies the connection flags and remote command land
// in the ssh argv in the expected order.
func TestQemuSSH_FlagParity(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/agent/network-get-interfaces",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, ifaceData("10.0.0.30"))
		})

	fr := exec.Fake()
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	deps.Runner = fr

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "ssh", "100",
		"-l", "admin", "-i", "key", "-p", "2222", "--", "uptime"))

	c := lastSSHCall(t, fr)
	require.Equal(t, []string{"-p", "2222", "-i", "key", "admin@10.0.0.30", "uptime"}, c.Args)
}

// TestQemuSSH_NoIPv4 errors and names --host when the agent exposes no usable
// IPv4 address.
func TestQemuSSH_NoIPv4(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/agent/network-get-interfaces",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteData(w, map[string]any{
				"result": []any{
					map[string]any{
						"name": "lo",
						"ip-addresses": []any{
							map[string]any{"ip-address-type": "ipv4", "ip-address": "127.0.0.1"},
						},
					},
					map[string]any{
						"name": "eth0",
						"ip-addresses": []any{
							map[string]any{"ip-address-type": "ipv6", "ip-address": "fe80::1"},
						},
					},
				},
			})
		})

	fr := exec.Fake()
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	deps.Runner = fr

	var buf bytes.Buffer
	err := run(deps, &buf, "ssh", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--host")
	require.Empty(t, fr.Calls, "ssh must not be invoked when no IP is found")
}
