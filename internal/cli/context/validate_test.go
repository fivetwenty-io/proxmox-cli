package context

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// validContext returns a context that passes every structural rule.
func validContext() *config.Context {
	return &config.Context{
		Host: "pve.example.com", Port: 8006, Protocol: "https",
		Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "t", Secret: "s"},
	}
}

// TestValidate_RejectsFTPProxy proves a proxy.url with a scheme outside the
// supported set fails validation, naming the scheme.
func TestValidate_RejectsFTPProxy(t *testing.T) {
	c := validContext()
	c.Proxy.URL = "ftp://x"

	path, cfg := makeConfig(t, &config.Config{CurrentContext: "lab", Contexts: map[string]*config.Context{"lab": c}})

	out, err := run(t, makeDeps(t, path, cfg), "", "validate", "lab")
	require.Error(t, err, "a context with an ftp proxy must fail validation")
	require.Contains(t, out, "INVALID")
	require.Contains(t, out, "proxy.url ftp://x must use scheme socks5, socks5h, or http")
}

// TestValidate_ReportsMalformedJump proves a stored ssh.jump that does not
// parse is reported in the context's error list, and fails the command.
func TestValidate_ReportsMalformedJump(t *testing.T) {
	c := validContext()
	c.SSH.Jump = "bad;host"

	deps := jsonDeps(t, &config.Config{CurrentContext: "lab", Contexts: map[string]*config.Context{"lab": c}})

	out, _, err := runWithConnectionFlags(t, context.Background(), deps, "validate", "lab")
	require.Error(t, err)

	entry := parseValidateJSON(t, out)["lab"]
	require.Equal(t, "INVALID", entry.Status)
	require.Len(t, entry.Errors, 1)
	require.True(t, strings.HasPrefix(entry.Errors[0], `ssh.jump "bad;host" is not valid: `), entry.Errors[0])

	// "none" and a blank chain both mean a direct dial, so neither is an error.
	for _, jump := range []string{"none", "  "} {
		c.SSH.Jump = jump

		path, cfg := makeConfig(t, &config.Config{CurrentContext: "lab", Contexts: map[string]*config.Context{"lab": c}})

		_, err = run(t, makeDeps(t, path, cfg), "", "validate", "lab")
		require.NoError(t, err, "ssh.jump %q", jump)
	}

	// The resolver compares "none" as written, so a padded "none" names a
	// bastion called none rather than disabling the jump.
	for _, jump := range []string{" none", "none ", "\tnone\n"} {
		c.SSH.Jump = jump

		out, _, err := runWithConnectionFlags(t, context.Background(),
			jsonDeps(t, &config.Config{CurrentContext: "lab", Contexts: map[string]*config.Context{"lab": c}}),
			"validate", "lab")
		require.Error(t, err, "ssh.jump %q", jump)

		entry := parseValidateJSON(t, out)["lab"]
		require.Equal(t, "INVALID", entry.Status)
		require.Equal(t, []string{fmt.Sprintf(`ssh.jump %q has whitespace around "none", so it names a bastion `+
			`called none instead of dialling direct; write none with nothing around it`, jump)}, entry.Errors)
	}
}

