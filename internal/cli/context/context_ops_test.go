package context

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// ---- test helpers -----------------------------------------------------------

// runOpsCmd builds the context group with injected deps, sets output to buf,
// and executes args.
func runOpsCmd(cfg *config.Config, tmpPath string, buf *bytes.Buffer, args ...string) error {
	deps := &cli.Deps{
		Cfg:        cfg,
		ConfigPath: tmpPath,
		Out:        output.New(),
		Format:     output.FormatJSON,
	}
	cmd := Group(nil)
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	return cmd.Execute()
}

// scratchConfig writes cfg to a temp file and returns the path.
func scratchConfig(t *testing.T, cfg *config.Config) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yml")
	require.NoError(t, config.Save(p, cfg))
	return p
}

// labContext returns a fully-valid token-auth Context for use in tests.
func labContext() *config.Context {
	return &config.Context{
		Host:     "10.0.0.1",
		Port:     8006,
		Protocol: "https",
		Realm:    "pam",
		Auth: config.AuthBlock{
			Type:     "token",
			Username: "root@pam",
			TokenID:  "mytoken",
			Secret:   "s3cr3t",
		},
	}
}

// ---- copy tests -------------------------------------------------------------

func TestContextCopy_Happy(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "copy", "lab", "staging"))

	// Reload and verify dst exists and is a deep copy.
	loaded, err := config.Load(p)
	require.NoError(t, err)
	require.Contains(t, loaded.Contexts, "staging")
	require.Contains(t, loaded.Contexts, "lab", "src must still exist")

	src := loaded.Contexts["lab"]
	dst := loaded.Contexts["staging"]
	require.Equal(t, src.Host, dst.Host)
	require.Equal(t, src.Auth.TokenID, dst.Auth.TokenID)
	require.Equal(t, src.Auth.Secret, dst.Auth.Secret)

	// Verify independence: mutating dst pointer does not affect src pointer.
	dst.Host = "mutated"
	require.NotEqual(t, loaded.Contexts["lab"].Host, dst.Host, "copy must be independent")
}

// TestContextCopy_PreservesTLSFields guards against a copy verb silently
// dropping a TLSBlock field added after config.CloneContext was first written
// (Tofu is one such later field, and it must survive a copy).
func TestContextCopy_PreservesTLSFields(t *testing.T) {
	src := labContext()
	src.TLS = config.TLSBlock{
		Insecure:    true,
		Fingerprint: "AA:BB:CC",
		CACert:      "/etc/ssl/certs/ca.pem",
		Tofu:        true,
	}
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": src,
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "copy", "lab", "staging"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	dst := loaded.Contexts["staging"]
	require.NotNil(t, dst)

	require.Equal(t, src.TLS.Insecure, dst.TLS.Insecure)
	require.Equal(t, src.TLS.Fingerprint, dst.TLS.Fingerprint)
	require.Equal(t, src.TLS.CACert, dst.TLS.CACert)
	require.Equal(t, src.TLS.Tofu, dst.TLS.Tofu)
}

// TestContextCopy_PreservesProduct guards against the copy verb silently
// dropping Product, mirroring TestContextCopy_PreservesTLSFields's intent for
// the pve/pbs product selector.
func TestContextCopy_PreservesProduct(t *testing.T) {
	src := labContext()
	src.Product = config.ProductPBS
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": src,
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "copy", "lab", "staging"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	dst := loaded.Contexts["staging"]
	require.NotNil(t, dst)
	require.Equal(t, config.ProductPBS, dst.Product)
}

