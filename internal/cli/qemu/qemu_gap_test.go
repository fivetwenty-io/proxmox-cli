package qemu

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// ---------------------------------------------------------------------------
// M6 — --autostart on create and config set
// ---------------------------------------------------------------------------

// TestQemuGap_AutostartCreate asserts --autostart reaches the POST body on create.
func TestQemuGap_AutostartCreate(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100", "--autostart"))
	require.Equal(t, "1", parseForm(t, body).Get("autostart"))
}

// TestQemuGap_AutostartAbsentCreate asserts autostart is NOT sent when the flag is absent.
func TestQemuGap_AutostartAbsentCreate(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100"))
	require.Empty(t, parseForm(t, body).Get("autostart"))
}

// TestQemuGap_AutostartConfigSet asserts --autostart reaches the PUT body on config set.
func TestQemuGap_AutostartConfigSet(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--autostart"))
	require.Equal(t, "1", parseForm(t, body).Get("autostart"))
}

// TestQemuGap_AutostartAbsentConfigSet asserts autostart is NOT sent when flag is absent.
func TestQemuGap_AutostartAbsentConfigSet(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--name", "testvm"))
	require.Empty(t, parseForm(t, body).Get("autostart"))
}

// ---------------------------------------------------------------------------
// M7 — --cdrom on create and config set
// ---------------------------------------------------------------------------

// TestQemuGap_CdromCreate asserts --cdrom reaches the POST body on create.
func TestQemuGap_CdromCreate(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100", "--cdrom", "local:iso/debian.iso,media=cdrom"))
	require.Equal(t, "local:iso/debian.iso,media=cdrom", parseForm(t, body).Get("cdrom"))
}

// TestQemuGap_CdromAbsentCreate asserts cdrom is NOT sent when flag is absent on create.
func TestQemuGap_CdromAbsentCreate(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100"))
	require.Empty(t, parseForm(t, body).Get("cdrom"))
}

// TestQemuGap_CdromConfigSet asserts --cdrom reaches the PUT body on config set.
func TestQemuGap_CdromConfigSet(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--cdrom", "local:iso/debian.iso,media=cdrom"))
	require.Equal(t, "local:iso/debian.iso,media=cdrom", parseForm(t, body).Get("cdrom"))
}

// TestQemuGap_CdromAbsentConfigSet asserts cdrom is NOT sent when flag is absent on config set.
func TestQemuGap_CdromAbsentConfigSet(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--name", "testvm"))
	require.Empty(t, parseForm(t, body).Get("cdrom"))
}

// ---------------------------------------------------------------------------
// M8 — --crypted on agent set-user-password
// ---------------------------------------------------------------------------

// TestQemuGap_AgentSetUserPasswordCrypted asserts --crypted reaches the POST body.
func TestQemuGap_AgentSetUserPasswordCrypted(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/set-user-password",
		func(w http.ResponseWriter, r *http.Request) {
			body = readBody(t, r)
			testhelper.WriteData(w, nil)
		})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	stdin := strings.NewReader("$6$rounds=4096$abc$hashedpw\n")
	require.NoError(t, runWithStdin(deps, &buf, stdin,
		"agent", "set-user-password", "100",
		"--username", "root", "--yes", "--crypted"))
	require.Equal(t, "1", parseForm(t, body).Get("crypted"))
}

// TestQemuGap_AgentSetUserPasswordCryptedAbsent asserts crypted is NOT sent when flag absent.
func TestQemuGap_AgentSetUserPasswordCryptedAbsent(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/agent/set-user-password",
		func(w http.ResponseWriter, r *http.Request) {
			body = readBody(t, r)
			testhelper.WriteData(w, nil)
		})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	stdin := strings.NewReader("plaintextpassword\n")
	require.NoError(t, runWithStdin(deps, &buf, stdin,
		"agent", "set-user-password", "100",
		"--username", "root", "--yes"))
	require.Empty(t, parseForm(t, body).Get("crypted"))
}

// ---------------------------------------------------------------------------
// M9 — command tree registration for cpu list, machine list, cpu-flags
// ---------------------------------------------------------------------------

// TestQemuGap_DiscoveryCommandTree verifies the new sub-commands are registered.
func TestQemuGap_DiscoveryCommandTree(t *testing.T) {
	root := Group(&cli.Deps{})

	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["cpu"], "qemu must expose cpu sub-group")
	require.True(t, names["machine"], "qemu must expose machine sub-group")
	require.True(t, names["cpu-flags"], "qemu must expose cpu-flags command")

	var cpuCmd, machineCmd *cobra.Command
	for _, c := range root.Commands() {
		switch c.Name() {
		case "cpu":
			cpuCmd = c
		case "machine":
			machineCmd = c
		}
	}
	require.NotNil(t, cpuCmd)
	require.NotNil(t, machineCmd)

	cpuSubs := make(map[string]bool)
	for _, c := range cpuCmd.Commands() {
		cpuSubs[c.Name()] = true
	}
	require.True(t, cpuSubs["list"], "qemu cpu must expose list")

	machineSubs := make(map[string]bool)
	for _, c := range machineCmd.Commands() {
		machineSubs[c.Name()] = true
	}
	require.True(t, machineSubs["list"], "qemu machine must expose list")
}

