package qemu

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// --- security show ----------------------------------------------------------

func TestSecurityShow_Posture(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"digest":     "abc123",
			"protection": 1,
			"agent":      "1,freeze-fs=0",
			"bios":       "ovmf",
			"efidisk0":   "local-lvm:1,efitype=4m,pre-enrolled-keys=1",
			"tpmstate0":  "local-lvm:0,version=v2.0",
			"amd-sev":    "type=snp,no-debug=1",
			"cpu":        "host,flags=+spec-ctrl;-pcid",
			"net0":       "virtio=AA:BB:CC,bridge=vmbr0,firewall=1",
			"net1":       "virtio=DD:EE:FF,bridge=vmbr0",
			"args":       "-device something",
			"hookscript": "local:snippets/hook.pl",
			"hostpci0":   "0000:01:00,pcie=1",
		})
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/firewall/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"enable": 1, "policy_in": "DROP"})
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "show", "100"))

	out := buf.String()
	require.Contains(t, out, "protection")
	require.Contains(t, out, "true")
	require.Contains(t, out, "pre-enrolled")
	require.Contains(t, out, "WARNING: raw QEMU arguments are set")
	require.Contains(t, out, "WARNING: a hookscript is configured")
}

func TestSecurityShow_JSONRaw(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"digest": "d1"})
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/firewall/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})

	deps := depsFor(t, ac, output.FormatJSON, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "show", "100"))

	var posture securityPosture
	require.NoError(t, json.Unmarshal(buf.Bytes(), &posture))
	require.Equal(t, "100", posture.VMID)
	require.False(t, posture.Protection)
	require.Equal(t, "legacy-bios", posture.Boot.Posture)
	require.Empty(t, posture.Risks)
	// No WARNING/note lines should leak into structured JSON output.
	require.NotContains(t, buf.String(), "WARNING")
}

// TestSecurityShow_SeabiosOmitsEfitype is a regression for A6: a seabios VM
// with no efidisk0 must not report boot.efitype (or the pre-enrolled-keys
// default) in the rendered posture — those fields are only meaningful once an
// EFI vars disk exists.
func TestSecurityShow_SeabiosOmitsEfitype(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/firewall/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "show", "100"))

	out := buf.String()
	require.NotContains(t, out, "boot.efitype")
	require.NotContains(t, out, "boot.pre_enrolled_keys")
	require.Contains(t, out, "boot.bios")
}

func TestSecurityShow_NetEnumeration(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"net0": "virtio=AA:BB:CC,bridge=vmbr0,firewall=1",
			"net1": "virtio=DD:EE:FF,bridge=vmbr1",
		})
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/firewall/options", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})

	deps := depsFor(t, ac, output.FormatJSON, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "show", "100"))

	var posture securityPosture
	require.NoError(t, json.Unmarshal(buf.Bytes(), &posture))
	require.Len(t, posture.NICs, 2)
	require.True(t, posture.NICs[0].Firewall)
	require.False(t, posture.NICs[1].Firewall)
}

func TestSecurityShow_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "denied")
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "security", "show", "100"))
}

// --- security list -----------------------------------------------------------

func TestSecurityList_SortsRiskyFirst(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"type": "qemu", "vmid": 100, "name": "safe", "node": "pve1"},
			map[string]any{"type": "qemu", "vmid": 101, "name": "risky", "node": "pve1"},
			map[string]any{"type": "lxc", "vmid": 200, "name": "ct", "node": "pve1"},
		})
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"args": "-foo"})
	})

	deps := depsFor(t, ac, output.FormatJSON, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "list"))

	var rows []securityListRow
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rows))
	require.Len(t, rows, 2)
	require.Equal(t, "101", rows[0].VMID, "risky VM must sort first")
	require.Equal(t, "100", rows[1].VMID)
}

func TestSecurityList_NodeFilter(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"type": "qemu", "vmid": 100, "name": "a", "node": "pve1"},
			map[string]any{"type": "qemu", "vmid": 101, "name": "b", "node": "pve2"},
		})
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})

	deps := depsFor(t, ac, output.FormatJSON, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "list"))

	var rows []securityListRow
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rows))
	require.Len(t, rows, 1)
	require.Equal(t, "100", rows[0].VMID)
}

// TestSecurityList_TolerateOneVMConfigReadFailure is a regression for A2: one
// VM's config read failing (e.g. missing VM.Audit, transient error) must not
// abort the whole cluster-wide audit. The failing VM's row is degraded with
// an error marker and a stderr warning; the other VM's row still renders.
func TestSecurityList_TolerateOneVMConfigReadFailure(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"type": "qemu", "vmid": 100, "name": "ok", "node": "pve1"},
			map[string]any{"type": "qemu", "vmid": 101, "name": "denied", "node": "pve1"},
		})
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/101/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "denied")
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "list"))

	out := buf.String()
	require.Contains(t, out, "warning: skipping VM 101")
	require.Contains(t, out, "100")
	require.Contains(t, out, "ok")
}

func TestSecurityList_NICFWCount(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"type": "qemu", "vmid": 100, "name": "a", "node": "pve1"},
		})
	})
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"net0": "virtio=AA:BB:CC,firewall=1",
			"net1": "virtio=DD:EE:FF",
		})
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "list"))
	require.Contains(t, buf.String(), "1/2")
}

// --- security protection ----------------------------------------------------

func TestSecurityProtectionEnable(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"digest": "d1"})
	})
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "protection", "enable", "100"))

	form := parseForm(t, body)
	require.Equal(t, "1", form.Get("protection"))
	require.Equal(t, "d1", form.Get("digest"), "digest must auto-attach from the read")
	require.Contains(t, buf.String(), "enabled")
}

func TestSecurityProtectionEnable_NoOp(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"protection": 1})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "protection", "enable", "100"))
	require.Contains(t, buf.String(), "already protected; no change.")
}

func TestSecurityProtectionDisable_WarnsAndDigestOverride(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"protection": 1, "digest": "auto-digest"})
	})
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "protection", "disable", "100", "--digest", "explicit-digest"))

	form := parseForm(t, body)
	require.Equal(t, "0", form.Get("protection"))
	require.Equal(t, "explicit-digest", form.Get("digest"), "explicit --digest must override the auto value")
	require.Contains(t, buf.String(), "WARNING: clearing the protection flag")
}

func TestSecurityCommandTree(t *testing.T) {
	root := Group(&cli.Deps{})
	var securityCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "security" {
			securityCmd = c
		}
	}
	require.NotNil(t, securityCmd, "qemu group must register the security sub-command")

	names := make(map[string]bool)
	for _, c := range securityCmd.Commands() {
		names[c.Name()] = true
	}
	want := []string{"show", "list", "protection", "agent", "secureboot", "tpm", "confidential", "cpu-flags", "nic"}
	for _, w := range want {
		require.True(t, names[w], "expected security sub-command %q", w)
	}
}