func TestContextCopy_MissingSrc(t *testing.T) {
	cfg := &config.Config{
		Contexts: map[string]*config.Context{
			"lab": labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "copy", "nonexistent", "dst")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestContextCopy_ExistingDstNoForce(t *testing.T) {
	cfg := &config.Config{
		Contexts: map[string]*config.Context{
			"lab":     labContext(),
			"staging": labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "copy", "lab", "staging")
	require.Error(t, err)
	require.Contains(t, err.Error(), "already exists")
}

func TestContextCopy_ExistingDstForce(t *testing.T) {
	orig := labContext()
	orig.Host = "original-host"
	over := labContext()
	over.Host = "old-staging-host"

	cfg := &config.Config{
		Contexts: map[string]*config.Context{
			"lab":     orig,
			"staging": over,
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "copy", "lab", "staging", "--force"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	require.Equal(t, "original-host", loaded.Contexts["staging"].Host)
}

func TestContextCopy_Select(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "copy", "lab", "staging", "--select"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	require.Equal(t, "staging", loaded.CurrentContext)
	require.Equal(t, "lab", loaded.PreviousContext)
}

func TestContextCopy_SameSrcDst(t *testing.T) {
	cfg := &config.Config{
		Contexts: map[string]*config.Context{
			"lab": labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "copy", "lab", "lab")
	require.Error(t, err)
	require.Contains(t, err.Error(), "must differ")
}

// ---- add tests --------------------------------------------------------------

// TestContextAdd_RejectsInvalidSSHPortAndJump verifies that an out-of-range
// --ssh-port and an unparseable --ssh-jump are both refused before a context
// is written, so `add` never persists a context that no ssh-based command
// could then use.
func TestContextAdd_RejectsInvalidSSHPortAndJump(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "badport",
		"--host", "10.1.3.1",
		"--auth-type", "token",
		"--username", "root@pam",
		"--token-id", "tok",
		"--secret", "${SECRET}",
		"--ssh-port", "99999",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ssh.port 99999 is out of range [1, 65535]")

	updated := reloadCfg(t, path)
	require.NotContains(t, updated.Contexts, "badport",
		"an out-of-range --ssh-port must not write a context")

	_, err = run(t, deps, "", "add", "badjump",
		"--host", "10.1.3.2",
		"--auth-type", "token",
		"--username", "root@pam",
		"--token-id", "tok",
		"--secret", "${SECRET}",
		"--ssh-jump", ",",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), `ssh.jump "," is not valid: hop 1 is empty`)

	updated = reloadCfg(t, path)
	require.NotContains(t, updated.Contexts, "badjump",
		"an unparseable --ssh-jump must not write a context")
}

// TestContextAdd_RejectsInvalidProxyURL verifies that add runs
// config.ValidateProxyBlock and config.ValidateTimeoutBlock after its
// auth-flag checks and before config.Save, so a malformed --proxy-url or
// --timeout-* is rejected with the exact validator text and never reaches
// the saved config.
func TestContextAdd_RejectsInvalidProxyURL(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "badproxy",
		"--host", "10.1.3.3",
		"--username", "root@pam",
		"--token-id", "e2e",
		"--secret", "00000000-0000-0000-0000-000000000000",
		"--proxy-url", "ftp://nope",
	)
	require.Error(t, err)
	require.Equal(t, "proxy.url ftp://nope must use scheme socks5, socks5h, or http", err.Error())

	updated := reloadCfg(t, path)
	require.NotContains(t, updated.Contexts, "badproxy",
		"a rejected --proxy-url must not write a context")

	_, err = run(t, deps, "", "add", "badtimeout",
		"--host", "10.1.3.4",
		"--username", "root@pam",
		"--token-id", "e2e",
		"--secret", "00000000-0000-0000-0000-000000000000",
		"--timeout-connect", "0s",
	)
	require.Error(t, err)
	require.Equal(t, "timeout.connect must be greater than zero", err.Error())

	updated = reloadCfg(t, path)
	require.NotContains(t, updated.Contexts, "badtimeout",
		"a rejected --timeout-connect must not write a context")
}

// TestContextAdd_RejectsProxyURLCredentials verifies a --proxy-url carrying
// userinfo is rejected outright, with the password nowhere in the output, so
// a credential embedded in the URL cannot bypass config.ResolveSecret, its
// literal warning, and the keychain.
func TestContextAdd_RejectsProxyURLCredentials(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "add", "badcreds",
		"--host", "10.1.3.5",
		"--username", "root@pam",
		"--token-id", "e2e",
		"--secret", "00000000-0000-0000-0000-000000000000",
		"--proxy-url", "socks5://u:p@h:1080",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(),
		"proxy.url socks5://<redacted>@h:1080 must not embed credentials; use proxy.username and proxy.password")
	require.NotContains(t, out, "p@h", "the embedded password must never reach the output")
	require.NotContains(t, err.Error(), "p@h", "the embedded password must never reach the error text")

	updated := reloadCfg(t, path)
	require.NotContains(t, updated.Contexts, "badcreds",
		"a --proxy-url carrying credentials must not write a context")
}

// TestContextAdd_RejectsProxyURLPathQueryFragment verifies a --proxy-url
// whose password holds an unescaped "/", "?", or "#", which url.Parse would
// read as host "pmx" on port 4711, is refused with the URL masked whole and
// never written.
func TestContextAdd_RejectsProxyURLPathQueryFragment(t *testing.T) {
	for _, raw := range []string{
		"socks5://pmx:4711/x@proxy:1080",
		"socks5://pmx:4711?x@proxy:1080",
		"socks5://pmx:4711#x@proxy:1080",
	} {
		path, cfg := makeConfig(t, &config.Config{})
		deps := makeDeps(t, path, cfg)

		out, err := run(t, deps, "", "add", "badpath",
			"--host", "10.1.3.6",
			"--username", "root@pam",
			"--token-id", "e2e",
			"--secret", "00000000-0000-0000-0000-000000000000",
			"--proxy-url", raw,
		)
		require.EqualError(t, err, "proxy.url socks5://<redacted> must not carry a path, a query, or a fragment; "+
			"a password that contains a reserved character such as /, ?, or # must be percent-encoded", raw)
		require.NotContains(t, out, "4711", raw)
		require.NotContains(t, err.Error(), "4711", raw)

		require.NotContains(t, reloadCfg(t, path).Contexts, "badpath",
			"a rejected --proxy-url must not write a context")
	}
}

// TestContextAdd_WarnsOnInlineProxyPassword verifies --proxy-password emits
// the same inline-literal warning as --secret, classifying with
// config.IsSecretReference so a value such as "$uper$ecret" (which merely
// starts with "$" without naming a valid environment variable) warns too.
func TestContextAdd_WarnsOnInlineProxyPassword(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "add", "proxywarn",
		"--host", "10.1.3.6",
		"--username", "root@pam",
		"--token-id", "e2e",
		"--secret", "${SECRET}",
		"--proxy-url", "socks5h://127.0.0.1:1080",
		"--proxy-username", "pmx",
		"--proxy-password", "$uper$ecret",
	)
	require.NoError(t, err, "an inline-literal --proxy-password must warn, not fail, the add")
	require.Contains(t, out,
		"WARN: --proxy-password looks like an inline literal; prefer ${ENV_VAR} or keychain:PATH")

	updated := reloadCfg(t, path)
	require.Contains(t, updated.Contexts, "proxywarn", "a warned-but-valid add must still persist")
}

// TestContextAdd_NoWarnOnReferenceProxyPassword verifies a --proxy-password
// that already names an env reference or a keychain path never warns,
// pinning config.IsSecretReference's classification against a mutant that
// would warn on every non-empty password.
func TestContextAdd_NoWarnOnReferenceProxyPassword(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "add", "envref",
		"--host", "10.1.3.7",
		"--username", "root@pam",
		"--token-id", "e2e",
		"--secret", "${SECRET}",
		"--proxy-url", "socks5h://127.0.0.1:1080",
		"--proxy-username", "pmx",
		"--proxy-password", "${PROXY_SECRET}",
	)
	require.NoError(t, err)
	require.NotContains(t, out, "WARN: --proxy-password", "an env-var reference must never warn")

	out, err = run(t, deps, "", "add", "keychainref",
		"--host", "10.1.3.8",
		"--username", "root@pam",
		"--token-id", "e2e",
		"--secret", "${SECRET}",
		"--proxy-url", "socks5h://127.0.0.1:1080",
		"--proxy-username", "pmx",
		"--proxy-password", "keychain:pmx/proxy",
	)
	require.NoError(t, err)
	require.NotContains(t, out, "WARN: --proxy-password", "a keychain reference must never warn")
}

// TestContextAdd_WarnsOnInlineSecret verifies --secret classifies with
// config.IsSecretReference, not the old "$" prefix check, so a value such as
// "$uper$ecret" (which merely starts with "$" without naming a valid
// environment variable) warns as an inline literal.
func TestContextAdd_WarnsOnInlineSecret(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "add", "secretwarn",
		"--host", "10.1.3.9",
		"--username", "root@pam",
		"--token-id", "e2e",
		"--secret", "$uper$ecret",
	)
	require.NoError(t, err, "an inline-literal --secret must warn, not fail, the add")
	require.Contains(t, out,
		"WARN: --secret looks like an inline literal; prefer ${ENV_VAR} or keychain:PATH")

	updated := reloadCfg(t, path)
	require.Equal(t, "$uper$ecret", updated.Contexts["secretwarn"].Auth.Secret,
		"a warned-but-valid add must still persist")
}

