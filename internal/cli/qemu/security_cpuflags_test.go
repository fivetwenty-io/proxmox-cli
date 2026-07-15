package qemu

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestSecurityCpuFlagsSet_EnableDisableComposesFlags(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"cpu": "host,hidden=1"})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "cpu-flags", "set", "100",
		"--enable", "spec-ctrl,ssbd", "--disable", "pcid", "--force"))

	require.Equal(t, "host,hidden=1,flags=+spec-ctrl;+ssbd;-pcid", parseForm(t, body).Get("cpu"),
		"cputype and hidden must round-trip while flags= is composed")
}

func TestSecurityCpuFlagsSet_UnknownFlagRejectedWithDidYouMean(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "cpu-flags", "set", "100", "--enable", "spec-ctrl2")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown CPU flag")
	require.Contains(t, err.Error(), `did you mean "spec-ctrl"`)
}

func TestSecurityCpuFlagsSet_ClearRemovesOnlyFlagsPair(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"cpu": "host,flags=+spec-ctrl,hidden=1"})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "cpu-flags", "set", "100", "--clear"))
	require.Equal(t, "host,hidden=1", parseForm(t, body).Get("cpu"))
}

func TestSecurityCpuFlagsSet_NoCPUKeyComposesFlagsOnly(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "cpu-flags", "set", "100", "--enable", "aes"))
	require.Equal(t, "flags=+aes", parseForm(t, body).Get("cpu"))
}

// TestSecurityCpuFlagsSet_MitigationDisableGate is a regression pillar for the
// --force gate on disabling a mitigation flag.
func TestSecurityCpuFlagsSet_MitigationDisableGate(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "cpu-flags", "set", "100", "--disable", "spec-ctrl")
	require.Error(t, err)
	require.Contains(t, err.Error(), "without --force")
}

// TestSecurityCpuFlagsSet_DuplicateNamesDeduped is a regression for A1:
// "--enable spec-ctrl,spec-ctrl" (and the same for --disable) must produce a
// single +spec-ctrl token, not a doubled, malformed flags= value.
func TestSecurityCpuFlagsSet_DuplicateNamesDeduped(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "cpu-flags", "set", "100",
		"--enable", "spec-ctrl,spec-ctrl", "--disable", "pcid,pcid"))

	require.Equal(t, "flags=+spec-ctrl;-pcid", parseForm(t, body).Get("cpu"))
	out := buf.String()
	require.Equal(t, 1, strings.Count(out, "+spec-ctrl"), "message must list the flag once")
	require.Equal(t, 1, strings.Count(out, "-pcid"), "message must list the flag once")
}

func TestSecurityCpuFlagsSet_SameFlagInBothListsError(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "cpu-flags", "set", "100", "--enable", "aes", "--disable", "aes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "named in both")
}

func TestSecurityCpuFlagsDescribe_OfflineNoClient(t *testing.T) {
	// No HTTP handler is registered at all; a live client call would panic on
	// an unhandled route or return a connection-refused error. Success here
	// proves the command never touches deps.API.
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "cpu-flags", "describe"))
	require.Contains(t, buf.String(), "spec-ctrl")
	require.Contains(t, buf.String(), "Spectre")
}
