package apiclient_test

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// newProxiedClient builds an *http.Client routed through spec by
// apiclient.ApplyProxyTransport, the way pmx's own transports are,
// disabling keep-alives so a test's connections close as soon as the
// response has been read, which is what lets the stand-ins' relay
// goroutines return promptly during t.Cleanup.
func newProxiedClient(t *testing.T, spec apiclient.ProxySpec) *http.Client {
	t.Helper()

	tr := &http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only origin cert
	}
	require.NoError(t, apiclient.ApplyProxyTransport(tr, spec, 5*time.Second))

	return &http.Client{Transport: tr}
}

// unresolvableOriginURL builds a request URL pointing at host, whose name is
// never registered in DNS, on the real backend's port. A SOCKS5 dial never
// resolves it locally (net/http hands the hostname straight to the proxy),
// so a request that still reaches the origin proves the name travelled
// unresolved all the way to testhelper.SOCKS5StandIn.
func unresolvableOriginURL(t *testing.T, origin *httptest.Server, host string) string {
	t.Helper()

	originURL, err := url.Parse(origin.URL)
	require.NoError(t, err)

	_, port, err := net.SplitHostPort(originURL.Host)
	require.NoError(t, err)

	return "http://" + net.JoinHostPort(host, port) + "/"
}

// TestProxyOptions_SOCKS5ReachesServer covers both accepted SOCKS5 schemes,
// since ProxyFunc's scheme allow-list treats socks5 and socks5h as two
// distinct entries and a passing socks5 case says nothing about socks5h.
func TestProxyOptions_SOCKS5ReachesServer(t *testing.T) {
	t.Parallel()

	for _, scheme := range []string{"socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			t.Parallel()

			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("origin reached"))
			}))
			t.Cleanup(origin.Close)

			proxy := testhelper.SOCKS5StandIn(t)

			proxyURL, err := url.Parse(scheme + "://" + proxy.Addr)
			require.NoError(t, err)

			client := newProxiedClient(t, apiclient.ProxySpec{URL: proxyURL})

			reqURL := unresolvableOriginURL(t, origin, "pmx-proxy-test.invalid")

			resp, err := client.Get(reqURL)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, "origin reached", string(body))

			conns := proxy.Connections()
			require.Len(t, conns, 1)

			host, _, err := net.SplitHostPort(conns[0].Target)
			require.NoError(t, err)
			require.Equal(t, "pmx-proxy-test.invalid", host, "the proxy must see the unresolved hostname, not an IP")
		})
	}
}

func TestProxyOptions_SOCKS5SendsUsernamePassword(t *testing.T) {
	t.Parallel()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(origin.Close)

	proxy := testhelper.SOCKS5StandIn(t)

	proxyURL, err := url.Parse("socks5://" + proxy.Addr)
	require.NoError(t, err)
	proxyURL.User = url.UserPassword("pmxuser", "pmxpass")

	client := newProxiedClient(t, apiclient.ProxySpec{URL: proxyURL})

	resp, err := client.Get(unresolvableOriginURL(t, origin, "pmx-auth-test.invalid"))
	require.NoError(t, err)
	_ = resp.Body.Close()

	conns := proxy.Connections()
	require.Len(t, conns, 1)
	require.True(t, conns[0].Authenticated)
	require.Equal(t, "pmxuser", conns[0].Username)
	require.Equal(t, "pmxpass", conns[0].Password)
}

// TestProxySpec_SpecialCharacterPasswords covers every character that is
// unsafe to concatenate into a URL by hand: url.UserPassword escapes each
// of them in the wire form, but the SOCKS5 subnegotiation carries the raw
// bytes, so the stand-in must see the password exactly as written, and no
// formatting of the ProxySpec may ever print it.
func TestProxySpec_SpecialCharacterPasswords(t *testing.T) {
	t.Parallel()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(origin.Close)

	passwords := []string{
		"has@sign",
		"has/slash",
		"has:colon",
		"has#hash",
		"has%percent",
	}

	for _, password := range passwords {
		t.Run(password, func(t *testing.T) {
			t.Parallel()

			proxy := testhelper.SOCKS5StandIn(t)

			proxyURL, err := url.Parse("socks5://" + proxy.Addr)
			require.NoError(t, err)
			proxyURL.User = url.UserPassword("pmxuser", password)

			spec := apiclient.ProxySpec{URL: proxyURL, PasswordRef: "unused-in-this-test"}

			client := newProxiedClient(t, spec)

			resp, err := client.Get(unresolvableOriginURL(t, origin, "pmx-special-test.invalid"))
			require.NoError(t, err)
			_ = resp.Body.Close()

			conns := proxy.Connections()
			require.Len(t, conns, 1)
			require.Equal(t, password, conns[0].Password, "the exact password must reach the proxy")

			printed := fmt.Sprintf("%v", spec)
			require.Equal(t, "socks5://pmxuser:<redacted>@"+proxy.Addr, printed,
				"String must redact the password rather than let it through in any form")
		})
	}
}