// ---- edit tests -------------------------------------------------------------

// TestContextEdit_SuccessfulEdit verifies that a well-behaved fake editor
// that appends a known YAML line causes the change to be persisted.
func TestContextEdit_SuccessfulEdit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script not portable on Windows")
	}

	ctx := labContext()
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": ctx},
	}
	p := scratchConfig(t, cfg)

	// Write a fake editor that appends default-node to the temp file.
	editorDir := t.TempDir()
	editorPath := filepath.Join(editorDir, "fake-editor.sh")
	script := `#!/bin/sh
# Append default-node line to the file passed as $1 and exit 0.
printf '\ndefault-node: edited-node\n' >> "$1"
`
	require.NoError(t, os.WriteFile(editorPath, []byte(script), 0o755))
	t.Setenv("EDITOR", editorPath)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "edit", "lab"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	require.Equal(t, "edited-node", loaded.Contexts["lab"].DefaultNode)
}

// TestContextEdit_EditorFailingExitAbortsChange verifies that a non-zero
// editor exit code leaves the config unchanged.
func TestContextEdit_EditorFailingExitAbortsChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script not portable on Windows")
	}

	ctx := labContext()
	origNode := "original-node"
	ctx.DefaultNode = origNode
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": ctx},
	}
	p := scratchConfig(t, cfg)

	// Fake editor exits non-zero without modifying the file.
	editorDir := t.TempDir()
	editorPath := filepath.Join(editorDir, "fail-editor.sh")
	require.NoError(t, os.WriteFile(editorPath, []byte("#!/bin/sh\nexit 1\n"), 0o755))
	t.Setenv("EDITOR", editorPath)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "edit", "lab")
	require.Error(t, err)
	require.Contains(t, err.Error(), "editor exited with error")

	// Config must be unchanged.
	loaded, err2 := config.Load(p)
	require.NoError(t, err2)
	require.Equal(t, origNode, loaded.Contexts["lab"].DefaultNode)
}

