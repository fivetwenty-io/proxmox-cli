package api

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// This file is a white-box companion to api_test.go (package api_test): it
// exercises contextOptions and isInteractiveInput directly since both are
// unexported and there is no other way to inspect the pve.Options a context
// produces without a real or mocked TLS handshake (pve.Client does not expose
// the Options it was built from).

// testCmdWithConfigPath returns a bare *cobra.Command carrying a --config
// flag set to path, mirroring the flag the root command registers, with
// stdin/stderr wired so contextOptions has somewhere to read/write a TOFU
// prompt from/to if it ever activates during a test. It also carries an empty
// *cli.Deps in its context (cli.GetDeps requires this — see
// newAuthClientForContext, which reads Deps.Insecure before any other work).
func testCmdWithConfigPath(path, stdin string, stderr *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("config", path, "")
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetErr(stderr)
	cmd.SetContext(cli.WithDeps(context.Background(), &cli.Deps{}))
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
// contextOptions — TOFU gating
// ---------------------------------------------------------------------------

func TestContextOptions_TofuDisabled_OptionsUnchanged(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(false, false)

	opts, _, err := contextOptions(cmd, ctx, false, "prod", "admin@pam", "pam", "", "secretpw", "", "")
	require.NoError(t, err)

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

	opts, _, err := contextOptions(cmd, ctx, false, "prod", "admin@pam", "pam", "", "secretpw", "", "")
	require.NoError(t, err)

	require.Equal(t, "/home/user/.config/pmx/fingerprints/prod.json", opts.FingerprintCachePath,
		"tls.tofu=true must set the per-context fingerprint cache path")
	require.NotNil(t, opts.ManualVerifyCallback,
		"tls.tofu=true must install the manual-verify callback")
}

func TestContextOptions_TofuEnabledButInsecure_OptionsUnchanged(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(true, true)

	opts, _, err := contextOptions(cmd, ctx, false, "prod", "admin@pam", "pam", "", "secretpw", "", "")
	require.NoError(t, err)

	require.Empty(t, opts.FingerprintCachePath,
		"tls.insecure=true must suppress TOFU wiring even when tls.tofu=true")
	require.Nil(t, opts.ManualVerifyCallback)
}

func TestContextOptions_DifferentContexts_DistinctCachePaths(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(true, false)

	prod, _, err := contextOptions(cmd, ctx, false, "prod", "admin@pam", "pam", "", "secretpw", "", "")
	require.NoError(t, err)
	staging, _, err := contextOptions(cmd, ctx, false, "staging", "admin@pam", "pam", "", "secretpw", "", "")
	require.NoError(t, err)

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
	// — this is pre-existing BuildOptions behavior.
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(false, false)

	opts, _, err := contextOptions(cmd, ctx, false, "prod", "", "",
		"dummy@pam!oidc=00000000-0000-0000-0000-000000000000", "", "", "")
	require.NoError(t, err)

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

	opts, _, err := contextOptions(cmd, ctx, true, "prod", "admin@pam", "pam", "", "secretpw", "", "")
	require.NoError(t, err)

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

	opts, _, err := contextOptions(cmd, ctx, false, "prod", "admin@pam", "pam", "", "secretpw", "", "")
	require.NoError(t, err)

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
// buildClientForOIDC — product dispatch (no network call; construction alone
// proves the seam wired the matching adapter). See api_test.go's
// TestAuthLogin_OIDC_PBSContext_Success and TestAuthLogin_OIDC_PDMContext_Success
// for full end-to-end OIDC login coverage against fake PBS/PDM servers.
// ---------------------------------------------------------------------------

func TestBuildClientForOIDC_DispatchesByProduct(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)

	cases := []struct {
		name    string
		product string
		want    any
	}{
		{"empty product defaults to PVE", "", &pveAuthClient{}},
		{"pve", config.ProductPVE, &pveAuthClient{}},
		{"pbs", config.ProductPBS, &pbsAuthClient{}},
		{"pdm", config.ProductPDM, &pdmAuthClient{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := sampleAuthContext(false, false)
			ctx.Product = tc.product

			ac, _, err := buildClientForOIDC(cmd, ctx, "prod")
			require.NoError(t, err)
			require.IsType(t, tc.want, ac)
		})
	}
}

// ---------------------------------------------------------------------------
// newAuthClientForContext — product dispatch
// ---------------------------------------------------------------------------

// TestNewAuthClientForContext_DispatchesByProduct verifies that
// newAuthClientForContext wraps the constructed client in the adapter
// matching ctx.Product, for every supported product plus the empty-Product
// (pre-Product-field config) fallback to PVE. No network call is made:
// client construction alone is enough to prove the switch dispatched
// correctly.
func TestNewAuthClientForContext_DispatchesByProduct(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)

	cases := []struct {
		name    string
		product string
		want    any
	}{
		{"empty product defaults to PVE", "", &pveAuthClient{}},
		{"pve", config.ProductPVE, &pveAuthClient{}},
		{"pbs", config.ProductPBS, &pbsAuthClient{}},
		{"pdm", config.ProductPDM, &pdmAuthClient{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := sampleAuthContext(false, false)
			ctx.Product = tc.product

			ac, _, err := newAuthClientForContext(cmd, ctx, "prod", "admin@pam", "pam", "secretpw", "", "", "")
			require.NoError(t, err)
			require.IsType(t, tc.want, ac)
		})
	}
}

// TestNewAuthClientForContext_UnsupportedProduct_Errors verifies the default
// switch arm rejects a product string that is neither pve/pbs/pdm nor empty.
func TestNewAuthClientForContext_UnsupportedProduct_Errors(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(false, false)
	ctx.Product = "bogus"

	ac, _, err := newAuthClientForContext(cmd, ctx, "prod", "admin@pam", "pam", "secretpw", "", "", "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus")
	require.Nil(t, ac)
}

// --- parseOIDCRedirect -----------------------------------------------------

// TestParseOIDCRedirect_ExtractsCodeAndState covers the happy path: both
// parameters come back verbatim, including a state value carrying characters
// that must survive query decoding.
func TestParseOIDCRedirect_ExtractsCodeAndState(t *testing.T) {
	code, state, err := parseOIDCRedirect(
		"https://pve.example.test/?code=auth-code-123&state=csrf%2Fvalue%3D%3D")
	require.NoError(t, err)
	require.Equal(t, "auth-code-123", code)
	require.Equal(t, "csrf/value==", state)
}

// TestParseOIDCRedirect_ErrorsNeverEchoTheCode covers the disclosure path.
// The authorization code is a single-use credential, and these errors reach
// both the terminal and the JSONL exit record, which persists to disk.
func TestParseOIDCRedirect_ErrorsNeverEchoTheCode(t *testing.T) {
	const secretCode = "super-secret-auth-code"

	cases := []struct {
		name string
		url  string
	}{
		{"missing state", "https://pve.example.test/?code=" + secretCode},
		{"missing code", "https://pve.example.test/?state=abc&token=" + secretCode},
		{"unparseable", "https://pve.example.test/%zz?code=" + secretCode},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseOIDCRedirect(tc.url)
			require.Error(t, err)
			require.NotContains(t, err.Error(), secretCode,
				"the authorization code must never reach an error message or the log")
			require.Contains(t, err.Error(), "pve.example.test",
				"the host must survive so the operator can still recognise the URL")
		})
	}
}