// TestProxySpec_StringOmitsPasswordRef pins that a stored literal in
// PasswordRef never reaches output, whether printed with %v, %#v, or logged
// through log/slog, regardless of whether URL is also set.
func TestProxySpec_StringOmitsPasswordRef(t *testing.T) {
	t.Parallel()

	const secret = "hunter2"

	proxyURL, err := url.Parse("socks5://proxy.example:1080")
	require.NoError(t, err)

	specs := map[string]apiclient.ProxySpec{
		"direct spec":      {PasswordRef: secret},
		"url-bearing spec": {URL: proxyURL, Username: "pmx", PasswordRef: secret},
	}

	for name, spec := range specs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.NotContains(t, fmt.Sprintf("%v", spec), secret)
			require.NotContains(t, fmt.Sprintf("%#v", spec), secret,
				"GoString must not fall back to Go's default struct dump")

			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, nil))
			logger.Info("proxy configured", "proxy", spec)

			require.NotContains(t, buf.String(), secret)
		})
	}
}

func TestProxyOptions_HTTPProxyReceivesConnect(t *testing.T) {
	t.Parallel()

	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("origin reached"))
	}))
	t.Cleanup(origin.Close)

	proxy := newHTTPConnectStandIn(t)

	proxyURL, err := url.Parse("http://" + proxy.addr)
	require.NoError(t, err)

	spec := apiclient.ProxySpec{URL: proxyURL}

	opts, err := apiclient.ApplyProxyOptions(pve.Options{}, spec, time.Second)
	require.NoError(t, err)
	require.NotNil(t, opts.Proxy)

	got, err := opts.Proxy(httptest.NewRequest(http.MethodGet, origin.URL, nil))
	require.NoError(t, err)
	require.Equal(t, proxyURL.String(), got.String())

	client := newProxiedClient(t, spec)

	resp, err := client.Get(origin.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "origin reached", string(body))

	recs := proxy.records()
	require.Len(t, recs, 1)
	require.Equal(t, http.MethodConnect, recs[0].method)
}

func TestProxyOptions_EnvIgnoredUnlessOptedIn(t *testing.T) {
	t.Parallel()

	off, err := apiclient.ApplyProxyOptions(pve.Options{}, apiclient.ProxySpec{FromEnv: false}, time.Second)
	require.NoError(t, err)
	require.Nil(t, off.Proxy)

	on, err := apiclient.ApplyProxyOptions(pve.Options{}, apiclient.ProxySpec{FromEnv: true}, time.Second)
	require.NoError(t, err)
	require.NotNil(t, on.Proxy)
	require.Equal(t,
		reflect.ValueOf(http.ProxyFromEnvironment).Pointer(),
		reflect.ValueOf(on.Proxy).Pointer(),
		"the environment toggle must set opts.Proxy to http.ProxyFromEnvironment itself, not a look-alike",
	)
}

// TestProxyOptions_DirectLeavesProxyNil pins ApplyProxyOptions's own doc
// comment: a direct spec returns opts unchanged. Seeding opts.Proxy with a
// non-nil function before the call is what tells "unchanged" apart from
// "cleared to nil" — starting from nil cannot distinguish the two.
func TestProxyOptions_DirectLeavesProxyNil(t *testing.T) {
	t.Parallel()

	existing := func(*http.Request) (*url.URL, error) { return nil, nil }

	opts, err := apiclient.ApplyProxyOptions(pve.Options{Host: "pve", Proxy: existing}, apiclient.ProxySpec{},
		time.Second)
	require.NoError(t, err)
	require.NotNil(t, opts.Proxy, "a direct spec must leave an existing opts.Proxy in place")
	require.Equal(t,
		reflect.ValueOf(existing).Pointer(),
		reflect.ValueOf(opts.Proxy).Pointer(),
		"a direct spec must not replace the caller's proxy function",
	)
	require.Equal(t, "pve", opts.Host)
}

func TestProxyFunc_RejectsHTTPSScheme(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("https://proxy.example:8080")
	require.NoError(t, err)

	_, err = apiclient.ProxyFunc(apiclient.ProxySpec{URL: u})
	require.Error(t, err)
	require.Contains(t, err.Error(), "socks5")
}

func TestProxyFunc_RejectsUnknownScheme(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("ftp://proxy.example:21")
	require.NoError(t, err)

	_, err = apiclient.ProxyFunc(apiclient.ProxySpec{URL: u})
	require.Error(t, err)
}

