package context

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// wrongFingerprint is a well-formed pin that no test server presents.
const wrongFingerprint = "00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff" +
	":00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff"

// probeTarget converts an httptest server URL into a Context pointing at it,
// with TLS verification disabled (httptest uses a self-signed cert).
func probeTarget(t *testing.T, ts *httptest.Server, product string) *config.Context {
	t.Helper()
	u, err := url.Parse(ts.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	return &config.Context{
		Host:     u.Hostname(),
		Port:     port,
		Protocol: u.Scheme,
		Product:  product,
		TLS:      config.TLSBlock{Insecure: true},
		Auth:     config.AuthBlock{Type: "token", Username: "root@pam", TokenID: "t", Secret: "s"},
	}
}

// resolveProbe resolves ctx with no overrides, failing the test on error.
func resolveProbe(t *testing.T, ctx *config.Context) cli.Connection {
	t.Helper()

	conn, err := cli.ResolveConnection("probe", ctx, cli.ConnectionOverrides{})
	require.NoError(t, err)

	return conn
}

// mustProbe probes conn and fails the test on a setup error.
func mustProbe(t *testing.T, conn cli.Connection) probeResult {
	t.Helper()

	got, err := probeContext(context.Background(), conn)
	require.NoError(t, err)

	return got
}

// serverPort returns the port ts listens on.
func serverPort(t *testing.T, ts *httptest.Server) int {
	t.Helper()

	u, err := url.Parse(ts.URL)
	require.NoError(t, err)

	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	return port
}

func TestProbeContext_Reachable_PVEServerHeader(t *testing.T) {
	// Record the request path in the handler and assert it after the probe —
	// require.* must not run inside the server goroutine (FailNow is only
	// valid on the test goroutine).
	var gotPath string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Server", "pve-api-daemon/3.0")
		_, _ = w.Write([]byte(`{"data":{"version":"8.4.1","release":"8.4"}}`))
	}))
	defer ts.Close()

	got := mustProbe(t, resolveProbe(t, probeTarget(t, ts, config.ProductPVE)))

	require.True(t, got.Reachable)
	require.NoError(t, got.Err)
	require.NoError(t, got.Err)
	require.Equal(t, config.ProductPVE, got.ProductGuess)
	require.Equal(t, "/", gotPath)
	require.Equal(t, "direct", got.Via)
}

// TestProbeContext_ReachableUnderShortBoundDespiteSlow401 models the Proxmox
// daemons, which answer the root page at once and hold every 401 for about
// three seconds. A request bound shorter than that hold must still find the
// host reachable, which fails if the probe moves back onto an API path.
func TestProbeContext_ReachableUnderShortBoundDespiteSlow401(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "pve-api-daemon/3.0")
		if r.URL.Path != "/" {
			select {
			case <-time.After(3 * time.Second):
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusUnauthorized)

			return
		}
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer ts.Close()

	c := probeTarget(t, ts, config.ProductPVE)
	c.Timeout.Request = "1s"

	got := mustProbe(t, resolveProbe(t, c))

	require.True(t, got.Reachable, probeErrText(got))
	require.Equal(t, config.ProductPVE, got.ProductGuess)
}

// TestProbeContext_DoesNotFollowRedirects covers a front proxy that sends
// the root page to a login host. The redirect already proves the configured
// host reachable, and following it would dial a host the context never named.
func TestProbeContext_DoesNotFollowRedirects(t *testing.T) {
	var followed atomic.Bool

	login := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		followed.Store(true)
	}))
	defer login.Close()

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, login.URL+"/login", http.StatusFound)
	}))
	defer ts.Close()

	got := mustProbe(t, resolveProbe(t, probeTarget(t, ts, config.ProductPVE)))

	require.True(t, got.Reachable, probeErrText(got))
	require.False(t, followed.Load(), "the probe must not follow a redirect")
}

func TestProbeContext_Reachable_PBSServerHeader(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "proxmox-backup-proxy/3.3")
		_, _ = w.Write([]byte(`{"data":{"version":"3.3.2"}}`))
	}))
	defer ts.Close()

	got := mustProbe(t, resolveProbe(t, probeTarget(t, ts, config.ProductPVE)))

	require.True(t, got.Reachable)
	require.Equal(t, config.ProductPBS, got.ProductGuess,
		"a PBS server header must be identified regardless of the context's declared product")
}

