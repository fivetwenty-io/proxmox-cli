package context

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// TestContextUpdate_SingleField verifies one flag changes one field and every
// other field survives untouched, without $EDITOR ever launching.
func TestContextUpdate_SingleField(t *testing.T) {
	t.Setenv("EDITOR", "/nonexistent/editor-must-not-run")

	seed := &config.Config{Contexts: map[string]*config.Context{"lab": labContext()}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "update", "lab", "--secret", "keychain:pve-cli/lab")
	require.NoError(t, err)
	require.Contains(t, out, `Context "lab" updated.`)

	updated := reloadCfg(t, path)
	ctx := updated.Contexts["lab"]
	require.Equal(t, "keychain:pve-cli/lab", ctx.Auth.Secret)
	require.Equal(t, "10.0.0.1", ctx.Host, "unrelated fields must be preserved")
	require.Equal(t, "root@pam", ctx.Auth.Username)
	require.Equal(t, "mytoken", ctx.Auth.TokenID)
	require.Equal(t, 8006, ctx.Port)
}

// TestContextUpdate_AllFieldFlags audits every persisted-field flag on
// `context update`, mirroring TestContextAudit_Add_AllFlags.
//
// --proxy-from-env is exercised separately by
// TestContextUpdate_NewConnectionFields, not here, because
// config.ValidateProxyBlock rejects proxy.url and proxy.from-env being set
// together, and this test also sets --proxy-url.
func TestContextUpdate_AllFieldFlags(t *testing.T) {
	seed := &config.Config{Contexts: map[string]*config.Context{"lab": labContext()}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "update", "lab",
		"--host", "10.9.9.9",
		"--port", "9006",
		"--protocol", "http",
		"--realm", "pve",
		"--auth-type", "token",
		"--username", "alice@pve",
		"--token-id", "citoken",
		"--secret", "${CI_SECRET}",
		"--insecure",
		"--fingerprint", strings.Repeat("AA:", 31)+"AA",
		"--ca-cert", "/etc/ssl/lab-ca.pem",
		"--tofu",
		"--default-node", "node3",
		"--default-output", "yaml",
		"--product", "pbs",
		"--ssh-user", "admin",
		"--ssh-port", "2222",
		"--ssh-identity", "/home/admin/.ssh/id_ed25519",
		"--ssh-jump", "bastion.example.com",
		"--proxy-url", "socks5h://proxy.example.com:1080",
		"--proxy-username", "proxyuser",
		"--proxy-password", "${PROXY_SECRET}",
		"--timeout-connect", "5s",
		"--timeout-tls-handshake", "10s",
		"--timeout-request", "30s",
	)
	require.NoError(t, err)

	ctx := reloadCfg(t, path).Contexts["lab"]
	require.Equal(t, "10.9.9.9", ctx.Host)
	require.Equal(t, 9006, ctx.Port, "explicit --port must win over the product port rule")
	require.Equal(t, "http", ctx.Protocol)
	require.Equal(t, "pve", ctx.Realm)
	require.Equal(t, "token", ctx.Auth.Type)
	require.Equal(t, "alice@pve", ctx.Auth.Username)
	require.Equal(t, "citoken", ctx.Auth.TokenID)
	require.Equal(t, "${CI_SECRET}", ctx.Auth.Secret)
	require.True(t, ctx.TLS.Insecure)
	require.Equal(t, strings.Repeat("AA:", 31)+"AA", ctx.TLS.Fingerprint)
	require.Equal(t, "/etc/ssl/lab-ca.pem", ctx.TLS.CACert)
	require.True(t, ctx.TLS.Tofu)
	require.Equal(t, "node3", ctx.DefaultNode)
	require.Equal(t, "yaml", ctx.DefaultOutput)
	require.Equal(t, config.ProductPBS, ctx.Product)
	require.Equal(t, "admin", ctx.SSH.User)
	require.Equal(t, 2222, ctx.SSH.Port)
	require.Equal(t, "/home/admin/.ssh/id_ed25519", ctx.SSH.Identity)
	require.Equal(t, "bastion.example.com", ctx.SSH.Jump)
	require.Equal(t, "socks5h://proxy.example.com:1080", ctx.Proxy.URL)
	require.Equal(t, "proxyuser", ctx.Proxy.Username)
	require.Equal(t, "${PROXY_SECRET}", ctx.Proxy.Password)
	require.Equal(t, "5s", ctx.Timeout.Connect)
	require.Equal(t, "10s", ctx.Timeout.TLSHandshake)
	require.Equal(t, "30s", ctx.Timeout.Request)
}

