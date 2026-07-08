package api_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/cli/api"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/exec"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// run executes an api sub-command through the real root command so that the
// production PersistentPreRunE wires Deps (Out, Format, Cfg loaded from cfgPath,
// Runner) and applies the noClient annotation. The deps argument is accepted for
// API symmetry with other group tests but is unused: the api group never relies
// on an injected API client (login/refresh build their own from config).
func run(t *testing.T, _ *cli.Deps, cfgPath string, args ...string) (string, error) {
	t.Helper()

	// Keep env-derived flag defaults out of the way.
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())
	root.AddCommand(api.NewCommand())

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)

	full := append([]string{"--config", cfgPath, "api"}, args...)
	root.SetArgs(full)
	err := root.Execute()
	return buf.String(), err
}

// newTestDeps is retained for call-site symmetry; the real PersistentPreRunE
// builds the live Deps, so this returns a minimal placeholder.
func newTestDeps(t *testing.T) *cli.Deps {
	t.Helper()
	return &cli.Deps{
		Out:    output.New(),
		Format: output.FormatTable,
		Cfg:    &config.Config{},
		Runner: exec.Fake(),
	}
}

// writeConfig writes cfg to path with SaveForce so tests can seed contexts.
func writeConfig(t *testing.T, path string, cfg *config.Config) {
	t.Helper()
	require.NoError(t, config.SaveForce(path, cfg))
}

// seedCfg returns a config with one password context and one token context.
func seedCfg() *config.Config {
	return &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]*config.Context{
			"prod": {
				Host:     "pve.example.com",
				Port:     8006,
				Protocol: "https",
				Realm:    "pam",
				Auth: config.AuthBlock{
					Type:     "token",
					Username: "root@pam",
					TokenID:  "cli",
					Secret:   "${PMX_TOKEN}",
				},
			},
			"lab": {
				Host:     "lab.example.com",
				Port:     8006,
				Protocol: "https",
				Realm:    "pam",
				Auth: config.AuthBlock{
					Type:     "password",
					Username: "admin@pam",
					Secret:   "${LAB_PW}",
				},
			},
		},
	}
}

func loadCfg(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := config.Load(path)
	require.NoError(t, err)
	return cfg
}

// fakeHostPort splits the fake server's host:port into separate host and int
// port so config contexts can be built without doubling the port.
func fakeHostPort(t *testing.T, f *testhelper.FakePVE) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(f.Server.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return host, port
}

// ---------------------------------------------------------------------------
// auth login
// ---------------------------------------------------------------------------

func TestAuthLogin_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	gotBody := map[string]string{}
	f.HandleFunc("POST /api2/json/access/ticket", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotBody["username"] = r.PostFormValue("username")
		gotBody["password"] = r.PostFormValue("password")
		testhelper.WriteData(w, map[string]any{
			"username":            "admin@pam",
			"ticket":              "PVE:admin@pam:DEADBEEF",
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
					Username: "admin@pam",
					Secret:   "secretpw",
				},
				TLS: config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path, "auth", "login", "--context", "lab", "--password", "secretpw")
	require.NoError(t, err)
	require.Contains(t, out, "admin@pam")

	// The ticket request must have carried the username + password.
	require.Equal(t, "admin@pam", gotBody["username"])
	require.Equal(t, "secretpw", gotBody["password"])

	// Session must be persisted in config.
	saved := loadCfg(t, path)
	require.NotNil(t, saved.Contexts["lab"].Auth.Session)
	require.Equal(t, "PVE:admin@pam:DEADBEEF", saved.Contexts["lab"].Auth.Session.Ticket)
	require.Equal(t, "csrf-token-xyz", saved.Contexts["lab"].Auth.Session.CSRF)
}

