// Package redact provides helpers for masking secrets before they are
// written to command output or logs.
package redact

import (
	"net/url"
	"regexp"
	"strings"
)

// Placeholder is substituted for any redacted secret value.
const Placeholder = "<redacted>"

// Password returns Placeholder if s is non-empty, or the empty string if s
// is empty. It never returns the input verbatim, so callers can always emit
// its result in place of a raw password without risking a leak when no
// password was configured.
func Password(s string) string {
	if s == "" {
		return ""
	}
	return Placeholder
}

// Line returns line with every occurrence of secret replaced by Placeholder.
// If secret is empty, line is returned unchanged (there is nothing to
// redact, and replacing occurrences of "" would corrupt the string).
func Line(line, secret string) string {
	if secret == "" {
		return line
	}
	return strings.ReplaceAll(line, secret, Placeholder)
}

// SensitiveKeyMarkers are the lowercase substrings that mark a parameter or
// argument key as carrying a credential. Over-matching is deliberate: masking
// a non-secret costs nothing, while leaking one into a log file is
// unrecoverable.
var SensitiveKeyMarkers = []string{
	"password", "passwd", "passphrase", "secret", "token", "ticket", "csrf",
	"credential", "apikey", "privatekey",
}

// SensitiveKey reports whether key names a credential, matching any entry of
// SensitiveKeyMarkers case-insensitively as a substring.
func SensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, marker := range SensitiveKeyMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// queryParamRE matches one key=value pair of a URL query string, anchored on
// the "?" or "&" that introduces it so it cannot match a bare key=value pair
// in ordinary prose. The value runs to the next separator or to whitespace,
// since these strings are frequently embedded in a longer error message.
var queryParamRE = regexp.MustCompile(`([?&])([^?&=\s]+)=([^&\s"']*)`)

// QueryParams returns s with the value of every sensitive query parameter
// replaced by Placeholder, leaving the rest of the string — including
// non-sensitive parameters — intact.
//
// It operates on free text rather than a parsed URL because the strings that
// need this are error messages with a URL embedded in them, not URLs. A GET
// or DELETE carries its parameters in the query string, so a command with a
// --password flag (node scan pbs requires one) otherwise writes that
// credential verbatim into the request URL, which is then logged and quoted
// back in the error text.
func QueryParams(s string) string {
	if !strings.ContainsAny(s, "?&") {
		return s
	}
	return queryParamRE.ReplaceAllStringFunc(s, func(match string) string {
		parts := queryParamRE.FindStringSubmatch(match)
		if len(parts) != 4 || !SensitiveKey(parts[2]) {
			return match
		}
		return parts[1] + parts[2] + "=" + Placeholder
	})
}

