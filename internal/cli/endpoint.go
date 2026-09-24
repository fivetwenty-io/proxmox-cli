package cli

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// redactedPlaceholder replaces a userinfo password in an endpoint error
// message. It is a local constant, not internal/redact's Placeholder,
// because this file must not import internal/redact: ParseEndpoint runs
// before a client exists, and redact.ProxyURL would mask an unparseable
// value whole (turning "https://[::1" into "https://<redacted>") even when
// it carries no "@" and so no credential to hide.
const redactedPlaceholder = "<redacted>"

// EndpointOverride is one parsed --api-endpoint value. A zero field means
// the operator omitted that component, so the context's value stands. Host
// holds an IPv6 literal in brackets, such as "[::1]", which is the form
// pve.Options.GetBaseURL needs and the form a context's host: already uses.
type EndpointOverride struct {
	Host     string
	Port     int
	Protocol string
}

// ParseEndpoint parses raw in the form [scheme://]host[:port]. source names
// the value's origin for the error text, and it is either "--api-endpoint"
// or "$PMX_API_ENDPOINT".
//
// The accepted forms are host, host:port, scheme://host, scheme://host:port,
// and the bracketed IPv6 equivalents such as [::1]:8006 and
// https://[::1]:8006. A bare IPv6 literal without brackets, such as ::1, is
// a host with no port: without brackets there is no way to tell an address
// colon from a port colon, so a bare IPv6 literal never carries a port.
// Every IPv6 host leaves the parser bracketed, whichever form it arrived
// in, so [::1], [::1]:8006, https://[::1]:8006, and a bare ::1 all yield
// Host: "[::1]".
//
// The scheme must be https or http, and the port must be numeric and lie
// between 1 and 65535. The parser rejects userinfo, a path other than empty
// or "/", a query, and a fragment. An empty raw parses to a zero
// EndpointOverride with no error, which is what an unset flag or unset
// environment variable produces.
func ParseEndpoint(source, raw string) (EndpointOverride, error) {
	if raw == "" {
		return EndpointOverride{}, nil
	}

	invalid := func(reason string) error {
		return fmt.Errorf("invalid %s %q: %s", source, maskUserinfoPassword(raw), reason)
	}

	rest := raw
	protocol := ""
	if idx := strings.Index(rest, "://"); idx >= 0 {
		protocol = strings.ToLower(rest[:idx])
		rest = rest[idx+len("://"):]
		if protocol != "https" && protocol != "http" {
			return EndpointOverride{}, invalid("scheme must be https or http")
		}
	}

	// Everything up to the first "/", "?", or "#" is the authority (the
	// optional userinfo plus host[:port]); everything from there on is the
	// path, query, or fragment, none of which an endpoint override may
	// carry, save for a bare trailing "/".
	authority := rest
	suffix := ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority = rest[:i]
		suffix = rest[i:]
	}
	if suffix != "" && suffix != "/" {
		return EndpointOverride{}, invalid("must not carry a path, a query, or a fragment")
	}

	if strings.Contains(authority, "@") {
		return EndpointOverride{}, invalid("must not carry credentials")
	}

	// Empty brackets name no host at all. They are caught here because
	// splitAuthority would otherwise reject them as a malformed IPv6
	// literal, which misstates the problem.
	if strings.HasPrefix(authority, "[]") {
		return EndpointOverride{}, invalid("host is empty")
	}

	host, portStr, err := splitAuthority(authority)
	if err != nil {
		return EndpointOverride{}, invalid("want [scheme://]host[:port]")
	}
	if host == "" {
		return EndpointOverride{}, invalid("host is empty")
	}

	port := 0
	if portStr != "" {
		if !isAllDigits(portStr) {
			return EndpointOverride{}, invalid(fmt.Sprintf("port %q is not a number", portStr))
		}
		p, convErr := strconv.Atoi(portStr)
		if convErr != nil {
			return EndpointOverride{}, invalid(fmt.Sprintf("port %q is not a number", portStr))
		}
		if p < 1 || p > 65535 {
			return EndpointOverride{}, invalid(fmt.Sprintf("port %d is out of range [1, 65535]", p))
		}
		port = p
	}

	return EndpointOverride{Host: host, Port: port, Protocol: protocol}, nil
}