// TestContextUpdate_NewConnectionFields verifies each new ssh.*, proxy.*, and
// timeout.* flag persists independently, with every other field preserved,
// mirroring TestContextUpdate_SingleField's one-flag-at-a-time style.
func TestContextUpdate_NewConnectionFields(t *testing.T) {
	seed := &config.Config{Contexts: map[string]*config.Context{"lab": labContext(), "lab2": labContext()}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "update", "lab", "--ssh-user", "admin")
	require.NoError(t, err)
	require.Equal(t, "admin", reloadCfg(t, path).Contexts["lab"].SSH.User)
	require.Equal(t, "10.0.0.1", reloadCfg(t, path).Contexts["lab"].Host, "unrelated fields must be preserved")

	_, err = run(t, deps, "", "update", "lab", "--ssh-port", "2222")
	require.NoError(t, err)
	require.Equal(t, 2222, reloadCfg(t, path).Contexts["lab"].SSH.Port)
	require.Equal(t, "admin", reloadCfg(t, path).Contexts["lab"].SSH.User, "an earlier field must survive a later update")

	_, err = run(t, deps, "", "update", "lab", "--ssh-identity", "/home/admin/.ssh/id_ed25519")
	require.NoError(t, err)
	require.Equal(t, "/home/admin/.ssh/id_ed25519", reloadCfg(t, path).Contexts["lab"].SSH.Identity)

	_, err = run(t, deps, "", "update", "lab", "--ssh-jump", "bastion.example.com")
	require.NoError(t, err)

	final := reloadCfg(t, path).Contexts["lab"]
	require.Equal(t, "admin", final.SSH.User)
	require.Equal(t, 2222, final.SSH.Port)
	require.Equal(t, "/home/admin/.ssh/id_ed25519", final.SSH.Identity)
	require.Equal(t, "bastion.example.com", final.SSH.Jump)

	// proxy.from-env is exercised on "lab" too, since it never sets
	// proxy.url and config.ValidateProxyBlock rejects the two being set
	// together.
	_, err = run(t, deps, "", "update", "lab", "--proxy-from-env")
	require.NoError(t, err)
	fromEnvCtx := reloadCfg(t, path).Contexts["lab"]
	require.NotNil(t, fromEnvCtx.Proxy.FromEnv, "--proxy-from-env must persist a non-nil pointer")
	require.True(t, *fromEnvCtx.Proxy.FromEnv)
	require.Equal(t, "bastion.example.com", fromEnvCtx.SSH.Jump, "an earlier field must survive a later update")

	// proxy.url, proxy.username, proxy.password, and the three timeouts are
	// exercised on "lab2", which never sets proxy.from-env. Each step reloads
	// the whole context and compares it against an expected value that
	// accumulates one field at a time, so a step that clobbers a field it was
	// never asked to touch (not just the one field a later step legitimately
	// overwrites) is caught, e.g. --timeout-tls-handshake also clearing
	// timeout.request.
	want2 := labContext()
	want2.Product = config.ProductPVE // config.ApplyDefaults runs on every update call

	want2.Proxy.URL = "socks5h://proxy.example.com:1080"
	_, err = run(t, deps, "", "update", "lab2", "--proxy-url", "socks5h://proxy.example.com:1080")
	require.NoError(t, err)
	require.Equal(t, want2, reloadCfg(t, path).Contexts["lab2"])

	want2.Proxy.Username = "proxyuser"
	_, err = run(t, deps, "", "update", "lab2", "--proxy-username", "proxyuser")
	require.NoError(t, err)
	require.Equal(t, want2, reloadCfg(t, path).Contexts["lab2"])

	want2.Proxy.Password = "${PROXY_PASS}"
	_, err = run(t, deps, "", "update", "lab2", "--proxy-password", "${PROXY_PASS}")
	require.NoError(t, err)
	require.Equal(t, want2, reloadCfg(t, path).Contexts["lab2"])

	want2.Timeout.Connect = "5s"
	_, err = run(t, deps, "", "update", "lab2", "--timeout-connect", "5s")
	require.NoError(t, err)
	require.Equal(t, want2, reloadCfg(t, path).Contexts["lab2"])

	// timeout-request is set before timeout-tls-handshake, on purpose: a
	// mutant in which --timeout-tls-handshake also blanks timeout.request is
	// only observable if timeout.request already holds a non-empty value when
	// that step runs.
	want2.Timeout.Request = "30s"
	_, err = run(t, deps, "", "update", "lab2", "--timeout-request", "30s")
	require.NoError(t, err)
	require.Equal(t, want2, reloadCfg(t, path).Contexts["lab2"])

	want2.Timeout.TLSHandshake = "10s"
	_, err = run(t, deps, "", "update", "lab2", "--timeout-tls-handshake", "10s")
	require.NoError(t, err)
	require.Equal(t, want2, reloadCfg(t, path).Contexts["lab2"])
}