func TestProxyFunc_RejectsMissingHost(t *testing.T) {
	t.Parallel()

	u := &url.URL{Scheme: "socks5"}

	_, err := apiclient.ProxyFunc(apiclient.ProxySpec{URL: u})
	require.Error(t, err)
}

// TestProxyFunc_RejectsMalformedHost covers every shape validateProxyHost
// exists to catch: a host with no hostname, a host whose trailing text
// after a colon is not a valid port, and a port outside 1-65535. None of
// these can arise from a legitimate url.Parse of a socks5 or http proxy
// URL a context could store, but ProxyFunc takes a *url.URL, which a
// resolver can also build by hand, so each is rejected here rather than
// left to fail the first time a request dials through it.
func TestProxyFunc_RejectsMalformedHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		u    *url.URL
	}{
		{"empty hostname with only a port", &url.URL{Scheme: "socks5", Host: ":1080"}},
		{"port is not numeric", &url.URL{Scheme: "socks5", Host: "proxy:notaport"}},
		{"port is zero", &url.URL{Scheme: "socks5", Host: "proxy:0"}},
		{"port is above 65535", &url.URL{Scheme: "socks5", Host: "proxy:99999"}},
		{"opaque url has no host at all", &url.URL{Scheme: "socks5", Opaque: "proxy:1080"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := apiclient.ProxyFunc(apiclient.ProxySpec{URL: tc.u})
			require.Error(t, err)
		})
	}
}

func TestProxySpec_String(t *testing.T) {
	t.Parallel()

	direct := apiclient.ProxySpec{}
	require.Equal(t, "direct", direct.String())

	fromEnv := apiclient.ProxySpec{FromEnv: true}
	require.Equal(t, "from environment", fromEnv.String())

	u, err := url.Parse("socks5://pmx:s3cret@proxy.example:1080")
	require.NoError(t, err)

	withURL := apiclient.ProxySpec{URL: u}
	require.Equal(t, "socks5://pmx:<redacted>@proxy.example:1080", withURL.String())
}

// --- http CONNECT stand-in ------------------------------------------------
//
// The http-proxy path needs only a CONNECT relay, so this stand-in stays
// local to the file rather than joining testhelper.SOCKS5StandIn: net/http
// itself only ever sends a CONNECT and then hands over the raw bytes.

type httpConnectRecord struct {
	method string
	target string
}

type httpConnectStandIn struct {
	addr string

	mu   sync.Mutex
	recs []httpConnectRecord
}

func (p *httpConnectStandIn) records() []httpConnectRecord {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]httpConnectRecord, len(p.recs))
	copy(out, p.recs)

	return out
}

func (p *httpConnectStandIn) record(rec httpConnectRecord) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recs = append(p.recs, rec)
}

func newHTTPConnectStandIn(t *testing.T) *httpConnectStandIn {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	p := &httpConnectStandIn{addr: listener.Addr().String()}

	var wg sync.WaitGroup

	// The accept loop itself is tracked in wg, not just the connections it
	// spawns: that keeps wg's counter above zero for as long as the loop
	// might still call wg.Go, so Cleanup's wg.Wait can never return while a
	// connection accepted just before listener.Close is still being added.
	wg.Go(func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			wg.Go(func() {
				p.serve(t, conn)
			})
		}
	})

	t.Cleanup(func() {
		_ = listener.Close()
		wg.Wait()
	})

	return p
}

// serve reads exactly one HTTP request off conn. A CONNECT is tunnelled to
// the real backend by port alone, matching testhelper.SOCKS5StandIn's own
// "every origin behind this is local" simplification; anything else gets a
// plain 405, since no test in this file sends a plain-http request through
// this stand-in.
func (p *httpConnectStandIn) serve(t *testing.T, conn net.Conn) {
	t.Helper()
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)

	req, err := http.ReadRequest(reader)
	if err != nil {
		return
	}

	p.record(httpConnectRecord{method: req.Method, target: req.Host})

	if req.Method != http.MethodConnect {
		_, _ = conn.Write([]byte("HTTP/1.1 405 Method Not Allowed\r\n\r\n"))

		return
	}

	_, port, err := net.SplitHostPort(req.Host)
	if err != nil {
		_, _ = conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))

		return
	}

	backend, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))

		return
	}
	defer func() { _ = backend.Close() }()

	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	var relayWG sync.WaitGroup

	relayWG.Go(func() {
		// reader may already hold bytes read past the request's blank line
		// (bufio over-reads), so drain it before falling back to the raw
		// connection for the rest of the TLS handshake bytes.
		_, _ = io.Copy(backend, io.MultiReader(reader, conn))
	})

	relayWG.Go(func() {
		_, _ = io.Copy(conn, backend)
	})

	relayWG.Wait()
}