// TestContextEdit_InvalidYAMLRejected verifies that a fake editor that writes
// invalid YAML causes an error and preserves the temp file.
func TestContextEdit_InvalidYAMLRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script not portable on Windows")
	}

	ctx := labContext()
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": ctx},
	}
	p := scratchConfig(t, cfg)

	// Fake editor overwrites the file with broken YAML.
	editorDir := t.TempDir()
	editorPath := filepath.Join(editorDir, "bad-yaml-editor.sh")
	script := "#!/bin/sh\nprintf 'host: [\nbad yaml' > \"$1\"\nexit 0\n"
	require.NoError(t, os.WriteFile(editorPath, []byte(script), 0o755))
	t.Setenv("EDITOR", editorPath)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "edit", "lab")
	require.Error(t, err)
	// Error must contain a temp file path for recovery.
	require.Contains(t, err.Error(), ".pmx-context-")

	// That preserved file holds the context verbatim, secret included, and
	// nothing ever removes it. It must therefore live in the 0700 config
	// directory, not in a world-traversable $TMPDIR.
	require.Contains(t, err.Error(), filepath.Dir(p),
		"the preserved credential-bearing file must sit beside the config, not in $TMPDIR")
	require.NotContains(t, err.Error(), os.TempDir()+string(os.PathSeparator)+"pve-context-")
}

// TestContextEdit_NoEditorEnvReturnsError verifies that an unset $EDITOR (and
// $VISUAL) produces a helpful error message.
func TestContextEdit_NoEditorEnvReturnsError(t *testing.T) {
	ctx := labContext()
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": ctx},
	}
	p := scratchConfig(t, cfg)

	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "edit", "lab")
	require.Error(t, err)
	require.Contains(t, err.Error(), "$EDITOR is not set")
}

// TestContextEdit_DefaultsToCurrentContext verifies that omitting the name arg
// uses the current context.
func TestContextEdit_DefaultsToCurrentContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script not portable on Windows")
	}

	ctx := labContext()
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": ctx},
	}
	p := scratchConfig(t, cfg)

	// Fake editor: no-op (exit 0 without modifying file).
	editorDir := t.TempDir()
	editorPath := filepath.Join(editorDir, "noop.sh")
	require.NoError(t, os.WriteFile(editorPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	t.Setenv("EDITOR", editorPath)

	var buf bytes.Buffer
	// No name arg — should use current context "lab".
	require.NoError(t, runOpsCmd(cfg, p, &buf, "edit"))
}

// TestContextEdit_NoCurrentContextErrors verifies that with no arg and no
// current-context set, an error is returned.
func TestContextEdit_NoCurrentContextErrors(t *testing.T) {
	ctx := labContext()
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"lab": ctx},
	}
	p := scratchConfig(t, cfg)

	t.Setenv("EDITOR", "true") // valid editor path
	t.Setenv("VISUAL", "")

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "edit")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no current-context")
}

// TestContextEdit_RejectsInvalidSSHJump verifies that an edited context whose
// ssh.jump no ssh-based command could use is rejected after the strict
// decode, with the temp file preserved for recovery exactly as an invalid
// YAML or a failed StrictValidateContext leaves it.
func TestContextEdit_RejectsInvalidSSHJump(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script not portable on Windows")
	}

	ctx := labContext()
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": ctx},
	}
	p := scratchConfig(t, cfg)

	// Fake editor appends an ssh.jump chain no ssh-based command could use.
	editorDir := t.TempDir()
	editorPath := filepath.Join(editorDir, "bad-jump-editor.sh")
	script := "#!/bin/sh\nprintf '\\nssh:\\n  jump: x;id\\n' >> \"$1\"\nexit 0\n"
	require.NoError(t, os.WriteFile(editorPath, []byte(script), 0o755))
	t.Setenv("EDITOR", editorPath)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "edit", "lab")
	require.Error(t, err)
	require.Contains(t, err.Error(),
		`ssh.jump "x;id" is not valid: hop 1: host "x;id" is not a hostname, IPv4 address, or bracketed IPv6 literal`)
	// The path in the message is there whether or not the file survives, so
	// the file itself is checked: it must still exist and hold the edit.
	const preservedMarker = "temp file preserved at "
	msg := err.Error()
	idx := strings.Index(msg, preservedMarker)
	require.GreaterOrEqual(t, idx, 0, "the error must name the preserved temp file")
	tmpPath := strings.TrimSpace(msg[idx+len(preservedMarker):])
	require.Contains(t, filepath.Base(tmpPath), ".pmx-context-")
	t.Cleanup(func() { _ = os.Remove(tmpPath) })
	require.FileExists(t, tmpPath, "the temp file must be preserved for recovery")
	kept, readErr := os.ReadFile(tmpPath) //nolint:gosec // G304: path comes from the command's own error text
	require.NoError(t, readErr)
	require.Contains(t, string(kept), "jump: x;id")

	loaded, err2 := config.Load(p)
	require.NoError(t, err2)
	require.Equal(t, "", loaded.Contexts["lab"].SSH.Jump, "a rejected edit must not persist")
}

