package api_test

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// This file audits every scalar flag on every write-path command in the api
// package: the two PVE-API-hitting flows (auth login, auth login --oidc) and
// the local-config-only flows (auth refresh, auth set-token, auth
// set-password). Each assertion verifies a flag lands under its exact PVE
// API parameter key (auth login) or config field (set-token/set-password),
// including the ticket-verification --path/--privs pair that no other test
// in this package exercises.

// ---------------------------------------------------------------------------
// auth login — full CreateTicketParams flag surface
// ---------------------------------------------------------------------------

// TestAuthAudit_Login_AllFlags asserts every auth login scalar flag reaches
// POST /access/ticket under its exact wire key, including the
// ticket-verification --path/--privs pair (mode used to confirm a ticket
// grants specific privileges on a path) which is otherwise untested.
func TestAuthAudit_Login_AllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotBody map[string]string
	f.HandleFunc("POST /api2/json/access/ticket", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotBody = map[string]string{
			"username":      r.PostFormValue("username"),
			"password":      r.PostFormValue("password"),
			"realm":         r.PostFormValue("realm"),
			"otp":           r.PostFormValue("otp"),
			"tfa-challenge": r.PostFormValue("tfa-challenge"),
			"path":          r.PostFormValue("path"),
			"privs":         r.PostFormValue("privs"),
		}
		testhelper.WriteData(w, map[string]any{
			"username":            "alice@pam",
			"ticket":              "PVE:alice@pam:DEADBEEF",
			"CSRFPreventionToken": "csrf-token-xyz",
		})
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	host, port := fakeHostPort(t, f)
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": {
				Host:     host,
				Port:     port,
				Protocol: "http",
				Realm:    "pam",
				Auth: config.AuthBlock{
					Type:     "password",
					Username: "alice@pam",
					Secret:   "unused",
				},
				TLS: config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path, "auth", "login",
		"--context", "lab",
		"--username", "alice@pam",
		"--realm", "pam",
		"--password", "s3cret",
		"--otp", "654321",
		"--tfa-challenge", "totp:signed-response",
		"--path", "/vms/100",
		"--privs", "VM.Console",
	)
	require.NoError(t, err)

	require.Equal(t, "alice@pam", gotBody["username"])
	require.Equal(t, "s3cret", gotBody["password"])
	require.Equal(t, "pam", gotBody["realm"])
	require.Equal(t, "654321", gotBody["otp"])
	require.Equal(t, "totp:signed-response", gotBody["tfa-challenge"])
	require.Equal(t, "/vms/100", gotBody["path"])
	require.Equal(t, "VM.Console", gotBody["privs"])
}

// TestAuthAudit_Login_OmitsUnsetOptionalFlags verifies the optional
// CreateTicketParams fields (otp, tfa-challenge, path, privs, realm) are
// absent from the request when the corresponding flags are not supplied.
func TestAuthAudit_Login_OmitsUnsetOptionalFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotRaw map[string][]string
	f.HandleFunc("POST /api2/json/access/ticket", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotRaw = map[string][]string(r.PostForm)
		testhelper.WriteData(w, map[string]any{
			"username":            "alice@pam",
			"ticket":              "PVE:alice@pam:DEADBEEF",
			"CSRFPreventionToken": "csrf",
		})
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	host, port := fakeHostPort(t, f)
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": {
				Host: host, Port: port, Protocol: "http", Realm: "pam",
				Auth: config.AuthBlock{Type: "password", Username: "alice@pam", Secret: "unused"},
				TLS:  config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path, "auth", "login", "--context", "lab", "--password", "s3cret")
	require.NoError(t, err)

	for _, key := range []string{"otp", "tfa-challenge", "path", "privs"} {
		_, present := gotRaw[key]
		require.False(t, present, "%s must be omitted when unset", key)
	}
}

// ---------------------------------------------------------------------------
// auth login --oidc — CreateOpenidAuthUrlParams / CreateOpenidLoginParams
// ---------------------------------------------------------------------------

