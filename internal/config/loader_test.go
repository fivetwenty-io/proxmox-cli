package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// writeConfigFile writes an empty (or near-empty) config.yml to dir/name so
// tests have a configPath to resolve globs and file-mode checks against, and
// returns its full path. mode is applied after creation.
func writeConfigFile(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("current-context: \"\"\n"), 0o600))
	require.NoError(t, os.Chmod(path, mode))
	return path
}

func TestResolveLabs_InlineOnly_NameDefaultsToMapKey(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": {Mode: "nested"},
			"beta":  {Name: "beta-explicit", Mode: "nested"},
		},
	}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Len(t, labs, 2)

	require.NotNil(t, labs["alpha"])
	require.Equal(t, "alpha", labs["alpha"].Name)

	require.NotNil(t, labs["beta-explicit"])
	require.Equal(t, "beta-explicit", labs["beta-explicit"].Name)
	require.Nil(t, labs["beta"])
}

func TestResolveLabs_IncludeGlob_NameFromFileKeyAndFilenameStem(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "gamma.yaml"),
		[]byte("name: gamma-explicit\nmode: nested\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "delta.yaml"),
		[]byte("mode: nested\n"),
		0o600,
	))

	cfg := &config.Config{Include: []string{"*.yaml"}}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Len(t, labs, 2)

	require.NotNil(t, labs["gamma-explicit"])
	require.Equal(t, "gamma-explicit", labs["gamma-explicit"].Name)

	require.NotNil(t, labs["delta"])
	require.Equal(t, "delta", labs["delta"].Name)
}

func TestResolveLabs_LabsDirSugar_ResolvesDirGlob(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	labsDir := filepath.Join(dir, "labs.d")
	require.NoError(t, os.MkdirAll(labsDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(labsDir, "epsilon.yaml"),
		[]byte("mode: nested\n"),
		0o600,
	))

	cfg := &config.Config{LabsDir: "labs.d"}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Len(t, labs, 1)
	require.NotNil(t, labs["epsilon"])
}

func TestResolveLabs_DuplicateName_InlineVsInclude_Errors(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	labFile := filepath.Join(dir, "zeta.yaml")
	require.NoError(t, os.WriteFile(labFile, []byte("name: zeta\nmode: nested\n"), 0o600))

	cfg := &config.Config{
		Labs:    map[string]*config.Lab{"zeta": {Mode: "nested"}},
		Include: []string{"*.yaml"},
	}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, `duplicate lab "zeta"`)
	require.ErrorContains(t, err, "config.yml (inline)")
	require.ErrorContains(t, err, labFile)
}

func TestResolveLabs_DuplicateName_IncludeVsInclude_Errors(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	subA := filepath.Join(dir, "a")
	subB := filepath.Join(dir, "b")
	require.NoError(t, os.MkdirAll(subA, 0o700))
	require.NoError(t, os.MkdirAll(subB, 0o700))

	fileA := filepath.Join(subA, "eta.yaml")
	fileB := filepath.Join(subB, "eta.yaml")
	require.NoError(t, os.WriteFile(fileA, []byte("mode: nested\n"), 0o600))
	require.NoError(t, os.WriteFile(fileB, []byte("mode: nested\n"), 0o600))

	cfg := &config.Config{Include: []string{"a/*.yaml", "b/*.yaml"}}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, `duplicate lab "eta"`)
	require.ErrorContains(t, err, fileA)
	require.ErrorContains(t, err, fileB)
}

func TestResolveLabs_SameFileMatchedByOverlappingGlobs_LoadsOnce(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	labsDir := filepath.Join(dir, "labs.d")
	require.NoError(t, os.MkdirAll(labsDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(labsDir, "theta.yaml"),
		[]byte("mode: nested\n"),
		0o600,
	))

	// labs_dir expands to labs.d/*.yaml — the explicit include overlaps it,
	// so both globs match the same file. Relative and absolute forms of the
	// same pattern must also collapse to one load.
	cfg := &config.Config{
		LabsDir: "labs.d",
		Include: []string{"labs.d/*.yaml", filepath.Join(labsDir, "*.yaml")},
	}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Len(t, labs, 1)
	require.NotNil(t, labs["theta"])
	require.Equal(t, "theta", labs["theta"].Name)
}

