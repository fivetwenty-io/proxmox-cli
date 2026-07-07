package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/config"
)

// ── Fixtures ─────────────────────────────────────────────────────────────────

// sampleConfig returns a fully populated Config for round-trip tests.
func sampleConfig() *config.Config {
	return &config.Config{
		CurrentContext: "prod",
		DefaultOutput:  "table",
		Contexts: map[string]*config.Context{
			"prod": {
				Host:     "pve.example.com",
				Port:     8006,
				Protocol: "https",
				Realm:    "pam",
				Auth: config.AuthBlock{
					Type:     "token",
					Username: "root@pam",
					TokenID:  "deploy",
					Secret:   "${PMX_TOKEN_SECRET}",
				},
				TLS: config.TLSBlock{
					Insecure:    false,
					Fingerprint: "AA:BB:CC",
					CACert:      "/etc/ssl/certs/ca.pem",
					Tofu:        true,
				},
			},
			"staging": {
				Host:     "pve-staging.example.com",
				Port:     8006,
				Protocol: "https",
				Realm:    "pam",
				Auth: config.AuthBlock{
					Type:     "password",
					Username: "admin@pam",
					Secret:   "$PMX_STAGING_PASS",
					Session: &config.Session{
						Ticket:    "PVE:admin@pam:abc123",
						CSRF:      "csrf-token",
						ExpiresAt: time.Now().Add(2 * time.Hour).Unix(),
					},
				},
				TLS: config.TLSBlock{
					Insecure: true,
				},
			},
		},
	}
}

// ── DefaultPath ───────────────────────────────────────────────────────────────

func TestDefaultPath_XDGSet(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/xdg")
	got := config.DefaultPath()
	require.Equal(t, filepath.Join("/custom/xdg", "pmx", "config.yml"), got)
}

func TestDefaultPath_XDGUnset(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	got := config.DefaultPath()
	require.Equal(t, filepath.Join(home, ".config", "pmx", "config.yml"), got)
}

// ── Load ──────────────────────────────────────────────────────────────────────

func TestLoad_MissingFile_ReturnsEmptyConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.yml")

	cfg, err := config.Load(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Empty(t, cfg.CurrentContext)
	require.Nil(t, cfg.Contexts)
}

func TestLoad_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	original := sampleConfig()
	require.NoError(t, config.Save(path, original))

	loaded, err := config.Load(path)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Equal(t, "prod", loaded.CurrentContext)
	require.Equal(t, "table", loaded.DefaultOutput)
	require.Len(t, loaded.Contexts, 2)

	prod := loaded.Contexts["prod"]
	require.NotNil(t, prod)
	require.Equal(t, "pve.example.com", prod.Host)
	require.Equal(t, "token", prod.Auth.Type)
	require.Equal(t, "${PMX_TOKEN_SECRET}", prod.Auth.Secret)
	require.Equal(t, "AA:BB:CC", prod.TLS.Fingerprint)
	require.Equal(t, "/etc/ssl/certs/ca.pem", prod.TLS.CACert)
	require.True(t, prod.TLS.Tofu)

	staging := loaded.Contexts["staging"]
	require.NotNil(t, staging)
	require.NotNil(t, staging.Auth.Session)
	require.Equal(t, "PVE:admin@pam:abc123", staging.Auth.Session.Ticket)
	require.Equal(t, "csrf-token", staging.Auth.Session.CSRF)
}

func TestLoad_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	// An unclosed sequence is genuinely invalid YAML for goccy/go-yaml.
	require.NoError(t, os.WriteFile(path, []byte("current-context: [unclosed"), 0o600))

	_, err := config.Load(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse config")
}

// ── contexts/current-context round-trip ──────────────────────────────────────

