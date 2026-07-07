package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// encKeyConfigPath is the base /config/encryption-keys endpoint.
const encKeyConfigPath = "/api2/json/config/encryption-keys"

// encKeyId is the sample key ID reused across encryption-key tests.
const encKeyId = "key1"

// --- encryption-key ls ---------------------------------------------------------------

func TestEncKeyLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+encKeyConfigPath, &rec, []map[string]any{
		{"id": "key2", "kdf": "none", "created": 1700000000, "modified": 1700000000},
		{
			"id": "key1", "kdf": "scrypt", "fingerprint": "aa:bb:cc", "hint": "my hint",
			"created": 1690000000, "modified": 1690000001,
		},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newEncryptionKeyCmd(), "encryption-key", "ls", "--include-archived")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, encKeyConfigPath, rec.path)
	require.Equal(t, "1", rec.query.Get("include-archived"))

	out := buf.String()
	require.Contains(t, out, "key1")
	require.Contains(t, out, "key2")
	require.Contains(t, out, "my hint")
}

func TestEncKeyLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+encKeyConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newEncryptionKeyCmd(), "encryption-key", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list encryption keys")
}

// --- encryption-key add ---------------------------------------------------------------

func TestEncKeyAdd_CreatesKey(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+encKeyConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newEncryptionKeyCmd(), "encryption-key", "add", encKeyId)
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, encKeyConfigPath, rec.path)
	require.Equal(t, encKeyId, rec.form.Get("id"))
	_, present := rec.form["key"]
	require.False(t, present, "key must be omitted from the body when unset")
	require.Contains(t, buf.String(), `Encryption key "key1" created.`)
}

func TestEncKeyAdd_WithExistingKey(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+encKeyConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newEncryptionKeyCmd(), "encryption-key", "add", encKeyId, "--key", "rawkeydata")
	require.NoError(t, err)
	require.Equal(t, "rawkeydata", rec.form.Get("key"))
}

func TestEncKeyAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+encKeyConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid key")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newEncryptionKeyCmd(), "encryption-key", "add", encKeyId)
	require.Error(t, err)
	require.Contains(t, err.Error(), "create encryption key")
}

// --- encryption-key delete ---------------------------------------------------------------

func TestEncKeyDelete_DeletesKey(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+encKeyConfigPath+"/"+encKeyId, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newEncryptionKeyCmd(), "encryption-key", "delete", encKeyId, "--digest", "abc123", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, encKeyConfigPath+"/"+encKeyId, rec.path)
	require.Equal(t, "abc123", rec.query.Get("digest"))
	require.Contains(t, buf.String(), `Encryption key "key1" deleted.`)
}

func TestEncKeyDelete_RequiresYes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+encKeyConfigPath+"/"+encKeyId, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newEncryptionKeyCmd(), "encryption-key", "delete", encKeyId)
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Contains(t, err.Error(), "--yes/-y")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

func TestEncKeyDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+encKeyConfigPath+"/"+encKeyId, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newEncryptionKeyCmd(), "encryption-key", "delete", encKeyId, "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete encryption key")
}

// --- encryption-key toggle-archive ---------------------------------------------------------------

func TestEncKeyToggleArchive_TogglesState(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+encKeyConfigPath+"/"+encKeyId, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newEncryptionKeyCmd(), "encryption-key", "toggle-archive", encKeyId, "--digest", "abc123")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, encKeyConfigPath+"/"+encKeyId, rec.path)
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.Contains(t, buf.String(), `Encryption key "key1" archive state toggled.`)
}

func TestEncKeyToggleArchive_OmitsUnsetDigest(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+encKeyConfigPath+"/"+encKeyId, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newEncryptionKeyCmd(), "encryption-key", "toggle-archive", encKeyId)
	require.NoError(t, err)

	_, present := rec.form["digest"]
	require.False(t, present, "digest must be omitted from the body when unset")
}

func TestEncKeyToggleArchive_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+encKeyConfigPath+"/"+encKeyId, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "toggle failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newEncryptionKeyCmd(), "encryption-key", "toggle-archive", encKeyId)
	require.Error(t, err)
	require.Contains(t, err.Error(), "toggle archive state of encryption key")
}