func TestResolveLabs_RelativeGlob_ResolvesAgainstConfigDir(t *testing.T) {
	dir := t.TempDir()
	nestedDir := filepath.Join(dir, "conf")
	require.NoError(t, os.MkdirAll(nestedDir, 0o700))
	configPath := writeConfigFile(t, nestedDir, "config.yml", 0o600)

	require.NoError(t, os.WriteFile(
		filepath.Join(nestedDir, "theta.yaml"),
		[]byte("mode: nested\n"),
		0o600,
	))

	cfg := &config.Config{Include: []string{"*.yaml"}}

	// A relative working directory elsewhere must not affect resolution:
	// the glob resolves against configPath's directory, not os.Getwd().
	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Len(t, labs, 1)
	require.NotNil(t, labs["theta"])
}

func TestResolveLabs_FileMode0600Enforcement(t *testing.T) {
	t.Run("group-readable config with secret errors", func(t *testing.T) {
		dir := t.TempDir()
		configPath := writeConfigFile(t, dir, "config.yml", 0o644)
		cfg := &config.Config{DefaultUserPassword: "s3cret-test!"}

		_, err := config.ResolveLabs(cfg, configPath)
		require.Error(t, err)
		require.ErrorContains(t, err, "chmod 0600")
		require.NotContains(t, err.Error(), "s3cret-test!")
	})

	t.Run("group-readable config without secret is fine", func(t *testing.T) {
		dir := t.TempDir()
		configPath := writeConfigFile(t, dir, "config.yml", 0o644)
		cfg := &config.Config{}

		_, err := config.ResolveLabs(cfg, configPath)
		require.NoError(t, err)
	})

	t.Run("0600 config with secret is fine", func(t *testing.T) {
		dir := t.TempDir()
		configPath := writeConfigFile(t, dir, "config.yml", 0o600)
		cfg := &config.Config{DefaultUserPassword: "s3cret-test!"}

		_, err := config.ResolveLabs(cfg, configPath)
		require.NoError(t, err)
	})
}

func TestResolveLabs_MalformedLabFile_ErrorsWithPath(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	badFile := filepath.Join(dir, "broken.yaml")
	require.NoError(t, os.WriteFile(badFile, []byte("mode: [nested\n"), 0o600))

	cfg := &config.Config{Include: []string{"*.yaml"}}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, badFile)
}

func TestResolveLabs_EmptyLabFile_Errors(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	emptyFile := filepath.Join(dir, "empty.yaml")
	require.NoError(t, os.WriteFile(emptyFile, []byte(""), 0o600))

	cfg := &config.Config{Include: []string{"*.yaml"}}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, emptyFile)
	require.ErrorContains(t, err, "is empty")
}

func TestResolveLabs_CommentOnlyLabFile_Errors(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	commentFile := filepath.Join(dir, "commented.yaml")
	require.NoError(t, os.WriteFile(
		commentFile,
		[]byte("# this is meant to become a lab someday\n# still nothing here\n\n"),
		0o600,
	))

	cfg := &config.Config{Include: []string{"*.yaml"}}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, commentFile)
	require.ErrorContains(t, err, "is empty")
}

func TestResolveLabs_ConfigShapedLabFile_ErrorsNamingUnknownKey(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	wrapperFile := filepath.Join(dir, "pasted.yaml")
	require.NoError(t, os.WriteFile(
		wrapperFile,
		[]byte("labs:\n  copied:\n    mode: nested\n"),
		0o600,
	))

	cfg := &config.Config{Include: []string{"*.yaml"}}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, wrapperFile)
	require.ErrorContains(t, err, "labs")
}

func TestResolveLabs_UnknownFieldInLabFile_ErrorsNamingField(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	typoFile := filepath.Join(dir, "typo.yaml")
	require.NoError(t, os.WriteFile(
		typoFile,
		[]byte("name: typo-lab\nmode: nested\nnetwork:\n  vxlan_tg: 5\n"),
		0o600,
	))

	cfg := &config.Config{Include: []string{"*.yaml"}}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, typoFile)
	require.ErrorContains(t, err, "vxlan_tg")
}

func TestResolveLabs_NilLabsMap_IsFine(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Empty(t, labs)
}