func TestLoad_ContextsCurrentContext_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	cfg := &config.Config{
		CurrentContext: "dev",
		Contexts: map[string]*config.Context{
			"dev": {
				Host: "dev.example.com",
				Port: 8006,
				Auth: config.AuthBlock{Type: "token", Secret: "tok"},
			},
		},
	}
	require.NoError(t, config.Save(path, cfg))

	// Verify the YAML file uses "contexts:" and "current-context:" keys.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), "current-context:")
	require.Contains(t, string(raw), "contexts:")
	require.NotContains(t, string(raw), "current-target:")
	require.NotContains(t, string(raw), "targets:")

	loaded, err := config.Load(path)
	require.NoError(t, err)
	require.Equal(t, "dev", loaded.CurrentContext)
	require.NotNil(t, loaded.Contexts["dev"])
}

// ── previous-context persist ──────────────────────────────────────────────────

func TestLoad_PreviousContext_Persist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	cfg := &config.Config{
		CurrentContext:  "prod",
		PreviousContext: "staging",
		Contexts: map[string]*config.Context{
			"prod": {
				Host: "prod.example.com",
				Port: 8006,
				Auth: config.AuthBlock{Type: "token", Secret: "tok"},
			},
			"staging": {
				Host: "staging.example.com",
				Port: 8006,
				Auth: config.AuthBlock{Type: "token", Secret: "tok2"},
			},
		},
	}
	require.NoError(t, config.Save(path, cfg))

	loaded, err := config.Load(path)
	require.NoError(t, err)
	require.Equal(t, "prod", loaded.CurrentContext)
	require.Equal(t, "staging", loaded.PreviousContext)

	// Verify YAML key emitted correctly.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), "previous-context:")
}

// ── unknown-key tolerance ─────────────────────────────────────────────────────

func TestLoad_UnknownKeys_Tolerated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	// YAML with unknown top-level and nested keys should not error on load.
	yml := `current-context: dev
unknown-top-key: ignored
contexts:
  dev:
    host: dev.example.com
    port: 8006
    unknown-context-key: also-ignored
    auth:
      type: token
      secret: tok
`
	require.NoError(t, os.WriteFile(path, []byte(yml), 0o600))

	loaded, err := config.Load(path)
	require.NoError(t, err)
	require.Equal(t, "dev", loaded.CurrentContext)
	require.NotNil(t, loaded.Contexts["dev"])
	require.Equal(t, "dev.example.com", loaded.Contexts["dev"].Host)
}

// ── ApplyDefaults ─────────────────────────────────────────────────────────────

func TestApplyDefaults_FillsMissingFields(t *testing.T) {
	c := &config.Context{Host: "host.example.com"}
	config.ApplyDefaults(c)
	require.Equal(t, 8006, c.Port)
	require.Equal(t, "https", c.Protocol)
	require.Equal(t, "pam", c.Realm)
}

func TestApplyDefaults_PreservesExplicitValues(t *testing.T) {
	c := &config.Context{
		Host:     "host.example.com",
		Port:     8007,
		Protocol: "http",
		Realm:    "ldap",
	}
	config.ApplyDefaults(c)
	require.Equal(t, 8007, c.Port, "explicit port must not be overwritten")
	require.Equal(t, "http", c.Protocol, "explicit protocol must not be overwritten")
	require.Equal(t, "ldap", c.Realm, "explicit realm must not be overwritten")
}

// ── ValidateContext ────────────────────────────────────────────────────────────

func TestValidateContext_ValidTokenAuth_NoError(t *testing.T) {
	c := &config.Context{
		Host: "host.example.com",
		Auth: config.AuthBlock{Type: "token", Secret: "s"},
	}
	require.NoError(t, config.ValidateContext(c))
}

func TestValidateContext_MissingHost_ReturnsError(t *testing.T) {
	c := &config.Context{
		Auth: config.AuthBlock{Type: "token", Secret: "s"},
	}
	err := config.ValidateContext(c)
	require.Error(t, err)
	require.Contains(t, err.Error(), "host is required")
}

// ── StrictValidateContext ──────────────────────────────────────────────────────