// TestParseOIDCRedirect_EmptyURL covers the guard that keeps an empty paste
// from being reported as a parse failure.
func TestParseOIDCRedirect_EmptyURL(t *testing.T) {
	_, _, err := parseOIDCRedirect("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")
}

// ---------------------------------------------------------------------------
// The shared connection path: helpers
// ---------------------------------------------------------------------------

// insecureWarning is the line cli.WarnInsecureTLS prints.
const insecureWarning = "WARN: TLS certificate verification disabled"

// authRun runs `pmx --config cfgPath --no-log <args>` through the real root
// command with the auth group attached, so the production PersistentPreRunE
// wires Deps, including the lazy connection overrides, and applies the
// refusal of an unused --api-* flag. It captures standard output and
// standard error separately.
func authRun(t *testing.T, cfgPath string, args ...string) (string, string, error) {
	t.Helper()

	t.Setenv("PMX_OUTPUT", "table")
	t.Setenv("PMX_NODE", "")
	t.Setenv("PMX_CONTEXT", "")

	root, cleanup := cli.NewRootCmd("pmx")
	defer cleanup()
	root.SetContext(context.Background())
	root.AddCommand(newAuthCmd())

	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"--config", cfgPath, "--no-log"}, args...))

	err := root.Execute()

	return stdout.String(), stderr.String(), err
}

// writeAuthConfig saves a config whose only context is "lab" to a fresh
// file and returns the file's path.
func writeAuthConfig(t *testing.T, lab *config.Context) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, config.SaveForce(path, &config.Config{
		CurrentContext: "lab",
		Contexts:       map[string]*config.Context{"lab": lab},
	}))

	return path
}

// reloadLab reads the config file back from disk and returns its "lab"
// context, which is what a later invocation would see.
func reloadLab(t *testing.T, path string) *config.Context {
	t.Helper()

	cfg, err := config.Load(path)
	require.NoError(t, err)
	require.Contains(t, cfg.Contexts, "lab")

	return cfg.Contexts["lab"]
}