func TestResolveLabs_GlobMatchingZeroFiles_IsNotAnError(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{Include: []string{"nothing-here/*.yaml"}}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Empty(t, labs)
}

// --- vnet-ID derivation and uniqueness ------------------------------------

// TestResolveLabs_VnetIDDerivedWhenUnset covers a lab whose config leaves
// network.vnet_id empty: ResolveLabs must fill it in via
// config.DeriveVnetID(name), truncating a long name to 8 characters.
func TestResolveLabs_VnetIDDerivedWhenUnset(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"wayneeseguin": {Mode: "nested"},
		},
	}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Equal(t, "wayneese", labs["wayneeseguin"].Network.VnetID)
}

// TestResolveLabs_ExplicitVnetIDWinsOverDerived covers a lab that sets
// network.vnet_id explicitly to a value diverging from what DeriveVnetID
// would produce: the explicit value must be kept verbatim.
func TestResolveLabs_ExplicitVnetIDWinsOverDerived(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"wayneeseguin": {Mode: "nested", Network: config.LabNetwork{VnetID: "custom1"}},
		},
	}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Equal(t, "custom1", labs["wayneeseguin"].Network.VnetID)
}

// TestResolveLabs_VnetIDCollisionAcrossLabs_Errors covers two labs whose
// names truncate to the same 8-character vnet ID (both starting
// "collide-"): ResolveLabs must refuse rather than silently letting two
// labs share one vnet.
func TestResolveLabs_VnetIDCollisionAcrossLabs_Errors(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			// Both strip to no hyphens and truncate to the same first 8
			// characters ("collidea"), even though the full names differ.
			"collideaaa1": {Mode: "nested"},
			"collideaaa2": {Mode: "nested"},
		},
	}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "collidea")
	require.ErrorContains(t, err, "collideaaa1")
	require.ErrorContains(t, err, "collideaaa2")
}

// TestResolveLabs_ExplicitVnetIDCollidesWithAnother_Errors covers an
// explicit network.vnet_id that happens to collide with another lab's
// (derived or explicit) vnet ID.
func TestResolveLabs_ExplicitVnetIDCollidesWithAnother_Errors(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"alpha": {Mode: "nested", Network: config.LabNetwork{VnetID: "shared"}},
			"beta":  {Mode: "nested", Network: config.LabNetwork{VnetID: "shared"}},
		},
	}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "shared")
}

// --- topology validation --------------------------------------------------

// TestResolveLabs_InvalidTopologyNodes_Errors covers ResolveLabs wiring
// ValidateTopology in: a lab whose topology.nodes is out of [1, 5] must
// fail to resolve, naming the lab.
func TestResolveLabs_InvalidTopologyNodes_Errors(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"wayne": {Mode: "nested", Topology: config.LabTopology{Nodes: 9}},
		},
	}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "wayne")
	require.ErrorContains(t, err, "topology.nodes")
}

// TestResolveLabs_ValidTopology_Passes covers a well-formed multi-node
// topology resolving cleanly end-to-end through ResolveLabs.
func TestResolveLabs_ValidTopology_Passes(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	cfg := &config.Config{
		Labs: map[string]*config.Lab{
			"pve-cpi": {Mode: "nested", Topology: config.LabTopology{Nodes: 3}},
		},
	}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Equal(t, 3, labs["pve-cpi"].Topology.Nodes)
	require.Equal(t, "pvecpi", labs["pve-cpi"].Network.VnetID)
}

// TestResolveLabs_OSDDisksSizeMissing_Errors covers ResolveLabs wiring
// ValidateStorage in: a labs.d lab file setting storage.osd_disks.count
// without storage.osd_disks.size_gb must fail to resolve, naming the missing
// field.
func TestResolveLabs_OSDDisksSizeMissing_Errors(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	labsDir := filepath.Join(dir, "labs.d")
	require.NoError(t, os.MkdirAll(labsDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(labsDir, "ceph.yaml"),
		[]byte("mode: nested\nstorage:\n  osd_disks:\n    count: 2\n"),
		0o600,
	))

	cfg := &config.Config{LabsDir: "labs.d"}

	_, err := config.ResolveLabs(cfg, configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "ceph")
	require.ErrorContains(t, err, "osd_disks.size_gb")
}

