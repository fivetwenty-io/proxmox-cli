package api_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
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
	t.Setenv("PVE_TARGET", "")

	root := cli.NewRootCmd()
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

// writeConfig writes cfg to path with SaveForce so tests can seed targets.
func writeConfig(t *testing.T, path string, cfg *config.Config) {
	t.Helper()
	require.NoError(t, config.SaveForce(path, cfg))
}

// seedCfg returns a config with one password target and one token target.
func seedCfg() *config.Config {
	return &config.Config{
		CurrentTarget: "prod",
		Targets: map[string]*config.Target{
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
// port so config targets can be built without doubling the port.
func fakeHostPort(t *testing.T, f *testhelper.FakePVE) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(f.Server.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return host, port
}

// ---------------------------------------------------------------------------
// targets
// ---------------------------------------------------------------------------

func TestTargets_List(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path, "targets")
	require.NoError(t, err)
	require.Contains(t, out, "prod")
	require.Contains(t, out, "lab")
	require.Contains(t, out, "pve.example.com")
	require.Contains(t, out, "token")
	require.Contains(t, out, "password")
}

func TestTargets_ListEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, &config.Config{})

	deps := newTestDeps(t)
	deps.Cfg = &config.Config{}

	out, err := run(t, deps, path, "targets")
	require.NoError(t, err)
	require.Contains(t, out, "No targets")
}

func TestTargets_ListEmpty_JSON_IsEmptyArray(t *testing.T) {
	t.Setenv("PVE_OUTPUT", "json")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, &config.Config{})

	root := cli.NewRootCmd()
	root.SetContext(context.Background())
	root.AddCommand(api.NewCommand())

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--config", path, "-o", "json", "api", "targets"})
	require.NoError(t, root.Execute())

	out := strings.TrimSpace(buf.String())
	require.Equal(t, "[]", out,
		"empty target list must serialise as an empty JSON array, not a message object")
	require.NotContains(t, out, "message")
}

func TestTargets_HiddenTopLevelAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	t.Setenv("PVE_OUTPUT", "table")
	t.Setenv("PVE_NODE", "")
	t.Setenv("PVE_TARGET", "")

	root := cli.NewRootCmd()
	root.SetContext(context.Background())
	// Register the api group and its hidden top-level aliases the way the real
	// binary does (via the group registry).
	cli.AddGroups(root, &cli.Deps{})

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	// `pve targets` must work exactly like `pve api targets`.
	root.SetArgs([]string{"--config", path, "targets"})
	require.NoError(t, root.Execute())

	out := buf.String()
	require.Contains(t, out, "prod")
	require.Contains(t, out, "lab")
}

// ---------------------------------------------------------------------------
// target show
// ---------------------------------------------------------------------------

func TestTargetShow_Success(t *testing.T) {
	// Set the referenced env var to a distinctive value so we can prove the
	// resolved secret VALUE is never printed — only its source form.
	t.Setenv("PVE_TOKEN", "leakcanary-show-9f3c")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path, "target", "prod", "show")
	require.NoError(t, err)
	require.Contains(t, out, "pve.example.com")
	require.Contains(t, out, "token")
	require.Contains(t, out, "cli")
	// Secret value itself must NOT be printed; only its source form.
	require.Contains(t, out, "${PVE_TOKEN}")
	require.NotContains(t, out, "leakcanary-show-9f3c",
		"target show must report the secret source, never the resolved value")
}

func TestTargetShow_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path, "target", "ghost", "show")
	require.Error(t, err)
	require.Contains(t, err.Error(), "ghost")
}

// ---------------------------------------------------------------------------
// target add
// ---------------------------------------------------------------------------

func TestTargetAdd_Token(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, &config.Config{})

	deps := newTestDeps(t)
	deps.Cfg = &config.Config{}

	out, err := run(t, deps, path,
		"target", "new", "add",
		"--host", "h1.example.com",
		"--token", "mytoken=abc123",
		"--username", "root@pam",
	)
	require.NoError(t, err)
	require.Contains(t, out, "new")

	cfg := loadCfg(t, path)
	require.NotNil(t, cfg.Targets["new"])
	require.Equal(t, "h1.example.com", cfg.Targets["new"].Host)
	require.Equal(t, "token", cfg.Targets["new"].Auth.Type)
	require.Equal(t, "mytoken", cfg.Targets["new"].Auth.TokenID)
	require.Equal(t, "abc123", cfg.Targets["new"].Auth.Secret)
}

func TestTargetAdd_Switch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, &config.Config{})

	deps := newTestDeps(t)
	deps.Cfg = &config.Config{}

	_, err := run(t, deps, path,
		"target", "primary", "add",
		"--host", "h.example.com",
		"--token", "t=secret",
		"--username", "root@pam",
		"--switch",
	)
	require.NoError(t, err)

	cfg := loadCfg(t, path)
	require.Equal(t, "primary", cfg.CurrentTarget)
}

func TestTargetAdd_MissingHost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, &config.Config{})

	deps := newTestDeps(t)
	deps.Cfg = &config.Config{}

	_, err := run(t, deps, path, "target", "bad", "add")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// target remove
// ---------------------------------------------------------------------------

