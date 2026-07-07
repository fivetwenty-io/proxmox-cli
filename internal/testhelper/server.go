// Package testhelper provides shared test utilities including a fake PVE httptest server.
package testhelper

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
	"github.com/stretchr/testify/require"
)

// routeKey uniquely identifies a route by HTTP method and path.
// Method may be empty to match all methods.
type routeKey struct {
	method string
	path   string
}

// routingHandler is an http.Handler that dispatches to registered per-route
// handlers.  It supports dynamic registration and overriding of routes after
// construction, which http.ServeMux does not allow.
type routingHandler struct {
	mu     sync.RWMutex
	routes map[routeKey]http.Handler
	// orderedKeys preserves registration order for longest-path matching.
	orderedKeys []routeKey
}

func newRoutingHandler() *routingHandler {
	return &routingHandler{
		routes: make(map[routeKey]http.Handler),
	}
}

// register installs or replaces the handler for the given method+path pair.
// The pattern format follows the enhanced Go 1.22 mux syntax accepted by
// http.ServeMux: "METHOD /path" or just "/path".  If the method prefix is
// omitted the key uses an empty method (matches all methods).
func (h *routingHandler) register(pattern string, handler http.Handler) {
	method, path := parsePattern(pattern)
	key := routeKey{method: method, path: path}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.routes[key]; !exists {
		h.orderedKeys = append(h.orderedKeys, key)
	}
	h.routes[key] = handler
}

// ServeHTTP dispatches the incoming request to the best-matching registered
// handler.  Matching priority: exact method+path > wildcard-method+path >
// longest-prefix match (method then wildcard).  Returns 404 if no handler
// is found.
func (h *routingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// 1. Exact method + exact path.
	if handler, ok := h.routes[routeKey{method: r.Method, path: r.URL.Path}]; ok {
		handler.ServeHTTP(w, r)
		return
	}
	// 2. Wildcard method + exact path.
	if handler, ok := h.routes[routeKey{method: "", path: r.URL.Path}]; ok {
		handler.ServeHTTP(w, r)
		return
	}
	// 3. Longest-prefix: method-specific then wildcard.
	var best http.Handler
	bestLen := -1
	for _, key := range h.orderedKeys {
		if key.method != "" && key.method != r.Method {
			continue
		}
		if strings.HasPrefix(r.URL.Path, key.path) && len(key.path) > bestLen {
			best = h.routes[key]
			bestLen = len(key.path)
		}
	}
	if best != nil {
		best.ServeHTTP(w, r)
		return
	}

	http.NotFound(w, r)
}

