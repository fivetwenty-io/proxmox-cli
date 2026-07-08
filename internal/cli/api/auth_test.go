package api

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/pmx-cli/internal/config"
)

// This file is a white-box companion to api_test.go (package api_test): it
// exercises contextOptions and isInteractiveInput directly since both are
// unexported and there is no other way to inspect the pve.Options a context
// produces without a real or mocked TLS handshake (pve.Client does not expose
// the Options it was built from).

// testCmdWithConfigPath returns a bare *cobra.Command carrying a --config
// flag set to path, mirroring the flag the root command registers, with
// stdin/stderr wired so contextOptions has somewhere to read/write a TOFU
// prompt from/to if it ever activates during a test.
func testCmdWithConfigPath(path, stdin string, stderr *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("config", path, "")
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetErr(stderr)
	return cmd
}

// sampleAuthContext returns a fully-valid password-auth Context with the
// given tls.tofu / tls.insecure values, for use across contextOptions tests.
func sampleAuthContext(tofu, insecure bool) *config.Context {
	return &config.Context{
		Host:     "pve.example.com",
		Port:     8006,
		Protocol: "https",
		Realm:    "pam",
		Auth: config.AuthBlock{
			Type:     "password",
			Username: "admin@pam",
			Secret:   "secretpw",
		},
		TLS: config.TLSBlock{
			Tofu:     tofu,
			Insecure: insecure,
		},
	}
}

// ---------------------------------------------------------------------------
// contextOptions — TOFU gating (IMP-02c)
// ---------------------------------------------------------------------------

func TestContextOptions_TofuDisabled_OptionsUnchanged(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(false, false)

	opts := contextOptions(cmd, ctx, false, "prod", "admin@pam", "pam", "", "secretpw", "", "")

	require.Empty(t, opts.FingerprintCachePath,
		"tls.tofu=false must leave FingerprintCachePath empty")
	require.Nil(t, opts.ManualVerifyCallback,
		"tls.tofu=false must leave ManualVerifyCallback nil")
	require.Equal(t, "pve.example.com", opts.Host, "unrelated Options fields must be preserved")
	require.Equal(t, "secretpw", opts.Password)
}

func TestContextOptions_TofuEnabled_WiresFingerprintPinning(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(true, false)

	opts := contextOptions(cmd, ctx, false, "prod", "admin@pam", "pam", "", "secretpw", "", "")

	require.Equal(t, "/home/user/.config/pmx/fingerprints/prod.json", opts.FingerprintCachePath,
		"tls.tofu=true must set the per-context fingerprint cache path")
	require.NotNil(t, opts.ManualVerifyCallback,
		"tls.tofu=true must install the manual-verify callback")
}

func TestContextOptions_TofuEnabledButInsecure_OptionsUnchanged(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(true, true)

	opts := contextOptions(cmd, ctx, false, "prod", "admin@pam", "pam", "", "secretpw", "", "")

	require.Empty(t, opts.FingerprintCachePath,
		"tls.insecure=true must suppress TOFU wiring even when tls.tofu=true")
	require.Nil(t, opts.ManualVerifyCallback)
}

func TestContextOptions_DifferentContexts_DistinctCachePaths(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(true, false)

	prod := contextOptions(cmd, ctx, false, "prod", "admin@pam", "pam", "", "secretpw", "", "")
	staging := contextOptions(cmd, ctx, false, "staging", "admin@pam", "pam", "", "secretpw", "", "")

	require.NotEqual(t, prod.FingerprintCachePath, staging.FingerprintCachePath,
		"each context must persist trust decisions to its own cache file, "+
			"even when built from the same auth client construction path")
}

func TestContextOptions_TokenCredentialPassedThrough(t *testing.T) {
	// buildClientForOIDC's placeholder-token path calls contextOptions with a
	// non-empty token and empty user/realm/password/ticket/csrf, exactly as
	// buildClientForOIDC itself does; confirm that shape still round-trips
	// through BuildOptions alongside TOFU gating (tofu disabled here, so no
	// fingerprint wiring is expected). BuildOptions formats APIToken as
	// "user!token" regardless of whether user is empty, hence the leading "!"
	// — this is pre-existing BuildOptions behavior, unchanged by IMP-02c.
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(false, false)

	opts := contextOptions(cmd, ctx, false, "prod", "", "",
		"dummy@pam!oidc=00000000-0000-0000-0000-000000000000", "", "", "")

	require.Equal(t, "!dummy@pam!oidc=00000000-0000-0000-0000-000000000000", opts.APIToken)
	require.Empty(t, opts.FingerprintCachePath)
	require.Nil(t, opts.ManualVerifyCallback)
}

// ---------------------------------------------------------------------------
// contextOptions — global --insecure flag merge
// ---------------------------------------------------------------------------

