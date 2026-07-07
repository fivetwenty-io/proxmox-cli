package context

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/config"
)

// This file audits every scalar flag on every context sub-command, following
// the flag_audit_test.go convention established in internal/cli/access.
//
// Adaptation: unlike qemu/lxc/node/sdn/cluster/storage/access, context
// commands are config-file-backed, not API-wire-backed — there is no PVE
// request to inspect. Each assertion below instead reloads the persisted
// config.yml (via reloadCfg, round-tripped through config.Save/config.Load)
// and checks the flag landed under the correct config.Context / config.Config
// struct field (which in turn maps 1:1 to a yaml key via that field's yaml
// tag), rather than checking a request body/query param.
//
// Flag inventory covered here: add (13 persisted-field flags + --select/
// --force behavior flags), copy (--force, --select), rm (--force, --yes/-y),
// validate (--all). edit, ls, previous, select, and show carry zero
// command-specific flags (only positional args and inherited global flags) —
// this is a deliberate, confirmed omission, not a gap.

// ---------------------------------------------------------------------------
// context add
// ---------------------------------------------------------------------------

// TestContextAudit_Add_AllFlags asserts every persisted-field flag on
// `context add` lands under its exact config.Context struct field (yaml key).
func TestContextAudit_Add_AllFlags(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "audited",
		"--host", "10.1.2.3",
		"--port", "8443",
		"--protocol", "http",
		"--realm", "pve",
		"--auth-type", "token",
		"--username", "alice@pve",
		"--token-id", "citoken",
		"--secret", "${CI_SECRET}",
		"--insecure",
		"--fingerprint", "AA:BB:CC:DD",
		"--tofu",
		"--default-node", "node3",
		"--default-output", "yaml",
	)
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	ctx, ok := updated.Contexts["audited"]
	require.True(t, ok, "add must persist the new context")

	require.Equal(t, "10.1.2.3", ctx.Host)
	require.Equal(t, 8443, ctx.Port)
	require.Equal(t, "http", ctx.Protocol)
	require.Equal(t, "pve", ctx.Realm)
	require.Equal(t, "token", ctx.Auth.Type)
	require.Equal(t, "alice@pve", ctx.Auth.Username)
	require.Equal(t, "citoken", ctx.Auth.TokenID)
	require.Equal(t, "${CI_SECRET}", ctx.Auth.Secret)
	require.True(t, ctx.TLS.Insecure)
	require.Equal(t, "AA:BB:CC:DD", ctx.TLS.Fingerprint)
	require.True(t, ctx.TLS.Tofu, "--tofu must persist as tls.tofu: true")
	require.Equal(t, "node3", ctx.DefaultNode)
	require.Equal(t, "yaml", ctx.DefaultOutput)
}

// TestContextAudit_Add_OmitsUnsetFlags verifies unset optional flags persist
// as zero values (or their documented default), not as leftover placeholders,
// mirroring TestAccessAudit_DomainCreate_OmitsUnsetFlags's intent for the
// config-file-backed case.
func TestContextAudit_Add_OmitsUnsetFlags(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "minimal",
		"--host", "10.1.2.4",
		"--auth-type", "token",
		"--username", "root@pam",
		"--token-id", "tok",
		"--secret", "${SECRET}",
	)
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	ctx, ok := updated.Contexts["minimal"]
	require.True(t, ok)

	require.Equal(t, "root@pam", ctx.Auth.Username, "--username must persist as given")
	require.Equal(t, "", ctx.TLS.Fingerprint, "unset --fingerprint must persist empty")
	require.False(t, ctx.TLS.Insecure, "unset --insecure must default false")
	require.False(t, ctx.TLS.Tofu, "unset --tofu must default false")
	require.Equal(t, "", ctx.DefaultNode, "unset --default-node must persist empty")
	require.Equal(t, "", ctx.DefaultOutput, "unset --default-output must persist empty")
	require.Equal(t, 8006, ctx.Port, "unset --port must default to 8006")
	require.Equal(t, "https", ctx.Protocol, "unset --protocol must default to https")
	require.Equal(t, "pam", ctx.Realm, "unset --realm must default to pam")
}