// misusedSSHJumps are ssh.jump values that carry a password ssh has no
// syntax for, each mapped to the distinctive runs of that password. The
// shapes cover the plain and ssh:// forms, a comma that splits the password
// across hops, a percent-encoded colon, an "@" inside the password, a
// password whose first part also parses as a port, and a directory login in
// front of the colon.
var misusedSSHJumps = map[string][]string{
	"ssh://admin:Hunter2secret@bastion":              {"Hunter2secret"},
	"admin:Zq9alpha,Xk7bravo@bastion":                {"Zq9alpha", "Xk7bravo"},
	"ssh://admin:Zq9alpha,Xk7bravo@bastion":          {"Zq9alpha", "Xk7bravo"},
	"bastion,ssh://u:Zq9alpha,Xk7bravo,Wm4charlie@h": {"Zq9alpha", "Xk7bravo", "Wm4charlie"},
	"ssh://u%3AZq9alpha@h":                           {"Zq9alpha"},
	"ssh://u%3aZq9alpha,Xk7bravo@h":                  {"Zq9alpha", "Xk7bravo"},
	"u:Zq9alpha%3AXk7bravo@h":                        {"Zq9alpha", "Xk7bravo"},
	"u:Zq9alpha@Xk7bravo@bastion":                    {"Zq9alpha", "Xk7bravo"},
	"ssh://u:Zq9alpha@Xk7bravo,Wm4charlie@bastion":   {"Zq9alpha", "Xk7bravo", "Wm4charlie"},
	"u:4242,Xk7b!Wm4c@h":                             {"4242", "Xk7b", "Wm4c"},
	"admin@Zq9alpha:Xk7bravo@bastion":                {"Xk7bravo"},
	"edge:22,admin:Zq9alpha,Xk7bravo@inner":          {"Zq9alpha", "Xk7bravo"},
	"u:Zq9alpha,,Xk7bravo@h":                         {"Zq9alpha", "Xk7bravo"},
}

// runJumpVerb runs `pmx context <verb>` through cli.Execute with chain as
// the context's ssh.jump, so the error, both standard streams, and the
// audit log under a throwaway HOME are exactly what an operator's run would
// produce. It returns all of that text joined, after checking that the
// command failed on the chain and that the exit record was written.
func runJumpVerb(t *testing.T, verb, chain string) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")
	t.Setenv("PMX_LOG_LAYOUT", "")
	t.Setenv("PMX_LOG_LEVEL", "")

	cfgPath := scratchConfig(t, &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": labContext()},
	})

	args := []string{"pmx", "--config", cfgPath, "context"}

	switch verb {
	case "add":
		args = append(args, "add", "jumped", "--host", "10.1.3.2", "--auth-type", "token",
			"--username", "root@pam", "--token-id", "tok", "--secret", "${SECRET}", "--ssh-jump", chain)
	case "update":
		args = append(args, "update", "lab", "--ssh-jump", chain)
	case "edit":
		snippet := filepath.Join(t.TempDir(), "jump.yml")
		require.NoError(t, os.WriteFile(snippet, []byte("\nssh:\n  jump: "+strconv.Quote(chain)+"\n"), 0o600))

		editor := filepath.Join(t.TempDir(), "jump-editor.sh")
		script := "#!/bin/sh\ncat " + strconv.Quote(snippet) + " >> \"$1\"\n"
		require.NoError(t, os.WriteFile(editor, []byte(script), 0o700))
		t.Setenv("EDITOR", editor)

		args = append(args, "edit", "lab")
	default:
		t.Fatalf("unknown verb %q", verb)
	}

	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	require.NoError(t, err)

	oldArgs, oldStdout, oldStderr := os.Args, os.Stdout, os.Stderr
	os.Args, os.Stdout, os.Stderr = args, stdout, stderr
	execErr := cli.Execute("pmx", []cli.GroupFactory{Group})
	os.Args, os.Stdout, os.Stderr = oldArgs, oldStdout, oldStderr

	require.NoError(t, stdout.Close())
	require.NoError(t, stderr.Close())
	require.Error(t, execErr, "%s must reject %q", verb, chain)
	require.Contains(t, execErr.Error(), "is not valid")

	var b strings.Builder
	b.WriteString(execErr.Error())

	for _, f := range []*os.File{stdout, stderr} {
		raw, rerr := os.ReadFile(f.Name())
		require.NoError(t, rerr)
		b.Write(raw)
	}

	var logs strings.Builder

	walkErr := filepath.WalkDir(filepath.Join(home, ".pmx", "logs"),
		func(path string, d os.DirEntry, werr error) error {
			if werr != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return werr
			}

			raw, rerr := os.ReadFile(path) //nolint:gosec // G304: path is under this test's own temp HOME
			logs.Write(raw)

			return rerr
		})
	require.NoError(t, walkErr)
	require.Contains(t, logs.String(), `"msg":"exit"`, "%s must write its exit record", verb)
	require.Contains(t, logs.String(), "is not valid", "the exit record must carry the error")
	b.WriteString(logs.String())

	return b.String()
}