// ProxyURL returns raw with any password replaced by Placeholder, so it is
// the one function every path that prints a proxy URL goes through.
//
// A URL that carries a password becomes "scheme://user:<redacted>@host:port"
// (with any path, query, or fragment preserved), and a URL that carries none
// — because it has no userinfo at all, or a username with no password — is
// returned unchanged. A username that decodes to text holding ":", as in
// "socks5://pmx%3As3cret@proxy", is a percent-encoded user:password pair
// and becomes "scheme://<redacted>@host:port". A value url.Parse rejects becomes "scheme://<redacted>"
// when a scheme can be recovered by hand, or Placeholder alone when it
// cannot, so a password can never escape through a parse error: whatever
// url.Parse could not make sense of, it could not have extracted a password
// from either.
//
// url.Parse also accepts several malformed proxy URLs without extracting any
// userinfo at all — a missing "//" turns the whole thing into an opaque
// value, and an unescaped "@" can land in the path, the query, or the
// fragment instead of the userinfo it was meant to introduce. Any "@" a
// successful parse does not account for in u.User is treated the same as a
// parse failure and reduced to the scheme-only or bare placeholder, because
// there is no way to tell a stray "@" from one that opens a password url.Parse
// missed.
//
// The masked form is built by plain string concatenation, never by handing
// Placeholder to url.UserPassword, which would percent-encode the angle
// brackets into "%3Credacted%3E" and defeat the point of a human-readable
// placeholder.
func ProxyURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return maskUnparseableProxyURL(raw)
	}

	if u.Opaque != "" ||
		(u.User == nil && strings.Contains(raw, "@")) ||
		strings.Contains(u.Path, "@") ||
		strings.Contains(u.RawQuery, "@") ||
		strings.Contains(u.Fragment, "@") {
		return maskUnparseableProxyURL(raw)
	}

	if u.User == nil {
		return raw
	}

	// A username whose decoded form holds ":" is a whole user:password
	// pair that the operator percent-encoded, so it is masked like a
	// password.
	username := u.User.Username()
	encodedPair := strings.Contains(username, ":")

	if _, hasPassword := u.User.Password(); !hasPassword && !encodedPair {
		return raw
	}

	var b strings.Builder

	if u.Scheme != "" {
		b.WriteString(u.Scheme)
		b.WriteString("://")
	}

	if encodedPair {
		b.WriteString(Placeholder)
	} else {
		b.WriteString(username)
		b.WriteByte(':')
		b.WriteString(Placeholder)
	}

	b.WriteByte('@')
	b.WriteString(u.Host)
	b.WriteString(u.EscapedPath())

	if u.RawQuery != "" {
		b.WriteByte('?')
		b.WriteString(u.RawQuery)
	}

	if u.Fragment != "" {
		b.WriteByte('#')
		b.WriteString(u.EscapedFragment())
	}

	return b.String()
}

// maskUnparseableProxyURL handles a value url.Parse rejects. It recovers
// whatever comes before the first "://" by hand, so a caller still sees
// which scheme was configured, as "scheme://<redacted>". When no such prefix
// exists, or it does not look like a URL scheme, it falls back to Placeholder
// alone rather than guessing.
func maskUnparseableProxyURL(raw string) string {
	scheme, _, ok := strings.Cut(raw, "://")
	if !ok || !isURLScheme(scheme) {
		return Placeholder
	}

	return scheme + "://" + Placeholder
}

// isURLScheme reports whether s has the shape of a URL scheme per RFC 3986
// §3.1: a letter, followed by any number of letters, digits, "+", "-", or
// ".".
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

// urlUserinfoRE matches a "scheme://user:pass@" prefix embedded anywhere in
// free text, capturing "scheme://user" and "pass" separately. The "//" is
// optional, so the "socks5:user:pass@host" typo is caught too. The username
// group may be empty (a URL may have a password with no user) and may itself
// contain "@" or "/", and the password group runs greedily to the last "@"
// in the run of non-whitespace characters that follows, which is where the
// userinfo of a real URL ends (RFC 3986 section 3.2.1), not the first "@",
// which would stop short of a password that itself contains one.
var urlUserinfoRE = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*:(?://)?[^\s:]*):(\S*)@`)

// URLUserinfo returns s with every "scheme://user:pass@" it contains
// rewritten as "scheme://user:<redacted>@", leaving everything else alone.
// It exists for free-form error text that quotes a URL whole, where the
// credential to redact is not known ahead of time as a discrete secret
// value.
//
// Because it cannot know where a URL ends in free text, it may mask text
// that only looks like "word:word:more@", and it cannot mask a password
// that contains whitespace: the password match stops at the first space, so
// in "socks5://u:pa s3cret@h" nothing is masked. A caller that holds the
// raw URL as a discrete value must use ProxyURL instead.
//
// It does not cover the text of a url.Parse error, which can itself quote a
// password verbatim (for example "invalid port %q after host" on a
// malformed proxy URL); callers must not append that text to a message this
// function is expected to clean.
func URLUserinfo(s string) string {
	return urlUserinfoRE.ReplaceAllString(s, "${1}:"+Placeholder+"@")
}
