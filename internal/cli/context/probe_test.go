package context

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

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

	got := probeContext(probeTarget(t, ts, config.ProductPVE), 2*time.Second, true)

	require.True(t, got.Reachable)
	require.Empty(t, got.ReachErr)
	require.Equal(t, config.ProductPVE, got.ProductGuess)
	require.Equal(t, "/api2/json/version", gotPath)
}

func TestProbeContext_Reachable_PBSServerHeader(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "proxmox-backup-proxy/3.3")
		_, _ = w.Write([]byte(`{"data":{"version":"3.3.2"}}`))
	}))
	defer ts.Close()

	got := probeContext(probeTarget(t, ts, config.ProductPVE), 2*time.Second, true)

	require.True(t, got.Reachable)
	require.Equal(t, config.ProductPBS, got.ProductGuess,
		"a PBS server header must be identified regardless of the context's declared product")
}

func TestProbeContext_Reachable_UnknownServer(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	got := probeContext(probeTarget(t, ts, config.ProductPVE), 2*time.Second, true)

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

	got := probeContext(target, 2*time.Second, false)

	require.True(t, got.Reachable, "a correctly pinned self-signed host must probe reachable: %s", got.ReachErr)
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
	target.TLS = config.TLSBlock{
		Fingerprint: "00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff" +
			":00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff",
	}

	got := probeContext(target, 2*time.Second, false)

	require.False(t, got.Reachable, "a mismatched pin must not be reported as reachable")
	require.Contains(t, got.ReachErr, "fingerprint")
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
		target := probeTarget(t, ts, config.ProductPVE)
		target.TLS = config.TLSBlock{Fingerprint: form}

		got := probeContext(target, 2*time.Second, false)
		require.True(t, got.Reachable, "fingerprint form %q must be accepted: %s", form, got.ReachErr)
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

	got := probeContext(target, 1*time.Second, true)

	require.False(t, got.Reachable)
	require.NotEmpty(t, got.ReachErr)
}