// TestContextJump_NeverEchoesAPassword proves that no run of a password
// misused in ssh.jump reaches the error, the terminal, or the audit log of
// `context add`, `context update`, or `context edit`, whatever the
// password holds.
func TestContextJump_NeverEchoesAPassword(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script not portable on Windows")
	}

	chains := slices.Sorted(maps.Keys(misusedSSHJumps))

	for i, chain := range chains {
		runs := misusedSSHJumps[chain]

		for _, verb := range []string{"add", "update", "edit"} {
			// The subtest name reaches every t.TempDir path, and edit's
			// message names a temp file, so it must not carry the chain.
			t.Run(fmt.Sprintf("%s chain %d", verb, i), func(t *testing.T) {
				t.Logf("chain %q", chain)

				text := runJumpVerb(t, verb, chain)
				require.Contains(t, text, "<redacted>", "the quoted chain must show where it was masked")

				for _, run := range runs {
					require.NotContains(t, text, run, "%s leaked part of the password in %q", verb, chain)
				}
			})
		}
	}
}

// ---- edit --product tests -----------------------------------------------------

func TestContextEdit_Product_SwapsDefaultPort(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]*config.Context{"lab": labContext()}}
	p := scratchConfig(t, cfg)

	// labContext has port 8006 (the pve default) — switching to pbs must
	// re-default the port to 8007 without touching $EDITOR.
	t.Setenv("EDITOR", "/nonexistent/editor-must-not-run")
	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "edit", "lab", "--product", "pbs"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	require.Equal(t, config.ProductPBS, loaded.Contexts["lab"].Product)
	require.Equal(t, 8007, loaded.Contexts["lab"].Port)
}

func TestContextEdit_Product_KeepsCustomPortWithNote(t *testing.T) {
	src := labContext()
	src.Port = 9006
	cfg := &config.Config{Contexts: map[string]*config.Context{"lab": src}}
	p := scratchConfig(t, cfg)

	t.Setenv("EDITOR", "/nonexistent/editor-must-not-run")
	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "edit", "lab", "--product", "pbs"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	require.Equal(t, config.ProductPBS, loaded.Contexts["lab"].Product)
	require.Equal(t, 9006, loaded.Contexts["lab"].Port, "a custom port must survive a product change")
	require.Contains(t, buf.String(), "port 9006 kept")
	require.Contains(t, buf.String(), "8007")
}

func TestContextEdit_Product_ZeroPortGetsNewDefault(t *testing.T) {
	src := labContext()
	src.Port = 0
	cfg := &config.Config{Contexts: map[string]*config.Context{"lab": src}}
	p := scratchConfig(t, cfg)

	t.Setenv("EDITOR", "/nonexistent/editor-must-not-run")
	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "edit", "lab", "--product", "pdm"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	require.Equal(t, 8443, loaded.Contexts["lab"].Port)
}

func TestContextEdit_Product_Invalid(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]*config.Context{"lab": labContext()}}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "edit", "lab", "--product", "bogus")
	require.Error(t, err)
	require.Contains(t, err.Error(), "pve, pbs, pdm")
}

// ---- validate tests ---------------------------------------------------------

func TestContextValidate_ValidContext(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": labContext()},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "validate", "lab"))
	require.Contains(t, buf.String(), "OK")
}

func TestContextValidate_MissingHost(t *testing.T) {
	ctx := labContext()
	ctx.Host = ""
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"bad": ctx},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "bad")
	require.Error(t, err)
	require.Contains(t, buf.String(), "host is required")
}

func TestContextValidate_TokenAuthMissingTokenID(t *testing.T) {
	ctx := labContext()
	ctx.Auth.TokenID = ""
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"bad": ctx},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "bad")
	require.Error(t, err)
	require.Contains(t, buf.String(), "token-id")
}

func TestContextValidate_PasswordAuthMissingUsername(t *testing.T) {
	ctx := labContext()
	ctx.Auth.Type = "password"
	ctx.Auth.TokenID = ""
	ctx.Auth.Username = ""
	ctx.Auth.Secret = "pass"
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"bad": ctx},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "bad")
	require.Error(t, err)
	require.Contains(t, buf.String(), "username")
}

