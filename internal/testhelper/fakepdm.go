package testhelper

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	"github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm"
	"github.com/stretchr/testify/require"
)

// FakePDM is a fake Proxmox Datacenter Manager HTTP server for use in tests.
// It wraps an httptest.Server pre-configured with PDM-shaped JSON responses.
// It reuses the same routingHandler, WriteData, and WriteError helpers as
// FakePVE/FakePBS since the wire envelope ({"data": ...} / {"errors": {"msg":
// ...}}) is identical across products; only the port and credential header
// names differ (see pdm.DefaultOptions).
type FakePDM struct {
	// Server is the underlying httptest.Server. Use Server.URL as the base URL.
	Server *httptest.Server

	// Options returns a pve.Options configured to point at the fake server
	// with HTTP (no TLS) and a dummy PDM API token so construction succeeds.
	Options pve.Options

	router *routingHandler
}

// NewFakePDM creates a new FakePDM instance pre-loaded with default
// PDM-shaped responses for common endpoints:
//
//   - GET /api2/json/version
//   - GET /api2/json/ping
//
// The caller may override these or register additional routes via Handle /
// HandleJSON at any time.  The server is closed automatically via t.Cleanup.
func NewFakePDM(t *testing.T) *FakePDM {
	t.Helper()

	router := newRoutingHandler()
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	f := &FakePDM{
		Server: ts,
		router: router,
	}

	// Default route: /api2/json/version
	f.HandleJSON("GET /api2/json/version", map[string]any{
		"release": "1",
		"repoid":  "abc123de",
		"version": "1.1",
	})

	// Default route: /api2/json/ping
	f.HandleJSON("GET /api2/json/ping", map[string]any{
		"pong": true,
	})

	// Build options pointing at the fake HTTP server. The listener address is
	// "host:port"; split it so Host and Port are set independently. Leaving Port
	// unset would let the client default it to 8443 and append a second port,
	// producing an invalid base URL like "http://127.0.0.1:PORT:8443".
	// Use a dummy PDM API token so Options.Validate() passes.
	host, portStr, err := net.SplitHostPort(ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("testhelper: parse listener address: %v", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("testhelper: parse listener port: %v", err)
	}

	f.Options = pdm.DefaultOptions(pve.Options{
		Host:     host,
		Port:     port,
		Protocol: pve.ProtocolHTTP,
		APIToken: "root@pdm!test=00000000-0000-0000-0000-000000000000",
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
func (f *FakePDM) Handle(pattern string, handler http.Handler) {
	f.router.register(pattern, handler)
}

// HandleFunc registers an http.HandlerFunc for the given pattern.
// An existing handler for the same pattern is replaced.
func (f *FakePDM) HandleFunc(pattern string, fn http.HandlerFunc) {
	f.router.register(pattern, fn)
}

// HandleJSON registers a route that always responds with the PDM-shaped envelope:
//
//	{"data": <payload>}
//
// The payload is marshalled to JSON on each request.  An existing handler for
// the same pattern is replaced (unlike http.ServeMux which panics on conflicts).
// Panics if payload cannot be marshalled (test-time only).
func (f *FakePDM) HandleJSON(pattern string, payload any) {
	f.router.register(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteData(w, payload)
	}))
}

// BaseURL returns the base URL of the underlying httptest.Server (e.g. "http://127.0.0.1:PORT").
func (f *FakePDM) BaseURL() string {
	return f.Server.URL
}

// MustNewClient constructs a pve.Client pointing at the fake PDM server via
// pdm.NewClient (which applies the PDM wire-protocol defaults). Fails the
// test immediately if client construction fails.
func (f *FakePDM) MustNewClient(t *testing.T) pve.Client {
	t.Helper()

	c, err := pdm.NewClient(f.Options)
	require.NoError(t, err, "testhelper: construct fake pdm client")

	return c
}