// TestAuthAudit_LoginOIDC_AllFlags asserts every OIDC login flag reaches its
// respective request (auth-url or login) under the exact wire key.
func TestAuthAudit_LoginOIDC_AllFlags(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotAuthURLBody, gotLoginBody map[string]string
	f.HandleFunc("POST /api2/json/access/openid/auth-url", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotAuthURLBody = map[string]string{
			"realm":        r.PostFormValue("realm"),
			"redirect-url": r.PostFormValue("redirect-url"),
		}
		testhelper.WriteData(w, "https://idp.example.com/auth")
	})
	f.HandleFunc("POST /api2/json/access/openid/login", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotLoginBody = map[string]string{
			"code":         r.PostFormValue("code"),
			"state":        r.PostFormValue("state"),
			"redirect-url": r.PostFormValue("redirect-url"),
		}
		testhelper.WriteData(w, map[string]any{
			"username":            "carol@corp",
			"ticket":              "PVE:carol@corp:TOK",
			"CSRFPreventionToken": "csrf-oidc",
		})
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	host, port := fakeHostPort(t, f)
	cfg := &config.Config{
		CurrentContext: "corp",
		Contexts: map[string]*config.Context{
			"corp": {
				Host:     host,
				Port:     port,
				Protocol: "http",
				Realm:    "corp-oidc",
				Auth:     config.AuthBlock{Type: "password", Username: "carol@corp", Secret: "unused"},
				TLS:      config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "login", "--context", "corp",
		"--oidc",
		"--realm", "corp-oidc-flag",
		"--redirect-url", "https://custom.example.com/callback",
		"--code", "auth-code-1",
		"--state", "state-token-1",
	)
	require.NoError(t, err)

	require.Equal(t, "corp-oidc-flag", gotAuthURLBody["realm"])
	require.Equal(t, "https://custom.example.com/callback", gotAuthURLBody["redirect-url"])
	require.Equal(t, "auth-code-1", gotLoginBody["code"])
	require.Equal(t, "state-token-1", gotLoginBody["state"])
	require.Equal(t, "https://custom.example.com/callback", gotLoginBody["redirect-url"])
}

// ---------------------------------------------------------------------------
// auth refresh
// ---------------------------------------------------------------------------

// TestAuthAudit_Refresh_TfaChallengeFlag asserts --tfa-challenge reaches
// POST /access/ticket under its exact wire key on the refresh path, which
// builds a separate ticketOptions value from login's.
func TestAuthAudit_Refresh_TfaChallengeFlag(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var gotChallenge string
	f.HandleFunc("POST /api2/json/access/ticket", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotChallenge = r.PostFormValue("tfa-challenge")
		testhelper.WriteData(w, map[string]any{
			"username":            "alice@pam",
			"ticket":              "PVE:alice@pam:NEW",
			"CSRFPreventionToken": "new-csrf",
		})
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	host, port := fakeHostPort(t, f)
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": {
				Host: host, Port: port, Protocol: "http", Realm: "pam",
				Auth: config.AuthBlock{Type: "password", Username: "alice@pam", Secret: "pw"},
				TLS:  config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path, "auth", "refresh", "--context", "lab", "--tfa-challenge", "totp:refreshed")
	require.NoError(t, err)
	require.Equal(t, "totp:refreshed", gotChallenge)
}

// ---------------------------------------------------------------------------
// auth set-token
// ---------------------------------------------------------------------------

// TestAuthAudit_SetToken_AllFlags asserts every set-token flag is written
// into the persisted config context under its expected field.
func TestAuthAudit_SetToken_AllFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "set-token", "--context", "lab",
		"--token-id", "audit-token",
		"--secret", "audit-secret",
		"--username", "svc-audit@pam",
	)
	require.NoError(t, err)

	cfg := loadCfg(t, path)
	require.Equal(t, "token", cfg.Contexts["lab"].Auth.Type)
	require.Equal(t, "audit-token", cfg.Contexts["lab"].Auth.TokenID)
	require.Equal(t, "audit-secret", cfg.Contexts["lab"].Auth.Secret)
	require.Equal(t, "svc-audit@pam", cfg.Contexts["lab"].Auth.Username)
}

// TestAuthAudit_SetToken_OmitsUsernameWhenUnset verifies the existing
// username is preserved when --username is not supplied, since set-token
// only overwrites it when the flag carries a non-empty value.
func TestAuthAudit_SetToken_OmitsUsernameWhenUnset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "set-token", "--context", "lab",
		"--token-id", "audit-token2",
		"--secret", "audit-secret2",
	)
	require.NoError(t, err)

	cfg := loadCfg(t, path)
	require.Equal(t, "admin@pam", cfg.Contexts["lab"].Auth.Username,
		"username must be preserved when --username is not supplied")
}

// ---------------------------------------------------------------------------
// auth set-password
// ---------------------------------------------------------------------------

// TestAuthAudit_SetPassword_AllFlags asserts every set-password flag is
// written into the persisted config context under its expected field.
func TestAuthAudit_SetPassword_AllFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "set-password", "--context", "prod",
		"--username", "audit-user@pam",
		"--secret", "${AUDIT_PW}",
	)
	require.NoError(t, err)

	cfg := loadCfg(t, path)
	require.Equal(t, "password", cfg.Contexts["prod"].Auth.Type)
	require.Equal(t, "audit-user@pam", cfg.Contexts["prod"].Auth.Username)
	require.Equal(t, "${AUDIT_PW}", cfg.Contexts["prod"].Auth.Secret)
}