func TestAuthLogin_ServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/access/ticket", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteError(w, http.StatusUnauthorized, "authentication failure")
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	host, port := fakeHostPort(t, f)
	cfg := &config.Config{
		Contexts: map[string]*config.Context{
			"lab": {
				Host:     host,
				Port:     port,
				Protocol: "http",
				Realm:    "pam",
				Auth: config.AuthBlock{
					Type:     "password",
					Username: "admin@pam",
					Secret:   "wrong",
				},
				TLS: config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path, "auth", "login", "--context", "lab", "--password", "wrong")
	require.Error(t, err)
}

func TestAuthLogin_OTPAndTfaChallenge(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	gotBody := map[string]string{}
	f.HandleFunc("POST /api2/json/access/ticket", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotBody["otp"] = r.PostFormValue("otp")
		gotBody["tfa-challenge"] = r.PostFormValue("tfa-challenge")
		testhelper.WriteData(w, map[string]any{
			"username":            "admin@pam",
			"ticket":              "PVE:admin@pam:DEADBEEF",
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
				Auth: config.AuthBlock{Type: "password", Username: "admin@pam", Secret: "pw"},
				TLS:  config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path, "auth", "login", "--context", "lab",
		"--password", "pw", "--otp", "123456", "--tfa-challenge", "totp:resp")
	require.NoError(t, err)
	require.Equal(t, "123456", gotBody["otp"])
	require.Equal(t, "totp:resp", gotBody["tfa-challenge"])
}

// TestAuthLogin_GlobalInsecureFlag_WarnsAndSucceeds verifies that the global
// --insecure flag (normally consumed only by PersistentPreRunE for commands
// that build their client through the root, see
// internal/cli/root_test.go:TestPersistentPreRunE_Insecure_WarnsOnStderr) is
// also honored by `auth login`, which builds its own client outside
// PersistentPreRunE. The context here deliberately leaves tls.insecure unset
// so the only source of the insecure behavior is the --insecure flag itself;
// this both proves the flag is registered/inherited on the auth command tree
// (cobra would reject an unregistered flag) and that it triggers the same
// WarnInsecureTLS stderr warning normal commands emit.
func TestAuthLogin_GlobalInsecureFlag_WarnsAndSucceeds(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/access/ticket", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"username":            "admin@pam",
			"ticket":              "PVE:admin@pam:DEADBEEF",
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
					Username: "admin@pam",
					Secret:   "secretpw",
				},
				// tls.insecure deliberately unset: --insecure alone must trigger the warning.
			},
		},
	}
	writeConfig(t, path, cfg)

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path, "auth", "login", "--context", "lab",
		"--password", "secretpw", "--insecure")
	require.NoError(t, err)
	require.Contains(t, out, "WARN: TLS certificate verification disabled",
		"--insecure must emit the same stderr warning normal commands emit, "+
			"even though this context's tls.insecure is unset")
	require.Contains(t, out, "admin@pam")
}

// ---------------------------------------------------------------------------
// auth whoami
// ---------------------------------------------------------------------------

func TestAuthWhoami_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/access/permissions", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{"/": map[string]int{"Sys.Audit": 1}})
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	host, port := fakeHostPort(t, f)
	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]*config.Context{
			"prod": {
				Host: host, Port: port, Protocol: "http", Realm: "pam",
				Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "cli", Secret: "s3cr3t"},
				TLS:  config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path, "auth", "whoami", "--context", "prod")
	require.NoError(t, err)
	require.Contains(t, out, "root@pam!cli")
}

func TestAuthWhoami_AuthFailure(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/access/permissions", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusUnauthorized, "invalid token")
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	host, port := fakeHostPort(t, f)
	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]*config.Context{
			"prod": {
				Host: host, Port: port, Protocol: "http", Realm: "pam",
				Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "cli", Secret: "bad"},
				TLS:  config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path, "auth", "whoami", "--context", "prod")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// auth logout
// ---------------------------------------------------------------------------

func TestAuthLogout_WipesSession(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	logoutCalled := false
	f.HandleFunc("DELETE /api2/json/access/ticket", func(w http.ResponseWriter, r *http.Request) {
		logoutCalled = true
		testhelper.WriteData(w, map[string]any{})
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
					Username: "admin@pam",
					Secret:   "pw",
					Session: &config.Session{
						Ticket: "PVE:admin@pam:OLD",
						CSRF:   "old-csrf",
					},
				},
				TLS: config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path, "auth", "logout", "--context", "lab")
	require.NoError(t, err)
	require.True(t, logoutCalled, "logout must call DELETE /access/ticket to invalidate the server ticket")
	require.Contains(t, out, "lab")

	saved := loadCfg(t, path)
	require.Nil(t, saved.Contexts["lab"].Auth.Session, "session must be wiped on logout")
}

func TestAuthLogout_NoSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	// prod has token auth, no session — logout should still succeed (idempotent).
	_, err := run(t, deps, path, "auth", "logout", "--context", "prod")
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// auth status
// ---------------------------------------------------------------------------

func TestAuthStatus_Token(t *testing.T) {
	t.Setenv("PMX_TOKEN", "resolvedsecret")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path, "auth", "status", "--context", "prod")
	require.NoError(t, err)
	require.Contains(t, out, "token")
	require.Contains(t, out, "cli")
	require.Contains(t, out, "${PMX_TOKEN}")
	// The resolved secret VALUE must never appear in auth status output.
	require.NotContains(t, out, "resolvedsecret",
		"auth status must report the secret source, never the resolved value")
}

