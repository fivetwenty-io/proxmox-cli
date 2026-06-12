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

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/cli/api"
	"github.com/fivetwenty-io/pve-cli/internal/config"
	"github.com/fivetwenty-io/pve-cli/internal/exec"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// run executes an api sub-command through the real root command so that the
// production PersistentPreRunE wires Deps (Out, Format, Cfg loaded from cfgPath,
// Runner) and applies the noClient annotation. The deps argument is accepted for
// API symmetry with other group tests but is unused: the api group never relies
// on an injected API client (login/refresh build their own from config).
func run(t *testing.T, _ *cli.Deps, cfgPath string, args ...string) (string, error) {
	t.Helper()

	// Keep env-derived flag defaults out of the way.
	t.Setenv("PVE_OUTPUT", "table")
	t.Setenv("PVE_NODE", "")
	t.Setenv("PVE_CONTEXT", "")

	root, cleanup := cli.NewRootCmd()
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
					Secret:   "${PVE_TOKEN}",
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
	_ = logoutCalled

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path, "auth", "logout", "--context", "lab")
	require.NoError(t, err)
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
	t.Setenv("PVE_TOKEN", "resolvedsecret")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path, "auth", "status", "--context", "prod")
	require.NoError(t, err)
	require.Contains(t, out, "token")
	require.Contains(t, out, "cli")
	require.Contains(t, out, "${PVE_TOKEN}")
	// The resolved secret VALUE must never appear in auth status output.
	require.NotContains(t, out, "resolvedsecret",
		"auth status must report the secret source, never the resolved value")
}

func TestAuthStatus_UnresolvedSecret(t *testing.T) {
	// PVE_TOKEN intentionally unset — status should report it as unresolved
	// rather than failing.
	t.Setenv("PVE_TOKEN", "")
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
// registration + annotations
// ---------------------------------------------------------------------------

func TestGroup_AllSubcommandsNoClient(t *testing.T) {
	group := api.NewCommand()
	leaves := leafCommands(group)
	require.NotEmpty(t, leaves)
	for _, c := range leaves {
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