func TestStrictValidateContext_FullyValidContext_NoErrors(t *testing.T) {
	c := &config.Context{
		Host:     "host.example.com",
		Port:     8006,
		Protocol: "https",
		Auth:     config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "deploy", Secret: "s"},
		TLS:      config.TLSBlock{Fingerprint: strictFingerprint},
	}
	errs := config.StrictValidateContext(c)
	require.Empty(t, errs)
}

func TestStrictValidateContext_MissingTokenID_ReturnsWarning(t *testing.T) {
	// Lenient ValidateContext accepts a token auth with no token-id, but the
	// strict write-time rule set requires it.
	c := &config.Context{
		Host: "host.example.com",
		Auth: config.AuthBlock{Type: "token", Secret: "s"},
	}
	require.NoError(t, config.ValidateContext(c), "lenient validation should not require token-id")

	errs := config.StrictValidateContext(c)
	require.NotEmpty(t, errs)
	require.Contains(t, errs, "auth.token-id is required for token auth")
}

// TestStrictValidateContext_TokenMissingUsername pins that token auth requires
// a username: the Proxmox API header is USER@REALM!TOKENID=SECRET, so a context
// without a username cannot authenticate.
func TestStrictValidateContext_TokenMissingUsername(t *testing.T) {
	c := &config.Context{
		Host: "host.example.com",
		Auth: config.AuthBlock{Type: "token", TokenID: "deploy", Secret: "s"},
	}
	errs := config.StrictValidateContext(c)
	require.Contains(t, errs, "auth.username is required for token auth (user@realm, e.g. root@pam)")
}

// TestStrictValidateContext_UsernameWithBang catches the exact hand-edited
// misconfiguration where the full token id was pasted into auth.username.
func TestStrictValidateContext_UsernameWithBang(t *testing.T) {
	c := &config.Context{
		Host: "host.example.com",
		Auth: config.AuthBlock{Type: "token", Username: "root@pam!deploy", TokenID: "deploy", Secret: "s"},
	}
	errs := config.StrictValidateContext(c)
	require.Contains(t, errs,
		`auth.username must not contain "!"; put the token name in auth.token-id, not the username`)
}

// TestStrictValidateContext_TokenIDWithAtOrBang catches a full user@realm!name
// identifier that landed in auth.token-id instead of being split.
func TestStrictValidateContext_TokenIDWithAtOrBang(t *testing.T) {
	c := &config.Context{
		Host: "host.example.com",
		Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "root@pam!deploy", Secret: "s"},
	}
	errs := config.StrictValidateContext(c)
	require.Contains(t, errs,
		`auth.token-id must be the token name only (no "@" or "!"); put user@realm in auth.username`)
}

// strictFingerprint is a syntactically valid colon-separated hex SHA-256
// fingerprint (32 pairs) used to exercise the passing StrictValidateContext case.
const strictFingerprint = "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:" +
	"AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"

// ── ResolveContext ─────────────────────────────────────────────────────────────

func TestResolveContext_UsesCurrentContext(t *testing.T) {
	cfg := sampleConfig()
	ctx, name, err := config.ResolveContext(cfg, "")
	require.NoError(t, err)
	require.Equal(t, "prod", name)
	require.Equal(t, "pve.example.com", ctx.Host)
}

func TestResolveContext_OverrideOverridesCurrentContext(t *testing.T) {
	cfg := sampleConfig()
	ctx, name, err := config.ResolveContext(cfg, "staging")
	require.NoError(t, err)
	require.Equal(t, "staging", name)
	require.Equal(t, "pve-staging.example.com", ctx.Host)
}

func TestResolveContext_NotFound(t *testing.T) {
	cfg := sampleConfig()
	_, _, err := config.ResolveContext(cfg, "doesnotexist")
	require.Error(t, err)
	require.Contains(t, err.Error(), "doesnotexist")
}

func TestResolveContext_NoCurrentContext_NoOverride(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]*config.Context{}}
	_, _, err := config.ResolveContext(cfg, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no context specified")
}

func TestResolveContext_NilConfig(t *testing.T) {
	_, _, err := config.ResolveContext(nil, "prod")
	require.Error(t, err)
}