func TestAuthStatus_UnresolvedSecret(t *testing.T) {
	// PMX_TOKEN intentionally unset — status should report it as unresolved
	// rather than failing.
	t.Setenv("PMX_TOKEN", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path, "auth", "status", "--context", "prod")
	require.NoError(t, err)
	require.Contains(t, out, "no")
}

func TestAuthStatus_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path, "auth", "status", "--context", "ghost")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// auth refresh
// ---------------------------------------------------------------------------

func TestAuthRefresh_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/access/ticket", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"username":            "admin@pam",
			"ticket":              "PVE:admin@pam:NEW",
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
				Host:     host,
				Port:     port,
				Protocol: "http",
				Realm:    "pam",
				Auth: config.AuthBlock{
					Type:     "password",
					Username: "admin@pam",
					Secret:   "pw",
				},
				TLS: config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path, "auth", "refresh", "--context", "lab")
	require.NoError(t, err)
	require.Contains(t, out, "admin@pam")

	saved := loadCfg(t, path)
	require.NotNil(t, saved.Contexts["lab"].Auth.Session)
	require.Equal(t, "PVE:admin@pam:NEW", saved.Contexts["lab"].Auth.Session.Ticket)
}

func TestAuthRefresh_TokenContextRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	// prod is a token context — refresh only applies to password auth.
	_, err := run(t, deps, path, "auth", "refresh", "--context", "prod")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// auth set-token
// ---------------------------------------------------------------------------

func TestAuthSetToken_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path,
		"auth", "set-token", "--context", "lab",
		"--token-id", "newid", "--secret", "newsecret",
		"--username", "svc@pam",
	)
	require.NoError(t, err)
	require.Contains(t, out, "lab")

	cfg := loadCfg(t, path)
	require.Equal(t, "token", cfg.Contexts["lab"].Auth.Type)
	require.Equal(t, "newid", cfg.Contexts["lab"].Auth.TokenID)
	require.Equal(t, "newsecret", cfg.Contexts["lab"].Auth.Secret)
	require.Equal(t, "svc@pam", cfg.Contexts["lab"].Auth.Username)
	require.Nil(t, cfg.Contexts["lab"].Auth.Session, "switching to token auth must drop any session")
}

func TestAuthSetToken_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "set-token", "--context", "ghost",
		"--token-id", "x", "--secret", "y",
	)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// auth set-password
// ---------------------------------------------------------------------------

func TestAuthSetPassword_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path,
		"auth", "set-password", "--context", "prod",
		"--username", "root@pam", "--secret", "${MY_PW}",
	)
	require.NoError(t, err)
	require.Contains(t, out, "prod")

	cfg := loadCfg(t, path)
	require.Equal(t, "password", cfg.Contexts["prod"].Auth.Type)
	require.Equal(t, "root@pam", cfg.Contexts["prod"].Auth.Username)
	require.Equal(t, "${MY_PW}", cfg.Contexts["prod"].Auth.Secret)
	require.Empty(t, cfg.Contexts["prod"].Auth.TokenID, "switching to password auth must drop token-id")
}

func TestAuthSetPassword_MissingUsername(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "set-password", "--context", "prod",
		"--secret", "x",
	)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// auth login --oidc
// ---------------------------------------------------------------------------