// TestContextUpdate_NewFlagsCountAsFields verifies the new ssh-* flags count
// toward updateFieldFlags, so a bare `--ssh-jump` alone does not trip the
// "no fields to update" guard.
func TestContextUpdate_NewFlagsCountAsFields(t *testing.T) {
	seed := &config.Config{Contexts: map[string]*config.Context{"lab": labContext()}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "update", "lab", "--ssh-jump", "h")
	require.NoError(t, err, "--ssh-jump alone must count as a field to update")
	require.Equal(t, "h", reloadCfg(t, path).Contexts["lab"].SSH.Jump)
}

// TestContextUpdate_EmptyTimeoutDeletesKey verifies an empty --timeout-*
// value deletes the corresponding key, which is how an operator returns a
// timeout to its default: TimeoutBlock's fields carry "omitempty", so an
// empty string stops the key from being written on the next save.
func TestContextUpdate_EmptyTimeoutDeletesKey(t *testing.T) {
	seeded := labContext()
	seeded.Timeout = config.TimeoutBlock{Connect: "5s", TLSHandshake: "10s", Request: "30s"}
	seed := &config.Config{Contexts: map[string]*config.Context{"lab": seeded}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "update", "lab", "--timeout-connect", "")
	require.NoError(t, err)

	ctx := reloadCfg(t, path).Contexts["lab"]
	require.Equal(t, "", ctx.Timeout.Connect, "an empty --timeout-connect must delete the key")
	require.Equal(t, "10s", ctx.Timeout.TLSHandshake, "an untouched timeout must be preserved")
	require.Equal(t, "30s", ctx.Timeout.Request, "an untouched timeout must be preserved")

	raw, err := os.ReadFile(path) //nolint:gosec // G304: path is the test's own scratch config
	require.NoError(t, err)
	require.NotContains(t, string(raw), "connect:", "the deleted key must not round-trip back into the file")
}

