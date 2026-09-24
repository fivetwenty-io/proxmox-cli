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
	u, ok := parseProxyURL(raw)
	if !ok {
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

// ProxyHost returns the host and port a proxy URL names, such as
// "proxy.example:1080", for a record that identifies the proxy without its
// credentials. It returns "" for an empty value, for a URL that names no
// host, and whenever ProxyURL would mask the value whole: when url.Parse
// rejects it, or when it holds an "@" that its userinfo does not account
// for. In that second case the parsed host can itself be the start of the
// credential, as in "socks5://pmx:4711/x@proxy:1080", which url.Parse reads
// as host "pmx:4711" with the rest of the password in the path.
func ProxyHost(raw string) string {
	u, ok := parseProxyURL(raw)
	if !ok {
		return ""
	}

	return u.Host
}

// parseProxyURL parses raw and reports whether the result can be trusted to
// hold any credential in u.User. It fails when url.Parse fails, when the
// value is opaque because the "//" is missing, and when an "@" that u.User
// does not account for sits anywhere in the value, because that "@" can be
// the end of a password url.Parse misread as a host, a path, a query, or a
// fragment.
func parseProxyURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}

	if u.Opaque != "" ||
		(u.User == nil && strings.Contains(raw, "@")) ||
		strings.Contains(u.Path, "@") ||
		strings.Contains(u.RawQuery, "@") ||
		strings.Contains(u.Fragment, "@") {
		return nil, false
	}

	return u, true
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

// urlUserinfoRE matches a "scheme://user:pass@" candidate embedded anywhere
// in free text, capturing the scheme, the "//user" part, and the password
// separately. The "//" is optional, so the "socks5:user:pass@host" typo is
// caught too. The username group may be empty (a URL may have a password
// with no user) and may itself contain "@" or "/", and the password group
// runs greedily to the last "@" in the run of non-whitespace characters that
// follows, which is where the userinfo of a real URL ends (RFC 3986 section
// 3.2.1), not the first "@", which would stop short of a password that
// itself contains one. URLUserinfo then rejects or shortens each candidate
// that is not userinfo at all.
var urlUserinfoRE = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*):((?://)?[^\s:]*):(\S*)@`)

// URLUserinfo returns s with every "scheme://user:pass@" it contains
// rewritten as "scheme://user:<redacted>@", leaving everything else alone.
// It exists for free-form error text that quotes a URL whole, where the
// credential to redact is not known ahead of time as a discrete secret
// value.
//
// Text that only resembles userinfo is left as written:
//
//   - An http, https, ws, or wss URL whose "password" is a port followed by
//     "/", "?", or "#", as in "https://h:8006/access/users/alice@pve", is a
//     host, a port, and a path that holds a user ID.
//
//   - An ssh URL whose "password" is a port followed by "," or "/", as in
//     "ssh://admin@bastion:2222,root@inner", is a jump chain.
//
//   - A "scheme" of hexadecimal digits right after "[" or ":", as in
//     "admin@[fd00::1]:22,root@inner", is part of an IPv6 literal, and so is
//     a "username" that holds "[" or "]".
//
//   - A "password" that holds "://" runs into the next URL, so it is cut
//     back to the last "@" before that URL, and the candidate is dropped
//     when no "@" is left.
//
// Because it cannot know where a URL ends in free text, it may still mask
// text that only looks like "word:word:more@", and it cannot mask a password
// that contains whitespace: the password match stops at the first space, so
// in "socks5://u:pa s3cret@h" nothing is masked. Nor can it mask an http or
// https proxy URL whose password is all digits followed by "/", "?", or "#",
// such as "http://pmx:4711/x@proxy:3128", because that text is the same
// shape as an API URL with a user ID in its path. A caller that holds the
// raw URL as a discrete value must mask it with ProxyURL instead.
//
// It does not cover the text of a url.Parse error, which can itself quote a
// password verbatim (for example "invalid port %q after host" on a
// malformed proxy URL); callers must not append that text to a message this
// function is expected to clean.
func URLUserinfo(s string) string {
	var b strings.Builder

	copied, pos := 0, 0

	for pos < len(s) {
		loc := urlUserinfoRE.FindStringSubmatchIndex(s[pos:])
		if loc == nil {
			break
		}

		start := pos + loc[0]
		scheme := s[pos+loc[2] : pos+loc[3]]
		user := s[pos+loc[4] : pos+loc[5]]
		passStart := pos + loc[6]
		pass := s[passStart : pos+loc[7]]

		n, ok := userinfoPasswordLen(s, start, scheme, user, pass)
		if !ok {
			// Resume inside the rejected candidate, so a real URL that
			// its greedy password swallowed is still found.
			pos = passStart

			continue
		}

		b.WriteString(s[copied:passStart])
		b.WriteString(Placeholder)
		b.WriteByte('@')

		copied = passStart + n + 1
		pos = copied
	}

	if copied == 0 {
		return s
	}

	b.WriteString(s[copied:])

	return b.String()
}

// userinfoPasswordLen decides whether one urlUserinfoRE candidate is a
// password, given the whole text s, the offset where the candidate starts,
// and its scheme, "//user", and password groups. It returns the length of
// the password to mask, which is shorter than pass when pass runs into a
// second URL, and false when the candidate is not userinfo.
func userinfoPasswordLen(s string, start int, scheme, user, pass string) (int, bool) {
	if start > 0 && (s[start-1] == '[' || s[start-1] == ':') && isHex(scheme) {
		return 0, false
	}

	if strings.ContainsAny(user, "[]") {
		return 0, false
	}

	if i := strings.Index(pass, "://"); i >= 0 {
		j := strings.LastIndexByte(pass[:i], '@')
		if j < 0 {
			return 0, false
		}

		pass = pass[:j]
	}

	switch strings.ToLower(scheme) {
	case "http", "https", "ws", "wss":
		if isPortThen(pass, "/?#") {
			return 0, false
		}
	case "ssh":
		if isPortThen(pass, ",/") {
			return 0, false
		}
	}

	return len(pass), true
}

// isPortThen reports whether s starts with one to five decimal digits
// followed by one of the bytes in ends, which is the shape of a port
// followed by the character that ends an authority.
func isPortThen(s, ends string) bool {
	digits := 0
	for digits < len(s) && digits < 6 && s[digits] >= '0' && s[digits] <= '9' {
		digits++
	}

	return digits >= 1 && digits <= 5 && digits < len(s) && strings.IndexByte(ends, s[digits]) >= 0
}

// isHex reports whether s is non-empty and made only of hexadecimal digits.
func isHex(s string) bool {
	if s == "" {
		return false
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}

	return true
}
