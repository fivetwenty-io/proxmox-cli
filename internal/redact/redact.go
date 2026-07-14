// Package redact provides helpers for masking secrets before they are
// written to command output or logs.
package redact

import "strings"

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