// TestValidate_RefusesAPIFlagsWithoutConnect proves every --api-* flag is
// refused without --connect, because nothing would dial, while the PMX_API_*
// variables stay silent, and that validate carries the annotation that
// keeps the root from refusing the flags itself.
func TestValidate_RefusesAPIFlagsWithoutConnect(t *testing.T) {
	cfg := &config.Config{CurrentContext: "lab", Contexts: map[string]*config.Context{"lab": validContext()}}

	_, _, err := runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg), "validate", "lab", "--api-jump", "b")
	require.EqualError(t, err, "--api-jump has no effect without --connect")

	flagArgs := map[string][]string{
		"api-endpoint":              {"--api-endpoint", "h"},
		"api-jump":                  {"--api-jump", "b"},
		"api-proxy":                 {"--api-proxy", "socks5h://proxy:1080"},
		"api-proxy-from-env":        {"--api-proxy-from-env"},
		"api-ca-cert":               {"--api-ca-cert", "/etc/pmx/ca.pem"},
		"api-fingerprint":           {"--api-fingerprint", wrongFingerprint},
		"api-connect-timeout":       {"--api-connect-timeout", "2s"},
		"api-tls-handshake-timeout": {"--api-tls-handshake-timeout", "2s"},
		"api-request-timeout":       {"--api-request-timeout", "2s"},
	}

	names := connectionFlagNames()
	require.Len(t, names, len(flagArgs), "every root connection flag must be covered here")

	for _, name := range names {
		args, ok := flagArgs[name]
		require.True(t, ok, "no sample value for --%s", name)

		_, _, err := runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg),
			append([]string{"validate", "lab"}, args...)...)
		require.EqualError(t, err, "--"+name+" has no effect without --connect")
	}

	t.Setenv("PMX_API_JUMP", "b")

	out, stderr, err := runWithConnectionFlags(t, context.Background(), jsonDeps(t, cfg), "validate", "lab")
	require.NoError(t, err, "an exported variable stays silent without --connect")
	require.Equal(t, "OK", parseValidateJSON(t, out)["lab"].Status)
	require.NotContains(t, stderr, "PMX_API_JUMP")

	// The command exactly as its factory returns it, and as the group
	// mounts it, carries both annotations.
	requireValidateAnnotations(t, newValidateCmd())

	var mounted *cobra.Command

	for _, c := range Group(nil).Commands() {
		if c.Name() == "validate" {
			mounted = c
		}
	}

	require.NotNil(t, mounted)
	requireValidateAnnotations(t, mounted)
}

// requireValidateAnnotations asserts cmd is a noClient command that
// consumes the connection overrides.
func requireValidateAnnotations(t *testing.T, cmd *cobra.Command) {
	t.Helper()

	require.Equal(t, "true", cmd.Annotations["noClient"])
	require.Equal(t, "true", cmd.Annotations[cli.AnnotationUsesConnection])
}

// TestValidate_LongHelpDescribesRouting pins the long help's new rules and
// the exact proxy-environment sentence.
func TestValidate_LongHelpDescribesRouting(t *testing.T) {
	long := newValidateCmd().Long

	for _, want := range []string{
		"  * proxy.url, if set, is a socks5, socks5h, or http URL with a host and no embedded credentials\n",
		"  * proxy.username and proxy.password are only set alongside proxy.url\n",
		"  * ssh.jump, if set, parses as [user@]host[:port] or ssh://[user@]host[:port] (comma-separated for a " +
			"chain), where a host is a name, an IPv4 address, or a bracketed IPv6 literal, and an @ inside the " +
			"user of an ssh:// hop is written %40\n",
		"  * each timeout, if set, is a positive duration such as 5s or 500ms\n",
		"The probe no longer honours $HTTPS_PROXY on its own; set proxy.from-env: true on the context, " +
			"or pass --api-proxy-from-env, to probe through the proxy environment.",
	} {
		require.Contains(t, long, want)
	}
}

// TestValidate_HelpNamesTOFULimit proves that neither the long help nor the
// --connect flag promises the trust a real call gets from the tls.tofu
// cache, which the probe does not read.
func TestValidate_HelpNamesTOFULimit(t *testing.T) {
	cmd := newValidateCmd()

	require.Contains(t, cmd.Long, "The probe does not read the trust-on-first-use cache, so a context that "+
		"trusts its host only through tls.tofu fails the probe with a certificate error and reads as "+
		"unreachable; pin the certificate with tls.fingerprint to probe it.")
	require.NotContains(t, cmd.Long, "TLS trust, and timeouts a real API call")

	usage := cmd.Flags().Lookup("connect").Usage
	require.Contains(t, usage, "not the tls.tofu cache")
}
