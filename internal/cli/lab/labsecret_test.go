package lab

import (
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

func TestLabKeychainService_NamespacesByOuterContext(t *testing.T) {
	assert.Equal(t, "pmx-lab.fleet.demo", labKeychainService("fleet", "demo"))
	assert.Equal(t, "pmx-lab.fleet-b.test-lab", labKeychainService("fleet-b", "test-lab"))
	assert.NotEqual(t, labKeychainService("fleet-a", "demo"), labKeychainService("fleet-b", "demo"),
		"same-named labs on different outer contexts must not share a service")
	assert.NotEqual(t, labLegacyKeychainService("demo"), labKeychainService("fleet", "demo"))
}

// TestLabKeychainService_DisambiguatesHyphenSplits pins the reason the
// namespace separator is "." rather than "-": with "-", outer "a-b" plus lab
// "c" and outer "a" plus lab "b-c" would both produce "pmx-lab-a-b-c".
func TestLabKeychainService_DisambiguatesHyphenSplits(t *testing.T) {
	assert.NotEqual(t, labKeychainService("a-b", "c"), labKeychainService("a", "b-c"))
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

			svc := labKeychainService(name, "demo")
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
		{"pmx-lab.ops_east_0123456789ab.demo", true},
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
