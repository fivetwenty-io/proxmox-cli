package config

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// warnOnce ensures the inline-secret warning is emitted at most once per process.
var (
	inlineLiteralOnce sync.Once
)

// ResolveSecret resolves a secret string into its plaintext value.
//
// Resolution rules (in order, the documented 3-tier precedence):
//  1. ${NAME} — reads the environment variable NAME; error if unset.
//  2. $NAME — an environment reference only when NAME is a valid shell variable
//     name (letters, digits, underscore; not starting with a digit) AND that
//     variable is set. A value that merely begins with '$' (for example a
//     literal password like "$up3rS3cret") is NOT silently turned into a failed
//     env lookup: when the bare $NAME form is not a resolvable env reference it
//     falls through to be treated as a literal with the inline-secret warning.
//  3. keychain:<path> — delegates to keychainLookup(path); availability depends on build tags.
//  4. Any other value — treated as a literal; a one-time warning is emitted to stderr.
func ResolveSecret(s string) (string, error) {
	switch {
	case strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}"):
		// ${NAME} form — explicit env reference; an unset variable is an error.
		name := s[2 : len(s)-1]
		return envLookup(name)

	case strings.HasPrefix(s, "$") && !strings.HasPrefix(s, "${"):
		// $NAME form. Only treat it as an env reference when the remainder is a
		// valid variable name and that variable is actually set; otherwise the
		// value is a literal secret that happens to start with '$'.
		name := s[1:]
		if isValidEnvName(name) {
			if val, ok := os.LookupEnv(name); ok {
				return val, nil
			}
		}
		return literalSecret(s), nil

	case strings.HasPrefix(s, "keychain:"):
		path := strings.TrimPrefix(s, "keychain:")
		return keychainLookup(path)

	default:
		return literalSecret(s), nil
	}
}

// literalSecret returns s unchanged after emitting the one-time inline-secret
// warning to stderr.
func literalSecret(s string) string {
	inlineLiteralOnce.Do(func() {
		fmt.Fprintln(os.Stderr, "WARN: inline secret in config; prefer an environment-variable reference or keychain:PATH")
	})
	return s
}

// isValidEnvName reports whether name is a syntactically valid environment
// variable name: a non-empty run of ASCII letters, digits, or underscores that
// does not start with a digit.
func isValidEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
		isDigit := c >= '0' && c <= '9'
		if i == 0 && !isLetter {
			return false
		}
		if !isLetter && !isDigit {
			return false
		}
	}
	return true
}

// envLookup returns the value of the named environment variable.
// Returns an error if the variable is unset or empty.
func envLookup(name string) (string, error) {
	val, ok := os.LookupEnv(name)
	if !ok {
		return "", fmt.Errorf("environment variable %q is not set", name)
	}
	return val, nil
}