func TestAuthLogin_OIDC_NonInteractive_Success(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	// auth-url endpoint returns a JSON string (the authorization URL).
	var gotAuthURLBody map[string]string
	f.HandleFunc("POST /api2/json/access/openid/auth-url", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotAuthURLBody = map[string]string{
			"realm":        r.PostFormValue("realm"),
			"redirect-url": r.PostFormValue("redirect-url"),
		}
		testhelper.WriteData(w, "https://idp.example.com/auth?response_type=code&client_id=pve")
	})

	// login endpoint returns the same shape as POST /access/ticket.
	var gotLoginBody map[string]string
	f.HandleFunc("POST /api2/json/access/openid/login", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotLoginBody = map[string]string{
			"code":         r.PostFormValue("code"),
			"state":        r.PostFormValue("state"),
			"redirect-url": r.PostFormValue("redirect-url"),
		}
		testhelper.WriteData(w, map[string]any{
			"username":            "alice@corp",
			"ticket":              "PVE:alice@corp:OIDCTOKEN",
			"CSRFPreventionToken": "csrf-oidc-xyz",
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
				Auth: config.AuthBlock{
					Type:     "password",
					Username: "alice@corp",
					Secret:   "unused",
				},
				TLS: config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path,
		"auth", "login",
		"--context", "corp",
		"--oidc",
		"--realm", "corp-oidc",
		"--code", "mycode123",
		"--state", "mystate456",
	)
	require.NoError(t, err)
	require.Contains(t, out, "alice@corp")

	// auth-url must carry the realm and redirect-url.
	require.Equal(t, "corp-oidc", gotAuthURLBody["realm"])
	require.NotEmpty(t, gotAuthURLBody["redirect-url"])

	// login must carry the code, state, and the same redirect-url.
	require.Equal(t, "mycode123", gotLoginBody["code"])
	require.Equal(t, "mystate456", gotLoginBody["state"])
	require.Equal(t, gotAuthURLBody["redirect-url"], gotLoginBody["redirect-url"],
		"redirect-url in login call must match the one sent to auth-url")

	// Session must be persisted with the returned ticket.
	saved := loadCfg(t, path)
	require.NotNil(t, saved.Contexts["corp"].Auth.Session)
	require.Equal(t, "PVE:alice@corp:OIDCTOKEN", saved.Contexts["corp"].Auth.Session.Ticket)
	require.Equal(t, "csrf-oidc-xyz", saved.Contexts["corp"].Auth.Session.CSRF)
}

func TestAuthLogin_OIDC_ExplicitRedirectUrl(t *testing.T) {
	f := testhelper.NewFakePVE(t)

	var gotAuthURLBody, gotLoginBody map[string]string
	f.HandleFunc("POST /api2/json/access/openid/auth-url", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotAuthURLBody = map[string]string{"redirect-url": r.PostFormValue("redirect-url")}
		testhelper.WriteData(w, "https://idp.example.com/auth")
	})
	f.HandleFunc("POST /api2/json/access/openid/login", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotLoginBody = map[string]string{"redirect-url": r.PostFormValue("redirect-url")}
		testhelper.WriteData(w, map[string]any{
			"username":            "bob@corp",
			"ticket":              "PVE:bob@corp:TOK",
			"CSRFPreventionToken": "csrf2",
		})
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	host, port := fakeHostPort(t, f)
	cfg := &config.Config{
		CurrentContext: "c",
		Contexts: map[string]*config.Context{
			"c": {
				Host:     host,
				Port:     port,
				Protocol: "http",
				Realm:    "myoidc",
				Auth:     config.AuthBlock{Type: "password", Username: "bob@corp", Secret: "x"},
				TLS:      config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "login", "--context", "c",
		"--oidc", "--realm", "myoidc",
		"--redirect-url", "https://custom.example.com",
		"--code", "c1", "--state", "s1",
	)
	require.NoError(t, err)
	require.Equal(t, "https://custom.example.com", gotAuthURLBody["redirect-url"])
	require.Equal(t, "https://custom.example.com", gotLoginBody["redirect-url"])
}

func TestAuthLogin_OIDC_RealmFromContext(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/access/openid/auth-url", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, "https://idp.example.com/auth")
	})
	f.HandleFunc("POST /api2/json/access/openid/login", func(w http.ResponseWriter, r *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"username":            "carol@corp",
			"ticket":              "PVE:carol@corp:T",
			"CSRFPreventionToken": "x",
		})
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	host, port := fakeHostPort(t, f)
	cfg := &config.Config{
		CurrentContext: "c",
		Contexts: map[string]*config.Context{
			"c": {
				Host: host, Port: port, Protocol: "http",
				Realm: "corp-oidc", // realm in context — no --realm flag needed
				Auth:  config.AuthBlock{Type: "password", Username: "carol@corp", Secret: "pw"},
				TLS:   config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	// No --realm flag: must fall back to context's realm.
	_, err := run(t, deps, path,
		"auth", "login", "--context", "c",
		"--oidc", "--code", "c1", "--state", "s1",
	)
	require.NoError(t, err)
}

func TestAuthLogin_OIDC_RealmRequired(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	// Context has no realm configured; --realm not supplied either.
	cfg := &config.Config{
		CurrentContext: "c",
		Contexts: map[string]*config.Context{
			"c": {
				Host: "pve.example.com", Port: 8006, Protocol: "https",
				Auth: config.AuthBlock{Type: "password", Username: "u@pam", Secret: "pw"},
			},
		},
	}
	writeConfig(t, path, cfg)
	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "login", "--context", "c",
		"--oidc", "--code", "x", "--state", "y",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "realm")
}

func TestAuthLogin_OIDC_PasswordConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	cfg := &config.Config{
		CurrentContext: "c",
		Contexts: map[string]*config.Context{
			"c": {
				Host: "pve.example.com", Port: 8006, Protocol: "https",
				Realm: "corp",
				Auth:  config.AuthBlock{Type: "password", Username: "u@corp", Secret: "pw"},
			},
		},
	}
	writeConfig(t, path, cfg)
	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "login", "--context", "c",
		"--oidc", "--realm", "corp",
		"--password", "somepw",
		"--code", "x", "--state", "y",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--password")
}

func TestAuthLogin_OIDC_OTPConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	cfg := &config.Config{
		CurrentContext: "c",
		Contexts: map[string]*config.Context{
			"c": {
				Host: "pve.example.com", Port: 8006, Protocol: "https",
				Realm: "corp",
				Auth:  config.AuthBlock{Type: "password", Username: "u@corp", Secret: "pw"},
			},
		},
	}
	writeConfig(t, path, cfg)
	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "login", "--context", "c",
		"--oidc", "--realm", "corp",
		"--otp", "123456",
		"--code", "x", "--state", "y",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--otp")
}

func TestAuthLogin_OIDC_TfaChallengeConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	cfg := &config.Config{
		CurrentContext: "c",
		Contexts: map[string]*config.Context{
			"c": {
				Host: "pve.example.com", Port: 8006, Protocol: "https",
				Realm: "corp",
				Auth:  config.AuthBlock{Type: "password", Username: "u@corp", Secret: "pw"},
			},
		},
	}
	writeConfig(t, path, cfg)
	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "login", "--context", "c",
		"--oidc", "--realm", "corp",
		"--tfa-challenge", "totp:resp",
		"--code", "x", "--state", "y",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--tfa-challenge")
}

