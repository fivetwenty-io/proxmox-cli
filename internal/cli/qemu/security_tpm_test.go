package qemu

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestSecurityTpmAdd_ExplicitVersionBody(t *testing.T) {
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
	require.NoError(t, run(deps, &buf, "security", "tpm", "add", "100", "--storage", "local-lvm"))

	require.Equal(t, "local-lvm:1,version=v2.0", parseForm(t, body).Get("tpmstate0"),
		"CLI default v2.0 must be explicit in the composed string even though the API default is v1.2")
}

func TestSecurityTpmAdd_VersionChangeRefusal(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"tpmstate0": "local-lvm:0,version=v1.2"})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "tpm", "add", "100", "--storage", "local-lvm", "--version", "v2.0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "version cannot be changed in place")
	require.Contains(t, err.Error(), "tpm remove 100 --force")
}

func TestSecurityTpmAdd_SameVersionNoOp(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"tpmstate0": "local-lvm:0,version=v2.0"})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "tpm", "add", "100", "--storage", "local-lvm"))
	require.Contains(t, buf.String(), "no change")
}

// TestSecurityTpmShow_AbsentSubKeyIsV12 verifies the absent-version-sub-key
// semantics: no version= present means the PVE default v1.2 applies.
func TestSecurityTpmShow_AbsentSubKeyIsV12(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"tpmstate0": "local-lvm:0"})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "tpm", "show", "100"))
	require.Contains(t, buf.String(), "v1.2")
}

func TestSecurityTpmRemove_DigestPassthrough(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"tpmstate0": "local-lvm:0,version=v2.0", "digest": "auto"})
	})
	stubStoppedStatus(f, "100")
	var body string
	f.HandleFunc("PUT /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		testhelper.WriteData(w, nil)
	})

	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "tpm", "remove", "100", "--force", "--digest", "override"))

	form := parseForm(t, body)
	require.Equal(t, "tpmstate0", form.Get("delete"))
	require.Equal(t, "override", form.Get("digest"))
	require.Contains(t, buf.String(), "WARNING: destroying TPM state")
}

func TestSecurityTpmRemove_RequiresForce(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"tpmstate0": "local-lvm:0,version=v2.0"})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	err := run(deps, &buf, "security", "tpm", "remove", "100")
	require.Error(t, err)
	require.Contains(t, err.Error(), "without --force")
}

func TestSecurityTpmRemove_AbsentNoOp(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/qemu/100/config", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{})
	})
	deps := depsFor(t, ac, output.FormatTable, "pve1", false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "security", "tpm", "remove", "100", "--force"))
	require.Contains(t, buf.String(), "has no TPM state device; no change.")
}
