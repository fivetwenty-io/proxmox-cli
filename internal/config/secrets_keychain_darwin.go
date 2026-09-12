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
	out, err := keychainOutput(args...)
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			msg := strings.TrimSpace(string(ee.Stderr))
			if msg == "" {
				msg = "item not found (add it with: security add-generic-password -s " +
					service + " -a <account> -w)"
			}
			return "", fmt.Errorf("keychain lookup for %q failed: %s%s", path, msg, keychainNotVisibleHint(msg))
		}
		return "", fmt.Errorf("keychain lookup for %q: %w", path, err)
	}

	// security -w prints the password followed by a trailing newline.
	return strings.TrimRight(string(out), "\r\n"), nil
}

// keychainOutput runs /usr/bin/security with args and returns its stdout,
// with stderr available on the returned *exec.ExitError. It is a package var
// so tests can drive the read path without touching the real login keychain.
var keychainOutput = func(args ...string) ([]byte, error) {
	return exec.Command("/usr/bin/security", args...).Output() //nolint:gosec // fixed binary, vetted args
}

// keychainNotVisibleMsg is appended to a not-found lookup error, and returned
// from a store attempt, when the process cannot see the login keychain at all.
const keychainNotVisibleMsg = "no default keychain is visible to this process; " +
	"pmx is running under a sandbox that denies keychain access, or HOME is not the login user's home"

// keychainVisible reports whether this process can see a default user
// keychain. security(1) reports an item as not found both when the item is
// absent and when no keychain is reachable at all, which happens under a
// sandbox that denies keychain access or when HOME is not the login user's
// home directory. In that state `security default-keychain -d user` fails,
// so the two cases can be told apart. The check reads preferences only and
// never prompts.
func keychainVisible() bool {
	_, err := keychainRun("", "default-keychain", "-d", "user")
	return err == nil
}

// keychainNotVisibleHint returns a parenthetical explaining a not-found
// lookup when the real cause is an invisible keychain, and "" otherwise. A
// genuine not-found (the keychain is reachable and the item is absent) gets
// no hint, so the ordinary message stays unchanged.
func keychainNotVisibleHint(stderr string) string {
	if !strings.Contains(strings.ToLower(stderr), "could not be found") || keychainVisible() {
		return ""
	}
	return " (" + keychainNotVisibleMsg + ")"
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

// keychainFieldSafe reports whether s is a single non-empty token that
// survives `security -i` verbatim. That reader tokenizes each line with
// shell-like rules, so three classes of character do not round-trip:
// whitespace (splits the token), control characters (a newline injects a
// second command into the interactive session), and backslash (consumed as an
// escape — storing "a\b" yields "ab", and the add still reports success, so a
// caller would otherwise be told a credential was stored that no lookup can
// ever match). Quotes, $, and backticks were measured and do round-trip
// unchanged, so they stay allowed.
func keychainFieldSafe(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' {
			return false
		}
	}
	return true
}

// maxKeychainDuplicates bounds purgeKeychainItems' delete loop. Real
// accumulations are a handful of items (one per failed -U update); the cap only
// guards against security(1) reporting success without deleting, which would
// otherwise loop forever.
const maxKeychainDuplicates = 32

// purgeKeychainItems deletes every generic-password item matching (service,
// account) from the login keychain and returns nil once none remain.
// delete-generic-password removes only the first match per invocation, and
// duplicates for one (service, account) were observed to accumulate: -U did
// not update the existing item and add-generic-password inserted a second one
// instead. The cause was not established; the item ACL trusts
// /usr/bin/security rather than this binary (see StoreKeychainSecret), so a
// changed pmx signature is not it. Deleting needs no access to the secret, so
// this loop clears every leftover regardless of who can read it.
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
// item for that (service, account) — including leftovers -U could not
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
		// Never echo the secret; report only the shape violation. Refusing is
		// the point: `security -i` would store a silently altered value and
		// still report success (see keychainFieldSafe).
		return fmt.Errorf(
			"keychain store: secret must be non-empty and free of whitespace, control characters, " +
				"and backslashes")
	}
	// Fail fast when no keychain is reachable. In that state the purge below
	// reports not-found (so it would succeed vacuously) and the add then
	// blocks on an authorization dialog the process cannot show, or fails
	// with errAuthorizationInteractionNotAllowed (-60008).
	if !keychainVisible() {
		return fmt.Errorf("keychain store for service %q account %q: %s", service, account, keychainNotVisibleMsg)
	}
	if err := purgeKeychainItems(service, account); err != nil {
		return fmt.Errorf("keychain store: clear existing items: %w", err)
	}
	// -U stays as a guard against a concurrent add between the purge and here.
	//
	// No -T: any -T suppresses the default trusted-application entry, which
	// is /usr/bin/security, the binary keychainLookup execs to read the secret
	// back. Without -T that entry stands and lookups succeed without a prompt
	// (measured on macOS 26.6). Naming pmx there instead leaves only pmx in the
	// list, and every later lookup blocks on an authorization dialog. It could
	// not have helped a direct Security.framework reader either: security(1)
	// stamps every item with the partition ID "apple-tool:", which gates out
	// any non-Apple reader regardless of -T, Developer ID builds included.
	line := fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n", service, account, secret)
	if stderr, err := keychainRun(line, "-i"); err != nil {
		return fmt.Errorf("keychain store for service %q account %q failed: %s: %w",
			service, account, strings.TrimSpace(stderr), err)
	}
	return nil
}

// DeleteKeychainSecret removes all generic-password items (service, account)
// from the macOS login keychain, including duplicates left behind by adds that
// did not update the original. A "not found" result (the item was
// never created, or was already removed) is treated as success, so cleanup is
// idempotent.
func DeleteKeychainSecret(service, account string) error {
	return purgeKeychainItems(service, account)
}
