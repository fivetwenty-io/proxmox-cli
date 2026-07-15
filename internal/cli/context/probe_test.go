package context

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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

	got := probeContext(probeTarget(t, ts, config.ProductPVE), 2*time.Second)

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

	got := probeContext(probeTarget(t, ts, config.ProductPVE), 2*time.Second)

	require.True(t, got.Reachable)
	require.Equal(t, config.ProductPBS, got.ProductGuess,
		"a PBS server header must be identified regardless of the context's declared product")
}

func TestProbeContext_Reachable_UnknownServer(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	got := probeContext(probeTarget(t, ts, config.ProductPVE), 2*time.Second)

	require.True(t, got.Reachable)
	require.Empty(t, got.ProductGuess, "no false product claims without an identifying header")
}

func TestProbeContext_Unreachable(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	target := probeTarget(t, ts, config.ProductPVE)
	ts.Close() // now the port is closed

	got := probeContext(target, 1*time.Second)

	require.False(t, got.Reachable)
	require.NotEmpty(t, got.ReachErr)
}
