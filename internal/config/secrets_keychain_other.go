//go:build !darwin

package config

import "errors"

// keychainLookup is unavailable off macOS: the keychain backend is implemented
// with the macOS security(1) tool. Use an environment-variable reference
// (${VAR}) for the secret on other platforms.
func keychainLookup(_ string) (string, error) {
	return "", errors.New("keychain support is only available on macOS; use a ${VAR} env reference instead")
}