// splitAuthority splits authority (the userinfo-free host[:port] portion of
// an endpoint) into its host and port components. It returns an error when
// authority opens a bracketed IPv6 literal that never closes, when the text
// inside brackets or the text of a bare multi-colon host is not a real
// IPv6 address, when a colon is followed by no port at all, and when a
// plain hostname carries whitespace or a control character. Numeric and
// range validation of a non-empty port is left to the caller.
func splitAuthority(authority string) (host, port string, err error) {
	switch {
	case strings.HasPrefix(authority, "["):
		closeIdx := strings.IndexByte(authority, ']')
		if closeIdx < 0 {
			return "", "", fmt.Errorf("unterminated IPv6 literal")
		}
		if err := validateIPv6Literal(authority[1:closeIdx]); err != nil {
			return "", "", err
		}
		host = authority[:closeIdx+1]
		rem := authority[closeIdx+1:]
		if rem == "" {
			return host, "", nil
		}
		if !strings.HasPrefix(rem, ":") {
			return "", "", fmt.Errorf("unexpected text %q after IPv6 literal", rem)
		}
		if rem[1:] == "" {
			return "", "", fmt.Errorf("empty port")
		}
		return host, rem[1:], nil
	case strings.Count(authority, ":") >= 2:
		// A bare IPv6 literal, such as "::1" or "2001:db8::1". Without
		// brackets there is no way to separate the address from a port, so
		// the whole string is the host and it never carries a port.
		if err := validateIPv6Literal(authority); err != nil {
			return "", "", err
		}
		return "[" + authority + "]", "", nil
	case strings.Contains(authority, ":"):
		h, p, _ := strings.Cut(authority, ":")
		if p == "" {
			return "", "", fmt.Errorf("empty port")
		}
		if err := validateHostname(h); err != nil {
			return "", "", err
		}
		return h, p, nil
	default:
		if err := validateHostname(authority); err != nil {
			return "", "", err
		}
		return authority, "", nil
	}
}

// validateIPv6Literal reports an error unless s is exactly an IPv6 address
// with no zone. A zone id, such as the "%eth0" in a link-local address, is
// rejected rather than carried through, because the bracketed form a
// context's host: field and pve.Options.GetBaseURL expect has no defined
// way to spell one that survives a further url.Parse unescaped.
func validateIPv6Literal(s string) error {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return fmt.Errorf("invalid IPv6 literal %q: %w", s, err)
	}
	if !addr.Is6() || addr.Zone() != "" {
		return fmt.Errorf("invalid IPv6 literal %q", s)
	}
	return nil
}

// validateHostname reports an error if h contains whitespace or an ASCII
// control character, either of which means h was never a hostname to begin
// with, whatever shape it otherwise has.
func validateHostname(h string) error {
	for _, r := range h {
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("host %q contains whitespace or a control character", h)
		}
	}
	return nil
}

// isAllDigits reports whether s is a non-empty run of ASCII digits. It
// exists so a port is rejected before it reaches strconv.Atoi, which — like
// strconv.ParseInt — also accepts a leading "+" or "-" sign that a port
// number never carries.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// maskUserinfoPassword returns raw with anything that could be a userinfo
// password replaced by redactedPlaceholder. It works on the raw text rather
// than a parsed URL so a malformed value, one that would fail to parse at
// all, still renders safely in an error message.
//
// Without an "@" there is no userinfo, so raw comes back unchanged. With one,
// the boundaries of a password cannot be recovered from the text: a
// password may itself hold "@", "/", "?", "#", "://", or whitespace, so
// neither the last "@" nor the first delimiter reliably ends it, and the
// text after the last "@" may be the tail of a password that no host
// follows ("root:p@ss"). Masking therefore errs toward hiding too much:
//
//   - A leading "scheme://" is kept only when the text before "://" has the
//     shape of a URL scheme, so a password that holds "://" is never
//     mistaken for a prefix.
//   - Everything from the first ":" after that prefix to the end of raw is
//     masked, which covers the password wherever its "@" falls, and also
//     hides the host, port, and path that follow it.
//   - The username ahead of that ":" stays visible, which keeps a realm such
//     as root@pam readable, unless it holds a "%": a percent-encoded colon
//     there can hide a password inside the username, so it is masked too.
//   - With no ":" at all there is no password in URL terms, but the text
//     before the last "@" is masked anyway, because it may be a secret
//     pasted in the username slot.
//
// A password followed by no "@" at all, as in "root:s3cret", is
// indistinguishable from a host and port and is left as written.
func maskUserinfoPassword(raw string) string {
	prefix, rest := "", raw
	if scheme, after, ok := strings.Cut(raw, "://"); ok && isURLScheme(scheme) {
		prefix, rest = scheme+"://", after
	}

	at := strings.LastIndexByte(rest, '@')
	if at < 0 {
		return raw
	}

	colon := strings.IndexByte(rest, ':')
	if colon < 0 {
		return prefix + redactedPlaceholder + "@" + rest[at+1:]
	}

	user := rest[:colon]
	if strings.Contains(user, "%") {
		return prefix + redactedPlaceholder
	}

	return prefix + user + ":" + redactedPlaceholder
}

// isURLScheme reports whether s has the shape of a URL scheme per RFC 3986
// section 3.1: a letter, followed by any number of letters, digits, "+", "-",
// or ".".
func isURLScheme(s string) bool {
	if s == "" {
		return false
	}

	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
		case i > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.'):
		default:
			return false
		}
	}

	return true
}
