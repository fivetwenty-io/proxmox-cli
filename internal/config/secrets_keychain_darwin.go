//go:build darwin

package config

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"unicode"
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

// keychainFieldSafe reports whether s is a single non-empty token safe to
// interpolate into the whitespace-tokenized `security -i` command line: no
// whitespace (which would split the token) and no control characters (a
// newline would inject a second command into the interactive session).
func keychainFieldSafe(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// maxKeychainDuplicates bounds purgeKeychainItems' delete loop. Real
// accumulations are a handful of items (one per orphaning event); the cap only
// guards against security(1) reporting success without deleting, which would
// otherwise loop forever.
const maxKeychainDuplicates = 32

// purgeKeychainItems deletes every generic-password item matching (service,
// account) from the login keychain and returns nil once none remain.
// delete-generic-password removes only the first match per invocation, and
// duplicates for one (service, account) do accumulate: an add cannot update an
// existing item whose ACL is bound to a signing identity the current binary no
// longer has (each ad-hoc-signed local rebuild mints a new one), so -U falls
// back to inserting a second item. Deleting needs no ACL access to the secret,
// so this loop clears orphaned items the current build cannot read.
func purgeKeychainItems(service, account string) error {
	for range maxKeychainDuplicates {
		stderr, err := keychainRun("", "delete-generic-password", "-s", service, "-a", account)
		if err != nil {
			if strings.Contains(strings.ToLower(stderr), "could not be found") {
				return nil
			}
			return fmt.Errorf("keychain delete for service %q account %q failed: %s: %w",
				service, account, strings.TrimSpace(stderr), err)
		}
	}
	return fmt.Errorf("keychain delete for service %q account %q: items still present after %d deletions",
		service, account, maxKeychainDuplicates)
}

// StoreKeychainSecret stores secret in the macOS login keychain under the
// generic-password item (service, account). It first purges every existing
// item for that (service, account) — including ACL-orphaned ones -U could not
// update — so exactly one item exists afterwards, then adds the fresh value.
// The security(1) "add-generic-password" line — including the -w <secret>
// argument — is fed to `security -i` on stdin, so the secret never appears on
// any process's argv (which is world-readable via `ps`). Lab token secrets are
// UUID-form (no whitespace), so the interactive line parses unambiguously.
func StoreKeychainSecret(service, account, secret string) error {
	if !keychainFieldSafe(service) || !keychainFieldSafe(account) {
		return fmt.Errorf("keychain store requires non-empty, whitespace-free service and account")
	}
	if !keychainFieldSafe(secret) {
		// Never echo the secret; report only the shape violation.
		return fmt.Errorf("keychain store: secret must be non-empty and free of whitespace and control characters")
	}
	if err := purgeKeychainItems(service, account); err != nil {
		return fmt.Errorf("keychain store: clear existing items: %w", err)
	}
	// -U stays as a guard against a concurrent add between the purge and here.
	line := fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n", service, account, secret)
	if stderr, err := keychainRun(line, "-i"); err != nil {
		return fmt.Errorf("keychain store for service %q account %q failed: %s: %w",
			service, account, strings.TrimSpace(stderr), err)
	}
	return nil
}

// DeleteKeychainSecret removes all generic-password items (service, account)
// from the macOS login keychain, including duplicates left behind by adds that
// could not see an ACL-orphaned original. A "not found" result (the item was
// never created, or was already removed) is treated as success, so cleanup is
// idempotent.
func DeleteKeychainSecret(service, account string) error {
	return purgeKeychainItems(service, account)
}