// statusRows runs `auth status -o json` with extra arguments, requires it to
// exit 0, and returns its rows together with the raw standard output and
// standard error.
func statusRows(t *testing.T, path string, extra ...string) (map[string]string, string, string) {
	t.Helper()

	args := append([]string{"auth", "status", "-o", "json"}, extra...)
	stdout, stderr, err := authRun(t, path, args...)
	require.NoError(t, err, "auth status must exit 0; stderr: %s", stderr)

	var doc struct {
		Data map[string]string `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &doc), stdout)

	return doc.Data, stdout, stderr
}

// serverPort returns the port a httptest server listens on.
func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()

	_, portText, err := net.SplitHostPort(srv.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)

	return port
}

// tunnelArgs points the invocation at host:port through the SOCKS5 stand-in.
// A test starts the stand-in before the server behind it, so the server's
// cleanup runs first and closes the idle keep-alive connection the relay is
// still carrying; the stand-in's own cleanup waits for every relay to end.
// The stand-in dials 127.0.0.1 on the requested port whatever host it is
// asked for, so an endpoint override naming a host that does not resolve
// still reaches the local test server, and the host travels unresolved
// because the scheme is socks5h.
func tunnelArgs(host string, port int, socks *testhelper.SOCKS5Proxy) []string {
	return []string{
		"--api-endpoint", net.JoinHostPort(host, strconv.Itoa(port)),
		"--api-proxy", "socks5h://" + socks.Addr,
	}
}

// oidcServer is a TLS stand-in for the two public OpenID endpoints, which
// PVE, PBS, and PDM serve under the same paths. It records the redirect URL
// each request carried.
type oidcServer struct {
	srv *httptest.Server

	mu                          sync.Mutex
	authURLRedirects, loginURLs []string
}

func newOIDCServer(t *testing.T) *oidcServer {
	t.Helper()

	s := &oidcServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api2/json/access/openid/auth-url", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		s.authURLRedirects = append(s.authURLRedirects, r.PostFormValue("redirect-url"))
		s.mu.Unlock()
		testhelper.WriteData(w, "https://idp.example.com/auth?client_id=pmx")
	})
	mux.HandleFunc("POST /api2/json/access/openid/login", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		s.loginURLs = append(s.loginURLs, r.PostFormValue("redirect-url"))
		s.mu.Unlock()
		testhelper.WriteData(w, map[string]any{
			"username":            "alice@corp",
			"ticket":              "PVE:alice@corp:OIDC",
			"CSRFPreventionToken": "csrf-oidc",
		})
	})

	s.srv = httptest.NewTLSServer(mux)
	t.Cleanup(s.srv.Close)

	return s
}

// redirects returns the redirect URLs the auth-url and login requests
// carried, in order.
func (s *oidcServer) redirects() (authURL, login []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.authURLRedirects...), append([]string(nil), s.loginURLs...)
}

// oidcLab returns a context for an OpenID login against host. It omits
// port and protocol, so both come from the defaults, and it disables
// verification because the TLS stand-in's certificate names 127.0.0.1.
func oidcLab(host string) *config.Context {
	return &config.Context{
		Host:  host,
		Realm: "myoidc",
		Auth: config.AuthBlock{
			Type:     "password",
			Username: "alice@corp",
			Secret:   "${PMX_TEST_OIDC_UNUSED}",
		},
		TLS: config.TLSBlock{Insecure: true},
	}
}

// oidcLoginArgs is a non-interactive OpenID login.
func oidcLoginArgs(extra ...string) []string {
	return append([]string{"auth", "login", "--oidc", "--code", "the-code", "--state", "the-state"}, extra...)
}

// ---------------------------------------------------------------------------
// The OpenID redirect
// ---------------------------------------------------------------------------

// TestStoredEndpointURL_AppliesDefaults pins the redirect's default: the
// stored endpoint with the product's port and the https default applied to
// a copy, an IPv6 literal bracketed exactly once, and the stored context
// left untouched.
func TestStoredEndpointURL_AppliesDefaults(t *testing.T) {
	cases := []struct {
		name string
		ctx  config.Context
		want string
	}{
		{"pve defaults", config.Context{Host: "pve1.example.com"}, "https://pve1.example.com:8006"},
		{"pbs port", config.Context{Host: "pbs1.example.com", Product: config.ProductPBS}, "https://pbs1.example.com:8007"},
		{"pdm port", config.Context{Host: "pdm1.example.com", Product: config.ProductPDM}, "https://pdm1.example.com:8443"},
		{"explicit values", config.Context{Host: "pve1", Port: 443, Protocol: "http"}, "http://pve1:443"},
		{"bare ipv6", config.Context{Host: "::1"}, "https://[::1]:8006"},
		{"bracketed ipv6", config.Context{Host: "[::1]"}, "https://[::1]:8006"},
		{"no host", config.Context{}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored := tc.ctx
			before := stored

			require.Equal(t, tc.want, storedEndpointURL(&stored))
			require.Equal(t, before, stored, "rendering the redirect must not write defaults into the context")
		})
	}
}

// TestOIDCLogin_RedirectURLUsesResolvedDefaults is the reported bug: a
// context that omits port and protocol used to send the redirect
// "://host:0" to the identity provider.
func TestOIDCLogin_RedirectURLUsesResolvedDefaults(t *testing.T) {
	socks := testhelper.SOCKS5StandIn(t)
	srv := newOIDCServer(t)
	path := writeAuthConfig(t, oidcLab("pve1.example.com"))

	_, stderr, err := authRun(t, path,
		oidcLoginArgs(tunnelArgs("pve1.example.com", serverPort(t, srv.srv), socks)...)...)
	require.NoError(t, err, stderr)

	authURL, login := srv.redirects()
	require.Equal(t, []string{"https://pve1.example.com:8006"}, authURL)
	require.Equal(t, authURL, login, "the login must repeat the redirect the auth-url request sent")
	for _, r := range append(authURL, login...) {
		require.NotContains(t, r, "://pve1.example.com:0")
	}

	saved := reloadLab(t, path)
	require.NotNil(t, saved.Auth.Session)
	require.Equal(t, "PVE:alice@corp:OIDC", saved.Auth.Session.Ticket)
	require.Zero(t, saved.Port, "the login must not write the default port into the file")
	require.Empty(t, saved.Protocol, "the login must not write the default protocol into the file")
}

// TestOIDCLogin_RedirectURLUsesProductPort covers the product default: a
// PBS context with no port answers on 8007.
func TestOIDCLogin_RedirectURLUsesProductPort(t *testing.T) {
	socks := testhelper.SOCKS5StandIn(t)
	srv := newOIDCServer(t)
	lab := oidcLab("pbs1.example.com")
	lab.Product = config.ProductPBS
	path := writeAuthConfig(t, lab)

	_, stderr, err := authRun(t, path,
		oidcLoginArgs(tunnelArgs("pbs1.example.com", serverPort(t, srv.srv), socks)...)...)
	require.NoError(t, err, stderr)

	authURL, login := srv.redirects()
	require.Equal(t, []string{"https://pbs1.example.com:8007"}, authURL)
	require.Equal(t, authURL, login)
}

// TestOIDCLogin_RedirectIgnoresEndpointOverride pins the redirect as the
// identity registered with the provider: an endpoint override changes where
// the login connects, not what it tells the provider, while --redirect-url
// still replaces it, and auth status reports the override.
func TestOIDCLogin_RedirectIgnoresEndpointOverride(t *testing.T) {
	socks := testhelper.SOCKS5StandIn(t)
	srv := newOIDCServer(t)
	port := serverPort(t, srv.srv)
	path := writeAuthConfig(t, oidcLab("pve1.example.com"))

	_, stderr, err := authRun(t, path, oidcLoginArgs(tunnelArgs("pve9", port, socks)...)...)
	require.NoError(t, err, stderr)

	authURL, login := srv.redirects()
	require.Equal(t, []string{"https://pve1.example.com:8006"}, authURL)
	require.Equal(t, authURL, login)

	_, stderr, err = authRun(t, path, oidcLoginArgs(append(tunnelArgs("pve9", port, socks),
		"--redirect-url", "https://custom.example.com/cb")...)...)
	require.NoError(t, err, stderr)

	authURL, login = srv.redirects()
	require.Equal(t, []string{"https://pve1.example.com:8006", "https://custom.example.com/cb"}, authURL)
	require.Equal(t, authURL, login)

	rows, _, _ := statusRows(t, path, "--api-endpoint", "pve9:9999")
	require.Equal(t, "https://pve9:9999 (from --api-endpoint)", rows["Host"])
}

// TestOIDCLogin_RealmReadFromStoredContext pins the realm guard to the
// stored context. Resolving a context used to write realm: pam into the
// stored entry, so the guard never fired and an OpenID login without
// --realm sent "pam". An earlier invocation must not change that either.
func TestOIDCLogin_RealmReadFromStoredContext(t *testing.T) {
	socks := testhelper.SOCKS5StandIn(t)
	srv := newOIDCServer(t)
	port := serverPort(t, srv.srv)

	noRealm := oidcLab("pve1.example.com")
	noRealm.Realm = ""
	path := writeAuthConfig(t, noRealm)

	statusRows(t, path)
	require.Empty(t, reloadLab(t, path).Realm, "auth status must not store a default realm")

	_, _, err := authRun(t, path, oidcLoginArgs(tunnelArgs("pve1.example.com", port, socks)...)...)
	require.EqualError(t, err, `OIDC login requires --realm or a realm configured in context "lab"`)

	authURL, _ := srv.redirects()
	require.Empty(t, authURL, "the refusal must come before any request")

	path = writeAuthConfig(t, oidcLab("pve1.example.com"))
	_, stderr, err := authRun(t, path, oidcLoginArgs(tunnelArgs("pve1.example.com", port, socks)...)...)
	require.NoError(t, err, stderr)

	authURL, _ = srv.redirects()
	require.Len(t, authURL, 1)
}

// ---------------------------------------------------------------------------
// contextOptions on the shared builder
// ---------------------------------------------------------------------------

// cmdWithOverrides returns a bare command whose Deps resolve to ov.
func cmdWithOverrides(path string, ov cli.ConnectionOverrides, stderr *bytes.Buffer) *cobra.Command {
	cmd := testCmdWithConfigPath(path, "", stderr)
	cmd.SetContext(cli.WithDeps(context.Background(), &cli.Deps{
		Conn: func() (cli.ConnectionOverrides, error) { return ov, nil },
	}))

	return cmd
}

// TestAuthContextOptions_CarriesCACert covers the second half of the bug:
// the auth path never wired tls.ca-cert.
func TestAuthContextOptions_CarriesCACert(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(false, false)
	ctx.TLS.CACert = "/etc/pmx/lab-ca.pem"

	opts, conn, err := contextOptions(cmd, ctx, false, "prod", "admin@pam", "pam", "", "secretpw", "", "")
	require.NoError(t, err)

	require.NotNil(t, opts.SSLOptions)
	require.Equal(t, "/etc/pmx/lab-ca.pem", opts.SSLOptions.CACert)
	require.Equal(t, pve.SSLVerifyPeer, opts.SSLOptions.VerifyMode)
	require.True(t, opts.SSLOptions.VerifyHostname)
	require.Equal(t, "/etc/pmx/lab-ca.pem", conn.CACert)
}

// TestAuthContextOptions_AppliesDefaults covers a context that omits port
// and protocol: the options carry the product defaults, and the stored
// context stays as written.
func TestAuthContextOptions_AppliesDefaults(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)

	for _, tc := range []struct {
		product string
		port    int
	}{{config.ProductPVE, 8006}, {config.ProductPBS, 8007}, {config.ProductPDM, 8443}} {
		stored := &config.Context{Host: "node.example.com", Product: tc.product}

		opts, conn, err := contextOptions(cmd, stored, false, "prod", "u", "", "", "pw", "", "")
		require.NoError(t, err)
		require.Equal(t, tc.port, opts.Port, tc.product)
		require.Equal(t, "https", opts.Protocol, tc.product)
		require.Equal(t, tc.port, conn.Port, tc.product)
		require.Zero(t, stored.Port, "contextOptions must not write the default port")
		require.Empty(t, stored.Protocol, "contextOptions must not write the default protocol")
	}
}

// TestAuthContextOptions_OverridesReachTransport covers --api-jump and
// --api-proxy on the auth path: both reach the options the client is built
// from.
func TestAuthContextOptions_OverridesReachTransport(t *testing.T) {
	var stderr bytes.Buffer

	cmd := cmdWithOverrides("/home/user/.config/pmx/config.yml",
		cli.ConnectionOverrides{Jump: "admin@bastion", JumpSource: "--api-jump"}, &stderr)
	opts, conn, err := contextOptions(cmd, sampleAuthContext(false, false), false,
		"prod", "admin@pam", "pam", "", "secretpw", "", "")
	require.NoError(t, err)
	require.NotNil(t, opts.DialContext, "--api-jump must install the jump dialer")
	require.Equal(t, "admin@bastion", conn.Jump.Chain)
	require.Equal(t, "--api-jump", conn.JumpSource)

	cmd = cmdWithOverrides("/home/user/.config/pmx/config.yml",
		cli.ConnectionOverrides{Proxy: "socks5h://proxy.example.com:1080", ProxySource: "--api-proxy"}, &stderr)
	opts, conn, err = contextOptions(cmd, sampleAuthContext(false, false), false,
		"prod", "admin@pam", "pam", "", "secretpw", "", "")
	require.NoError(t, err)
	require.NotNil(t, opts.Proxy, "--api-proxy must install the proxy function")
	require.Equal(t, "--api-proxy", conn.ProxySource)

	req, err := http.NewRequest(http.MethodGet, "https://pve.example.com:8006/api2/json/version", nil)
	require.NoError(t, err)
	proxyURL, err := opts.Proxy(req)
	require.NoError(t, err)
	require.Equal(t, "socks5h://proxy.example.com:1080", proxyURL.String())
}

// TestAuthContextOptions_EndpointOverride covers --api-endpoint on the auth
// path: the options and the connection carry the overridden host, marked
// with its source.
func TestAuthContextOptions_EndpointOverride(t *testing.T) {
	var stderr bytes.Buffer
	cmd := cmdWithOverrides("/home/user/.config/pmx/config.yml",
		cli.ConnectionOverrides{Host: "pve9", Port: 9999, EndpointSource: "--api-endpoint"}, &stderr)

	opts, conn, err := contextOptions(cmd, sampleAuthContext(false, false), false,
		"prod", "admin@pam", "pam", "", "secretpw", "", "")
	require.NoError(t, err)
	require.Equal(t, "pve9", opts.Host)
	require.Equal(t, 9999, opts.Port)
	require.Equal(t, "--api-endpoint", conn.EndpointSource)
}

// TestAuthContextOptions_RunsBlockChecks covers the two block checks on the
// stored context: their messages come back joined under the context prefix,
// before anything is resolved.
func TestAuthContextOptions_RunsBlockChecks(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	ctx := sampleAuthContext(false, false)
	ctx.Proxy.Username = "pmx"
	ctx.Timeout.Connect = "0s"

	_, _, err := contextOptions(cmd, ctx, false, "prod", "admin@pam", "pam", "", "secretpw", "", "")
	require.EqualError(t, err,
		`context "prod": proxy.username is set but proxy.url is empty; timeout.connect must be greater than zero`)
}

// TestAuthContextOptions_MalformedOverrideFails covers an override that
// does not parse: the auth path fails with the resolver's error rather
// than ignoring it.
func TestAuthContextOptions_MalformedOverrideFails(t *testing.T) {
	var stderr bytes.Buffer
	cmd := testCmdWithConfigPath("/home/user/.config/pmx/config.yml", "", &stderr)
	cmd.SetContext(cli.WithDeps(context.Background(), &cli.Deps{
		Conn: func() (cli.ConnectionOverrides, error) {
			return cli.ConnectionOverrides{}, fmt.Errorf("invalid --api-endpoint %q: bad", "x:y:z")
		},
	}))

	_, _, err := contextOptions(cmd, sampleAuthContext(false, false), false,
		"prod", "admin@pam", "pam", "", "secretpw", "", "")
	require.EqualError(t, err, `invalid --api-endpoint "x:y:z": bad`)
}

// writeCACert writes a real PEM certificate, the TLS stand-in's own, so a
// client build that loads its CA bundle succeeds.
func writeCACert(t *testing.T) string {
	t.Helper()

	srv := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	path := filepath.Join(t.TempDir(), "ca.pem")
	block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	require.NoError(t, os.WriteFile(path, block, 0o600))

	return path
}

// optionsShape is the comparable form of pve.Options: every data field as
// it is, and each function field reduced to whether it is set.
type optionsShape struct {
	data                                                    pve.Options
	dial, proxy, manualVerify, registerFP, verifyFP, hasTLS bool
}

func shapeOf(opts pve.Options) optionsShape {
	s := optionsShape{
		dial:         opts.DialContext != nil,
		proxy:        opts.Proxy != nil,
		manualVerify: opts.ManualVerifyCallback != nil,
		registerFP:   opts.RegisterFingerprintCallback != nil,
		verifyFP:     opts.VerifyFingerprintCallback != nil,
		hasTLS:       opts.SSLOptions != nil,
	}
	opts.DialContext = nil
	opts.Proxy = nil
	opts.ManualVerifyCallback = nil
	opts.RegisterFingerprintCallback = nil
	opts.VerifyFingerprintCallback = nil
	s.data = opts

	return s
}

// TestAuthContextOptions_MatchesRootBuilder compares the auth path with the
// root path for six contexts. The connections must be equal, since
// JumpSpec, ProxySpec, and TimeoutSpec are plain data, and the options must
// agree on every data field, on which function fields are set, and on the
// proxy each one picks for the API URL.
func TestAuthContextOptions_MatchesRootBuilder(t *testing.T) {
	t.Setenv("PMX_TEST_MATCH_PW", "secretpw")
	t.Setenv("PMX_TEST_MATCH_PROXY_PW", "proxypw")

	base := func() *config.Context {
		return &config.Context{
			Host:  "node.example.com",
			Realm: "pam",
			Auth:  config.AuthBlock{Type: "password", Username: "admin@pam", Secret: "${PMX_TEST_MATCH_PW}"},
		}
	}

	caPath := writeCACert(t)

	cases := map[string]func(c *config.Context){
		"bare": func(*config.Context) {},
		"pbs":  func(c *config.Context) { c.Product = config.ProductPBS },
		"ca-cert": func(c *config.Context) {
			c.TLS.CACert = caPath
		},
		"tofu": func(c *config.Context) { c.TLS.Tofu = true },
		"jump": func(c *config.Context) { c.SSH.Jump = "admin@bastion:2222" },
		"proxy": func(c *config.Context) {
			c.Proxy = config.ProxyBlock{
				URL:      "socks5h://proxy.example.com:1080",
				Username: "pmx",
				Password: "${PMX_TEST_MATCH_PROXY_PW}",
			}
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			stored := base()
			mutate(stored)

			path := filepath.Join(t.TempDir(), "config.yml")
			cfg := &config.Config{CurrentContext: "lab", Contexts: map[string]*config.Context{"lab": stored}}
			require.NoError(t, config.SaveForce(path, cfg))

			var stderr bytes.Buffer
			cmd := testCmdWithConfigPath(path, "", &stderr)
			isTTY := func() bool { return false }

			_, _, rootConn, err := cli.BuildContextAnyClientConn(cmd, cfg, path, "lab", cli.ConnectionOverrides{}, isTTY)
			require.NoError(t, err)

			resolved, _, err := config.ResolveContext(cfg, "lab")
			require.NoError(t, err)
			rootOpts, _, err := cli.ContextOptions(cmd, resolved, "lab", path, cli.ConnectionOverrides{},
				cli.Credentials{}, isTTY)
			require.NoError(t, err)

			authOpts, authConn, err := contextOptions(cmd, cfg.Contexts["lab"], false,
				"lab", "admin@pam", "pam", "", "secretpw", "", "")
			require.NoError(t, err)

			require.Equal(t, rootConn, authConn, "the two paths must resolve the same connection")
			require.Equal(t, shapeOf(rootOpts), shapeOf(authOpts), "the two paths must build the same options")

			if rootOpts.Proxy != nil {
				req, err := http.NewRequest(http.MethodGet, "https://node.example.com/api2/json/version", nil)
				require.NoError(t, err)
				rootProxy, rootErr := rootOpts.Proxy(req)
				authProxy, authErr := authOpts.Proxy(req)
				require.Equal(t, rootErr, authErr)
				require.Equal(t, rootProxy, authProxy)
			}

			require.Equal(t, base().Port, cfg.Contexts["lab"].Port, "neither path may write the stored port")
		})
	}
}

// ---------------------------------------------------------------------------
// The auth verbs on the stored context
// ---------------------------------------------------------------------------

// ticketServer is a plain-HTTP PVE stand-in whose ticket endpoint issues a
// new ticket on every call and whose logout endpoint counts its calls. The
// kit may request a ticket of its own before the verb's request, so a test
// compares a stored session with the last ticket issued rather than with a
// fixed count.
type ticketServer struct {
	fake *testhelper.FakePVE

	mu              sync.Mutex
	tickets, logout int
	last            string
}

func newTicketServer(t *testing.T) *ticketServer {
	t.Helper()

	s := &ticketServer{fake: testhelper.NewFakePVE(t)}
	s.fake.HandleFunc("POST /api2/json/access/ticket", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		s.tickets++
		n := s.tickets
		s.last = fmt.Sprintf("PVE:admin@pam:T%d", n)
		ticket := s.last
		s.mu.Unlock()
		testhelper.WriteData(w, map[string]any{
			"username":            "admin@pam",
			"ticket":              ticket,
			"CSRFPreventionToken": fmt.Sprintf("csrf-%d", n),
		})
	})
	s.fake.HandleFunc("DELETE /api2/json/access/ticket", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		s.logout++
		s.mu.Unlock()
		testhelper.WriteData(w, map[string]any{})
	})

	return s
}

// counts returns how many ticket and logout requests arrived.
func (s *ticketServer) counts() (tickets, logout int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.tickets, s.logout
}

// lastTicket returns the most recent ticket the stand-in issued.
func (s *ticketServer) lastTicket() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.last
}

// port returns the port the stand-in listens on.
func (s *ticketServer) port(t *testing.T) int {
	t.Helper()

	return serverPort(t, s.fake.Server)
}

// passwordLab returns a password context for 127.0.0.1:port over plain
// HTTP that stores no realm, so a test can prove none gets written.
func passwordLab(port int) *config.Context {
	return &config.Context{
		Host:     "127.0.0.1",
		Port:     port,
		Protocol: "http",
		Auth: config.AuthBlock{
			Type:     "password",
			Username: "admin@pam",
			Secret:   "${PMX_TEST_LAB_PW}",
		},
	}
}

// TestAuthVerbs_PersistToStoredContext runs every verb that writes the
// config and reloads the file after each, so a verb that wrote to a copy
// instead of the stored context fails here.
func TestAuthVerbs_PersistToStoredContext(t *testing.T) {
	t.Setenv("PMX_TEST_LAB_PW", "secretpw")
	srv := newTicketServer(t)
	path := writeAuthConfig(t, passwordLab(srv.port(t)))

	_, stderr, err := authRun(t, path, "auth", "login")
	require.NoError(t, err, stderr)
	saved := reloadLab(t, path)
	require.NotNil(t, saved.Auth.Session)
	require.Equal(t, srv.lastTicket(), saved.Auth.Session.Ticket)
	require.NotEmpty(t, saved.Auth.Session.CSRF)
	loginTicket := saved.Auth.Session.Ticket
	require.Empty(t, saved.Realm, "login must not store the default realm")
	require.Empty(t, saved.Product, "login must not store the default product")

	_, stderr, err = authRun(t, path, "auth", "refresh")
	require.NoError(t, err, stderr)
	saved = reloadLab(t, path)
	require.NotNil(t, saved.Auth.Session)
	require.Equal(t, srv.lastTicket(), saved.Auth.Session.Ticket)
	require.NotEqual(t, loginTicket, saved.Auth.Session.Ticket, "refresh must store the new ticket")

	_, stderr, err = authRun(t, path, "auth", "logout")
	require.NoError(t, err, stderr)
	saved = reloadLab(t, path)
	require.Nil(t, saved.Auth.Session, "logout must clear the stored session")
	_, logouts := srv.counts()
	require.Equal(t, 1, logouts, "logout must invalidate the ticket on the server")

	_, stderr, err = authRun(t, path, "auth", "set-token", "--token-id", "cli", "--secret", "${PMX_TEST_TOKEN}")
	require.NoError(t, err, stderr)
	saved = reloadLab(t, path)
	require.Equal(t, "token", saved.Auth.Type)
	require.Equal(t, "cli", saved.Auth.TokenID)
	require.Equal(t, "${PMX_TEST_TOKEN}", saved.Auth.Secret)

	_, stderr, err = authRun(t, path, "auth", "set-password", "--username", "bob@pam", "--secret", "${PMX_TEST_BOB}")
	require.NoError(t, err, stderr)
	saved = reloadLab(t, path)
	require.Equal(t, "password", saved.Auth.Type)
	require.Equal(t, "bob@pam", saved.Auth.Username)
	require.Equal(t, "${PMX_TEST_BOB}", saved.Auth.Secret)
	require.Empty(t, saved.Auth.TokenID)

	require.Equal(t, "http", saved.Protocol)
	require.Equal(t, srv.port(t), saved.Port)
	require.Empty(t, saved.Realm)
}

// TestAuthSetPassword_RepairsEmptyAuthType covers the repair verbs: they
// never validate, because they exist to fix a broken auth block, so neither
// an empty auth.type nor a malformed proxy block stops them.
func TestAuthSetPassword_RepairsEmptyAuthType(t *testing.T) {
	path := writeAuthConfig(t, &config.Context{
		Host:  "pve1.example.com",
		Proxy: config.ProxyBlock{URL: "socks5://pmx:s3cr3t/x@proxy:1080"},
	})

	_, stderr, err := authRun(t, path, "auth", "set-password", "--username", "root@pam", "--secret", "${PMX_TEST_PW}")
	require.NoError(t, err, stderr)

	saved := reloadLab(t, path)
	require.Equal(t, "password", saved.Auth.Type)
	require.Equal(t, "root@pam", saved.Auth.Username)

	_, stderr, err = authRun(t, path, "auth", "set-token", "--token-id", "ci", "--secret", "${PMX_TEST_TOKEN}")
	require.NoError(t, err, stderr)
	require.Equal(t, "token", reloadLab(t, path).Auth.Type)
}

// authVerbFactories lists every auth verb's factory.
func authVerbFactories() map[string]func() *cobra.Command {
	return map[string]func() *cobra.Command{
		"login":        newAuthLoginCmd,
		"refresh":      newAuthRefreshCmd,
		"logout":       newAuthLogoutCmd,
		"status":       newAuthStatusCmd,
		"whoami":       newAuthWhoamiCmd,
		"set-token":    newAuthSetTokenCmd,
		"set-password": newAuthSetPasswordCmd,
	}
}

// TestAuthVerbs_ToleratesBareDeps runs every verb against a hand-built
// Deps, which has no config, no renderer, and no override resolver. Each
// must return an error rather than panic. A second pass gives the verbs a
// config, a context name, and a --config path in a temporary directory, but
// still no renderer or resolver. The verbs that need no server must then
// succeed and save the config there. login must fail on the unreachable
// host, refresh must refuse the token context, and whoami must report that
// no client was built.
func TestAuthVerbs_ToleratesBareDeps(t *testing.T) {
	run := func(t *testing.T, name, cfgPath string, deps *cli.Deps, args ...string) (string, error) {
		t.Helper()

		cmd := authVerbFactories()[name]()
		cmd.Flags().String("config", cfgPath, "")

		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetIn(strings.NewReader(""))
		cmd.SetContext(cli.WithDeps(context.Background(), deps))
		cmd.FParseErrWhitelist.UnknownFlags = true
		require.NoError(t, cmd.ParseFlags(args))

		var err error
		require.NotPanics(t, func() { err = cmd.RunE(cmd, nil) }, "auth %s panicked", name)

		return out.String(), err
	}

	for name := range authVerbFactories() {
		t.Run("empty/"+name, func(t *testing.T) {
			_, err := run(t, name, filepath.Join(t.TempDir(), "config.yml"), &cli.Deps{})
			require.Error(t, err)
		})
	}

	t.Run("partial", func(t *testing.T) {
		t.Setenv("PMX_TEST_TOKEN", "tok-secret")

		// The context names a loopback port nothing listens on, so a verb
		// that calls the API fails at once rather than waiting on a name
		// lookup.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		port := ln.Addr().(*net.TCPAddr).Port
		require.NoError(t, ln.Close())

		newCfg := func() *config.Config {
			return &config.Config{Contexts: map[string]*config.Context{"lab": {
				Host: "127.0.0.1", Port: port,
				Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "ci", Secret: "${PMX_TEST_TOKEN}"},
			}}}
		}

		flags := []string{"--token-id", "x", "--secret", "y", "--username", "u@pam", "--password", "pw"}

		for _, tc := range []struct {
			name, failure string
		}{
			{"status", ""},
			{"logout", ""},
			{"set-token", ""},
			{"set-password", ""},
			{"login", "connection refused"},
			{"refresh", "refresh applies only to password contexts"},
			{"whoami", "no API client was built"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				cfgPath := filepath.Join(t.TempDir(), "config.yml")

				out, err := run(t, tc.name, cfgPath, &cli.Deps{Cfg: newCfg(), CtxName: "lab"}, flags...)
				if tc.failure != "" {
					require.ErrorContains(t, err, tc.failure)
					return
				}

				require.NoError(t, err, "auth %s: %s", tc.name, out)

				if tc.name == "status" {
					require.Contains(t, out, fmt.Sprintf("https://127.0.0.1:%d", port))
					return
				}

				saved, err := config.Load(cfgPath)
				require.NoError(t, err, "auth %s must save the config at the --config path", tc.name)
				require.Contains(t, saved.Contexts, "lab")
			})
		}
	})
}

// TestAuthLogin_RefusesEnvEndpointHostChange covers the guard on the verbs
// that store a session: an ambient $PMX_API_ENDPOINT may not point the
// context at another host, while the same host on the command line may.
func TestAuthLogin_RefusesEnvEndpointHostChange(t *testing.T) {
	t.Setenv("PMX_TEST_LAB_PW", "secretpw")
	srv := newTicketServer(t)
	port := srv.port(t)

	lab := passwordLab(port)
	lab.Host = "pve1.example.com"
	path := writeAuthConfig(t, lab)

	t.Setenv("PMX_API_ENDPOINT", "127.0.0.1:"+strconv.Itoa(port))

	for _, verb := range []string{"login", "refresh"} {
		_, _, err := authRun(t, path, "auth", verb)
		require.EqualError(t, err, fmt.Sprintf(
			`$PMX_API_ENDPOINT points context "lab" at 127.0.0.1 instead of pve1.example.com; `+
				`auth %s stores a session, so pass --api-endpoint on the command line to confirm`, verb))
	}

	tickets, _ := srv.counts()
	require.Zero(t, tickets, "a refused verb must not reach the server")
	require.Nil(t, reloadLab(t, path).Auth.Session)

	_, stderr, err := authRun(t, path, "auth", "login", "--api-endpoint", "127.0.0.1:"+strconv.Itoa(port))
	require.NoError(t, err, stderr)
	saved := reloadLab(t, path)
	require.NotNil(t, saved.Auth.Session, "the session must be stored on the named context")
	require.Equal(t, "pve1.example.com", saved.Host, "the override must not be written to the context")

	_, stderr, err = authRun(t, path, "auth", "refresh", "--api-endpoint", "127.0.0.1:"+strconv.Itoa(port))
	require.NoError(t, err, stderr)

	// The same host in brackets is the same host: the guard passes, and the
	// verb goes on to fail on the secret that does not resolve, which proves
	// it got past the guard without dialling anything.
	v6 := passwordLab(port)
	v6.Host = "::1"
	v6.Auth.Secret = "${PMX_TEST_UNSET_PW}"
	path = writeAuthConfig(t, v6)
	t.Setenv("PMX_API_ENDPOINT", "[::1]:1")

	for _, verb := range []string{"login", "refresh"} {
		_, _, err := authRun(t, path, "auth", verb)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "points context", "[::1] and ::1 name the same host")
		require.Contains(t, err.Error(), "resolve password")
	}
}

// TestAuthVerbs_AnnotatedAsUsingConnection inspects each verb exactly as its
// factory returns it: the four verbs that consume the overrides carry the
// annotation next to noClient, and the two repair verbs refuse an --api-*
// flag, which they would otherwise ignore.
func TestAuthVerbs_AnnotatedAsUsingConnection(t *testing.T) {
	for _, name := range []string{"login", "refresh", "logout", "status"} {
		cmd := authVerbFactories()[name]()
		require.Equal(t, "true", cmd.Annotations["noClient"], "auth %s", name)
		require.Equal(t, "true", cmd.Annotations[cli.AnnotationUsesConnection], "auth %s", name)
	}

	for _, name := range []string{"set-token", "set-password"} {
		cmd := authVerbFactories()[name]()
		require.Equal(t, "true", cmd.Annotations["noClient"], "auth %s", name)
		require.Empty(t, cmd.Annotations[cli.AnnotationUsesConnection], "auth %s", name)
	}

	path := writeAuthConfig(t, &config.Context{Host: "pve1.example.com"})

	_, _, err := authRun(t, path, "auth", "set-token", "--api-endpoint", "pve9",
		"--token-id", "ci", "--secret", "${PMX_TEST_TOKEN}")
	require.EqualError(t, err, "--api-endpoint has no effect on pmx auth set-token")

	_, _, err = authRun(t, path, "auth", "set-password", "--api-jump", "bastion",
		"--username", "root@pam", "--secret", "${PMX_TEST_PW}")
	require.EqualError(t, err, "--api-jump has no effect on pmx auth set-password")

	require.Empty(t, reloadLab(t, path).Auth.Type, "a refused verb must not write the config")
}

// TestNoClient_MergesAnnotations pins noClient to merging, so a
// connection annotation set in a command literal survives it.
func TestNoClient_MergesAnnotations(t *testing.T) {
	cmd := noClient(&cobra.Command{Use: "x", Annotations: map[string]string{cli.AnnotationUsesConnection: "true"}})
	require.Equal(t, map[string]string{"noClient": "true", cli.AnnotationUsesConnection: "true"}, cmd.Annotations)

	cmd = noClient(&cobra.Command{Use: "y"})
	require.Equal(t, map[string]string{"noClient": "true"}, cmd.Annotations)
}

// ---------------------------------------------------------------------------
// auth status
// ---------------------------------------------------------------------------

// statusLab returns a token context that omits port and protocol.
func statusLab() *config.Context {
	return &config.Context{
		Host: "pve1.example.com",
		Auth: config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "ci", Secret: "${PMX_TEST_STATUS_TOKEN}"},
	}
}

// storedRows are the rows auth status takes from the stored context alone.
var storedRows = []string{
	"Context", "Host", "Product", "Auth-type", "Username", "Token-ID", "Secret-source", "Resolved", "Session",
}

// TestAuthStatus_ProxySecretRows covers the two proxy rows: an unresolvable
// keychain reference reads as a failed resolve through config.ResolveSecret
// on the stored reference, every other row still prints, and the command
// exits 0.
func TestAuthStatus_ProxySecretRows(t *testing.T) {
	t.Setenv("PMX_TEST_STATUS_TOKEN", "tokensecret")
	lab := statusLab()
	lab.Proxy = config.ProxyBlock{URL: "socks5h://proxy.example.com:1080", Username: "pmx", Password: "keychain:"}
	path := writeAuthConfig(t, lab)

	rows, stdout, _ := statusRows(t, path)

	_, resolveErr := config.ResolveSecret("keychain:")
	require.Error(t, resolveErr)

	require.Equal(t, "keychain: (keychain)", rows["Proxy-secret-source"])
	require.Equal(t, "no ("+resolveErr.Error()+")", rows["Proxy-resolved"])
	for _, key := range storedRows {
		require.Contains(t, rows, key)
	}
	require.Equal(t, "yes", rows["Resolved"])
	require.Equal(t, "proxy socks5h://proxy.example.com:1080", rows["Via"])
	require.NotContains(t, rows, "Connection")
	require.NotContains(t, stdout, "tokensecret")

	// Without a proxy password the two rows are absent.
	rows, _, _ = statusRows(t, writeAuthConfig(t, statusLab()))
	require.NotContains(t, rows, "Proxy-secret-source")
	require.NotContains(t, rows, "Proxy-resolved")
}

// TestAuthStatus_ClassifiesDollarLiteral covers the classification: a
// value that is not a reference is a literal and is never printed, and a
// $NAME reference whose variable is unset resolves as a literal, so its text
// is not printed either.
func TestAuthStatus_ClassifiesDollarLiteral(t *testing.T) {
	lab := statusLab()
	lab.Auth.Secret = "$uper$ecret"
	lab.Proxy = config.ProxyBlock{URL: "socks5h://proxy.example.com:1080", Username: "pmx", Password: "$uper$ecret"}
	path := writeAuthConfig(t, lab)

	rows, stdout, stderr := statusRows(t, path)
	require.Equal(t, "(inline literal)", rows["Secret-source"])
	require.Equal(t, "(inline literal)", rows["Proxy-secret-source"])
	require.NotContains(t, stdout, "uper$ecret")
	require.NotContains(t, stderr, "uper$ecret")

	t.Setenv("Hunter2", "")
	require.NoError(t, os.Unsetenv("Hunter2"))
	lab = statusLab()
	lab.Auth.Secret = "$Hunter2"
	lab.Proxy = config.ProxyBlock{URL: "socks5h://proxy.example.com:1080", Username: "pmx", Password: "$Hunter2"}

	rows, stdout, _ = statusRows(t, writeAuthConfig(t, lab))
	require.Equal(t, "(unset reference, resolves as a literal)", rows["Secret-source"])
	require.Equal(t, "(unset reference, resolves as a literal)", rows["Proxy-secret-source"])
	require.NotContains(t, stdout, "Hunter2")
}

// TestSecretSource classifies every shape config.ResolveSecret accepts.
func TestSecretSource(t *testing.T) {
	t.Setenv("PMX_TEST_SET_REF", "value")
	t.Setenv("PMX_TEST_UNSET_REF", "")
	require.NoError(t, os.Unsetenv("PMX_TEST_UNSET_REF"))

	cases := map[string]string{
		"":                       "(none)",
		"${PMX_TEST_SET_REF}":    "${PMX_TEST_SET_REF} (env)",
		"${PMX_TEST_UNSET_REF}":  "${PMX_TEST_UNSET_REF} (env)",
		"$PMX_TEST_SET_REF":      "$PMX_TEST_SET_REF (env)",
		"$PMX_TEST_UNSET_REF":    "(unset reference, resolves as a literal)",
		"keychain:pve/lab":       "keychain:pve/lab (keychain)",
		"$uper$ecret":            "(inline literal)",
		"$1abc":                  "(inline literal)",
		"plain-password":         "(inline literal)",
		"keychain-lookalike:x/y": "(inline literal)",
	}

	for secret, want := range cases {
		require.Equal(t, want, secretSource(secret), "secretSource(%q)", secret)
	}
}

// TestAuthStatus_ConnectionErrorRow covers a connection the resolver
// rejects: every stored row still prints, the resolver's error lands in the
// Connection row, and the command exits 0.
func TestAuthStatus_ConnectionErrorRow(t *testing.T) {
	lab := statusLab()
	lab.SSH.Jump = "x;id,h"
	path := writeAuthConfig(t, lab)

	_, wantErr := cli.ResolveConnection("lab", lab, cli.ConnectionOverrides{})
	require.Error(t, wantErr)

	rows, _, _ := statusRows(t, path)
	for _, key := range storedRows {
		require.Contains(t, rows, key)
	}
	require.Equal(t, "https://pve1.example.com:8006", rows["Host"])
	require.Equal(t, wantErr.Error(), rows["Connection"])
	require.NotContains(t, rows, "Via")

	lab = statusLab()
	lab.Timeout.Connect = "soon"
	rows, _, _ = statusRows(t, writeAuthConfig(t, lab))
	require.Equal(t, `context "lab": timeout.connect "soon" is not a duration (e.g. 5s, 500ms)`, rows["Connection"])
}

// TestAuthStatus_MarksOverrideSource covers the source marks, the Via row
// appearing only off the direct route, and the notes on standard error.
func TestAuthStatus_MarksOverrideSource(t *testing.T) {
	path := writeAuthConfig(t, statusLab())

	rows, _, stderr := statusRows(t, path)
	require.Equal(t, "https://pve1.example.com:8006", rows["Host"])
	require.NotContains(t, rows, "Via", "a direct route has no Via row")
	require.NotContains(t, stderr, "note:")

	t.Setenv("PMX_API_ENDPOINT", "pve9:9999")
	rows, _, stderr = statusRows(t, path, "--api-jump", "bastion")
	require.Equal(t, "https://pve9:9999 (from $PMX_API_ENDPOINT)", rows["Host"])
	require.Equal(t, "jump bastion (from --api-jump)", rows["Via"])
	require.Contains(t, stderr, `note: $PMX_API_ENDPOINT (pve9:9999) overrides the endpoint of context "lab"`)

	t.Setenv("PMX_API_ENDPOINT", "")
	lab := statusLab()
	lab.SSH.Jump = "bastion"
	rows, _, _ = statusRows(t, writeAuthConfig(t, lab))
	require.Equal(t, "jump bastion", rows["Via"], "a stored jump carries no source mark")

	rows, _, _ = statusRows(t, writeAuthConfig(t, lab), "--api-jump", "none")
	require.NotContains(t, rows, "Via", "--api-jump none restores the direct route")
}

// TestAuthVerbs_MalformedStoredProxyNeverLeaks runs the three proxy URLs
// that do not parse through the verbs that touch the connection. login and
// logout fail with the block checks' joined messages, auth status reports
// them in its Connection row and exits 0, and the password appears nowhere.
func TestAuthVerbs_MalformedStoredProxyNeverLeaks(t *testing.T) {
	t.Setenv("PMX_TEST_LAB_PW", "loginpw")

	cases := []struct{ url, password string }{
		{"socks5://pmx:s3cr3t/x@proxy:1080", "s3cr3t"},
		{"socks5://pmx:s3%zzt@proxy:1080", "s3%zzt"},
		{"socks5://u:s3cret@[::1", "s3cret"},
	}

	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			lab := passwordLab(8006)
			lab.Host = "pve1.example.com"
			lab.Proxy.URL = tc.url
			lab.Auth.Session = &config.Session{Ticket: "PVE:admin@pam:OLD", CSRF: "old"}
			path := writeAuthConfig(t, lab)

			msgs := config.ValidateProxyBlock(&config.ProxyBlock{URL: tc.url})
			require.NotEmpty(t, msgs)
			want := `context "lab": ` + strings.Join(msgs, "; ")

			for _, verb := range []string{"login", "logout"} {
				stdout, stderr, err := authRun(t, path, "auth", verb)
				require.EqualError(t, err, want, "auth %s", verb)
				for _, text := range []string{stdout, stderr, err.Error()} {
					require.NotContains(t, text, tc.password, "auth %s leaked the proxy password", verb)
				}
			}

			require.NotNil(t, reloadLab(t, path).Auth.Session, "a failed logout must leave the session in place")

			rows, stdout, stderr := statusRows(t, path)
			require.Equal(t, want, rows["Connection"])
			require.NotContains(t, stdout, tc.password)
			require.NotContains(t, stderr, tc.password)
		})
	}
}

// TestAuthLogin_ErrorNamesResolvedHost covers the error wrappers: under an
// endpoint override they name the host that was dialled, and a pin failure
// there is explained in terms of the override.
func TestAuthLogin_ErrorNamesResolvedHost(t *testing.T) {
	t.Setenv("PMX_TEST_LAB_PW", "secretpw")

	fake := testhelper.NewFakePVE(t)
	fake.HandleFunc("POST /api2/json/access/ticket", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusUnauthorized, "authentication failure")
	})
	fake.HandleFunc("DELETE /api2/json/access/ticket", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "logout broke")
	})
	endpoint := "127.0.0.1:" + strconv.Itoa(serverPort(t, fake.Server))

	lab := passwordLab(8006)
	lab.Host = "pve1.example.com"
	lab.Auth.Session = &config.Session{Ticket: "PVE:admin@pam:OLD", CSRF: "old"}
	path := writeAuthConfig(t, lab)

	_, _, err := authRun(t, path, "auth", "login", "--api-endpoint", endpoint)
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), "login to 127.0.0.1: "), err.Error())

	_, _, err = authRun(t, path, "auth", "logout", "--api-endpoint", endpoint)
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), "logout from 127.0.0.1: "), err.Error())

	// A pinned context reached under an endpoint override: the certificate
	// there does not match, and the error says why in terms of the override.
	tlsSrv := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(tlsSrv.Close)
	tlsEndpoint := "127.0.0.1:" + strconv.Itoa(serverPort(t, tlsSrv))

	pinned := passwordLab(8006)
	pinned.Host = "pve1.example.com"
	pinned.Protocol = ""
	pinned.TLS.Fingerprint = strings.TrimSuffix(strings.Repeat("AB:", 32), ":")
	path = writeAuthConfig(t, pinned)

	wantPin := fmt.Sprintf(`context "lab" pins a certificate that %s (from --api-endpoint) does not present; `+
		`pass --api-fingerprint for that host`, tlsEndpoint)

	_, _, err = authRun(t, path, "auth", "login", "--api-endpoint", tlsEndpoint)
	require.EqualError(t, err, wantPin)

	pinned.Auth.Session = &config.Session{Ticket: "PVE:admin@pam:OLD", CSRF: "old"}
	path = writeAuthConfig(t, pinned)
	_, _, err = authRun(t, path, "auth", "logout", "--api-endpoint", tlsEndpoint)
	require.EqualError(t, err, wantPin)
}

// TestAuthVerbs_WarnInsecureOnce covers the warning: the shared builder
// prints it, so an auth verb against an insecure context prints it exactly
// once.
func TestAuthVerbs_WarnInsecureOnce(t *testing.T) {
	t.Setenv("PMX_TEST_LAB_PW", "secretpw")
	srv := newTicketServer(t)
	lab := passwordLab(srv.port(t))
	lab.TLS.Insecure = true
	path := writeAuthConfig(t, lab)

	for _, verb := range []string{"login", "refresh", "logout"} {
		_, stderr, err := authRun(t, path, "auth", verb)
		require.NoError(t, err, stderr)
		require.Equal(t, 1, strings.Count(stderr, insecureWarning), "auth %s: %s", verb, stderr)
	}
}

// TestAuthHelp_DescribesConnection covers the help text: login explains the
// redirect under a tunnel, and status names its new rows.
func TestAuthHelp_DescribesConnection(t *testing.T) {
	path := writeAuthConfig(t, statusLab())

	stdout, _, err := authRun(t, path, "auth", "login", "--help")
	require.NoError(t, err)
	require.Contains(t, stdout, "defaults to the context's stored endpoint")
	require.Contains(t, stdout, "a tunnelled login needs no extra flag")

	stdout, _, err = authRun(t, path, "auth", "status", "--help")
	require.NoError(t, err)
	for _, row := range []string{"Via", "Proxy-secret-source", "Proxy-resolved", "Connection"} {
		require.Contains(t, stdout, row)
	}
}

// TestEndpointURL_Formats covers the renderer the Host row and the redirect
// share.
func TestEndpointURL_Formats(t *testing.T) {
	require.Equal(t, "https://pve9:9999", endpointURL("https", "pve9", 9999))
	require.Equal(t, "https://[2001:db8::1]:8006", endpointURL("https", "[2001:db8::1]", 8006))
	require.Equal(t, "https://[2001:db8::1]:8006", endpointURL("https", "2001:db8::1", 8006))
	require.Empty(t, endpointURL("https", "", 8006))

	u, err := url.Parse(endpointURL("https", "[::1]", 8006))
	require.NoError(t, err)
	require.Equal(t, "::1", u.Hostname())
}