func TestProbeContext_Reachable_UnknownServer(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	got := mustProbe(t, resolveProbe(t, probeTarget(t, ts, config.ProductPVE)))

	require.True(t, got.Reachable)
	require.Empty(t, got.ProductGuess, "no false product claims without an identifying header")
}

// TestProbeContext_PinnedFingerprintIsReachable covers the shape a real
// Proxmox context has: a self-signed certificate that never chains to a
// system root, trusted by a pinned fingerprint. Probing such a context with
// stock x509 verification reported "unreachable" against a perfectly healthy
// host, and the only lever that made validate pass was setting tls.insecure —
// which then disabled verification for every real API call that context made.
func TestProbeContext_PinnedFingerprintIsReachable(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "pve-api-daemon/3.0")
		_, _ = w.Write([]byte(`{"data":{"version":"8.4.1"}}`))
	}))
	defer ts.Close()

	target := probeTarget(t, ts, config.ProductPVE)
	target.TLS = config.TLSBlock{Fingerprint: serverCertFingerprint(t, ts)}

	got := mustProbe(t, resolveProbe(t, target))

	require.True(t, got.Reachable, "a correctly pinned self-signed host must probe reachable: %s", probeErrText(got))
	require.Equal(t, config.ProductPVE, got.ProductGuess)
}

// TestProbeContext_WrongFingerprintIsRejected is the other half: the pin must
// actually be enforced, not merely accepted as configuration.
func TestProbeContext_WrongFingerprintIsRejected(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	target := probeTarget(t, ts, config.ProductPVE)
	target.TLS = config.TLSBlock{Fingerprint: wrongFingerprint}

	got := mustProbe(t, resolveProbe(t, target))

	require.False(t, got.Reachable, "a mismatched pin must not be reported as reachable")
	require.Contains(t, probeErrText(got), "fingerprint")
}

// TestProbeContext_FingerprintFormatsAreEquivalent pins the normalisation: a
// fingerprint pasted from the PVE UI (colon-separated, upper case) and one
// written by pmx must compare equal.
func TestProbeContext_FingerprintFormatsAreEquivalent(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	colonHex := serverCertFingerprint(t, ts)

	for _, form := range []string{colonHex, strings.ToUpper(colonHex), strings.ReplaceAll(colonHex, ":", "")} {
		conn := resolveProbe(t, probeTarget(t, ts, config.ProductPVE))
		conn.Insecure = false
		conn.Fingerprint = form

		got := mustProbe(t, conn)
		require.True(t, got.Reachable, "fingerprint form %q must be accepted: %s", form, probeErrText(got))
	}
}

// serverCertFingerprint returns ts's leaf certificate SHA-256 in the
// colon-separated hex form Proxmox and pmx both write.
func serverCertFingerprint(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	cert := ts.Certificate()
	require.NotNil(t, cert)

	sum := sha256.Sum256(cert.Raw)
	parts := make([]string, 0, len(sum))
	for _, b := range sum {
		parts = append(parts, hex.EncodeToString([]byte{b}))
	}
	return strings.Join(parts, ":")
}

func TestProbeContext_Unreachable(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	target := probeTarget(t, ts, config.ProductPVE)
	ts.Close() // now the port is closed

	got := mustProbe(t, resolveProbe(t, target))

	require.False(t, got.Reachable)
	require.NotEmpty(t, probeErrText(got))
	require.Error(t, got.Err)
	require.Equal(t, "direct", got.Via, "an unreachable result still names its route")
}