func TestResolveContext_AppliesDefaults(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "minimal",
		Contexts: map[string]*config.Context{
			"minimal": {
				Host: "192.0.2.1",
				Auth: config.AuthBlock{
					Type:   "token",
					Secret: "tok",
				},
			},
		},
	}
	ctx, _, err := config.ResolveContext(cfg, "")
	require.NoError(t, err)
	require.Equal(t, 8006, ctx.Port)
	require.Equal(t, "https", ctx.Protocol)
	require.Equal(t, "pam", ctx.Realm)
}

func TestResolveContext_ValidatesHostRequired(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "bad",
		Contexts: map[string]*config.Context{
			"bad": {
				Auth: config.AuthBlock{Type: "token", Secret: "s"},
			},
		},
	}
	_, _, err := config.ResolveContext(cfg, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "host is required")
}

func TestResolveContext_ValidatesAuthType(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "bad",
		Contexts: map[string]*config.Context{
			"bad": {
				Host: "host",
				Auth: config.AuthBlock{Type: "oauth", Secret: "s"},
			},
		},
	}
	_, _, err := config.ResolveContext(cfg, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "auth.type must be")
}

// ── ResolveSecret ─────────────────────────────────────────────────────────────

func TestResolveSecret_EnvBraceForm(t *testing.T) {
	t.Setenv("TEST_PVE_SECRET", "supersecret")
	val, err := config.ResolveSecret("${TEST_PVE_SECRET}")
	require.NoError(t, err)
	require.Equal(t, "supersecret", val)
}

func TestResolveSecret_EnvDollarForm(t *testing.T) {
	t.Setenv("TEST_PVE_TOKEN", "mytoken")
	val, err := config.ResolveSecret("$TEST_PVE_TOKEN")
	require.NoError(t, err)
	require.Equal(t, "mytoken", val)
}

func TestResolveSecret_EnvUnset_ReturnsError(t *testing.T) {
	// Ensure the variable is definitely unset.
	require.NoError(t, os.Unsetenv("TEST_PVE_UNSET_12345"))
	_, err := config.ResolveSecret("${TEST_PVE_UNSET_12345}")
	require.Error(t, err)
	require.Contains(t, err.Error(), "TEST_PVE_UNSET_12345")
}

func TestResolveSecret_KeychainMissingEntry_ReturnsError(t *testing.T) {
	// A reference to an entry that does not exist must error rather than return a
	// value. On macOS this exercises the real security(1) lookup (item not found);
	// off macOS it exercises the unsupported-platform stub.
	ref := "keychain:pmx-cli-test-nonexistent-" + t.Name()
	_, err := config.ResolveSecret(ref)
	require.Error(t, err)
	if runtime.GOOS == "darwin" {
		require.Contains(t, err.Error(), "keychain lookup")
	} else {
		require.Contains(t, err.Error(), "only available on macOS")
	}
}

func TestResolveSecret_LiteralValue(t *testing.T) {
	// Redirect stderr so we can verify the warning doesn't panic; we don't
	// assert the exact text here because sync.Once means prior tests may have
	// already triggered it.
	val, err := config.ResolveSecret("plaintextpassword")
	require.NoError(t, err)
	require.Equal(t, "plaintextpassword", val)
}

func TestResolveSecret_BareDollar_IsLiteral(t *testing.T) {
	// "$" alone is not a valid env reference, so it is treated as a literal
	// secret rather than a hard error.
	val, err := config.ResolveSecret("$")
	require.NoError(t, err)
	require.Equal(t, "$", val)
}

func TestResolveSecret_LeadingDollarLiteral_NotTreatedAsEnv(t *testing.T) {
	// A literal password beginning with '$' that contains characters invalid in
	// an env-var name must be returned verbatim, not looked up as an env var.
	const literal = "$up3r-S3cret!"
	val, err := config.ResolveSecret(literal)
	require.NoError(t, err)
	require.Equal(t, literal, val)
}

