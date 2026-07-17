package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// labCtxUser is the username portion of the derived lab context account
// identifier. Every lab context uses this fixed value; it identifies the local
// pmx label "pmx@pve" in a derived lab context's token auth chain.
const labCtxUser = "pmx@pve"

// labCtxTokenName is the token name portion of the derived lab context account
// identifier. Every lab context uses this fixed value; it identifies the token
// created in the origin PVE cluster for lab context registration and renewal.
const labCtxTokenName = "pmx"

// labContextName returns the derived name of a lab context given the base lab
// name: "lab-" + name. The lab name itself (from config.Lab.Name) is always
// validated by validateLabNameCharset before it reaches this function, so no
// input sanitization is needed here.
func labContextName(name string) string {
	return "lab-" + name
}

// labKeychainService returns the keychain service identifier for a lab
// context's token secret, given the base lab name: "pmx-lab-" + name. The
// keychain service name is passed to the platform keychain API when storing or
// retrieving the secret portion of the lab context's token, where it serves as
// a namespace key, and must match the value used when the secret was stored.
func labKeychainService(name string) string {
	return "pmx-lab-" + name
}

// labCtxAccount returns the full account identifier for derived lab contexts:
// the concatenation of labCtxUser and labCtxTokenName joined by "!". Token
// accounts in Proxmox use the format "user!tokenname" to identify both the
// user owning the token and the token's own ID within that user's token space.
func labCtxAccount() string {
	return labCtxUser + "!" + labCtxTokenName
}

// parseTokenAddValue extracts the one-time secret from the JSON output of
// `pveum user token add ... --output-format json`, whose shape is
// {"full-tokenid":"pmx@pve!pmx","info":{...},"value":"<secret>"}. A missing,
// null, or empty value is an error: a lab context with an empty secret is
// broken, so it must never be treated as success.
func parseTokenAddValue(stdout string) (string, error) {
	var resp struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		return "", fmt.Errorf("parse token add output: %w", err)
	}
	if resp.Value == "" {
		return "", fmt.Errorf("token add returned no secret value")
	}
	return resp.Value, nil
}

// fingerprintRE matches the colon-separated hex SHA-256 form
// config.StrictValidateContext requires (mirrored here because that package's
// regex is unexported).
var fingerprintRE = regexp.MustCompile(`^(?i)[0-9a-f]{2}(?::[0-9a-f]{2}){31}$`)

// normalizeFingerprint strips the "sha256 Fingerprint=" prefix from a
// certificate fingerprint string (if present), validates the result is a
// valid colon-hex SHA-256 fingerprint using fingerprintRE, and returns the
// normalized hash. It returns an error when the input is not a valid SHA-256
// fingerprint format after prefix removal, or when the format is invalid.
//
// Valid inputs include:
//   - "sha256 Fingerprint=ab:cd:ef:..." (with prefix, which is stripped)
//   - "ab:cd:ef:..." (without prefix, returned as-is)
func normalizeFingerprint(raw string) (string, error) {
	// Strip the "sha256 Fingerprint=" prefix if present. The prefix check is
	// case-insensitive ("sha256" or "SHA256"), but the Fingerprint= part is
	// always capitalized in OpenSSH and PVE output.
	normalized := raw
	if strings.Contains(strings.ToLower(raw), "fingerprint=") {
		parts := strings.SplitN(raw, "=", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf(
				"normalize fingerprint: invalid format with fingerprint= prefix")
		}
		normalized = parts[1]
	}

	// Trim whitespace from the normalized hash.
	normalized = strings.TrimSpace(normalized)

	// Validate the hash matches the colon-hex SHA-256 format.
	if !fingerprintRE.MatchString(normalized) {
		return "", fmt.Errorf(
			"normalize fingerprint: %q does not match SHA-256 colon-hex format "+
				"(32 pairs of hex digits separated by colons)", raw)
	}

	return normalized, nil
}

// SSH Mint Sequence: labEnsureUser, labEnsureACL, labMintToken,
// labFetchFingerprint, labFetchHostname, labWaitForSSH.
//
// These functions implement the SSH bootstrap sequence for lab context
// registration: ensure the pmx@pve token user and Administrator ACL exist on
// the nested node, rotate the token to obtain a fresh secret, fetch the TLS
// fingerprint and hostname, and wait for SSH connectivity with bounded
// retries.

// labSSHWaitAttempts and labSSHWaitInterval bound the wait-for-SSH loop to
// roughly two minutes (24 * 5s), long enough for a freshly-started nested
// node to bring up sshd, short enough to fail a genuinely dead boot.
const (
	labSSHWaitAttempts = 24
	labSSHWaitInterval = 5 * time.Second
)