// TestProbeContext_UsesJumpDialer proves the probe dials through the
// context's ssh.jump: the ssh stand-in records exactly one invocation, which
// forwards the connection to the target, and Via names the bastion.
func TestProbeContext_UsesJumpDialer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "pve-api-daemon/3.0")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	t.Cleanup(ts.Close)

	script := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHForward})

	target := probeTarget(t, ts, config.ProductPVE)
	target.SSH.Jump = "admin@bastion.example.com"
	target.Timeout.Request = "900ms"

	conn := resolveProbe(t, target)
	conn.Jump.Program = script.Program

	_, tr, err := newProbeClient(conn)
	require.NoError(t, err)
	require.True(t, tr.DisableKeepAlives, "the probe must never park a jump connection in an idle pool")
	require.Nil(t, tr.Proxy, "a jump context with no proxy must not honour the proxy environment")

	apiclient.ReopenJumps()

	got := mustProbe(t, conn)
	require.True(t, got.Reachable, probeErrText(got))
	require.Equal(t, config.ProductPVE, got.ProductGuess)
	require.Equal(t, "jump admin@bastion.example.com", got.Via)

	invocations := script.Invocations(t)
	require.Len(t, invocations, 1, "the probe must reach the target through exactly one ssh")

	argv := invocations[0]
	w := slices.Index(argv, "-W")
	require.GreaterOrEqual(t, w, 0, argv)
	require.Equal(t, net.JoinHostPort(target.Host, strconv.Itoa(target.Port)), argv[w+1])
}

// TestProbeContext_UsesConfiguredProxy proves the probe honours proxy.url:
// the transport leaves SOCKS5 to pmx's own dial rather than to net/http, the
// SOCKS5 stand-in sees the target by name, and Via names the proxy.
func TestProbeContext_UsesConfiguredProxy(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "pve-api-daemon/3.0")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	t.Cleanup(ts.Close)

	socks := testhelper.SOCKS5StandIn(t)

	const host = "pve-probe.invalid"

	target := probeTarget(t, ts, config.ProductPVE)
	target.Host = host
	target.Proxy.URL = "socks5h://" + socks.Addr
	target.Timeout.Request = "900ms"

	conn := resolveProbe(t, target)

	_, tr, err := newProbeClient(conn)
	require.NoError(t, err)
	require.Nil(t, tr.Proxy, "net/http must not run its own SOCKS negotiation")
	require.NotNil(t, tr.DialContext)

	got := mustProbe(t, conn)
	require.True(t, got.Reachable, probeErrText(got))
	require.Equal(t, "proxy socks5h://"+socks.Addr, got.Via)

	connections := socks.Connections()
	require.Len(t, connections, 1)
	require.Equal(t, net.JoinHostPort(host, strconv.Itoa(target.Port)), connections[0].Target,
		"socks5h must hand the proxy the name, unresolved")
}

// TestProbeContext_ProxyFromEnvToggle proves proxy.from-env decides whether
// the probe's transport honours the proxy environment, by inspecting the
// transport rather than dialling anything.
func TestProbeContext_ProxyFromEnvToggle(t *testing.T) {
	base := func(fromEnv *bool) *config.Context {
		return &config.Context{
			Host: "pve.example.com", Port: 8006, Protocol: "https",
			Proxy: config.ProxyBlock{FromEnv: fromEnv},
		}
	}

	for _, tc := range []struct {
		name    string
		fromEnv *bool
	}{
		{"absent", nil},
		{"false", new(false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, tr, err := newProbeClient(resolveProbe(t, base(tc.fromEnv)))
			require.NoError(t, err)
			require.Nil(t, tr.Proxy, "the probe must ignore the proxy environment unless the context opts in")
		})
	}

	t.Run("true", func(t *testing.T) {
		_, tr, err := newProbeClient(resolveProbe(t, base(new(true))))
		require.NoError(t, err)
		require.NotNil(t, tr.Proxy)

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
			"https://pve.example.com:8006/api2/json/version", nil)
		require.NoError(t, err)

		proxyURL, err := tr.Proxy(req)
		require.NoError(t, err)
		require.NotNil(t, proxyURL)
		require.Equal(t, "env-proxy.test:3128", proxyURL.Host, "the probe must use the pinned $HTTPS_PROXY")
	})
}