// TestQemuGap_CpuListRequest asserts `qemu cpu list` calls the correct endpoint.
func TestQemuGap_CpuListRequest(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/capabilities/qemu/cpu",
		func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			testhelper.WriteData(w, []any{map[string]any{"name": "host"}})
		})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "cpu", "list"))
	require.Equal(t, "/api2/json/nodes/pve1/capabilities/qemu/cpu", gotPath)
	require.Contains(t, buf.String(), "host")
}

// TestQemuGap_MachineListRequest asserts `qemu machine list` calls the correct endpoint.
func TestQemuGap_MachineListRequest(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/capabilities/qemu/machines",
		func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			testhelper.WriteData(w, []any{map[string]any{"id": "q35"}})
		})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "machine", "list"))
	require.Equal(t, "/api2/json/nodes/pve1/capabilities/qemu/machines", gotPath)
	require.Contains(t, buf.String(), "q35")
}

// TestQemuGap_CpuFlagsRequest asserts `qemu cpu-flags` calls the correct endpoint.
func TestQemuGap_CpuFlagsRequest(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/capabilities/qemu/cpu-flags",
		func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			testhelper.WriteData(w, []any{map[string]any{"id": "aes"}})
		})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "cpu-flags"))
	require.Equal(t, "/api2/json/nodes/pve1/capabilities/qemu/cpu-flags", gotPath)
	require.Contains(t, buf.String(), "aes")
}

// TestQemuGap_ListCluster asserts `qemu list --cluster` reads the cluster
// resource inventory (GET /cluster/resources?type=vm) — /cluster/qemu is only
// a directory index (cpu-flags, custom-cpu-models), not a VM list — and keeps
// only qemu guests out of the mixed qemu+lxc response.
func TestQemuGap_ListCluster(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath, gotType string
	f.HandleFunc("GET /api2/json/cluster/resources", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotType = r.URL.Query().Get("type")
		testhelper.WriteData(w, []any{
			map[string]any{"vmid": 100, "name": "vm-cluster", "status": "running", "node": "pve2", "type": "qemu"},
			map[string]any{"vmid": 200, "name": "ct-cluster", "status": "running", "node": "pve2", "type": "lxc"},
		})
	})
	// No node required for --cluster mode.
	deps := depsFor(t, ac, output.FormatTable, "", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "list", "--cluster"))
	require.Equal(t, "/api2/json/cluster/resources", gotPath)
	require.Equal(t, "vm", gotType)
	require.Contains(t, buf.String(), "vm-cluster")
	require.NotContains(t, buf.String(), "ct-cluster", "lxc guests must be filtered out of qemu list")
}

// TestQemuGap_ListClusterAbsent asserts without --cluster the node-scoped endpoint is used.
func TestQemuGap_ListClusterAbsent(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{"vmid": 101, "name": "local-vm", "status": "stopped"},
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "list"))
	require.Equal(t, "/api2/json/nodes/pve1/qemu", gotPath)
	require.Contains(t, buf.String(), "local-vm")
}

// ---------------------------------------------------------------------------
// L3 — advanced flags on start and stop
// ---------------------------------------------------------------------------

// TestQemuGap_StartAdvancedFlags asserts migration-network, migration-type,
// targetstorage, nets-host-mtu, and with-conntrack-state reach the POST body.
func TestQemuGap_StartAdvancedFlags(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/start",
		func(w http.ResponseWriter, r *http.Request) {
			body = readBody(t, r)
			testhelper.WriteData(w, validUPID)
		})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "start", "100",
		"--migration-network", "10.0.0.0/24",
		"--migration-type", "insecure",
		"--targetstorage", "local-lvm",
		"--nets-host-mtu", "virtio0=1500",
		"--with-conntrack-state"))
	form := parseForm(t, body)
	require.Equal(t, "10.0.0.0/24", form.Get("migration_network"))
	require.Equal(t, "insecure", form.Get("migration_type"))
	require.Equal(t, "local-lvm", form.Get("targetstorage"))
	require.Equal(t, "virtio0=1500", form.Get("nets-host-mtu"))
	require.Equal(t, "1", form.Get("with-conntrack-state"))
}

// TestQemuGap_StartAdvancedFlagsAbsent asserts advanced flags are NOT sent when absent.
func TestQemuGap_StartAdvancedFlagsAbsent(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/start",
		func(w http.ResponseWriter, r *http.Request) {
			body = readBody(t, r)
			testhelper.WriteData(w, validUPID)
		})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "start", "100"))
	form := parseForm(t, body)
	require.Empty(t, form.Get("migration_network"))
	require.Empty(t, form.Get("migration_type"))
	require.Empty(t, form.Get("targetstorage"))
	require.Empty(t, form.Get("nets-host-mtu"))
	require.Empty(t, form.Get("with-conntrack-state"))
}

// TestQemuGap_StopMigratedfrom asserts --migratedfrom reaches the POST body on stop.
func TestQemuGap_StopMigratedfrom(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/stop",
		func(w http.ResponseWriter, r *http.Request) {
			body = readBody(t, r)
			testhelper.WriteData(w, validUPID)
		})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "stop", "100", "--migratedfrom", "pve2"))
	require.Equal(t, "pve2", parseForm(t, body).Get("migratedfrom"))
}

// TestQemuGap_StopMigratedfromAbsent asserts migratedfrom is NOT sent when absent.
func TestQemuGap_StopMigratedfromAbsent(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu/100/status/stop",
		func(w http.ResponseWriter, r *http.Request) {
			body = readBody(t, r)
			testhelper.WriteData(w, validUPID)
		})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "stop", "100"))
	require.Empty(t, parseForm(t, body).Get("migratedfrom"))
}