func TestResolveSecret_DollarNameUnset_FallsThroughToLiteral(t *testing.T) {
	// A bare $NAME whose env var is unset is a literal secret, not a failure.
	require.NoError(t, os.Unsetenv("TEST_PVE_UNSET_67890"))
	val, err := config.ResolveSecret("$TEST_PVE_UNSET_67890")
	require.NoError(t, err)
	require.Equal(t, "$TEST_PVE_UNSET_67890", val)
}

func TestResolveSecret_BraceNameUnset_StillErrors(t *testing.T) {
	// The explicit ${NAME} form remains a hard error when unset, preserving the
	// strict env-reference contract.
	require.NoError(t, os.Unsetenv("TEST_PVE_UNSET_BRACE_999"))
	_, err := config.ResolveSecret("${TEST_PVE_UNSET_BRACE_999}")
	require.Error(t, err)
}

// ── Save / Load round-trip ────────────────────────────────────────────────────

func TestSave_Atomic_ThenLoad_Equality(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pmx", "config.yml")

	original := sampleConfig()
	require.NoError(t, config.Save(path, original))

	// Verify the written file has 0600 permissions.
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	loaded, err := config.Load(path)
	require.NoError(t, err)

	require.Equal(t, original.CurrentContext, loaded.CurrentContext)
	require.Equal(t, original.DefaultOutput, loaded.DefaultOutput)
	require.Len(t, loaded.Contexts, len(original.Contexts))

	for name, orig := range original.Contexts {
		got, ok := loaded.Contexts[name]
		require.True(t, ok, "context %q missing after round-trip", name)
		require.Equal(t, orig.Host, got.Host)
		require.Equal(t, orig.Port, got.Port)
		require.Equal(t, orig.Auth.Type, got.Auth.Type)
		require.Equal(t, orig.Auth.Secret, got.Auth.Secret)
		require.Equal(t, orig.TLS.Insecure, got.TLS.Insecure)
		require.Equal(t, orig.TLS.Fingerprint, got.TLS.Fingerprint)
		require.Equal(t, orig.TLS.CACert, got.TLS.CACert)
		require.Equal(t, orig.TLS.Tofu, got.TLS.Tofu)
	}
}

func TestSave_NilConfig_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	err := config.Save(path, nil)
	require.Error(t, err)
}

func TestSave_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c", "config.yml")
	require.NoError(t, config.Save(nested, sampleConfig()))
	_, err := os.Stat(nested)
	require.NoError(t, err)
}

func TestSave_RejectsGroupReadableExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	// Create a file with group-readable bits.
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o640))

	err := config.Save(path, sampleConfig())
	require.Error(t, err)
	require.Contains(t, err.Error(), "group or world read/write bits")
}

func TestSave_TightensExistingLooseLeafDir(t *testing.T) {
	parent := t.TempDir()
	leaf := filepath.Join(parent, "pve")
	// Pre-create the leaf config directory with group/world-traversable perms,
	// the common default that MkdirAll would not tighten on its own.
	require.NoError(t, os.MkdirAll(leaf, 0o755))
	require.NoError(t, os.Chmod(leaf, 0o755))

	path := filepath.Join(leaf, "config.yml")
	require.NoError(t, config.Save(path, sampleConfig()))

	info, err := os.Stat(leaf)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm(),
		"leaf config dir should be tightened to 0700")
}

func TestSaveForce_OverridesPermissionCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	// Create a file with group-readable bits.
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o640))

	err := config.SaveForce(path, sampleConfig())
	require.NoError(t, err)

	// After save, permissions should be 0600.
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// ── Resolve (precedence) ──────────────────────────────────────────────────────

func TestResolve_FlagWinsOverAll(t *testing.T) {
	t.Setenv("PMX_TEST_KEY", "envval")
	got := config.Resolve("flagval", "PMX_TEST_KEY", "cfgval", "default")
	require.Equal(t, "flagval", got)
}

