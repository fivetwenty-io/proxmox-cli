//go:build !darwin

package config

import "errors"

// keychainLookup is unavailable off macOS: the keychain backend is implemented
// with the macOS security(1) tool. Use an environment-variable reference
// (${VAR}) for the secret on other platforms.
func keychainLookup(_ string) (string, error) {
	return "", errors.New("keychain support is only available on macOS; use a ${VAR} env reference instead")
}

// StoreKeychainSecret is unavailable off macOS; it always returns
// ErrKeychainUnsupported so callers can fall back to a literal-secret config
// entry (or a ${VAR} reference).
func StoreKeychainSecret(_, _, _ string) error { return ErrKeychainUnsupported }

// DeleteKeychainSecret is unavailable off macOS; it always returns
// ErrKeychainUnsupported. Cleanup callers treat this as a no-op.
func DeleteKeychainSecret(_, _ string) error { return ErrKeychainUnsupported }
