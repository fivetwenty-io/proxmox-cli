package apiclient

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"

	"github.com/fivetwenty-io/proxmox-cli/internal/redact"
)

// ProxySpec is a resolved outbound proxy. At most one of URL and FromEnv is
// meaningful, and the zero ProxySpec means "connect direct".
type ProxySpec struct {
	// URL is the parsed proxy endpoint, or nil for no explicit proxy. It
	// carries userinfo only when it came from $PMX_API_PROXY; a context's own
	// proxy credentials travel separately in Username and PasswordRef, which
	// a connection resolver joins onto a copy of URL with url.UserPassword
	// when it builds the transport, so a password containing @, /, :, #, or %
	// survives intact and is never concatenated into a string by hand.
	URL *url.URL

	// Username is the context's proxy.username. ProxyFunc ignores it; it
	// lives on ProxySpec so a resolver can carry it alongside URL before the
	// two are joined into the transport's proxy URL.
	Username string

	// PasswordRef is the context's proxy.password exactly as stored, which is
	// a secret reference or a literal. Nothing in this package resolves it,
	// and nothing in this package ever prints it.
	PasswordRef string

	// FromEnv honours HTTPS_PROXY (or HTTP_PROXY for an http:// target) and
	// NO_PROXY, exactly as http.ProxyFromEnvironment reads them. ALL_PROXY is
	// not honoured, because http.ProxyFromEnvironment does not read it.
	FromEnv bool
}

// String returns a form safe to print: redact.ProxyURL(p.URL.String()) when
// URL is set, "from environment" when FromEnv is set instead, or "direct"
// for the zero value. It never reads Username or PasswordRef, so formatting
// a ProxySpec — or any value that embeds one — with %v never risks printing
// a stored credential.
func (p ProxySpec) String() string {
	switch {
	case p.URL != nil:
		return redact.ProxyURL(p.URL.String())
	case p.FromEnv:
		return "from environment"
	default:
		return "direct"
	}
}

// LogValue implements slog.LogValuer with the same redacted form String
// returns, so a log/slog record holding a ProxySpec carries no more than an
// operator printing it with %v would see.
func (p ProxySpec) LogValue() slog.Value {
	return slog.StringValue(p.String())
}

// GoString implements fmt.GoStringer with the same redacted form String
// returns, so a %#v of a ProxySpec — or of a value that embeds one — never
// falls back to Go's default struct dump, which would print PasswordRef in
// the clear.
func (p ProxySpec) GoString() string {
	return "apiclient.ProxySpec(" + strconv.Quote(p.String()) + ")"
}

// proxySchemes are the schemes ProxyFunc accepts. https is deliberately left
// out of this round: an https proxy handshake clones the transport's own
// TLSClientConfig, which carries the Proxmox API's own trust decisions, and
// none of insecure, pinned, or trust-on-first-use verification behaves
// sensibly when reused against the proxy's own certificate.
var proxySchemes = map[string]bool{
	"socks5":  true,
	"socks5h": true,
	"http":    true,
}

// ProxyFunc returns the http.Transport.Proxy function for p, or nil when p
// selects a direct connection. It uses p.URL exactly as given, userinfo
// included, and ignores Username and PasswordRef entirely — a resolved
// ProxySpec that still needs its credentials joined onto URL is not yet
// ready to reach ProxyFunc. A URL whose scheme is not socks5, socks5h, or
// http, that carries no host, or whose port is not a number from 1 to
// 65535, is rejected here rather than left to fail the first time a
// request tries to dial through it. A SOCKS5 URL gets net/http's own
// negotiation this way, bounded only by the request, so pmx's transports
// install a proxy through ApplyProxyOptions or ApplyProxyTransport instead,
// which negotiate SOCKS5 themselves.
func ProxyFunc(p ProxySpec) (func(*http.Request) (*url.URL, error), error) {
	switch {
	case p.URL != nil:
		if !proxySchemes[p.URL.Scheme] {
			return nil, fmt.Errorf("proxy url %s: scheme %q is not supported (use socks5, socks5h, or http)",
				redact.ProxyURL(p.URL.String()), p.URL.Scheme)
		}

		if err := validateProxyHost(p.URL); err != nil {
			return nil, fmt.Errorf("proxy url %s: %w", redact.ProxyURL(p.URL.String()), err)
		}

		return http.ProxyURL(p.URL), nil

	case p.FromEnv:
		return http.ProxyFromEnvironment, nil

	default:
		return nil, nil
	}
}