// TestContextUpdate_EmptyProxyURLClearsCredentials verifies that
// --proxy-url "" clears proxy.url, proxy.username, and proxy.password
// together, because leaving the credentials behind would fail validation
// with "proxy.username is set but proxy.url is empty", and leaves
// proxy.from-env untouched. The seed context is built directly with
// config.Save (which performs no validation of its own) so it can carry
// both proxy.url and a "true" proxy.from-env at once — a combination no CLI
// command can write, but one an operator's hand-edited or older config file
// could still contain.
func TestContextUpdate_EmptyProxyURLClearsCredentials(t *testing.T) {
	fromEnv := true
	seeded := labContext()
	seeded.Proxy = config.ProxyBlock{
		URL:      "socks5h://proxy.example.com:1080",
		Username: "proxyuser",
		Password: "${PROXY_PASS}",
		FromEnv:  &fromEnv,
	}
	seed := &config.Config{Contexts: map[string]*config.Context{"lab": seeded}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "update", "lab", "--proxy-url", "")
	require.NoError(t, err)

	ctx := reloadCfg(t, path).Contexts["lab"]
	require.Equal(t, "", ctx.Proxy.URL)
	require.Equal(t, "", ctx.Proxy.Username, "clearing proxy.url must also clear proxy.username")
	require.Equal(t, "", ctx.Proxy.Password, "clearing proxy.url must also clear proxy.password")
	require.NotNil(t, ctx.Proxy.FromEnv, "proxy.from-env must be left alone")
	require.True(t, *ctx.Proxy.FromEnv)
}

// TestContextUpdate_WarnsOnInlineProxyPassword verifies --proxy-password
// emits the same inline-literal warning as --secret, classifying with
// config.IsSecretReference so a value such as "$uper$ecret" warns too.
func TestContextUpdate_WarnsOnInlineProxyPassword(t *testing.T) {
	seed := &config.Config{Contexts: map[string]*config.Context{"lab": labContext()}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "update", "lab",
		"--proxy-url", "socks5h://127.0.0.1:1080",
		"--proxy-username", "pmx",
		"--proxy-password", "$uper$ecret",
	)
	require.NoError(t, err, "an inline-literal --proxy-password must warn, not fail, the update")
	require.Contains(t, out,
		"WARN: --proxy-password looks like an inline literal; prefer ${ENV_VAR} or keychain:PATH")

	ctx := reloadCfg(t, path).Contexts["lab"]
	require.Equal(t, "$uper$ecret", ctx.Proxy.Password, "a warned-but-valid update must still persist")
}

// TestContextUpdate_NoWarnOnReferenceProxyPassword verifies a --proxy-password
// that already names an env reference or a keychain path never warns, pinning
// config.IsSecretReference's classification against a mutant that would warn
// on every non-empty password.
func TestContextUpdate_NoWarnOnReferenceProxyPassword(t *testing.T) {
	seed := &config.Config{Contexts: map[string]*config.Context{"lab": labContext(), "lab2": labContext()}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "update", "lab",
		"--proxy-url", "socks5h://127.0.0.1:1080",
		"--proxy-username", "pmx",
		"--proxy-password", "${PROXY_SECRET}",
	)
	require.NoError(t, err)
	require.NotContains(t, out, "WARN: --proxy-password", "an env-var reference must never warn")

	out, err = run(t, deps, "", "update", "lab2",
		"--proxy-url", "socks5h://127.0.0.1:1080",
		"--proxy-username", "pmx",
		"--proxy-password", "keychain:pmx/proxy",
	)
	require.NoError(t, err)
	require.NotContains(t, out, "WARN: --proxy-password", "a keychain reference must never warn")
}

// TestContextUpdate_WarnsOnInlineSecret verifies --secret classifies with
// config.IsSecretReference, not the old "$" prefix check, so a value such as
// "$uper$ecret" (which merely starts with "$" without naming a valid
// environment variable) warns as an inline literal.
func TestContextUpdate_WarnsOnInlineSecret(t *testing.T) {
	seed := &config.Config{Contexts: map[string]*config.Context{"lab": labContext()}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "update", "lab", "--secret", "$uper$ecret")
	require.NoError(t, err, "an inline-literal --secret must warn, not fail, the update")
	require.Contains(t, out,
		"WARN: --secret looks like an inline literal; prefer ${ENV_VAR} or keychain:PATH")

	ctx := reloadCfg(t, path).Contexts["lab"]
	require.Equal(t, "$uper$ecret", ctx.Auth.Secret, "a warned-but-valid update must still persist")
}

