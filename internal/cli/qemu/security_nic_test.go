package qemu

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestSecurityNicShow_Table(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"net0": "virtio=AA:BB:CC,bridge=vmbr0,firewall=1,tag=100",
		})
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/firewall/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"enable": 1})
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "nic", "show", "100"))
	out := buf.String()
	require.Contains(t, out, "virtio")
	require.Contains(t, out, "AA:BB:CC")
	require.Contains(t, out, "100", "vlan tag column")
}

// TestSecurityNicFirewall_OnOffPreservesOtherSuboptions is the headline
// regression test: flipping firewall must round-trip every other net[n]
// sub-option verbatim.
func TestSecurityNicFirewall_OnOffPreservesOtherSuboptions(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"net0": "virtio=AA:BB:CC,bridge=vmbr0,tag=50,mtu=1500,rate=10,queues=4,link_down=1",
		})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "nic", "firewall", "100", "--on", "--slot", "0"))

	require.Equal(t, "virtio=AA:BB:CC,bridge=vmbr0,tag=50,mtu=1500,rate=10,queues=4,link_down=1,firewall=1",
		parseForm(t, body).Get("net0"))
}

func TestSecurityNicFirewall_OffDeletesSubkey(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"net0": "virtio=AA:BB:CC,bridge=vmbr0,firewall=1",
		})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "nic", "firewall", "100", "--off", "--slot", "0"))

	require.Equal(t, "virtio=AA:BB:CC,bridge=vmbr0", parseForm(t, body).Get("net0"))
	require.Contains(t, buf.String(), "WARNING: disabling the firewall on net0")
}

func TestSecurityNicFirewall_AllFlipsEveryConfiguredNIC(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"net0": "virtio=AA:BB:CC,bridge=vmbr0",
			"net1": "virtio=DD:EE:FF,bridge=vmbr1,firewall=0",
		})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "nic", "firewall", "100", "--on", "--all"))

	form := parseForm(t, body)
	require.Equal(t, "virtio=AA:BB:CC,bridge=vmbr0,firewall=1", form.Get("net0"))
	require.Equal(t, "virtio=DD:EE:FF,bridge=vmbr1,firewall=1", form.Get("net1"))
}

func TestSecurityNicFirewall_MissingSlotError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"net0": "virtio=AA:BB:CC,bridge=vmbr0",
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "nic", "firewall", "100", "--on", "--slot", "3")
	require.Error(t, err)
	require.Contains(t, err.Error(), "has no net3")
	require.Contains(t, err.Error(), "net0")
}

func TestSecurityNicFirewall_NoOpAlreadyAtState(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"net0": "virtio=AA:BB:CC,bridge=vmbr0,firewall=1",
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "nic", "firewall", "100", "--on", "--slot", "0"))
	require.Contains(t, buf.String(), "already enabled")
	require.Contains(t, buf.String(), "no change")
}

// TestSecurityNicFirewall_RepeatedSlotDeduped is a regression for A7: a
// repeated "--slot 0 --slot 0" must report net0 once, not twice, even though
// the PUT itself was already deduped via the netParams map.
func TestSecurityNicFirewall_RepeatedSlotDeduped(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"net0": "virtio=AA:BB:CC,bridge=vmbr0",
		})
	})
	stubStoppedStatus(f, "100")
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "nic", "firewall", "100", "--on", "--slot", "0", "--slot", "0"))
	require.Equal(t, 1, strings.Count(buf.String(), "net0 firewall enabled"),
		"message must list net0 once even though --slot 0 was repeated")
}

func TestSecurityNicFirewall_RequiresOnOrOff(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "nic", "firewall", "100", "--slot", "0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--on or --off")
}