// TestContextAudit_Add_SelectFlag asserts --select promotes the new context
// to current-context and records the prior current-context as
// previous-context — a config.Config-level side effect, not a
// config.Context field.
func TestContextAudit_Add_SelectFlag(t *testing.T) {
	seed := &config.Config{
		CurrentContext: "existing",
		Contexts: map[string]*config.Context{
			"existing": labContext(),
		},
	}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "newctx",
		"--host", "10.0.0.9",
		"--auth-type", "token",
		"--username", "root@pam",
		"--token-id", "tok",
		"--secret", "${SECRET}",
		"--select",
	)
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, "newctx", updated.CurrentContext,
		"--select must set current-context to the new context name")
	require.Equal(t, "existing", updated.PreviousContext,
		"--select must record the prior current-context as previous-context")
}

// TestContextAudit_Add_ForceFlag asserts --force is the sole gate allowing
// `add` to overwrite an existing context name.
func TestContextAudit_Add_ForceFlag(t *testing.T) {
	seed := &config.Config{
		Contexts: map[string]*config.Context{
			"dup": labContext(),
		},
	}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "dup",
		"--host", "10.0.0.9", "--auth-type", "token", "--username", "root@pam",
		"--token-id", "tok", "--secret", "${SECRET}")
	require.Error(t, err, "add of a duplicate name must fail without --force")
	require.Contains(t, err.Error(), "--force")

	_, err = run(t, deps, "", "add", "dup",
		"--host", "10.0.0.9", "--auth-type", "token", "--username", "root@pam",
		"--token-id", "tok", "--secret", "${SECRET}", "--force")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, "10.0.0.9", updated.Contexts["dup"].Host,
		"--force must persist the overwritten host")
}

// TestContextAudit_Add_ProductFlag asserts --product lands under
// config.Context.Product (yaml key "product"), defaulting to "pve" when
// unset.
func TestContextAudit_Add_ProductFlag(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "defaultproduct",
		"--host", "10.1.2.5",
		"--auth-type", "token",
		"--username", "root@pam",
		"--token-id", "tok",
		"--secret", "${SECRET}",
	)
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, config.ProductPVE, updated.Contexts["defaultproduct"].Product,
		"unset --product must default to pve")

	_, err = run(t, deps, "", "add", "explicitpbs",
		"--host", "10.1.2.6",
		"--auth-type", "token",
		"--username", "root@pam",
		"--token-id", "tok",
		"--secret", "${SECRET}",
		"--product", "pbs",
	)
	require.NoError(t, err)

	updated = reloadCfg(t, path)
	require.Equal(t, config.ProductPBS, updated.Contexts["explicitpbs"].Product)
}

// TestContextAudit_Add_ProductRejectsInvalidValue asserts an unrecognised
// --product value is refused rather than silently persisted.
func TestContextAudit_Add_ProductRejectsInvalidValue(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "badproduct",
		"--host", "10.1.2.7",
		"--auth-type", "token",
		"--username", "root@pam",
		"--token-id", "tok",
		"--secret", "${SECRET}",
		"--product", "vmware",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--product must be")

	updated := reloadCfg(t, path)
	require.NotContains(t, updated.Contexts, "badproduct",
		"an invalid --product must not persist a context")
}

// TestContextAudit_Add_ProductPBS_DefaultsPort8007 asserts --product=pbs
// without an explicit --port defaults the persisted port to 8007 (the PBS API
// port), not the PVE default of 8006.
func TestContextAudit_Add_ProductPBS_DefaultsPort8007(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "pbsctx",
		"--host", "10.1.2.8",
		"--auth-type", "token",
		"--username", "root@pam",
		"--token-id", "tok",
		"--secret", "${SECRET}",
		"--product", "pbs",
	)
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, 8007, updated.Contexts["pbsctx"].Port,
		"--product=pbs without --port must default the port to 8007")
}

// TestContextAudit_Add_ProductPBS_ExplicitPortWins asserts an explicit --port
// is never overridden by the product-aware port default, even when
// --product=pbs is given.
func TestContextAudit_Add_ProductPBS_ExplicitPortWins(t *testing.T) {
	path, cfg := makeConfig(t, &config.Config{})
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "add", "pbsctxport",
		"--host", "10.1.2.9",
		"--auth-type", "token",
		"--username", "root@pam",
		"--token-id", "tok",
		"--secret", "${SECRET}",
		"--product", "pbs",
		"--port", "9000",
	)
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, 9000, updated.Contexts["pbsctxport"].Port,
		"explicit --port must win over the product-aware default")
}