// TestContextUpdate_ProxyFromEnvNeverWritesThroughStoredPointer verifies a
// rejected update never mutates the stored context's proxy.from-env pointer
// by writing through it: --proxy-from-env must assign a fresh pointer onto
// the copy under mutation, not the address the stored context already holds.
func TestContextUpdate_ProxyFromEnvNeverWritesThroughStoredPointer(t *testing.T) {
	seedFromEnv := false
	seeded := labContext()
	seeded.Proxy = config.ProxyBlock{FromEnv: &seedFromEnv}
	seed := &config.Config{Contexts: map[string]*config.Context{"lab": seeded}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	orig := deps.Cfg.Contexts["lab"]
	require.NotNil(t, orig.Proxy.FromEnv)
	require.False(t, *orig.Proxy.FromEnv)

	// --proxy-url together with --proxy-from-env fails ValidateProxyBlock
	// ("both set"), so this update never reaches config.Save.
	_, err := run(t, deps, "", "update", "lab",
		"--proxy-from-env",
		"--proxy-url", "socks5h://h:1080",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "proxy.url and proxy.from-env are both set; use one or the other")

	require.False(t, *orig.Proxy.FromEnv,
		"a rejected update must never write through the stored proxy.from-env pointer")
}

// TestContextUpdate_RejectsInvalidSSHJump verifies a chain no ssh-based
// command could use is refused, with the file left unchanged.
func TestContextUpdate_RejectsInvalidSSHJump(t *testing.T) {
	seed := &config.Config{Contexts: map[string]*config.Context{"lab": labContext()}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	before, err := os.ReadFile(path) //nolint:gosec // G304: path is the test's own scratch config
	require.NoError(t, err)

	_, err = run(t, deps, "", "update", "lab", "--ssh-jump", "x;id")
	require.Error(t, err)
	require.Contains(t, err.Error(),
		`ssh.jump "x;id" is not valid: hop 1: host "x;id" is not a hostname, IPv4 address, or bracketed IPv6 literal`)

	after, err := os.ReadFile(path) //nolint:gosec // G304: path is the test's own scratch config
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "a rejected --ssh-jump must leave the file unchanged")
}

// TestContextUpdate_InvalidSSHJumpJoinsStrictErrors verifies that when both
// the strict rules and the ssh.jump syntax check fail, one run reports all of
// them under the same "fails validation after update" prefix.
func TestContextUpdate_InvalidSSHJumpJoinsStrictErrors(t *testing.T) {
	seed := &config.Config{Contexts: map[string]*config.Context{"lab": labContext()}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "update", "lab", "--ssh-port", "99999", "--ssh-jump", ",")
	require.Error(t, err)
	require.Equal(t,
		`context "lab" fails validation after update: ssh.port 99999 is out of range [1, 65535]; `+
			`ssh.jump "," is not valid: hop 1 is empty`,
		err.Error())
}

// TestContextSSHFieldFlags_HelpOmitsAPIJump pins that the three --ssh-* field
// flags on add and update describe ssh and rsync only: none of them affects
// the API connection's jump hop, so their help must not suggest it does.
func TestContextSSHFieldFlags_HelpOmitsAPIJump(t *testing.T) {
	for _, cmd := range []*cobra.Command{newAddCmd(), newUpdateCmd()} {
		for _, name := range []string{"ssh-user", "ssh-port", "ssh-identity"} {
			fl := cmd.Flags().Lookup(name)
			require.NotNil(t, fl, "%s must define --%s", cmd.Name(), name)
			usage := strings.ToLower(fl.Usage)
			require.NotContains(t, usage, "api", "%s --%s help must not mention the API", cmd.Name(), name)
			require.NotContains(t, usage, "jump", "%s --%s help must not mention a jump hop", cmd.Name(), name)
		}
	}
}

// TestContextUpdate_FullTokenIDAlsoSetsUsername verifies a pasted full
// user@realm!tokenname identifier updates both the token name and the
// username, even when they disagree with the stored username.
func TestContextUpdate_FullTokenIDAlsoSetsUsername(t *testing.T) {
	seed := &config.Config{Contexts: map[string]*config.Context{"backup": labContext()}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "update", "backup", "--token-id", "pmx@pbs!admin")
	require.NoError(t, err)

	ctx := reloadCfg(t, path).Contexts["backup"]
	require.Equal(t, "pmx@pbs", ctx.Auth.Username)
	require.Equal(t, "admin", ctx.Auth.TokenID)

	// A conflicting explicit --username must be rejected.
	_, err = run(t, deps, "", "update", "backup",
		"--username", "root@pbs", "--token-id", "pmx@pbs!admin")
	require.Error(t, err)
	require.Contains(t, err.Error(), "conflicts")
}

// TestContextUpdate_ProductPortRule verifies the same port re-default rule as
// `context edit --product`: a port still at the old product's default follows
// the new product; a customized port is kept with a stderr note.
func TestContextUpdate_ProductPortRule(t *testing.T) {
	seed := &config.Config{Contexts: map[string]*config.Context{
		"defaultport": labContext(),
		"customport":  labContext(),
	}}
	seed.Contexts["customport"].Port = 9000
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "update", "defaultport", "--product", "pbs")
	require.NoError(t, err)
	require.Equal(t, 8007, reloadCfg(t, path).Contexts["defaultport"].Port,
		"port at the old default must follow the new product")

	out, err := run(t, deps, "", "update", "customport", "--product", "pbs")
	require.NoError(t, err)
	require.Equal(t, 9000, reloadCfg(t, path).Contexts["customport"].Port,
		"customized port must be preserved")
	require.Contains(t, out, "port 9000 kept")
}

