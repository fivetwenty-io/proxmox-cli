package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// tapeKeyConfigPath is the base /config/tape-encryption-keys endpoint
// (`tape key` CRUD).
const tapeKeyConfigPath = "/api2/json/config/tape-encryption-keys"

// tapeKeyFingerprint is the sample fingerprint reused across `tape key` tests.
const tapeKeyFingerprint = "aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99:" +
	"aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99"

// --- tape key ls -----------------------------------------------------------

func TestTapeKeyLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+tapeKeyConfigPath, []map[string]any{
		{"fingerprint": "zz:11", "kdf": "scrypt", "created": 1700000000, "modified": 1700000001, "hint": "second key"},
		{"fingerprint": "aa:00", "kdf": "pbkdf2", "created": 1690000000, "modified": 1690000001, "hint": "first key"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "ls")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "aa:00")
	require.Contains(t, out, "zz:11")
	require.Contains(t, out, "scrypt")
	require.Contains(t, out, "pbkdf2")
}

func TestTapeKeyLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+tapeKeyConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list tape encryption keys")
}

// --- tape key show -----------------------------------------------------------

func TestTapeKeyShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+tapeKeyConfigPath+"/"+tapeKeyFingerprint, map[string]any{
		"fingerprint": tapeKeyFingerprint, "kdf": "scrypt", "created": 1700000000, "modified": 1700000001,
		"hint": "backup key",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "show", tapeKeyFingerprint)
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "backup key")
	require.Contains(t, out, "scrypt")
}

func TestTapeKeyShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+tapeKeyConfigPath+"/"+tapeKeyFingerprint, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such key")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "show", tapeKeyFingerprint)
	require.Error(t, err)
	require.Contains(t, err.Error(), "show tape encryption key")
}

// --- tape key add ------------------------------------------------------------
//
// CreateTapeEncryptionKeysResponse is a json.RawMessage alias holding the new
// key's sha256 fingerprint as a JSON string. These tests confirm that string
// is decoded and rendered prominently, mirroring `user token add`'s secret
// rendering.

func TestTapeKeyAdd_RendersFingerprint(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapeKeyConfigPath, &rec, tapeKeyFingerprint)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "add", "--password", "s3cret", "--hint", "my hint")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapeKeyConfigPath, rec.path)
	require.Equal(t, "s3cret", rec.form.Get("password"))
	require.Equal(t, "my hint", rec.form.Get("hint"))
	require.Contains(t, buf.String(), tapeKeyFingerprint)
}

func TestTapeKeyAdd_RequiresPassword(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "add")
	require.Error(t, err)
}

func TestTapeKeyAdd_RejectsInvalidKdf(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "add", "--password", "s3cret", "--kdf", "sideways")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--kdf")
}

func TestTapeKeyAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapeKeyConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid key")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "add", "--password", "s3cret")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create tape encryption key")
}

func TestTapeKeyAdd_RejectsEmptyFingerprintResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("POST "+tapeKeyConfigPath, "")

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "add", "--password", "s3cret")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty fingerprint")
}

func TestTapeKeyAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapeKeyConfigPath, &rec, tapeKeyFingerprint)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "add",
		"--password", "s3cret",
		"--hint", "audit hint",
		"--kdf", "pbkdf2",
		"--key", `{"kdf":"pbkdf2"}`,
	)
	require.NoError(t, err)

	want := map[string]string{
		"password": "s3cret",
		"hint":     "audit hint",
		"kdf":      "pbkdf2",
		"key":      `{"kdf":"pbkdf2"}`,
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

// --- tape key update -----------------------------------------------------

func TestTapeKeyUpdate_UpdatesKey(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+tapeKeyConfigPath+"/"+tapeKeyFingerprint, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "update", tapeKeyFingerprint,
		"--hint", "new hint", "--new-password", "newpw", "--password", "oldpw", "--digest", "abc123")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, tapeKeyConfigPath+"/"+tapeKeyFingerprint, rec.path)
	require.Equal(t, "new hint", rec.form.Get("hint"))
	require.Equal(t, "newpw", rec.form.Get("new-password"))
	require.Equal(t, "oldpw", rec.form.Get("password"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.Contains(t, buf.String(), "updated")
}

func TestTapeKeyUpdate_RequiresHintAndNewPassword(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "update", tapeKeyFingerprint)
	require.Error(t, err)
}

func TestTapeKeyUpdate_WithForceOmitsPassword(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+tapeKeyConfigPath+"/"+tapeKeyFingerprint, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "update", tapeKeyFingerprint,
		"--hint", "reset hint", "--new-password", "newpw", "--force")
	require.NoError(t, err)
	require.Equal(t, "1", rec.form.Get("force"))
	_, present := rec.form["password"]
	require.False(t, present, "password must be omitted when unset even with --force")
}

func TestTapeKeyUpdate_RejectsInvalidKdf(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "update", tapeKeyFingerprint,
		"--hint", "h", "--new-password", "p", "--kdf", "sideways")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--kdf")
}

func TestTapeKeyUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+tapeKeyConfigPath+"/"+tapeKeyFingerprint, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "update", tapeKeyFingerprint,
		"--hint", "h", "--new-password", "p")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update tape encryption key")
}

// --- tape key delete -----------------------------------------------------

func TestTapeKeyDelete_DeletesKey(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+tapeKeyConfigPath+"/"+tapeKeyFingerprint, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "delete", tapeKeyFingerprint, "--digest", "abc123", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, tapeKeyConfigPath+"/"+tapeKeyFingerprint, rec.path)
	require.Equal(t, "abc123", rec.query.Get("digest"))
	require.Contains(t, buf.String(), "deleted")
}

func TestTapeKeyDelete_RequiresYes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+tapeKeyConfigPath+"/"+tapeKeyFingerprint, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "delete", tapeKeyFingerprint)
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Contains(t, err.Error(), "--yes/-y")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

func TestTapeKeyDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+tapeKeyConfigPath+"/"+tapeKeyFingerprint, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeKeyCmd(), "key", "delete", tapeKeyFingerprint, "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete tape encryption key")
}
