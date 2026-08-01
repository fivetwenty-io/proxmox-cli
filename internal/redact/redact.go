// Package redact provides helpers for masking secrets before they are
// written to command output or logs.
package redact

import (
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
