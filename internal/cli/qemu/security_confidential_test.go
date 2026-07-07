package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestSecurityConfidentialSet_SevTypePassthroughUnvalidated(t *testing.T) {
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
	require.NoError(t, run(deps, &buf, "security", "confidential", "set", "100", "--sev", "future-type-xyz"))
	require.Equal(t, "type=future-type-xyz", parseForm(t, body).Get("amd-sev"),
		"sev type values are unvalidated pass-through")
	require.Contains(t, buf.String(), "WARNING: confidential computing restricts")
}

// TestSecurityConfidentialSet_MergePreservesUnknownSubkeys is a regression
// pillar: adding --sev-no-debug must not disturb an existing allow-smt=1.
func TestSecurityConfidentialSet_MergePreservesUnknownSubkeys(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"amd-sev": "type=snp,allow-smt=1"})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "confidential", "set", "100", "--sev-no-debug"))
	require.Equal(t, "type=snp,allow-smt=1,no-debug=1", parseForm(t, body).Get("amd-sev"))
}

func TestSecurityConfidentialSet_SubFlagWithoutPlatformRequiresPlatform(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "confidential", "set", "100", "--sev-no-debug")
	require.Error(t, err)
	require.Contains(t, err.Error(), "require --sev")
}

func TestSecurityConfidentialSet_FamilyMutualExclusion(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "confidential", "set", "100", "--sev", "snp", "--tdx", "std")
	require.Error(t, err)
	require.Contains(t, err.Error(), "mutually exclusive")
}

// TestSecurityConfidentialSet_TdxAttestationDefaultsToZero verifies the
// mandatory-attestation-field rule: a fresh intel-tdx value without
// --tdx-attestation gets attestation=0 explicitly.
func TestSecurityConfidentialSet_TdxAttestationDefaultsToZero(t *testing.T) {
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
	require.NoError(t, run(deps, &buf, "security", "confidential", "set", "100", "--tdx", "std"))
	require.Equal(t, "type=std,attestation=0", parseForm(t, body).Get("intel-tdx"))
	require.Contains(t, buf.String(), "attestation defaulted to 0")
}

// TestSecurityConfidentialSet_PlatformSwapRefused is a regression for A5: the
// command's own help text says to clear the other platform first, so passing
// --tdx against a VM with amd-sev configured must error instead of silently
// swapping platforms in one PUT.
func TestSecurityConfidentialSet_PlatformSwapRefused(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"amd-sev": "type=snp"})
	})
	var putCalled bool
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		putCalled = true
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "confidential", "set", "100", "--tdx", "std")
	require.Error(t, err)
	require.Contains(t, err.Error(), "confidential clear")
	require.False(t, putCalled, "config must be left untouched when the platform swap is refused")
}

// TestSecurityConfidentialSet_PlatformSwapRefusedReverse mirrors
// TestSecurityConfidentialSet_PlatformSwapRefused for the opposite direction
// (intel-tdx configured, --sev requested).
func TestSecurityConfidentialSet_PlatformSwapRefusedReverse(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"intel-tdx": "type=std,attestation=0"})
	})
	var putCalled bool
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		putCalled = true
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "confidential", "set", "100", "--sev", "snp")
	require.Error(t, err)
	require.Contains(t, err.Error(), "confidential clear")
	require.False(t, putCalled, "config must be left untouched when the platform swap is refused")
}

func TestSecurityConfidentialClear_DeletesConfiguredPlatform(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"amd-sev": "type=snp"})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "confidential", "clear", "100"))
	require.Equal(t, "amd-sev", parseForm(t, body).Get("delete"))
	require.Contains(t, buf.String(), "WARNING: clearing amd-sev")
}

func TestSecurityConfidentialClear_NoneConfiguredNoOp(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "confidential", "clear", "100"))
	require.Contains(t, buf.String(), "no confidential-computing configuration; no change.")
}
