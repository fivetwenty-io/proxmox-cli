package lab

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// testConfigHash is a fixed config hash for tests that exercise naming only.
const testConfigHash = "0a1b2c3d"

// labTestService returns the namespaced service the invocation behind deps
// stores, and may delete, the lab named name's token secret under.
func labTestService(t *testing.T, deps *cli.Deps, name string) string {
	t.Helper()
	service, err := labCurrentKeychainService(deps, name)
	require.NoError(t, err)
	return service
}

// labTestRef returns the keychain reference to the lab token item of the lab
// named name under the namespace of the invocation behind deps.
func labTestRef(t *testing.T, deps *cli.Deps, name string) string {
	t.Helper()
	return config.KeychainRef(labTestService(t, deps, name), labCtxAccount())
}

func TestLabKeychainService_NamespacesByOuterContextAndConfig(t *testing.T) {
	assert.Equal(t, "pmx-lab.fleet-0a1b2c3d.demo", labKeychainService("fleet", testConfigHash, "demo"))
	assert.Equal(t, "pmx-lab.fleet-b-0a1b2c3d.test-lab", labKeychainService("fleet-b", testConfigHash, "test-lab"))
	assert.NotEqual(t, labKeychainService("fleet-a", testConfigHash, "demo"),
		labKeychainService("fleet-b", testConfigHash, "demo"),
		"same-named labs on different outer contexts must not share a service")
	assert.NotEqual(t, labKeychainService("lab", testConfigHash, "demo"),
		labKeychainService("lab", "ffffffff", "demo"),
		"same-named labs under same-named outer contexts in different configs must not share a service")
	assert.NotEqual(t, labLegacyKeychainService("demo"), labKeychainService("fleet", testConfigHash, "demo"))
}

// TestLabKeychainService_DisambiguatesHyphenSplits pins the reason the
// namespace separator is "." rather than "-": with "-", outer "a-b" plus lab
// "c" and outer "a" plus lab "b-c" would both produce "pmx-lab-a-b-c". The
// fixed-length config hash likewise keeps an outer name ending in "-<hex>"
// from colliding with a shorter outer name under another config.
func TestLabKeychainService_DisambiguatesHyphenSplits(t *testing.T) {
	assert.NotEqual(t, labKeychainService("a-b", testConfigHash, "c"), labKeychainService("a", testConfigHash, "b-c"))
	assert.NotEqual(t, labKeychainService("a-"+testConfigHash, "ffffffff", "c"),
		labKeychainService("a", testConfigHash, "c"))
}

func TestLabConfigHash(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a", "config.yaml")
	b := filepath.Join(dir, "b", "config.yaml")

	ha, err := labConfigHash(a)
	require.NoError(t, err)
	assert.Len(t, ha, labConfigHashLen)
	assert.Regexp(t, `^[0-9a-f]+$`, ha)

	again, err := labConfigHash(filepath.Join(dir, "a", ".", "x", "..", "config.yaml"))
	require.NoError(t, err)
	assert.Equal(t, ha, again, "the path is cleaned before hashing")

	hb, err := labConfigHash(b)
	require.NoError(t, err)
	assert.NotEqual(t, ha, hb, "configs at different paths hash differently")

	t.Chdir(dir)
	rel, err := labConfigHash(filepath.Join("a", "config.yaml"))
	require.NoError(t, err)
	assert.Equal(t, ha, rel, "a relative path is made absolute before hashing")

	_, err = labConfigHash("")
	require.ErrorIs(t, err, errNoConfigPath)
}

func TestLabCurrentKeychainService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	hash, err := labConfigHash(path)
	require.NoError(t, err)

	got, err := labCurrentKeychainService(&cli.Deps{CtxName: "fleet", ConfigPath: path, Cfg: &config.Config{}}, "demo")
	require.NoError(t, err)
	assert.Equal(t, "pmx-lab.fleet-"+hash+".demo", got)
	assert.True(t, labOwnsKeychainService(got, "demo"))

	_, err = labCurrentKeychainService(&cli.Deps{ConfigPath: path, Cfg: &config.Config{}}, "demo")
	require.ErrorIs(t, err, errNoOuterContext)
	_, err = labCurrentKeychainService(&cli.Deps{CtxName: "fleet", Cfg: &config.Config{}}, "demo")
	require.ErrorIs(t, err, errNoConfigPath)
}