// parsePattern splits an optional "METHOD /path" string into its parts.
func parsePattern(pattern string) (method, path string) {
	parts := strings.SplitN(pattern, " ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", parts[0]
}

// FakePVE is a fake PVE HTTP server for use in tests.
// It wraps an httptest.Server pre-configured with PVE-shaped JSON responses.
// Callers register routes via Handle or HandleJSON before use.
// Routes may be overridden at any time; the last registration wins.
type FakePVE struct {
	// Server is the underlying httptest.Server. Use Server.URL as the base URL.
	Server *httptest.Server

	// Options returns a pve.Options configured to point at the fake server
	// with HTTP (no TLS) and a dummy API token so construction succeeds.
	Options pve.Options

	router *routingHandler
}

// NewFakePVE creates a new FakePVE instance pre-loaded with default PVE-shaped
// responses for common endpoints:
//
//   - GET /api2/json/version
//   - GET /api2/json/cluster/status
//   - GET /api2/json/nodes
//
// The caller may override these or register additional routes via Handle /
// HandleJSON at any time.  The server is closed automatically via t.Cleanup.
func NewFakePVE(t *testing.T) *FakePVE {
	t.Helper()

	router := newRoutingHandler()
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	f := &FakePVE{
		Server: ts,
		router: router,
	}

	// Default route: /api2/json/version
	f.HandleJSON("GET /api2/json/version", map[string]any{
		"release": "8.1",
		"repoid":  "b46aac3b",
		"version": "8.1.4",
		"console": "xtermjs",
	})

	// Default route: /api2/json/cluster/status
	f.HandleJSON("GET /api2/json/cluster/status", []any{
		map[string]any{
			"type":    "cluster",
			"name":    "testcluster",
			"id":      "testcluster",
			"quorate": 1,
			"nodes":   1,
			"online":  1,
		},
		map[string]any{
			"type":   "node",
			"name":   "pve1",
			"id":     "node/pve1",
			"ip":     "192.168.1.10",
			"online": 1,
			"level":  "c",
			"nodeid": 1,
		},
	})

	// Default route: /api2/json/nodes
	f.HandleJSON("GET /api2/json/nodes", []any{
		map[string]any{
			"node":            "pve1",
			"status":          "online",
			"cpu":             0.01,
			"maxcpu":          4,
			"mem":             1024 * 1024 * 512,
			"maxmem":          1024 * 1024 * 1024 * 8,
			"disk":            0,
			"maxdisk":         1024 * 1024 * 1024 * 100,
			"uptime":          12345,
			"ssl_fingerprint": "AA:BB:CC",
		},
	})

	// Build options pointing at the fake HTTP server. The listener address is
	// "host:port"; split it so Host and Port are set independently. Leaving Port
	// unset would let the client default it to 8006 and append a second port,
	// producing an invalid base URL like "http://127.0.0.1:PORT:8006".
	// Use a dummy API token so Options.Validate() passes.
	host, portStr, err := net.SplitHostPort(ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("testhelper: parse listener address: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("testhelper: parse listener port: %v", err)
	}
	f.Options = pve.Options{
		Host:     host,
		Port:     port,
		Protocol: pve.ProtocolHTTP,
		APIToken: "root@pam!test=00000000-0000-0000-0000-000000000000",
		SSLOptions: &pve.SSLOptions{
			VerifyMode: pve.SSLVerifyNone,
		},
	}

	return f
}

// Handle registers an http.Handler for the given pattern.
// The pattern follows the format "METHOD /path" or "/path".
// If the method prefix is omitted, all HTTP methods are matched.
// An existing handler for the same pattern is replaced.
func (f *FakePVE) Handle(pattern string, handler http.Handler) {
	f.router.register(pattern, handler)
}

// HandleFunc registers an http.HandlerFunc for the given pattern.
// An existing handler for the same pattern is replaced.
func (f *FakePVE) HandleFunc(pattern string, fn http.HandlerFunc) {
	f.router.register(pattern, fn)
}

// HandleJSON registers a route that always responds with the PVE-shaped envelope:
//
//	{"data": <payload>}
//
// The payload is marshalled to JSON on each request.  An existing handler for
// the same pattern is replaced (unlike http.ServeMux which panics on conflicts).
// Panics if payload cannot be marshalled (test-time only).
func (f *FakePVE) HandleJSON(pattern string, payload any) {
	f.router.register(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteData(w, payload)
	}))
}

// BaseURL returns the base URL of the underlying httptest.Server (e.g. "http://127.0.0.1:PORT").
func (f *FakePVE) BaseURL() string {
	return f.Server.URL
}

// WriteData writes a PVE-shaped JSON response envelope {"data": payload} with HTTP 200.
func WriteData(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	env := map[string]any{"data": payload}
	if err := json.NewEncoder(w).Encode(env); err != nil {
		// unreachable in tests; the connection is in-process
		panic("testhelper.WriteData: encode failed: " + err.Error())
	}
}

// WriteError writes a PVE-shaped JSON error response {"errors": {"msg": message}}
// with the given HTTP status code.
func WriteError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	env := map[string]any{
		"errors": map[string]string{"msg": message},
	}
	if err := json.NewEncoder(w).Encode(env); err != nil {
		panic("testhelper.WriteError: encode failed: " + err.Error())
	}
}

// MustNewClient constructs a pve.Client pointing at the fake server.
// Fails the test immediately if client construction fails.
func (f *FakePVE) MustNewClient(t *testing.T) pve.Client {
	t.Helper()
	c, err := pve.NewClient(f.Options)
	require.NoError(t, err, "testhelper: construct fake pve client")
	return c
}
