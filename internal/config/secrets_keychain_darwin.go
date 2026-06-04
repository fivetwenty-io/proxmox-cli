//go:build darwin

package config

import (
	"fmt"
	"os/exec"
	"strings"
)

// keychainLookup resolves a "keychain:<service>[/<account>]" reference against
// the macOS login keychain using the security(1) tool. It runs
//
//	security find-generic-password -s <service> [-a <account>] -w
//
// and returns the stored password. The bare "keychain:<service>" form matches
// any account under that service name. Add an entry with, for example:
//
//	security add-generic-password -s pve/lab -a root@pam -w
//
// Errors from security (item not found, user cancelled the unlock prompt) are
// surfaced verbatim so the cause is visible. The secret itself is never logged.
func keychainLookup(path string) (string, error) {
	service, account, _ := strings.Cut(path, "/")
	if service == "" {
		return "", fmt.Errorf("keychain reference is empty (expected keychain:service[/account])")
	}

	args := []string{"find-generic-password", "-s", service}
	if account != "" {
		args = append(args, "-a", account)
	}
	args = append(args, "-w")

	// Fixed binary path; args are flag literals plus a service/account split from
	// the config reference, never a shell string. No injection surface.
	out, err := exec.Command("/usr/bin/security", args...).Output() //nolint:gosec // fixed binary, vetted args
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			msg := strings.TrimSpace(string(ee.Stderr))
			if msg == "" {
				msg = "item not found (add it with: security add-generic-password -s " +
					service + " -a <account> -w)"
			}
			return "", fmt.Errorf("keychain lookup for %q failed: %s", path, msg)
		}
		return "", fmt.Errorf("keychain lookup for %q: %w", path, err)
	}

	// security -w prints the password followed by a trailing newline.
	return strings.TrimRight(string(out), "\r\n"), nil
}
