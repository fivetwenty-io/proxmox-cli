package testhelper

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs"
	"github.com/stretchr/testify/require"
)

// FakePBS is a fake Proxmox Backup Server HTTP server for use in tests.
// It wraps an httptest.Server pre-configured with PBS-shaped JSON responses.
// It reuses the same routingHandler, WriteData, and WriteError helpers as
// FakePVE since the wire envelope ({"data": ...} / {"errors": {"msg": ...}})
// is identical between PVE and PBS; only the port and credential header
// names differ (see pbs.DefaultOptions).
type FakePBS struct {
	// Server is the underlying httptest.Server. Use Server.URL as the base URL.
	Server *httptest.Server

	// Options returns a pve.Options configured to point at the fake server
	// with HTTP (no TLS) and a dummy PBS API token so construction succeeds.
	Options pve.Options

	router *routingHandler
}

// NewFakePBS creates a new FakePBS instance pre-loaded with default
// PBS-shaped responses for common endpoints:
//
//   - GET /api2/json/version
//   - GET /api2/json/ping
//
// The caller may override these or register additional routes via Handle /
// HandleJSON at any time.  The server is closed automatically via t.Cleanup.
func NewFakePBS(t *testing.T) *FakePBS {
	t.Helper()

	router := newRoutingHandler()
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	f := &FakePBS{
		Server: ts,
		router: router,
	}

	// Default route: /api2/json/version
	f.HandleJSON("GET /api2/json/version", map[string]any{
		"release": "3",
		"repoid":  "abc123de",
		"version": "3.4",
	})

	// Default route: /api2/json/ping
	f.HandleJSON("GET /api2/json/ping", map[string]any{
		"pong": true,
	})

	// Build options pointing at the fake HTTP server. The listener address is
	// "host:port"; split it so Host and Port are set independently. Leaving Port
	// unset would let the client default it to 8007 and append a second port,
	// producing an invalid base URL like "http://127.0.0.1:PORT:8007".
	// Use a dummy PBS API token so Options.Validate() passes.
	host, portStr, err := net.SplitHostPort(ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("testhelper: parse listener address: %v", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("testhelper: parse listener port: %v", err)
	}

	f.Options = pbs.DefaultOptions(pve.Options{
		Host:     host,
		Port:     port,
		Protocol: pve.ProtocolHTTP,
		APIToken: "root@pbs!test=00000000-0000-0000-0000-000000000000",
		SSLOptions: &pve.SSLOptions{
			VerifyMode: pve.SSLVerifyNone,
		},
	})

	return f
}

// Handle registers an http.Handler for the given pattern.
// The pattern follows the format "METHOD /path" or "/path".
// If the method prefix is omitted, all HTTP methods are matched.
// An existing handler for the same pattern is replaced.
func (f *FakePBS) Handle(pattern string, handler http.Handler) {
	f.router.register(pattern, handler)
}

// HandleFunc registers an http.HandlerFunc for the given pattern.
// An existing handler for the same pattern is replaced.
func (f *FakePBS) HandleFunc(pattern string, fn http.HandlerFunc) {
	f.router.register(pattern, fn)
}

// HandleJSON registers a route that always responds with the PBS-shaped envelope:
//
//	{"data": <payload>}
//
// The payload is marshalled to JSON on each request.  An existing handler for
// the same pattern is replaced (unlike http.ServeMux which panics on conflicts).
// Panics if payload cannot be marshalled (test-time only).
func (f *FakePBS) HandleJSON(pattern string, payload any) {
	f.router.register(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteData(w, payload)
	}))
}

// BaseURL returns the base URL of the underlying httptest.Server (e.g. "http://127.0.0.1:PORT").
func (f *FakePBS) BaseURL() string {
	return f.Server.URL
}

// MustNewClient constructs a pve.Client pointing at the fake PBS server via
// pbs.NewClient (which applies the PBS wire-protocol defaults). Fails the
// test immediately if client construction fails.
func (f *FakePBS) MustNewClient(t *testing.T) pve.Client {
	t.Helper()

	c, err := pbs.NewClient(f.Options)
	require.NoError(t, err, "testhelper: construct fake pbs client")

	return c
}