// labSSHPollSleep is a seam for testing: it sleeps for the given duration,
// or is overridden in tests to sleep zero time so the wait loop runs at full
// speed.
var labSSHPollSleep = time.Sleep

// labEnsureUser creates the token-only user pmx@pve if it does not exist. A
// non-zero exit (the user already exists) is tolerated; only a transport
// failure aborts, matching this package's idempotency convention.
func labEnsureUser(deps *cli.Deps, ip string) error {
	_, err := runGuestSSH(deps, ip, "pveum user add "+labCtxUser)
	if err != nil && guestCommandTransportFailed(err) {
		return fmt.Errorf("ensure user %s on %s: %w", labCtxUser, ip, err)
	}
	return nil
}

// labEnsureACL grants pmx@pve the Administrator role on / of the nested
// cluster. `pveum acl modify` is idempotent (re-applying an identical ACL
// exits 0), so this needs no probe.
func labEnsureACL(deps *cli.Deps, ip string) error {
	cmd := fmt.Sprintf("pveum acl modify / --users %s --roles Administrator", labCtxUser)
	if _, err := runGuestSSH(deps, ip, cmd); err != nil {
		return fmt.Errorf("ensure ACL for %s on %s: %w", labCtxUser, ip, err)
	}
	return nil
}

// labMintToken rotates the pmx@pve!pmx token: it removes any existing token
// (ignoring not-found) then creates a fresh one with --privsep 0 and returns
// the one-time secret parsed from the JSON output. Rotation is the only way
// to obtain a usable secret, since PVE returns a token value exactly once and
// never again.
func labMintToken(deps *cli.Deps, ip string) (string, error) {
	// Remove first; a not-found token exits non-zero, which is fine.
	_, rerr := runGuestSSH(deps, ip, fmt.Sprintf("pveum user token remove %s %s", labCtxUser, labCtxTokenName))
	if rerr != nil && guestCommandTransportFailed(rerr) {
		return "", fmt.Errorf("remove existing token on %s: %w", ip, rerr)
	}

	addCmd := fmt.Sprintf("pveum user token add %s %s --privsep 0 --output-format json", labCtxUser, labCtxTokenName)
	res, err := runGuestSSH(deps, ip, addCmd)
	if err != nil {
		return "", fmt.Errorf("mint token on %s: %w", ip, err)
	}
	return parseTokenAddValue(res.Stdout)
}

// labFetchFingerprint reads and normalizes the node's API certificate
// fingerprint over ssh.
func labFetchFingerprint(deps *cli.Deps, ip string) (string, error) {
	res, err := runGuestSSH(deps, ip, "openssl x509 -noout -fingerprint -sha256 -in /etc/pve/local/pve-ssl.pem")
	if err != nil {
		return "", fmt.Errorf("fetch TLS fingerprint from %s: %w", ip, err)
	}
	return normalizeFingerprint(res.Stdout)
}

// labFetchHostname reads the nested node's PVE hostname over ssh (used as the
// context's DefaultNode on fresh create). An empty result is an error: the
// hostname becomes the context's default node, and an empty default node is
// unusable.
func labFetchHostname(deps *cli.Deps, ip string) (string, error) {
	res, err := runGuestSSH(deps, ip, "hostname")
	if err != nil {
		return "", fmt.Errorf("fetch hostname from %s: %w", ip, err)
	}

	hostname := strings.TrimSpace(res.Stdout)
	if hostname == "" {
		return "", fmt.Errorf("fetch hostname from %s: result is empty", ip)
	}

	return hostname, nil
}

// labWaitForSSH blocks until a benign `hostname` probe against ip succeeds
// over ssh, or the attempt budget or ctx is exhausted. Only transport-level
// failures (ssh cannot connect yet) are retried; a connected-but-non-zero
// probe returns success, since reachability is all this step needs.
func labWaitForSSH(ctx context.Context, deps *cli.Deps, ip string) error {
	for attempt := 0; attempt < labSSHWaitAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("waiting for ssh on %s: %w", ip, err)
		}
		_, err := runGuestSSH(deps, ip, "hostname")
		if err == nil || !guestCommandTransportFailed(err) {
			return nil
		}
		labSSHPollSleep(labSSHWaitInterval)
	}
	return fmt.Errorf("timed out waiting for ssh on %s after %d attempts", ip, labSSHWaitAttempts)
}