// keychainServiceSafe reports whether s survives `security -i` and the
// "keychain:<service>/<account>" reference format unchanged.
func keychainServiceSafe(s string) bool {
	if s == "" || strings.Contains(s, "/") {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' || r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

func TestLabKeychainNamespace_SanitizesUnsafeContextNames(t *testing.T) {
	unsafe := []string{
		"ops/east",
		"my ctx",
		"tab\there",
		"line\nbreak",
		`back\slash`,
		"dotted.name",
		"under_score",
		"ünïcode",
		strings.Repeat("x", labKeychainNamespaceMaxLen+1),
	}
	seen := map[string]string{}
	for _, name := range unsafe {
		t.Run(name, func(t *testing.T) {
			ns := labKeychainNamespace(name)
			assert.True(t, keychainServiceSafe(ns), "namespace %q must be keychain-safe", ns)
			assert.NotContains(t, ns, ".", "the namespace must never contain the service separator")
			assert.Contains(t, ns, "_", "a sanitized namespace always carries the _ marker")
			assert.Regexp(t, labKeychainNamespaceSegmentRE, ns)

			svc := labKeychainService(name, testConfigHash, "demo")
			assert.True(t, keychainServiceSafe(svc))
			assert.True(t, labOwnsKeychainService(svc, "demo"), "a sanitized service still belongs to its lab")
		})
		ns := labKeychainNamespace(name)
		if prev, dup := seen[ns]; dup {
			t.Fatalf("context names %q and %q map to the same namespace %q", prev, name, ns)
		}
		seen[ns] = name
	}
}

func TestLabKeychainNamespace_SanitizedNeverCollidesWithVerbatim(t *testing.T) {
	// "a b" sanitizes to "a_b_<hash>"; no verbatim name can contain "_", so
	// even a context literally named like the sanitized output is hashed too.
	sanitized := labKeychainNamespace("a b")
	assert.NotEqual(t, sanitized, labKeychainNamespace(sanitized))
	assert.NotEqual(t, labKeychainNamespace("a b"), labKeychainNamespace("a_b"))
	assert.NotEqual(t, labKeychainNamespace("a b"), labKeychainNamespace("a-b"))
	assert.Equal(t, "a-b", labKeychainNamespace("a-b"))
	assert.Equal(t, labKeychainNamespace("ops/east"), labKeychainNamespace("ops/east"), "deterministic")
}

func TestLabOwnsKeychainService(t *testing.T) {
	tests := []struct {
		service string
		want    bool
	}{
		{"pmx-lab-demo", true},
		{"pmx-lab.fleet.demo", true},
		{"pmx-lab.fleet-0a1b2c3d.demo", true},
		{"pmx-lab.ops_east_0123456789ab-0a1b2c3d.demo", true},
		{"pmx-lab-demo2", false},
		{"pmx-lab.fleet.demo2", false},
		{"pmx-lab.fleet.demo.extra", false},
		{"pmx-lab..demo", false},
		{"pmx-lab.fl/eet.demo", false},
		{"pmx-lab-other", false},
		{"ops-root", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.service, func(t *testing.T) {
			assert.Equal(t, tt.want, labOwnsKeychainService(tt.service, "demo"))
		})
	}
}

func TestLabOwnedSecretRef(t *testing.T) {
	svc, acct, ok := labOwnedSecretRef("keychain:pmx-lab-demo/pmx@pve!pmx", "demo")
	require.True(t, ok)
	assert.Equal(t, "pmx-lab-demo", svc)
	assert.Equal(t, "pmx@pve!pmx", acct)

	svc, _, ok = labOwnedSecretRef("keychain:pmx-lab.fleet.demo/pmx@pve!pmx", "demo")
	require.True(t, ok)
	assert.Equal(t, "pmx-lab.fleet.demo", svc)

	for _, ref := range []string{
		"",
		"${LAB_TOKEN}",
		"literal-secret",
		"keychain:pmx-lab-demo",              // bare service names no account
		"keychain:pmx-lab-demo/root@pam!pmx", // another identity's item
		"keychain:ops-root/pmx@pve!pmx",      // not a lab service
		"keychain:pmx-lab.fleet.demo2/pmx@pve!pmx", // another lab's item
	} {
		_, _, ok := labOwnedSecretRef(ref, "demo")
		assert.False(t, ok, "ref %q must not count as lab demo's secret", ref)
	}
}

func TestLabKeychainItemReferencedBy(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]*config.Context{
		"lab-demo": {Auth: config.AuthBlock{Secret: "keychain:pmx-lab-demo/pmx@pve!pmx"}},
		"zeta":     {Auth: config.AuthBlock{Secret: "keychain:pmx-lab-demo"}},
		"alpha":    {Proxy: config.ProxyBlock{Password: "keychain:pmx-lab-demo/pmx@pve!pmx"}},
		"nil-ctx":  nil,
		"other":    {Auth: config.AuthBlock{Secret: "keychain:pmx-lab-demo/root@pam!pmx"}},
	}}

	assert.Equal(t, "alpha", labKeychainItemReferencedBy(cfg, "lab-demo", "pmx-lab-demo", "pmx@pve!pmx"),
		"a proxy password reference counts, and the sorted first match wins")
	delete(cfg.Contexts, "alpha")
	assert.Equal(t, "zeta", labKeychainItemReferencedBy(cfg, "lab-demo", "pmx-lab-demo", "pmx@pve!pmx"),
		"a bare service reference covers every account")
	delete(cfg.Contexts, "zeta")
	assert.Equal(t, "", labKeychainItemReferencedBy(cfg, "lab-demo", "pmx-lab-demo", "pmx@pve!pmx"))
	assert.Equal(t, "lab-demo", labKeychainItemReferencedBy(cfg, "", "pmx-lab-demo", "pmx@pve!pmx"))
	assert.Equal(t, "", labKeychainItemReferencedBy(nil, "", "pmx-lab-demo", "pmx@pve!pmx"))
}

func TestLabOuterContextName(t *testing.T) {
	got, err := labOuterContextName(&cli.Deps{CtxName: "fleet", Cfg: &config.Config{CurrentContext: "other"}})
	require.NoError(t, err)
	assert.Equal(t, "fleet", got, "the invocation's selected context wins")

	got, err = labOuterContextName(&cli.Deps{Cfg: &config.Config{CurrentContext: "other"}})
	require.NoError(t, err)
	assert.Equal(t, "other", got, "current-context is the fallback")

	_, err = labOuterContextName(&cli.Deps{Cfg: &config.Config{}})
	require.ErrorIs(t, err, errNoOuterContext)
	_, err = labOuterContextName(nil)
	require.ErrorIs(t, err, errNoOuterContext)
}