func TestResolve_EnvWinsOverCfgAndDefault(t *testing.T) {
	t.Setenv("PMX_TEST_KEY2", "envval2")
	got := config.Resolve("", "PMX_TEST_KEY2", "cfgval", "default")
	require.Equal(t, "envval2", got)
}

func TestResolve_CfgWinsOverDefault(t *testing.T) {
	t.Setenv("PMX_TEST_KEY3", "")
	got := config.Resolve("", "PMX_TEST_KEY3", "cfgval", "default")
	require.Equal(t, "cfgval", got)
}

func TestResolve_DefaultWhenAllEmpty(t *testing.T) {
	require.NoError(t, os.Unsetenv("PMX_TEST_KEY4"))
	got := config.Resolve("", "PMX_TEST_KEY4", "", "default")
	require.Equal(t, "default", got)
}

func TestResolve_EmptyEnvKey_SkipsEnvLookup(t *testing.T) {
	got := config.Resolve("", "", "cfgval", "default")
	require.Equal(t, "cfgval", got)
}

func TestResolve_AllEmpty_ReturnsEmptyString(t *testing.T) {
	got := config.Resolve("", "", "", "")
	require.Equal(t, "", got)
}

// ── TLSBlock zero-value round-trip ────────────────────────────────────────────

func TestTLSBlock_ZeroValue_RoundTrip(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "t",
		Contexts: map[string]*config.Context{
			"t": {
				Host:     "host",
				Port:     8006,
				Protocol: "https",
				Realm:    "pam",
				Auth: config.AuthBlock{
					Type:   "token",
					Secret: "s",
				},
				// TLS left at zero value — no insecure, no fingerprint, no ca-cert, no tofu.
			},
		},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	require.NoError(t, config.Save(path, cfg))

	loaded, err := config.Load(path)
	require.NoError(t, err)
	ctx := loaded.Contexts["t"]
	require.False(t, ctx.TLS.Insecure)
	require.Empty(t, ctx.TLS.Fingerprint)
	require.Empty(t, ctx.TLS.CACert)
	require.False(t, ctx.TLS.Tofu)
}

// ── SSHBlock round-trip ────────────────────────────────────────────────────────

// TestSSHBlock_RoundTrip verifies that a fully populated ssh block survives a
// save/load cycle unchanged.
func TestSSHBlock_RoundTrip(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "t",
		Contexts: map[string]*config.Context{
			"t": {
				Host:     "host",
				Port:     8006,
				Protocol: "https",
				Realm:    "pam",
				Auth: config.AuthBlock{
					Type:   "token",
					Secret: "s",
				},
				SSH: config.SSHBlock{
					User:     "admin",
					Port:     2222,
					Identity: "/home/user/.ssh/id_ed25519",
				},
			},
		},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	require.NoError(t, config.Save(path, cfg))

	loaded, err := config.Load(path)
	require.NoError(t, err)
	ctx := loaded.Contexts["t"]
	require.Equal(t, "admin", ctx.SSH.User)
	require.Equal(t, 2222, ctx.SSH.Port)
	require.Equal(t, "/home/user/.ssh/id_ed25519", ctx.SSH.Identity)
}

// TestSSHBlock_ZeroValue_RoundTrip verifies that an unset ssh block round-trips
// as all-zero values, so commands can treat zero as "not set" and fall back to
// their own compiled-in defaults (user "root", port 22).
func TestSSHBlock_ZeroValue_RoundTrip(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "t",
		Contexts: map[string]*config.Context{
			"t": {
				Host:     "host",
				Port:     8006,
				Protocol: "https",
				Realm:    "pam",
				Auth: config.AuthBlock{
					Type:   "token",
					Secret: "s",
				},
				// SSH left at zero value — no user, no port, no identity.
			},
		},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	require.NoError(t, config.Save(path, cfg))

	loaded, err := config.Load(path)
	require.NoError(t, err)
	ctx := loaded.Contexts["t"]
	require.Empty(t, ctx.SSH.User)
	require.Zero(t, ctx.SSH.Port)
	require.Empty(t, ctx.SSH.Identity)
}

