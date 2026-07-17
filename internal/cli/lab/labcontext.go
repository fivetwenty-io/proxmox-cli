package lab

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
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

// parseTokenAddValue extracts the secret token value from the JSON response of
// a successful `pveum user token add` command, which returns `{"value":"<secret>"}`.
// It returns an error when the JSON is malformed, the "value" key is missing or
// null, or the value is not a string.
func parseTokenAddValue(stdout string) (string, error) {
	var response map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		return "", fmt.Errorf("parse token response JSON: %w", err)
	}

	valueRaw, ok := response["value"]
	if !ok {
		return "", fmt.Errorf("parse token response: missing 'value' key in JSON")
	}

	if valueRaw == nil {
		return "", fmt.Errorf("parse token response: 'value' key is null")
	}

	value, ok := valueRaw.(string)
	if !ok {
		return "", fmt.Errorf("parse token response: 'value' key is not a string")
	}

	return value, nil
}

// fingerprintRE is a compiled regexp that matches colon-separated hexadecimal
// SHA-256 fingerprints in the format "xx:xx:...:xx" where each xx is exactly
// two hex digits (0-9, a-f, A-F) and the total structure represents all 32
// bytes of a SHA-256 hash. The regexp ensures 32 colon-separated pairs of hex
// digits with no leading or trailing whitespace.
var fingerprintRE = regexp.MustCompile(
	`^[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){31}$`)

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