// validateProxyHost reports an error unless u carries a usable host: a
// non-empty hostname, and — when a port is present — a port that is a
// decimal integer from 1 to 65535. u.Opaque set means url.Parse found no
// "//" and so parsed no host at all.
//
// u.Port() itself returns "" for a Host such as "proxy:notaport", built by
// hand rather than through url.Parse, because the text after the last
// colon is not all decimal digits; that shape is caught separately, since
// otherwise it would read as a bare hostname of "proxy:notaport" with no
// port rather than as the malformed port it is.
func validateProxyHost(u *url.URL) error {
	if u.Opaque != "" || u.Hostname() == "" {
		return fmt.Errorf("missing host")
	}

	host := u.Host
	if !strings.HasPrefix(host, "[") && strings.Contains(host, ":") && u.Hostname() == host {
		return fmt.Errorf("invalid port in host %q", host)
	}

	portStr := u.Port()
	if portStr == "" {
		return nil
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid port %q", portStr)
	}

	return nil
}

// ApplyProxyOptions routes opts through p and returns the updated options.
// It returns opts unchanged, with a nil error, when p selects a direct
// connection, so a caller's own proxy function — set before a ProxySpec is
// resolved, or left over from an earlier call — survives untouched rather
// than being cleared to nil.
//
// An http proxy, and proxy.from-env, land on opts.Proxy for net/http to use.
// A SOCKS5 URL instead clears opts.Proxy and wraps opts.DialContext in
// SOCKSDialContext, bounded by socksBound, so it must run after anything
// else that sets DialContext, such as ApplyJumpSpec: the dial it finds
// there is the one that reaches the proxy.
func ApplyProxyOptions(opts pve.Options, p ProxySpec, socksBound time.Duration) (pve.Options, error) {
	proxy, dial, err := proxyRoute(p, opts.DialContext, socksBound)
	if err != nil {
		return opts, err
	}

	switch {
	case dial != nil:
		opts.Proxy = nil
		opts.DialContext = dial
	case proxy != nil:
		opts.Proxy = proxy
	}

	return opts, nil
}

// ApplyProxyTransport routes tr through p, as ApplyProxyOptions does for a
// client's options, for callers that build a bare http.Transport. It always
// sets tr.Proxy, to nil for a direct or SOCKS5 route, so an ambient proxy
// environment never applies unless p chose it, and a SOCKS5 route wraps the
// tr.DialContext already installed. On failure it leaves tr untouched.
func ApplyProxyTransport(tr *http.Transport, p ProxySpec, socksBound time.Duration) error {
	proxy, dial, err := proxyRoute(p, tr.DialContext, socksBound)
	if err != nil {
		return err
	}

	tr.Proxy = proxy

	if dial != nil {
		tr.DialContext = dial
	}

	return nil
}

// proxyRoute returns the proxy function and the dial function for p. A
// SOCKS5 URL yields only a dial function, which reaches the proxy through
// forward; an http URL or proxy.from-env yields only a proxy function; and a
// direct route yields neither.
func proxyRoute(p ProxySpec, forward DialFunc, socksBound time.Duration) (
	func(*http.Request) (*url.URL, error), DialFunc, error,
) {
	proxy, err := ProxyFunc(p)
	if err != nil || proxy == nil {
		return nil, nil, err
	}

	if IsSOCKSProxy(p.URL) {
		return nil, SOCKSDialContext(p.URL, socksBound, forward), nil
	}

	return proxy, nil, nil
}