// TestStrictValidateContext_SSHPortOutOfRange_ReturnsError verifies that
// ssh.port is bounds-checked the same way the top-level port is.
func TestStrictValidateContext_SSHPortOutOfRange_ReturnsError(t *testing.T) {
	c := &config.Context{
		Host:     "host.example.com",
		Port:     8006,
		Protocol: "https",
		Auth:     config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "deploy", Secret: "s"},
		TLS:      config.TLSBlock{Fingerprint: strictFingerprint},
		SSH:      config.SSHBlock{Port: 70000},
	}
	errs := config.StrictValidateContext(c)
	require.NotEmpty(t, errs)
	require.Contains(t, errs, "ssh.port 70000 is out of range [1, 65535]")
}

// TestStrictValidateContext_SSHPortZero_NoError verifies that an unset
// ssh.port (zero value) is not flagged as out of range.
func TestStrictValidateContext_SSHPortZero_NoError(t *testing.T) {
	c := &config.Context{
		Host:     "host.example.com",
		Port:     8006,
		Protocol: "https",
		Auth:     config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "deploy", Secret: "s"},
		TLS:      config.TLSBlock{Fingerprint: strictFingerprint},
	}
	errs := config.StrictValidateContext(c)
	require.Empty(t, errs)
}

// ── Product ────────────────────────────────────────────────────────────────

// TestProduct_RoundTrip verifies that an explicit product value survives a
// save/load cycle and is emitted under the "product:" yaml key.
func TestProduct_RoundTrip(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "backup",
		Contexts: map[string]*config.Context{
			"backup": {
				Host:     "pbs.example.com",
				Port:     8007,
				Protocol: "https",
				Realm:    "pam",
				Product:  config.ProductPBS,
				Auth: config.AuthBlock{
					Type:   "token",
					Secret: "s",
				},
			},
		},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	require.NoError(t, config.Save(path, cfg))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), "product: pbs")

	loaded, err := config.Load(path)
	require.NoError(t, err)
	require.Equal(t, config.ProductPBS, loaded.Contexts["backup"].Product)
}

// TestProduct_EmptyOmittedFromYAML verifies that an unset Product does not
// emit a "product:" key, preserving round-trip compatibility with config
// files written before Product existed.
func TestProduct_EmptyOmittedFromYAML(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "t",
		Contexts: map[string]*config.Context{
			"t": {
				Host:     "host",
				Port:     8006,
				Protocol: "https",
				Realm:    "pam",
				Auth:     config.AuthBlock{Type: "token", Secret: "s"},
			},
		},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	require.NoError(t, config.Save(path, cfg))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "product:")

	loaded, err := config.Load(path)
	require.NoError(t, err)
	require.Empty(t, loaded.Contexts["t"].Product)
}

// TestContext_IsPBS covers both branches of Context.IsPBS: an explicit "pbs"
// product, and the backward-compat empty-Product case which must NOT report
// as PBS (it defaults to PVE).
func TestContext_IsPBS(t *testing.T) {
	pbs := &config.Context{Product: config.ProductPBS}
	require.True(t, pbs.IsPBS())

	pve := &config.Context{Product: config.ProductPVE}
	require.False(t, pve.IsPBS())

	empty := &config.Context{}
	require.False(t, empty.IsPBS(), "empty Product must be treated as pve, not pbs")
}

// ── ApplyDefaults — Product / product-aware Port ─────────────────────────────

// TestApplyDefaults_ProductDefaultsToPVE verifies an unset Product defaults
// to "pve".
func TestApplyDefaults_ProductDefaultsToPVE(t *testing.T) {
	c := &config.Context{Host: "host.example.com"}
	config.ApplyDefaults(c)
	require.Equal(t, config.ProductPVE, c.Product)
}

// TestApplyDefaults_ProductPreservesExplicitValue verifies an explicit
// Product is not overwritten by ApplyDefaults.
func TestApplyDefaults_ProductPreservesExplicitValue(t *testing.T) {
	c := &config.Context{Host: "host.example.com", Product: config.ProductPBS}
	config.ApplyDefaults(c)
	require.Equal(t, config.ProductPBS, c.Product, "explicit product must not be overwritten")
}