func TestContextValidate_BadDefaultOutput(t *testing.T) {
	ctx := labContext()
	ctx.DefaultOutput = "xml"
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"bad": ctx},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "bad")
	require.Error(t, err)
	require.Contains(t, buf.String(), "default-output")
}

func TestContextValidate_BadFingerprint(t *testing.T) {
	ctx := labContext()
	ctx.TLS.Fingerprint = "not-a-fingerprint"
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"bad": ctx},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "bad")
	require.Error(t, err)
	require.Contains(t, buf.String(), "fingerprint")
}

func TestContextValidate_AllMixed(t *testing.T) {
	goodCtx := labContext()
	badCtx := labContext()
	badCtx.Host = ""

	cfg := &config.Config{
		CurrentContext: "good",
		Contexts: map[string]*config.Context{
			"good": goodCtx,
			"bad":  badCtx,
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "--all")
	require.Error(t, err, "--all with any invalid context must exit non-zero")
	out := buf.String()
	require.Contains(t, out, "OK")
	require.Contains(t, out, "INVALID")
}

func TestContextValidate_AllValid(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "a",
		Contexts: map[string]*config.Context{
			"a": labContext(),
			"b": labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "validate", "--all"))

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 2, "--all must report every context, not just the current one")
	for _, r := range got {
		require.Equal(t, "OK", r["status"], "context %v", r["name"])
		require.Empty(t, r["errors"])
	}
}

// TestContextValidate_AllSortedOrder verifies that `validate --all` emits rows
// in deterministic alphabetical order regardless of map iteration order (F-W6-02).
func TestContextValidate_AllSortedOrder(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "alpha",
		Contexts: map[string]*config.Context{
			"gamma": labContext(),
			"alpha": labContext(),
			"beta":  labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "validate", "--all"))

	out := buf.String()
	idxAlpha := strings.Index(out, "alpha")
	idxBeta := strings.Index(out, "beta")
	idxGamma := strings.Index(out, "gamma")

	require.True(t, idxAlpha >= 0, "alpha must appear in output")
	require.True(t, idxBeta >= 0, "beta must appear in output")
	require.True(t, idxGamma >= 0, "gamma must appear in output")
	require.True(t, idxAlpha < idxBeta, "alpha must precede beta in sorted output")
	require.True(t, idxBeta < idxGamma, "beta must precede gamma in sorted output")
}

func TestContextValidate_DefaultsToCurrentContext(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": labContext()},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "validate"))
	require.Contains(t, buf.String(), "OK")
}

func TestContextValidate_NoCurrentContextErrors(t *testing.T) {
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"lab": labContext()},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no current-context")
}

func TestContextValidate_UnknownContextErrors(t *testing.T) {
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"lab": labContext()},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "ghost")
	require.Error(t, err)
}

func TestContextValidate_ValidFingerprint(t *testing.T) {
	ctx := labContext()
	// 32 colon-separated uppercase hex pairs.
	ctx.TLS.Fingerprint = "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"ctx": ctx},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "validate", "ctx"))

	var got []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Len(t, got, 1)
	require.Equal(t, "OK", got[0]["status"], "a well-formed fingerprint must validate clean")
	require.Empty(t, got[0]["errors"])
}

// ---- validation-drift regression (F2 remediation) --------------------------

// TestContextEdit_TokenMissingTokenIDRejected pins the write-time rule that
// token auth requires token-id.  A fake editor removes the token-id line; the
// edit must be rejected and the config must not be modified.
func TestContextEdit_TokenMissingTokenIDRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script not portable on Windows")
	}

	ctx := labContext() // token auth with TokenID set
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": ctx},
	}
	p := scratchConfig(t, cfg)

	// Fake editor rewrites the file with a valid token context but no token-id.
	editorDir := t.TempDir()
	editorPath := filepath.Join(editorDir, "strip-tokenid.sh")
	script := `#!/bin/sh
# Write a token context missing token-id.
cat > "$1" <<'YAML'
host: 10.0.0.1
port: 8006
protocol: https
realm: pam
auth:
  type: token
  secret: s3cr3t
YAML
`
	require.NoError(t, os.WriteFile(editorPath, []byte(script), 0o755))
	t.Setenv("EDITOR", editorPath)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "edit", "lab")
	require.Error(t, err, "edit must be rejected when token-id is missing")
	require.Contains(t, err.Error(), "token-id")

	// Config must be unchanged — original token-id still present.
	loaded, err2 := config.Load(p)
	require.NoError(t, err2)
	require.Equal(t, "mytoken", loaded.Contexts["lab"].Auth.TokenID,
		"config must not be modified after rejected edit")
}

