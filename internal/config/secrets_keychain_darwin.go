//go:build darwin

package config

import (
	"bytes"
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

// keychainRun executes /usr/bin/security with args, feeding stdin on the
// process's standard input, and returns its captured stderr. It is a package
// var so tests can intercept the security(1) call without touching the real
// login keychain. The secret, when present, is passed only through stdin
// (never argv), so it is not exposed to `ps`.
var keychainRun = func(stdin string, args ...string) (string, error) {
	cmd := exec.Command("/usr/bin/security", args...) //nolint:gosec // fixed binary, vetted flags
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stderr.String(), err
}

// StoreKeychainSecret stores secret in the macOS login keychain under the
// generic-password item (service, account), creating it or updating it in
// place (-U). The security(1) "add-generic-password" line — including the
// -w <secret> argument — is fed to `security -i` on stdin, so the secret
// never appears on any process's argv (which is world-readable via `ps`).
// Lab token secrets are UUID-form (no whitespace), so the interactive line
// parses unambiguously.
func StoreKeychainSecret(service, account, secret string) error {
	if service == "" || account == "" {
		return fmt.Errorf("keychain store requires non-empty service and account")
	}
	line := fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n", service, account, secret)
	if stderr, err := keychainRun(line, "-i"); err != nil {
		return fmt.Errorf("keychain store for service %q account %q failed: %s: %w",
			service, account, strings.TrimSpace(stderr), err)
	}
	return nil
}

// DeleteKeychainSecret removes the generic-password item (service, account)
// from the macOS login keychain. A "not found" result (the item was never
// created, or was already removed) is treated as success, so cleanup is
// idempotent.
func DeleteKeychainSecret(service, account string) error {
	stderr, err := keychainRun("", "delete-generic-password", "-s", service, "-a", account)
	if err != nil {
		if strings.Contains(strings.ToLower(stderr), "could not be found") {
			return nil
		}
		return fmt.Errorf("keychain delete for service %q account %q failed: %s: %w",
			service, account, strings.TrimSpace(stderr), err)
	}
	return nil
}