func TestAuthLogin_OIDC_CodeWithoutState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	cfg := &config.Config{
		CurrentContext: "c",
		Contexts: map[string]*config.Context{
			"c": {
				Host: "pve.example.com", Port: 8006, Protocol: "https",
				Realm: "corp",
				Auth:  config.AuthBlock{Type: "password", Username: "u@corp", Secret: "pw"},
			},
		},
	}
	writeConfig(t, path, cfg)
	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "login", "--context", "c",
		"--oidc", "--realm", "corp",
		"--code", "x",
		// --state intentionally omitted
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--state")
}

func TestAuthLogin_OIDC_AuthUrlServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/access/openid/auth-url", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "unknown realm")
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	host, port := fakeHostPort(t, f)
	cfg := &config.Config{
		CurrentContext: "c",
		Contexts: map[string]*config.Context{
			"c": {
				Host: host, Port: port, Protocol: "http",
				Realm: "badoidc",
				Auth:  config.AuthBlock{Type: "password", Username: "u@corp", Secret: "pw"},
				TLS:   config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)
	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "login", "--context", "c",
		"--oidc", "--code", "x", "--state", "y",
	)
	require.Error(t, err)
}

func TestAuthLogin_OIDC_LoginServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("POST /api2/json/access/openid/auth-url", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, "https://idp.example.com/auth")
	})
	f.HandleFunc("POST /api2/json/access/openid/login", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusUnauthorized, "invalid code")
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	host, port := fakeHostPort(t, f)
	cfg := &config.Config{
		CurrentContext: "c",
		Contexts: map[string]*config.Context{
			"c": {
				Host: host, Port: port, Protocol: "http",
				Realm: "myoidc",
				Auth:  config.AuthBlock{Type: "password", Username: "u@corp", Secret: "pw"},
				TLS:   config.TLSBlock{Insecure: true},
			},
		},
	}
	writeConfig(t, path, cfg)
	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "login", "--context", "c",
		"--oidc", "--code", "expired-code", "--state", "s1",
	)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// registration + annotations
// ---------------------------------------------------------------------------

func TestGroup_AllSubcommandsNoClient(t *testing.T) {
	group := api.NewCommand()
	leaves := leafCommands(group)
	require.NotEmpty(t, leaves)
	// whoami is the deliberate exception: it queries GET /access/permissions to
	// confirm the stored credentials authenticate, so it needs a live client.
	clientCommands := map[string]bool{"api auth whoami": true}
	for _, c := range leaves {
		if clientCommands[c.CommandPath()] {
			require.NotEqual(t, "true", c.Annotations["noClient"],
				"command %q must NOT set noClient annotation (needs a live client)", c.CommandPath())
			continue
		}
		require.Equal(t, "true", c.Annotations["noClient"],
			"command %q must set noClient annotation", c.CommandPath())
	}
}

// leafCommands returns all runnable leaf commands under root.
func leafCommands(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		children := c.Commands()
		if len(children) == 0 {
			if c.RunE != nil || c.Run != nil {
				out = append(out, c)
			}
			return
		}
		for _, ch := range children {
			walk(ch)
		}
	}
	walk(root)
	return out
}
