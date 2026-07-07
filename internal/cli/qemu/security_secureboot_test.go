package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestSecuritySecurebootShow_Postures(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]any
		want    string
		noteMsg bool
	}{
		{"legacy", map[string]any{}, "legacy-bios", false},
		{"ovmf no efidisk", map[string]any{"bios": "ovmf"}, "ovmf-no-efidisk", true},
		{"efi no keys", map[string]any{"bios": "ovmf", "efidisk0": "local-lvm:1,efitype=2m"}, "efi-no-keys", false},
		{
			"pre-enrolled",
			map[string]any{"bios": "ovmf", "efidisk0": "local-lvm:1,efitype=4m,pre-enrolled-keys=1"},
			"pre-enrolled", false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, ac := newFakeClient(t)
			f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
				testhelper.WriteData(w, tc.config)
			})
			deps := depsFor(t, ac, output.FormatTable, "pve1", false)
			var buf bytes.Buffer
			require.NoError(t, run(deps, &buf, "security", "secureboot", "show", "100"))
			require.Contains(t, buf.String(), tc.want)
			if tc.noteMsg {
				require.Contains(t, buf.String(), "note: bios=ovmf without an efidisk0")
			}
		})
	}
}

// TestSecuritySecurebootEnable_FreshSinglePUT verifies a fresh enable composes
// bios=ovmf and efidisk0=... in one PUT.
func TestSecuritySecurebootEnable_FreshSinglePUT(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"digest": "d1"})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "secureboot", "enable", "100", "--storage", "local-lvm"))

	form := parseForm(t, body)
	require.Equal(t, "ovmf", form.Get("bios"))
	require.Equal(t, "local-lvm:1,efitype=4m,pre-enrolled-keys=1", form.Get("efidisk0"))
	require.Contains(t, buf.String(), "WARNING: switching VM 100 from SeaBIOS to OVMF")
}

func TestSecuritySecurebootEnable_MissingStorageError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "secureboot", "enable", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--storage is required")
}

func TestSecuritySecurebootEnable_ExistingRefusalWithoutRecreate(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"bios": "ovmf", "efidisk0": "local-lvm:1,efitype=2m",
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "secureboot", "enable", "100", "--storage", "local-lvm")
	require.Error(t, err)
	require.Contains(t, err.Error(), "refusing to replace")
	require.Contains(t, err.Error(), "--recreate")
}

func TestSecuritySecurebootEnable_RecreateBody(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"bios": "ovmf", "efidisk0": "local-lvm:1,efitype=2m",
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
	require.NoError(t, run(deps, &buf, "security", "secureboot", "enable", "100", "--recreate"))

	form := parseForm(t, body)
	require.Equal(t, "local-lvm:1,efitype=4m,pre-enrolled-keys=1", form.Get("efidisk0"),
		"recreate reuses storage from the existing volume")
	require.Contains(t, buf.String(), "moved to unused[n]")
}

func TestSecuritySecurebootEnable_AlreadyEnabledNoOp(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"bios": "ovmf", "efidisk0": "local-lvm:1,efitype=4m,pre-enrolled-keys=1",
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	// No --storage/--no-pre-enrolled-keys passed: the only way to reach a
	// genuine no-op, since either of those flags now takes priority over the
	// no-op and forces the --recreate refusal (see the test below).
	require.NoError(t, run(deps, &buf, "security", "secureboot", "enable", "100"))
	require.Contains(t, buf.String(), "no change")
}

// TestSecuritySecurebootEnable_StorageWithoutRecreateRefusesEvenAtPosture is a
// regression for A4: passing --storage against an already-correctly-configured
// efidisk0 must not silently no-op and ignore --storage. Without --recreate it
// must refuse, the same as any other existing-efidisk0 case.
func TestSecuritySecurebootEnable_StorageWithoutRecreateRefusesEvenAtPosture(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"bios": "ovmf", "efidisk0": "local-lvm:1,efitype=4m,pre-enrolled-keys=1",
		})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "secureboot", "enable", "100", "--storage", "local-lvm")
	require.Error(t, err)
	require.Contains(t, err.Error(), "refusing to replace")
	require.Contains(t, err.Error(), "--recreate")
}

// TestSecuritySecurebootEnable_MsCertInPlacePreservesVerbatim is a regression
// pillar: editing ms-cert alone must not disturb file=/size=.
func TestSecuritySecurebootEnable_MsCertInPlacePreservesVerbatim(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"bios":     "ovmf",
			"efidisk0": "local-lvm:1,efitype=4m,pre-enrolled-keys=1,size=528K",
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
	require.NoError(t, run(deps, &buf, "security", "secureboot", "enable", "100", "--ms-cert", "2023"))

	form := parseForm(t, body)
	require.Equal(t, "local-lvm:1,efitype=4m,pre-enrolled-keys=1,size=528K,ms-cert=2023", form.Get("efidisk0"))
	require.Empty(t, form.Get("bios"), "bios must not be re-sent when already ovmf")
}
