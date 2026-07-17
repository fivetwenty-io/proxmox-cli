package context

import (
	"bytes"
	"context"
	"strings"
	"testing"

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