// TestContextValidate_TokenMissingTokenIDInvalid pins that validate catches a
// token context with no token-id, ensuring add/edit/validate share the same rule.
func TestContextValidate_TokenMissingTokenIDInvalid(t *testing.T) {
	ctx := labContext()
	ctx.Auth.TokenID = "" // missing token-id
	cfg := &config.Config{
		Contexts: map[string]*config.Context{"bad": ctx},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "validate", "bad")
	require.Error(t, err)
	require.Contains(t, buf.String(), "token-id")
}

// ---- noClient annotation regression for copy/edit/validate -----------------

// ---- rename tests -------------------------------------------------------------

func TestContextRename_Happy_UpdatesPointers(t *testing.T) {
	cfg := &config.Config{
		CurrentContext:  "lab",
		PreviousContext: "lab",
		Contexts: map[string]*config.Context{
			"lab": labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "rename", "lab", "prod"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	require.NotContains(t, loaded.Contexts, "lab")
	require.Contains(t, loaded.Contexts, "prod")
	require.Equal(t, "10.0.0.1", loaded.Contexts["prod"].Host, "context body must survive rename")
	require.Equal(t, "prod", loaded.CurrentContext, "current-context pointer must follow the rename")
	require.Equal(t, "prod", loaded.PreviousContext, "previous-context pointer must follow the rename")
}

func TestContextRename_PointersToOtherContexts_Untouched(t *testing.T) {
	cfg := &config.Config{
		CurrentContext:  "other",
		PreviousContext: "other",
		Contexts: map[string]*config.Context{
			"lab":   labContext(),
			"other": labContext(),
		},
	}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	require.NoError(t, runOpsCmd(cfg, p, &buf, "rename", "lab", "prod"))

	loaded, err := config.Load(p)
	require.NoError(t, err)
	require.Equal(t, "other", loaded.CurrentContext)
	require.Equal(t, "other", loaded.PreviousContext)
}

func TestContextRename_MissingOld_ListsAvailable(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]*config.Context{"lab": labContext()}}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "rename", "ghost", "prod")
	require.Error(t, err)
	require.Contains(t, err.Error(), `context "ghost" not found`)
	require.Contains(t, err.Error(), "lab (pve)")
}

func TestContextRename_Collision(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]*config.Context{
		"lab":  labContext(),
		"prod": labContext(),
	}}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "rename", "lab", "prod")
	require.Error(t, err)
	require.Contains(t, err.Error(), `context "prod" already exists`)
}

func TestContextRename_SameName(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]*config.Context{"lab": labContext()}}
	p := scratchConfig(t, cfg)

	var buf bytes.Buffer
	err := runOpsCmd(cfg, p, &buf, "rename", "lab", "lab")
	require.Error(t, err)
	require.Contains(t, err.Error(), "must differ")
}

func TestContextCopyEditValidateAreNoClient(t *testing.T) {
	for _, name := range []string{"copy", "edit", "validate"} {
		t.Run(name, func(t *testing.T) {
			cmd := Group(&cli.Deps{})
			found := false
			for _, sub := range cmd.Commands() {
				if sub.Name() == name {
					found = true
					ann := sub.Annotations["noClient"]
					require.Equal(t, "true", ann,
						fmt.Sprintf("command %q must have Annotations[\"noClient\"]==\"true\"", name))
				}
			}
			require.True(t, found, "command %q must be registered under context group", name)
		})
	}
}

// TestContextValidate_ReportsUnknownConfigKeys covers the strict re-parse.
// config.Load ignores a key it does not recognise, so `fingerprnt` under a
// context means TLS pinning is quietly not happening; validate is the verb
// that exists to say so.
func TestContextValidate_ReportsUnknownConfigKeys(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": labContext()},
	}
	p := scratchConfig(t, cfg)

	// Append keys no field accepts, as a typo in a hand-edited config would.
	raw, err := os.ReadFile(p)
	require.NoError(t, err)
	raw = append(raw, []byte("\nretenton: 30\n")...)
	require.NoError(t, os.WriteFile(p, raw, 0o600))

	var out, errOut bytes.Buffer
	deps := &cli.Deps{Cfg: cfg, ConfigPath: p, Out: output.New(), Format: output.FormatJSON}
	cmd := Group(nil)
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"validate", "lab"})

	require.NoError(t, cmd.Execute(), "an unknown key must not fail the command")
	require.Contains(t, errOut.String(), "retenton", "the offending key must be named")
	require.Contains(t, out.String(), "OK", "the context itself is still valid")
}

// TestContextValidate_SilentOnACleanConfig is the other half: no warning when
// every key is known, or operators learn to ignore the one that matters.
func TestContextValidate_SilentOnACleanConfig(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": labContext()},
	}
	p := scratchConfig(t, cfg)

	var out, errOut bytes.Buffer
	deps := &cli.Deps{Cfg: cfg, ConfigPath: p, Out: output.New(), Format: output.FormatJSON}
	cmd := Group(nil)
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"validate", "lab"})

	require.NoError(t, cmd.Execute())
	require.NotContains(t, errOut.String(), "no setting matches")
}