// TestProbeContext_HonoursCACert proves a context's tls.ca-cert replaces the
// system roots for the probe, as it does for a real API call.
func TestProbeContext_HonoursCACert(t *testing.T) {
	ts, caPath := newCASignedTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "pve-api-daemon/3.0")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))

	target := probeTarget(t, ts, config.ProductPVE)
	target.TLS = config.TLSBlock{CACert: caPath}

	got := mustProbe(t, resolveProbe(t, target))
	require.True(t, got.Reachable, "a server signed by the context's CA must probe reachable: %s", probeErrText(got))

	target.TLS = config.TLSBlock{}

	got = mustProbe(t, resolveProbe(t, target))
	require.False(t, got.Reachable, "without the bundle the system roots must reject the stand-in CA")
}

// TestProbeContext_CABundleSetupErrors proves a CA bundle the probe cannot
// load is a setup error, returned before anything is dialled.
func TestProbeContext_CABundleSetupErrors(t *testing.T) {
	dir := t.TempDir()

	notPEM := filepath.Join(dir, "not-pem.crt")
	require.NoError(t, os.WriteFile(notPEM, []byte("not a certificate"), 0o600))

	cases := []struct {
		name, path, want string
	}{
		{"relative", "ca.pem", `CA bundle "ca.pem" must be an absolute path`},
		{"missing", filepath.Join(dir, "missing.pem"), "read CA bundle:"},
		{"not PEM", notPEM, "holds no PEM certificate"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := resolveProbe(t, &config.Context{
				Host: "127.0.0.1", Port: 1, Protocol: "https",
				TLS: config.TLSBlock{CACert: tc.path},
			})

			got, err := probeContext(context.Background(), conn)
			require.ErrorContains(t, err, tc.want)
			require.Equal(t, probeResult{}, got)
		})
	}
}

// TestProbeContext_SetupErrorBeforeDeferredClose proves a proxy password that
// does not resolve is returned as a setup error, with no panic from closing
// a transport ApplyToHTTPTransport never returned.
func TestProbeContext_SetupErrorBeforeDeferredClose(t *testing.T) {
	t.Setenv("PMX_TEST_PROBE_UNSET_PASSWORD", "")
	require.NoError(t, os.Unsetenv("PMX_TEST_PROBE_UNSET_PASSWORD"))

	conn := resolveProbe(t, &config.Context{
		Host: "127.0.0.1", Port: 1, Protocol: "https",
		Proxy: config.ProxyBlock{
			URL:      "socks5h://127.0.0.1:1",
			Username: "pmx",
			Password: "${PMX_TEST_PROBE_UNSET_PASSWORD}",
		},
	})

	require.NotPanics(t, func() {
		got, err := probeContext(context.Background(), conn)
		require.ErrorContains(t, err, `resolve proxy.password for context "probe"`)
		require.Equal(t, probeResult{}, got)
	})
}

// TestProbeBound pins the probe's whole-request bound as a pure function of
// the connection.
func TestProbeBound(t *testing.T) {
	cases := []struct {
		name string
		conn cli.Connection
		want time.Duration
	}{
		{
			name: "an explicit request bound wins",
			conn: cli.Connection{
				Timeouts: apiclient.TimeoutSpec{
					Connect: time.Second, TLSHandshake: time.Second, Request: 200 * time.Millisecond,
				},
				TimeoutsSet: cli.TimeoutsSet{Request: true},
			},
			want: 200 * time.Millisecond,
		},
		{
			name: "an explicit request bound wins through a jump too",
			conn: cli.Connection{
				Jump: apiclient.JumpSpec{Chain: "bastion"},
				Timeouts: apiclient.TimeoutSpec{
					Connect: 2 * time.Second, TLSHandshake: 3 * time.Second, Request: 7 * time.Second,
				},
				TimeoutsSet: cli.TimeoutsSet{Request: true},
			},
			want: 7 * time.Second,
		},
		{
			name: "a jump adds the connect and handshake bounds to five seconds",
			conn: cli.Connection{
				Jump: apiclient.JumpSpec{Chain: "bastion"},
				Timeouts: apiclient.TimeoutSpec{
					Connect: 2 * time.Second, TLSHandshake: 3 * time.Second, Request: 30 * time.Second,
				},
			},
			want: 10 * time.Second,
		},
		{
			name: "a hand-built jump connection falls back to the default bounds",
			conn: cli.Connection{Jump: apiclient.JumpSpec{Chain: "bastion"}},
			want: 20 * time.Second,
		},
		{
			name: "a default request bound keeps the probe's five seconds",
			conn: cli.Connection{
				Timeouts: apiclient.TimeoutSpec{
					Connect: 5 * time.Second, TLSHandshake: 10 * time.Second, Request: 30 * time.Second,
				},
			},
			want: 5 * time.Second,
		},
		{
			name: "a zero connection keeps the probe's five seconds",
			conn: cli.Connection{},
			want: 5 * time.Second,
		},
		{
			name: "a request bound marked set but zero is never written",
			conn: cli.Connection{TimeoutsSet: cli.TimeoutsSet{Request: true}},
			want: 5 * time.Second,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, probeBound(tc.conn))
		})
	}
}