func TestTargetRemove_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path, "target", "lab", "remove", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "lab")

	cfg := loadCfg(t, path)
	require.Nil(t, cfg.Targets["lab"])
	require.NotNil(t, cfg.Targets["prod"])
}

func TestTargetRemove_CurrentTargetCleared(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path, "target", "prod", "remove", "--yes")
	require.NoError(t, err)

	cfg := loadCfg(t, path)
	require.Empty(t, cfg.CurrentTarget, "current-target must be cleared when removed")
}

func TestTargetRemove_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path, "target", "ghost", "remove", "--yes")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// switch
// ---------------------------------------------------------------------------

func TestSwitch_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	out, err := run(t, deps, path, "switch", "lab")
	require.NoError(t, err)
	require.Contains(t, out, "lab")
	require.Contains(t, out, "lab.example.com")

	cfg := loadCfg(t, path)
	require.Equal(t, "lab", cfg.CurrentTarget)
}

func TestSwitch_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path, "switch", "ghost")
	require.Error(t, err)
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
		CurrentTarget: "lab",
		Targets: map[string]*config.Target{
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

	out, err := run(t, deps, path, "auth", "login", "--target", "lab", "--password", "secretpw")
	require.NoError(t, err)
	require.Contains(t, out, "admin@pam")

	// The ticket request must have carried the username + password.
	require.Equal(t, "admin@pam", gotBody["username"])
	require.Equal(t, "secretpw", gotBody["password"])

	// Session must be persisted in config.
	saved := loadCfg(t, path)
	require.NotNil(t, saved.Targets["lab"].Auth.Session)
	require.Equal(t, "PVE:admin@pam:DEADBEEF", saved.Targets["lab"].Auth.Session.Ticket)
	require.Equal(t, "csrf-token-xyz", saved.Targets["lab"].Auth.Session.CSRF)
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
		Targets: map[string]*config.Target{
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

	_, err := run(t, deps, path, "auth", "login", "--target", "lab", "--password", "wrong")
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
		CurrentTarget: "lab",
		Targets: map[string]*config.Target{
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

	out, err := run(t, deps, path, "auth", "logout", "--target", "lab")
	require.NoError(t, err)
	require.Contains(t, out, "lab")

	saved := loadCfg(t, path)
	require.Nil(t, saved.Targets["lab"].Auth.Session, "session must be wiped on logout")
}

func TestAuthLogout_NoSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	// prod has token auth, no session — logout should still succeed (idempotent).
	_, err := run(t, deps, path, "auth", "logout", "--target", "prod")
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

	out, err := run(t, deps, path, "auth", "status", "--target", "prod")
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

	out, err := run(t, deps, path, "auth", "status", "--target", "prod")
	require.NoError(t, err)
	require.Contains(t, out, "no")
}

func TestAuthStatus_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path, "auth", "status", "--target", "ghost")
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
		CurrentTarget: "lab",
		Targets: map[string]*config.Target{
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

	out, err := run(t, deps, path, "auth", "refresh", "--target", "lab")
	require.NoError(t, err)
	require.Contains(t, out, "admin@pam")

	saved := loadCfg(t, path)
	require.NotNil(t, saved.Targets["lab"].Auth.Session)
	require.Equal(t, "PVE:admin@pam:NEW", saved.Targets["lab"].Auth.Session.Ticket)
}

func TestAuthRefresh_TokenTargetRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	// prod is a token target — refresh only applies to password auth.
	_, err := run(t, deps, path, "auth", "refresh", "--target", "prod")
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
		"auth", "set-token", "--target", "lab",
		"--token-id", "newid", "--secret", "newsecret",
		"--username", "svc@pam",
	)
	require.NoError(t, err)
	require.Contains(t, out, "lab")

	cfg := loadCfg(t, path)
	require.Equal(t, "token", cfg.Targets["lab"].Auth.Type)
	require.Equal(t, "newid", cfg.Targets["lab"].Auth.TokenID)
	require.Equal(t, "newsecret", cfg.Targets["lab"].Auth.Secret)
	require.Equal(t, "svc@pam", cfg.Targets["lab"].Auth.Username)
	require.Nil(t, cfg.Targets["lab"].Auth.Session, "switching to token auth must drop any session")
}

func TestAuthSetToken_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "set-token", "--target", "ghost",
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
		"auth", "set-password", "--target", "prod",
		"--username", "root@pam", "--secret", "${MY_PW}",
	)
	require.NoError(t, err)
	require.Contains(t, out, "prod")

	cfg := loadCfg(t, path)
	require.Equal(t, "password", cfg.Targets["prod"].Auth.Type)
	require.Equal(t, "root@pam", cfg.Targets["prod"].Auth.Username)
	require.Equal(t, "${MY_PW}", cfg.Targets["prod"].Auth.Secret)
	require.Empty(t, cfg.Targets["prod"].Auth.TokenID, "switching to password auth must drop token-id")
}

func TestAuthSetPassword_MissingUsername(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeConfig(t, path, seedCfg())

	deps := newTestDeps(t)
	deps.Cfg = loadCfg(t, path)

	_, err := run(t, deps, path,
		"auth", "set-password", "--target", "prod",
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
