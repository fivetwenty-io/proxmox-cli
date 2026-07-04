package apiclient

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	pve "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/client"
)

// NewManualVerifyCallback returns a pve.Options.ManualVerifyCallback that
// implements interactive Trust-On-First-Use (TOFU) certificate acceptance,
// analogous to SSH's known_hosts prompt.
//
// isTTY reports whether in is an interactive terminal at call time. When
// isTTY returns false, the callback rejects the presented certificate
// unconditionally and never reads from in: no prompt is written, no read is
// attempted, and the connection fails closed exactly as it did before TOFU
// support existed (only fingerprints already trusted via
// Options.CachedFingerprints or previously persisted to
// Options.FingerprintCachePath are accepted). This guarantees a non-TTY
// invocation (scripts, CI, piped input) can never block waiting on stdin.
//
// When isTTY returns true, the callback writes the presented fingerprint and
// host to prompt and reads a single line from in, accepting the certificate
// when the trimmed, case-insensitive answer is "y" or "yes" and rejecting
// every other answer, including an empty line or an immediate EOF (e.g. a
// closed or already-exhausted input). Accepted fingerprints are persisted by
// the underlying pve-apiclient-go client when Options.FingerprintCachePath is
// set; this callback does not write the cache file itself.
func NewManualVerifyCallback(
	prompt io.Writer,
	in io.Reader,
	isTTY func() bool,
) func(pve.FingerprintVerificationRequest) bool {
	return func(req pve.FingerprintVerificationRequest) bool {
		if isTTY == nil || !isTTY() {
			return false
		}

		_, _ = fmt.Fprintf(prompt,
			"Unknown TLS certificate presented by %s\nFingerprint: %s\nTrust this certificate for future connections? [y/N]: ",
			req.Host, req.Fingerprint)

		reader := bufio.NewReader(in)

		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			// Immediate EOF / read error with no data at all: treat as a reject,
			// matching how an empty answer is handled below.
			return false
		}

		answer := strings.ToLower(strings.TrimSpace(line))

		return answer == "y" || answer == "yes"
	}
}

// fingerprintCacheFileName reduces contextName to a filesystem-safe file name
// component: only ASCII letters, digits, '-', and '_' pass through unchanged;
// every other rune (including '/', '\', and '.') is replaced with '_'. This
// keeps a maliciously or accidentally crafted context name (config.yml is
// user-editable) from escaping the fingerprints directory or colliding with a
// reserved file name, without imposing character restrictions on context
// names elsewhere in the CLI.
func fingerprintCacheFileName(contextName string) string {
	var b strings.Builder
	b.Grow(len(contextName))

	for _, r := range contextName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}

	if b.Len() == 0 {
		return "_"
	}

	return b.String()
}

// FingerprintCachePath returns the per-context TLS fingerprint cache file
// path used for TOFU persistence (see pve.Options.FingerprintCachePath),
// derived from configPath (the active pve config file) and contextName (the
// resolved context name). The cache file lives in a "fingerprints"
// subdirectory alongside the config file, e.g.
// ~/.config/pve/fingerprints/<context>.json, so that trust decisions for one
// context never leak into another and the file is created (with its parent
// directory) automatically by pve-apiclient-go on first accepted fingerprint.
//
// contextName must be non-empty; an empty contextName collapses to the
// literal file name "_.json" via fingerprintCacheFileName rather than
// producing an ambiguous or colliding path, since callers that have not yet
// resolved a context name should not be persisting fingerprint trust at all.
func FingerprintCachePath(configPath, contextName string) string {
	dir := filepath.Dir(configPath)
	return filepath.Join(dir, "fingerprints", fingerprintCacheFileName(contextName)+".json")
}