// TestProbeContext_KeepsFiveSecondBoundUnlessAsked proves a resolved
// connection keeps the probe's own five-second ceiling rather than the
// thirty-second request default, and takes an explicit timeout.request
// instead, asserted on the client without dialling.
func TestProbeContext_KeepsFiveSecondBoundUnlessAsked(t *testing.T) {
	ctx := &config.Context{Host: "pve.example.com", Port: 8006, Protocol: "https"}

	client, _, err := newProbeClient(resolveProbe(t, ctx))
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, client.Timeout)

	ctx.Timeout.Request = "200ms"

	client, _, err = newProbeClient(resolveProbe(t, ctx))
	require.NoError(t, err)
	require.Equal(t, 200*time.Millisecond, client.Timeout)
}

// TestProbeContext_JumpChildExitsAfterProbe proves a probe through a jump
// leaves no ssh child behind: the stand-in sees end-of-file on its standard
// input, writes its marker, and exits within a second of the probe
// returning, both when the target answered and when the probe timed out.
func TestProbeContext_JumpChildExitsAfterProbe(t *testing.T) {
	cases := []struct {
		name      string
		request   string
		hang      bool
		reachable bool
	}{
		{name: "reachable target", request: "900ms", reachable: true},
		{name: "probe timed out", request: "200ms", hang: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Plain http, so no first-byte timer is armed and the only bound
			// that can end the timed-out probe is the request bound. The
			// server never closes a connection itself, so the only thing that
			// can end the stand-in's forward is the probe closing its side.
			port := keepOpenHTTPServer(t, tc.hang)

			script := testhelper.SSHStandIn(t, testhelper.SSHStandInOptions{Mode: testhelper.SSHForward})

			target := &config.Context{
				Host: "127.0.0.1", Port: port, Protocol: "http", Product: config.ProductPVE,
				SSH:     config.SSHBlock{Jump: "bastion"},
				Timeout: config.TimeoutBlock{Request: tc.request},
			}

			conn := resolveProbe(t, target)
			conn.Jump.Program = script.Program
			require.Zero(t, conn.Jump.FirstByteTimeout, "plain http arms no first-byte timer")

			apiclient.ReopenJumps()

			got := mustProbe(t, conn)
			returned := time.Now()

			require.Equal(t, tc.reachable, got.Reachable, probeErrText(got))
			require.Len(t, script.Invocations(t), 1)

			deadline := returned.Add(time.Second)
			pid := testhelper.WaitPID(t, script.PIDFile, time.Second)

			for {
				if _, err := os.Stat(script.MarkerFile); err == nil {
					break
				}

				require.False(t, time.Now().After(deadline),
					"the ssh child never saw end-of-file within a second of the probe returning")
				time.Sleep(10 * time.Millisecond)
			}

			require.True(t, testhelper.WaitProcessGone(pid, time.Until(deadline)),
				"the ssh child was still running a second after the probe returned")
		})
	}
}

// TestProbeContext_PinsEvenWhenInsecure proves the probe enforces a pinned
// fingerprint on an insecure context, as every real API call does.
func TestProbeContext_PinsEvenWhenInsecure(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)

	target := probeTarget(t, ts, config.ProductPVE)
	target.TLS = config.TLSBlock{Insecure: true, Fingerprint: wrongFingerprint}

	conn := resolveProbe(t, target)
	require.True(t, conn.Insecure)

	got := mustProbe(t, conn)
	require.False(t, got.Reachable, "an insecure context must still fail a pin the server does not match")
	require.Contains(t, probeErrText(got), "tls fingerprint pin: ")
}