// TestContextUpdate_ValidationRejectsBadResult verifies a change that fails
// StrictValidateContext is refused and nothing is persisted.
func TestContextUpdate_ValidationRejectsBadResult(t *testing.T) {
	seed := &config.Config{Contexts: map[string]*config.Context{"lab": labContext()}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "update", "lab", "--host", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "fails validation")

	require.Equal(t, "10.0.0.1", reloadCfg(t, path).Contexts["lab"].Host,
		"a failed update must not persist anything")
}

// TestContextUpdate_ErrorPaths covers the no-flags, unknown-name, and
// no-current-context errors.
func TestContextUpdate_ErrorPaths(t *testing.T) {
	seed := &config.Config{Contexts: map[string]*config.Context{"lab": labContext()}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "update", "lab")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no fields to update")

	_, err = run(t, deps, "", "update", "ghost", "--host", "10.0.0.2")
	require.Error(t, err)
	require.Contains(t, err.Error(), `context "ghost" not found`)

	_, err = run(t, deps, "", "update", "--host", "10.0.0.2")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no current-context")

	require.NoError(t, config.Save(path, &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": labContext()},
	}))
	cfg2 := reloadCfg(t, path)
	deps2 := makeDeps(t, path, cfg2)

	// Without a name the current context is updated.
	_, err = run(t, deps2, "", "update", "--default-node", "node9")
	require.NoError(t, err)
	require.Equal(t, "node9", reloadCfg(t, path).Contexts["lab"].DefaultNode)
}

// TestContextUpdate_SetAlias verifies NormalizeAliases auto-grants the `set`
// verb alias to the update command (root assembly applies it to the whole
// tree; the bare Group used by other tests here does not).
func TestContextUpdate_SetAlias(t *testing.T) {
	seed := &config.Config{Contexts: map[string]*config.Context{"lab": labContext()}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	cmd := Group(nil)
	cli.NormalizeAliases(cmd)
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"set", "lab", "--default-node", "node5"})
	require.NoError(t, cmd.Execute())
	require.Equal(t, "node5", reloadCfg(t, path).Contexts["lab"].DefaultNode)
}

// TestContextUpdate_InlineSecretWarns verifies the same inline-literal secret
// warning as `context add`.
func TestContextUpdate_InlineSecretWarns(t *testing.T) {
	seed := &config.Config{Contexts: map[string]*config.Context{"lab": labContext()}}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	out, err := run(t, deps, "", "update", "lab", "--secret", "plaintext-uuid")
	require.NoError(t, err)
	require.Contains(t, out, "inline literal")
}