// TestResolveLabs_NFSExtraDatasets_Parses guards the strict decode. Every
// labs.d file goes through yaml.Strict(), so a key without a matching field
// fails the whole load and takes every pmx lab command down with it. The lab
// repo's scripts/60-nfs-service reads storage.nfs_extra_datasets as a
// space-separated scalar; pmx only has to accept and carry it.
func TestResolveLabs_NFSExtraDatasets_Parses(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConfigFile(t, dir, "config.yml", 0o600)

	labsDir := filepath.Join(dir, "labs.d")
	require.NoError(t, os.MkdirAll(labsDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(labsDir, "wayneeseguin.yaml"),
		[]byte("mode: nested\nstorage:\n  nfs_quota_gb: 300\n  nfs_extra_datasets: \"agents\"\n"),
		0o600,
	))

	cfg := &config.Config{LabsDir: "labs.d"}

	labs, err := config.ResolveLabs(cfg, configPath)
	require.NoError(t, err)
	require.Equal(t, "agents", labs["wayneeseguin"].Storage.NFSExtraDatasets)
	require.Equal(t, 300, config.EffectiveNFSQuotaGB(labs["wayneeseguin"]))
}

// ── ValidateProxyBlock / ValidateTimeoutBlock ──────────────────────────────

// validProxyContext returns a Context that passes StrictValidateContext with
// an empty proxy and timeout block, so a test can set exactly the one field
// it means to exercise without any unrelated error showing up in errs.
func validProxyContext() *config.Context {
	return &config.Context{
		Host:     "host.example.com",
		Port:     8006,
		Protocol: "https",
		Auth:     config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "deploy", Secret: "s"},
	}
}

func TestStrictValidateContext_ProxyURL(t *testing.T) {
	for _, scheme := range []string{"socks5", "socks5h", "http"} {
		c := validProxyContext()
		c.Proxy.URL = scheme + "://proxy.example.com:1080"
		errs := config.StrictValidateContext(c)
		require.Empty(t, errs, "scheme %s should pass", scheme)
	}

	c := validProxyContext()
	c.Proxy.URL = "https://proxy.example.com:1080"
	errs := config.StrictValidateContext(c)
	require.Contains(t, errs, "proxy.url https://proxy.example.com:1080 must use scheme socks5, socks5h, or http")

	c = validProxyContext()
	c.Proxy.URL = "ftp://proxy.example.com:21"
	errs = config.StrictValidateContext(c)
	require.Contains(t, errs, "proxy.url ftp://proxy.example.com:21 must use scheme socks5, socks5h, or http")

	// "socks5://host:port" fails url.Parse itself (a non-numeric port), so it
	// exercises the does-not-parse rule rather than the scheme rule.
	c = validProxyContext()
	c.Proxy.URL = "socks5://host:port"
	errs = config.StrictValidateContext(c)
	require.Contains(t, errs, "proxy.url socks5://<redacted> is not a valid URL")

	c = validProxyContext()
	c.Proxy.URL = "socks5://"
	errs = config.StrictValidateContext(c)
	require.Contains(t, errs, "proxy.url socks5:// must include a host")

	// A port with no hostname is not a host: dialling ":1080" would silently
	// reach whatever listens on localhost.
	c = validProxyContext()
	c.Proxy.URL = "socks5://:1080"
	errs = config.StrictValidateContext(c)
	require.Equal(t, []string{"proxy.url socks5://:1080 must include a host"}, errs)

	// Passing case: an IPv6 literal is a hostname even though it is bracketed.
	c = validProxyContext()
	c.Proxy.URL = "socks5://[::1]:1080"
	errs = config.StrictValidateContext(c)
	require.Empty(t, errs)
}

