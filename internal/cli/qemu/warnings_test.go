package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// --- config set --------------------------------------------------------------

func TestQemuConfigSet_ArgsFlag_Warns(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--args", "-fda /dev/fd0"))
	require.Contains(t, buf.String(), "WARNING: --args passes raw arguments")
}

func TestQemuConfigSet_HookscriptFlag_Warns(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--hookscript", "local:snippets/hook.sh"))
	require.Contains(t, buf.String(), "WARNING: the hookscript executes on the HOST")
}

func TestQemuConfigSet_HostpciFlag_Warns(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--hostpci", "0=0000:01:00,pcie=1"))
	require.Contains(t, buf.String(), "WARNING: PCI passthrough gives the guest direct")
}

func TestQemuConfigSet_ProtectionFalse_Warns(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--protection=false"))
	require.Contains(t, buf.String(), "WARNING: clearing the protection flag")
}

func TestQemuConfigSet_ProtectionTrue_NoWarn(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--protection=true"))
	require.NotContains(t, buf.String(), "WARNING")
}

func TestQemuConfigSet_DeleteProtection_Warns(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--delete", "protection,tags"))
	require.Contains(t, buf.String(), "WARNING: clearing the protection flag")
}

func TestQemuConfigSet_NoRiskyFlags_NoWarn(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--cores", "4"))
	require.NotContains(t, buf.String(), "WARNING")
}

func TestQemuConfigSet_SetArgs_Warns(t *testing.T) {
	f, ac := newFakeClient(t)
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--set", "args=-fda /dev/fd0"))
	require.Contains(t, buf.String(), "WARNING: --args passes raw arguments")
	require.Equal(t, "-fda /dev/fd0", parseForm(t, body).Get("args"))
}

func TestQemuConfigSet_SetProtectionFalse_Warns(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "config", "set", "100", "--set", "protection=0"))
	require.Contains(t, buf.String(), "WARNING: clearing the protection flag")
}

// --- create --------------------------------------------------------------

func TestQemuCreate_ArgsFlag_Warns(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100", "--args", "-fda /dev/fd0"))
	require.Contains(t, buf.String(), "WARNING: --args passes raw arguments")
}

func TestQemuCreate_HostpciFlag_Warns(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100", "--hostpci", "0=0000:01:00,pcie=1"))
	require.Contains(t, buf.String(), "WARNING: PCI passthrough gives the guest direct")
}

func TestQemuCreate_NoRiskyFlags_NoWarn(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("POST /api2/json/nodes/pve1/qemu", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, validUPID)
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", true)

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "create", "100", "--cores", "4"))
	require.NotContains(t, buf.String(), "WARNING")
}