// TestContextOptions_GlobalInsecureFlag_OverridesConfig verifies that a true
// flagInsecure argument disables certificate verification (via
// apiclient.BuildOptions' insecure parameter, surfaced here as a non-nil
// opts.SSLOptions with VerifyMode == pve.SSLVerifyNone) even when the
// context's own tls.insecure is false, and — since ApplyTOFUOptions treats
// "insecure" as a hard gate regardless of which input set it — also
// suppresses TOFU wiring even though tls.tofu is true on the context. This
// mirrors the precedence internal/cli/root.go applies for every other
// command: "insecure := pf.insecure || ctx.TLS.Insecure".
func TestContextOptions_GlobalInsecureFlag_OverridesConfig(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(true, false) // tls.tofu=true, tls.insecure=false

	opts := contextOptions(cmd, ctx, true, "prod", "admin@pam", "pam", "", "secretpw", "", "")

	require.NotNil(t, opts.SSLOptions, "flagInsecure=true must disable certificate verification")
	require.Equal(t, pve.SSLVerifyNone, opts.SSLOptions.VerifyMode)
	require.False(t, opts.SSLOptions.VerifyHostname)
	require.Empty(t, opts.FingerprintCachePath,
		"flagInsecure=true must suppress TOFU wiring even when tls.tofu=true")
	require.Nil(t, opts.ManualVerifyCallback)
}

// TestContextOptions_GlobalInsecureFlagUnset_ConfigOnlyBehaviorUnchanged
// verifies that flagInsecure=false leaves contextOptions' existing
// config-only (ctx.TLS.Insecure / ctx.TLS.Tofu) behavior unchanged — i.e. the
// merge introduces no regression for the pre-existing call sites that never
// pass the global flag.
func TestContextOptions_GlobalInsecureFlagUnset_ConfigOnlyBehaviorUnchanged(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(true, false) // tls.tofu=true, tls.insecure=false

	opts := contextOptions(cmd, ctx, false, "prod", "admin@pam", "pam", "", "secretpw", "", "")

	require.Nil(t, opts.SSLOptions, "flagInsecure=false, tls.insecure=false must leave SSLOptions nil")
	require.NotEmpty(t, opts.FingerprintCachePath, "tls.tofu=true must still wire TOFU when flagInsecure is false")
	require.NotNil(t, opts.ManualVerifyCallback)
}

// ---------------------------------------------------------------------------
// isInteractiveInput
// ---------------------------------------------------------------------------

func TestIsInteractiveInput_NonFileReader_ReturnsFalse(t *testing.T) {
	// strings.Reader is never *os.File, so this must always be non-interactive
	// regardless of content — the same behavior a real non-TTY invocation gets.
	require.False(t, isInteractiveInput(strings.NewReader("y\n")))
}

// ---------------------------------------------------------------------------
// requirePVEContext — product guard on the auth client builders
// ---------------------------------------------------------------------------

func TestRequirePVEContext_PBSProduct_Errors(t *testing.T) {
	ctx := sampleAuthContext(false, false)
	ctx.Product = config.ProductPBS

	err := requirePVEContext(ctx, "backup1")

	require.Error(t, err, "auth commands must reject PBS contexts")
	require.Contains(t, err.Error(), "backup1", "error must name the offending context")
	require.Contains(t, err.Error(), "Proxmox Backup Server")
}

func TestRequirePVEContext_PVEAndEmptyProduct_Allowed(t *testing.T) {
	ctx := sampleAuthContext(false, false)
	require.NoError(t, requirePVEContext(ctx, "prod"), "empty product means PVE")

	ctx.Product = config.ProductPVE
	require.NoError(t, requirePVEContext(ctx, "prod"))
}

func TestClientForContext_RejectsPBSContext(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(false, false)
	ctx.Product = config.ProductPBS

	ac, err := clientForContext(cmd, ctx, "backup1", "admin@pam", "pam", "secretpw", "", "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "Proxmox Backup Server")
	require.Nil(t, ac)
}

func TestBuildClientForOIDC_RejectsPBSContext(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(false, false)
	ctx.Product = config.ProductPBS

	ac, err := buildClientForOIDC(cmd, ctx, "backup1")

	require.Error(t, err)
	require.Contains(t, err.Error(), "Proxmox Backup Server")
	require.Nil(t, ac)
}

// TestRequirePVEContext_PDMProduct_Errors mirrors
// TestRequirePVEContext_PBSProduct_Errors for Proxmox Datacenter Manager: like
// PBS, a PDM context's ticket-based login is not wired yet, so requirePVEContext
// must reject it with guidance pointing at 'auth set-token'.
func TestRequirePVEContext_PDMProduct_Errors(t *testing.T) {
	ctx := sampleAuthContext(false, false)
	ctx.Product = config.ProductPDM

	err := requirePVEContext(ctx, "dc1")

	require.Error(t, err, "auth commands must reject PDM contexts")
	require.Contains(t, err.Error(), "dc1", "error must name the offending context")
	require.Contains(t, err.Error(), "Proxmox Datacenter Manager")
}

func TestClientForContext_RejectsPDMContext(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(false, false)
	ctx.Product = config.ProductPDM

	ac, err := clientForContext(cmd, ctx, "dc1", "admin@pam", "pam", "secretpw", "", "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "Proxmox Datacenter Manager")
	require.Nil(t, ac)
}

func TestBuildClientForOIDC_RejectsPDMContext(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(false, false)
	ctx.Product = config.ProductPDM

	ac, err := buildClientForOIDC(cmd, ctx, "dc1")

	require.Error(t, err)
	require.Contains(t, err.Error(), "Proxmox Datacenter Manager")
	require.Nil(t, ac)
}