func TestStrictValidateContext_ProxyCredentialsNeedURL(t *testing.T) {
	c := validProxyContext()
	c.Proxy.Username = "pmx"
	errs := config.StrictValidateContext(c)
	require.Equal(t, []string{"proxy.username is set but proxy.url is empty"}, errs)

	// A context setting only proxy.password produces exactly the one
	// proxy.url-is-empty message, and never also the
	// "without proxy.username" message that would fire if proxy.url were set.
	c = validProxyContext()
	c.Proxy.Password = "${PMX_PROXY_PASSWORD}"
	errs = config.StrictValidateContext(c)
	require.Equal(t, []string{"proxy.password is set but proxy.url is empty"}, errs)

	// With proxy.url set, the same unaccompanied proxy.password instead
	// triggers the "without proxy.username" rule.
	c = validProxyContext()
	c.Proxy.URL = "socks5://proxy.example.com:1080"
	c.Proxy.Password = "${PMX_PROXY_PASSWORD}"
	errs = config.StrictValidateContext(c)
	require.Contains(t, errs, "proxy.password is set without proxy.username")

	// Passing case for all three credential rules: a URL, a username, and a
	// password together produce no message at all.
	c = validProxyContext()
	c.Proxy = config.ProxyBlock{
		URL:      "socks5://proxy.example.com:1080",
		Username: "pmx",
		Password: "${PMX_PROXY_PASSWORD}",
	}
	errs = config.StrictValidateContext(c)
	require.Empty(t, errs)
}

func TestStrictValidateContext_ProxyURLAndFromEnvConflict(t *testing.T) {
	trueVal := true

	c := validProxyContext()
	c.Proxy.URL = "socks5://proxy.example.com:1080"
	c.Proxy.FromEnv = &trueVal
	errs := config.StrictValidateContext(c)
	require.Contains(t, errs, "proxy.url and proxy.from-env are both set; use one or the other")

	// Passing case: from-env alone, with proxy.url empty, is not a conflict.
	c = validProxyContext()
	c.Proxy.FromEnv = &trueVal
	errs = config.StrictValidateContext(c)
	require.Empty(t, errs)
}

func TestStrictValidateContext_ProxyURLRejectsUserinfo(t *testing.T) {
	c := validProxyContext()
	c.Proxy.URL = "socks5://user:pass@proxy.example.com:1080"
	errs := config.StrictValidateContext(c)
	require.Contains(t, errs,
		"proxy.url socks5://<redacted>@proxy.example.com:1080 must not embed credentials; "+
			"use proxy.username and proxy.password")

	// A bare username is a credential too: several proxy vendors authenticate
	// with an API key in the username position, so the message that rejects
	// embedded credentials masks the whole userinfo rather than echoing it.
	for _, raw := range []string{
		"socks5://SEKRIT@proxy.example.com:1080",
		"socks5://SEKRIT@proxy.example.com:1080/some/path?q=1#frag",
		"socks5://SEKRIT@other@proxy.example.com:1080",
	} {
		c = validProxyContext()
		c.Proxy.URL = raw
		errs = config.StrictValidateContext(c)
		require.NotEmpty(t, errs, "url %s should fail strict validation", raw)
		for _, m := range errs {
			require.NotContains(t, m, "SEKRIT", "StrictValidateContext echoed the username for %s", raw)
		}

		err := config.ValidateContext(c)
		require.Error(t, err, "url %s should fail lenient validation", raw)
		require.NotContains(t, err.Error(), "SEKRIT", "ValidateContext echoed the username for %s", raw)
	}

	c = validProxyContext()
	c.Proxy.URL = "socks5://SEKRIT@proxy.example.com:1080/some/path?q=1#frag"
	errs = config.StrictValidateContext(c)
	require.Equal(t, []string{
		"proxy.url socks5://<redacted>@proxy.example.com:1080/some/path?q=1#frag must not embed credentials; " +
			"use proxy.username and proxy.password",
	}, errs)

	// Passing case: the same host with no userinfo at all.
	c = validProxyContext()
	c.Proxy.URL = "socks5://proxy.example.com:1080"
	errs = config.StrictValidateContext(c)
	require.Empty(t, errs)
}