// TestApplyDefaults_PortDefaultIsProductAware verifies the Port default is
// 8006 for pve and 8007 for pbs when Port is unset.
func TestApplyDefaults_PortDefaultIsProductAware(t *testing.T) {
	cases := []struct {
		name     string
		product  string
		wantPort int
	}{
		{"empty product defaults to pve port", "", 8006},
		{"explicit pve", config.ProductPVE, 8006},
		{"explicit pbs", config.ProductPBS, 8007},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &config.Context{Host: "host.example.com", Product: tc.product}
			config.ApplyDefaults(c)
			require.Equal(t, tc.wantPort, c.Port)
		})
	}
}

// TestApplyDefaults_ExplicitPortNotOverriddenByProduct verifies an explicit
// Port is preserved even when Product is pbs (which would otherwise default
// to 8007).
func TestApplyDefaults_ExplicitPortNotOverriddenByProduct(t *testing.T) {
	c := &config.Context{Host: "host.example.com", Product: config.ProductPBS, Port: 9999}
	config.ApplyDefaults(c)
	require.Equal(t, 9999, c.Port, "explicit port must not be overwritten by product-aware default")
}

// ── StrictValidateContext — Product ───────────────────────────────────────────

// TestStrictValidateContext_InvalidProduct_ReturnsError verifies an
// unrecognised product value is rejected with a clear error message.
func TestStrictValidateContext_InvalidProduct_ReturnsError(t *testing.T) {
	c := &config.Context{
		Host:    "host.example.com",
		Product: "vmware",
		Auth:    config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "deploy", Secret: "s"},
	}
	errs := config.StrictValidateContext(c)
	require.Contains(t, errs, `product "vmware" must be "pve" or "pbs"`)
}

// TestStrictValidateContext_EmptyOrValidProduct_NoProductError verifies that
// "", "pve", and "pbs" all pass the product check.
func TestStrictValidateContext_EmptyOrValidProduct_NoProductError(t *testing.T) {
	for _, product := range []string{"", config.ProductPVE, config.ProductPBS} {
		c := &config.Context{
			Host:    "host.example.com",
			Product: product,
			Auth:    config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "deploy", Secret: "s"},
		}
		errs := config.StrictValidateContext(c)
		for _, e := range errs {
			require.NotContains(t, e, "product", "product %q must not raise a product error", product)
		}
	}
}

// ── ResolveContext — Product-aware defaults ───────────────────────────────────

// TestResolveContext_PBSContext_DefaultsPort8007 verifies ResolveContext
// applies the pbs-specific port default end-to-end.
func TestResolveContext_PBSContext_DefaultsPort8007(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "backup",
		Contexts: map[string]*config.Context{
			"backup": {
				Host:    "pbs.example.com",
				Product: config.ProductPBS,
				Auth:    config.AuthBlock{Type: "token", Secret: "tok"},
			},
		},
	}
	ctx, _, err := config.ResolveContext(cfg, "")
	require.NoError(t, err)
	require.Equal(t, 8007, ctx.Port)
	require.Equal(t, config.ProductPBS, ctx.Product)
}

// TestResolveContext_LenientValidate_UnknownProduct_NotRejected pins the
// documented leniency: unlike StrictValidateContext, load-time validation
// (via ResolveContext) does not reject an unrecognised product, so CLI
// startup does not hard-fail on a hand-edited or future config value.
func TestResolveContext_LenientValidate_UnknownProduct_NotRejected(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "weird",
		Contexts: map[string]*config.Context{
			"weird": {
				Host:    "host.example.com",
				Product: "vmware",
				Auth:    config.AuthBlock{Type: "token", Secret: "tok"},
			},
		},
	}
	_, _, err := config.ResolveContext(cfg, "")
	require.NoError(t, err, "load-time validation must not reject an unrecognised product")
}