// TestProbeContext_PinFailureWrapsSentinel proves the probe's pin failure
// keeps its "tls fingerprint pin: ..." text and carries cli.ErrPinMismatch,
// both from the verifier itself and through a whole probe.
func TestProbeContext_PinFailureWrapsSentinel(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)

	verify := fingerprintVerifier(wrongFingerprint)

	err := verify(tls.ConnectionState{PeerCertificates: []*x509.Certificate{ts.Certificate()}})
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), "tls fingerprint pin: "), err.Error())
	require.ErrorIs(t, err, cli.ErrPinMismatch)

	err = verify(tls.ConnectionState{})
	require.True(t, strings.HasPrefix(err.Error(), "tls fingerprint pin: "), err.Error())
	require.ErrorIs(t, err, cli.ErrPinMismatch)

	require.NoError(t, fingerprintVerifier(serverCertFingerprint(t, ts))(
		tls.ConnectionState{PeerCertificates: []*x509.Certificate{ts.Certificate()}}))

	target := probeTarget(t, ts, config.ProductPVE)
	target.TLS = config.TLSBlock{Fingerprint: wrongFingerprint}

	got := mustProbe(t, resolveProbe(t, target))
	require.False(t, got.Reachable)
	require.ErrorIs(t, got.Err, cli.ErrPinMismatch, "the pin failure must survive the transport's wrapping")
	require.Contains(t, probeErrText(got), "tls fingerprint pin: ")
}

// TestProbeContext_CancelledContextStopsProbe proves the probe runs under
// the command's context, so a cancelled command never waits for its bound.
func TestProbeContext_CancelledContextStopsProbe(t *testing.T) {
	release := make(chan struct{})

	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(ts.Close)
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := probeContext(ctx, resolveProbe(t, probeTarget(t, ts, config.ProductPVE)))
	require.NoError(t, err)
	require.False(t, got.Reachable)
	require.ErrorIs(t, got.Err, context.Canceled)
}

// newCASignedTLSServer starts a TLS server whose certificate is signed by a
// freshly minted certificate authority, and returns it with the path of that
// authority's PEM bundle.
func newCASignedTLSServer(t *testing.T, h http.Handler) (*httptest.Server, string) {
	t.Helper()

	now := time.Now()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "pmx probe test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)

	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		DNSNames:     []string{"localhost"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	require.NoError(t, err)

	ts := httptest.NewUnstartedServer(h)
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}}}
	ts.StartTLS()
	t.Cleanup(ts.Close)

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600))

	return ts, caPath
}

// keepOpenHTTPServer serves one fixed version response on every connection,
// or none when hang is set, and never closes a connection until the client
// does, whatever Connection header the client sent. It returns the loopback
// port it listens on.
func keepOpenHTTPServer(t *testing.T, hang bool) int {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var wg sync.WaitGroup

	wg.Go(func() {
		for {
			conn, acceptErr := l.Accept()
			if acceptErr != nil {
				return
			}

			wg.Go(func() {
				defer func() { _ = conn.Close() }()

				req, readErr := http.ReadRequest(bufio.NewReader(conn))
				if readErr != nil {
					return
				}

				_ = req.Body.Close()

				if !hang {
					const body = `{"data":{}}`
					_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nServer: pve-api-daemon/3.0\r\n"+
						"Content-Type: application/json\r\nContent-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"+body)
				}

				// Hold the connection until the client closes it.
				_, _ = io.Copy(io.Discard, conn)
			})
		}
	})

	t.Cleanup(func() {
		_ = l.Close()
		wg.Wait()
	})

	return l.Addr().(*net.TCPAddr).Port
}

// closedPort returns a loopback port nothing listens on.
func closedPort(t *testing.T) int {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())

	return port
}

// probeErrText returns the text of r's transport error, or "" when the
// probe reached its endpoint, for an assertion message.
func probeErrText(r probeResult) string {
	if r.Err == nil {
		return ""
	}

	return r.Err.Error()
}
