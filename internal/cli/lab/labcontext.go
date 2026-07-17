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