// ---------------------------------------------------------------------------
// context copy
// ---------------------------------------------------------------------------

// TestContextAudit_Copy_ForceFlag asserts --force is the sole gate allowing
// `copy` to overwrite an existing dst name.
func TestContextAudit_Copy_ForceFlag(t *testing.T) {
	src := labContext()
	src.Host = "src-host"
	dstOld := labContext()
	dstOld.Host = "old-dst-host"
	seed := &config.Config{
		Contexts: map[string]*config.Context{
			"src": src,
			"dst": dstOld,
		},
	}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "copy", "src", "dst")
	require.Error(t, err, "copy onto an existing dst must fail without --force")

	_, err = run(t, deps, "", "copy", "src", "dst", "--force")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, "src-host", updated.Contexts["dst"].Host,
		"--force must persist the overwritten dst host")
}

// TestContextAudit_Copy_SelectFlag asserts --select promotes dst to
// current-context, mirroring the add-verb assertion above.
func TestContextAudit_Copy_SelectFlag(t *testing.T) {
	seed := &config.Config{
		CurrentContext: "src",
		Contexts: map[string]*config.Context{
			"src": labContext(),
		},
	}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "copy", "src", "dst", "--select")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.Equal(t, "dst", updated.CurrentContext, "--select must set current-context to dst")
	require.Equal(t, "src", updated.PreviousContext, "--select must record src as previous-context")
}

// ---------------------------------------------------------------------------
// context rm
// ---------------------------------------------------------------------------

// TestContextAudit_Rm_ForceFlag asserts --force is the sole gate allowing
// removal of the active context.
func TestContextAudit_Rm_ForceFlag(t *testing.T) {
	seed := &config.Config{
		CurrentContext: "active",
		Contexts: map[string]*config.Context{
			"active": labContext(),
		},
	}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "rm", "active", "--yes")
	require.Error(t, err, "rm of the active context must fail without --force")
	require.Contains(t, err.Error(), "--force")

	_, err = run(t, deps, "", "rm", "active", "--yes", "--force")
	require.NoError(t, err)

	updated := reloadCfg(t, path)
	require.NotContains(t, updated.Contexts, "active",
		"--force must allow removing the active context")
	require.Equal(t, "", updated.CurrentContext,
		"--force removal of the active context must clear current-context")
}

// TestContextAudit_Rm_YesFlag asserts --yes (and its -y shorthand) is the
// sole gate allowing rm to proceed at all.
func TestContextAudit_Rm_YesFlag(t *testing.T) {
	seed := &config.Config{
		Contexts: map[string]*config.Context{
			"gone": labContext(),
		},
	}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	_, err := run(t, deps, "", "rm", "gone")
	require.Error(t, err, "rm without --yes must be refused")
	require.Contains(t, err.Error(), "--yes")

	_, err = run(t, deps, "", "rm", "gone", "-y")
	require.NoError(t, err, "-y shorthand must be equivalent to --yes")

	updated := reloadCfg(t, path)
	require.NotContains(t, updated.Contexts, "gone")
}

// ---------------------------------------------------------------------------
// context validate
// ---------------------------------------------------------------------------

// TestContextAudit_Validate_AllFlag asserts --all expands validation from the
// single resolved context to every context in the config.
func TestContextAudit_Validate_AllFlag(t *testing.T) {
	goodCtx := labContext()
	badCtx := labContext()
	badCtx.Host = ""
	seed := &config.Config{
		CurrentContext: "good",
		Contexts: map[string]*config.Context{
			"good": goodCtx,
			"bad":  badCtx,
		},
	}
	path, cfg := makeConfig(t, seed)
	deps := makeDeps(t, path, cfg)

	// Without --all: only the current context ("good") is validated.
	out, err := run(t, deps, "", "validate")
	require.NoError(t, err)
	require.NotContains(t, out, "bad")

	// With --all: every context is validated, surfacing bad's failure too.
	out, err = run(t, deps, "", "validate", "--all")
	require.Error(t, err, "--all must exit non-zero when any context is invalid")
	require.Contains(t, out, "bad")
	require.Contains(t, out, "good")
}
