package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/config"
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
					Secret:   "${PVE_TOKEN_SECRET}",
				},
				TLS: config.TLSBlock{
					Insecure:    false,
					Fingerprint: "AA:BB:CC",
					CACert:      "/etc/ssl/certs/ca.pem",
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
					Secret:   "$PVE_STAGING_PASS",
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
	require.Equal(t, filepath.Join("/custom/xdg", "pve", "config.yml"), got)
}

func TestDefaultPath_XDGUnset(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	got := config.DefaultPath()
	require.Equal(t, filepath.Join(home, ".config", "pve", "config.yml"), got)
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
	require.Equal(t, "${PVE_TOKEN_SECRET}", prod.Auth.Secret)
	require.Equal(t, "AA:BB:CC", prod.TLS.Fingerprint)
	require.Equal(t, "/etc/ssl/certs/ca.pem", prod.TLS.CACert)

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
	ref := "keychain:pve-cli-test-nonexistent-" + t.Name()
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
	path := filepath.Join(dir, "pve", "config.yml")

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
	t.Setenv("PVE_TEST_KEY", "envval")
	got := config.Resolve("flagval", "PVE_TEST_KEY", "cfgval", "default")
	require.Equal(t, "flagval", got)
}

func TestResolve_EnvWinsOverCfgAndDefault(t *testing.T) {
	t.Setenv("PVE_TEST_KEY2", "envval2")
	got := config.Resolve("", "PVE_TEST_KEY2", "cfgval", "default")
	require.Equal(t, "envval2", got)
}

func TestResolve_CfgWinsOverDefault(t *testing.T) {
	t.Setenv("PVE_TEST_KEY3", "")
	got := config.Resolve("", "PVE_TEST_KEY3", "cfgval", "default")
	require.Equal(t, "cfgval", got)
}

func TestResolve_DefaultWhenAllEmpty(t *testing.T) {
	require.NoError(t, os.Unsetenv("PVE_TEST_KEY4"))
	got := config.Resolve("", "PVE_TEST_KEY4", "", "default")
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
				// TLS left at zero value — no insecure, no fingerprint, no ca-cert.
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
}