// TestValidateProxyBlock_NeverPrintsPassword runs three URLs whose password
// url.Parse cannot make sense of through every validation path that checks a
// proxy block — ValidateProxyBlock directly, StrictValidateContext, the
// lenient ValidateContext, and ResolveContext, which wraps the lenient check
// for CLI startup — and checks that not one produced message
// contains the password substring. url.Parse quotes its whole input in its
// own error text, and for a password containing "/" or "%" it quotes the
// password a second time in the reason, which is exactly why no message here
// may ever append a raw url.Parse error.
func TestValidateProxyBlock_NeverPrintsPassword(t *testing.T) {
	cases := []struct {
		url      string
		password string
	}{
		{"socks5://pmx:s3cr3t/x@proxy:1080", "s3cr3t"},
		{"socks5://pmx:s3%zzt@proxy:1080", "s3%zzt"},
		{"socks5://u:s3cret@[::1", "s3cret"},
	}

	for _, tc := range cases {
		msgs := config.ValidateProxyBlock(&config.ProxyBlock{URL: tc.url})
		require.NotEmpty(t, msgs, "url %s should fail validation", tc.url)
		for _, m := range msgs {
			require.NotContains(t, m, tc.password, "ValidateProxyBlock leaked password for %s", tc.url)
		}

		strictCtx := validProxyContext()
		strictCtx.Proxy.URL = tc.url
		strictErrs := config.StrictValidateContext(strictCtx)
		require.NotEmpty(t, strictErrs, "url %s should fail strict validation", tc.url)
		for _, m := range strictErrs {
			require.NotContains(t, m, tc.password, "StrictValidateContext leaked password for %s", tc.url)
		}

		lenientCtx := validProxyContext()
		lenientCtx.Proxy.URL = tc.url
		err := config.ValidateContext(lenientCtx)
		require.Error(t, err, "url %s should fail lenient validation", tc.url)
		require.NotContains(t, err.Error(), tc.password, "ValidateContext leaked password for %s", tc.url)

		resolveCtx := validProxyContext()
		resolveCtx.Proxy.URL = tc.url
		cfg := &config.Config{
			CurrentContext: "proxied",
			Contexts:       map[string]*config.Context{"proxied": resolveCtx},
		}
		_, _, err = config.ResolveContext(cfg, "")
		require.Error(t, err, "url %s should fail ResolveContext", tc.url)
		require.NotContains(t, err.Error(), tc.password, "ResolveContext leaked password for %s", tc.url)
	}
}

func TestStrictValidateContext_Timeouts(t *testing.T) {
	for _, tc := range []struct {
		field string
		set   func(c *config.Context, v string)
		valid string
	}{
		{"connect", func(c *config.Context, v string) { c.Timeout.Connect = v }, "5s"},
		{"tls-handshake", func(c *config.Context, v string) { c.Timeout.TLSHandshake = v }, "10s"},
		{"request", func(c *config.Context, v string) { c.Timeout.Request = v }, "30s"},
	} {
		c := validProxyContext()
		tc.set(c, tc.valid)
		errs := config.StrictValidateContext(c)
		require.Empty(t, errs, "valid timeout.%s should pass", tc.field)

		c = validProxyContext()
		tc.set(c, "not-a-duration")
		errs = config.StrictValidateContext(c)
		require.Contains(t, errs,
			fmt.Sprintf("timeout.%s %q is not a duration (e.g. 5s, 500ms)", tc.field, "not-a-duration"))

		c = validProxyContext()
		tc.set(c, "0s")
		errs = config.StrictValidateContext(c)
		require.Contains(t, errs, fmt.Sprintf("timeout.%s must be greater than zero", tc.field))

		c = validProxyContext()
		tc.set(c, "-1s")
		errs = config.StrictValidateContext(c)
		require.Contains(t, errs, fmt.Sprintf("timeout.%s must be greater than zero", tc.field))
	}
}

// TestValidateContext_JoinsProxyAndTimeoutMessages pins the join order and
// separator: the lenient ValidateContext appends ValidateProxyBlock's
// messages before ValidateTimeoutBlock's and joins the combined list with
// "; ", so a caller printing err.Error() sees every structural defect in one
// line rather than only the first.
func TestValidateContext_JoinsProxyAndTimeoutMessages(t *testing.T) {
	c := validProxyContext()
	c.Proxy.Username = "pmx"
	c.Timeout.Connect = "0s"

	err := config.ValidateContext(c)
	require.Error(t, err)
	require.Equal(t,
		"proxy.username is set but proxy.url is empty; timeout.connect must be greater than zero",
		err.Error())
}

func TestValidateProxyBlock_NilPointer_ReturnsNil(t *testing.T) {
	require.Nil(t, config.ValidateProxyBlock(nil))
}

func TestValidateTimeoutBlock_NilPointer_ReturnsNil(t *testing.T) {
	require.Nil(t, config.ValidateTimeoutBlock(nil))
}
