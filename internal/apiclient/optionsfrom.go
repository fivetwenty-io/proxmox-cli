package apiclient

import (
	"fmt"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
)

// BuildOptions maps individual configuration fields onto a pve.Options value
// ready for pve.NewClient.
//
// Authentication priority (first non-empty wins):
//  1. API token — if token is non-empty, username@realm!tokenid=secret format is
//     assembled and set on Options.APIToken.
//  2. Ticket — if ticket is non-empty, Options.Ticket is set (password ignored)
//     and Options.CSRFToken is set from csrf. The CSRF token is required so that
//     non-GET requests under ticket (session) authentication carry the
//     PVECSRFPreventionToken header; without it Proxmox rejects every write with
//     HTTP 401. NewAPIClient propagates this token onto the live authenticator.
//  3. Password — if username and password are both non-empty, Options.Username and
//     Options.Password are set; realm is appended to username as "user@realm".
//
// TLS:
//   - insecure=true sets SSLVerifyNone on a populated SSLOptions block.
//   - fingerprint non-empty adds it to CachedFingerprints as trusted.
//
// Default port 0 is accepted (pve.Options.setDefaults fills in 8006).
// Default protocol "" is accepted (pve.Options.setDefaults fills in "https").
func BuildOptions(
	host string,
	port int,
	protocol string,
	username string,
	realm string,
	token string,
	password string,
	ticket string,
	csrf string,
	insecure bool,
	fingerprint string,
) pve.Options {
	opts := pve.Options{
		Host:     host,
		Port:     port,
		Protocol: protocol,
	}

	switch {
	case token != "":
		// Format: user@realm!tokenid=secret
		// The tokenid comes from the token argument; realm is embedded in username
		// if the caller already formatted it (user@realm) or appended here.
		user := qualifiedUser(username, realm)
		opts.APIToken = fmt.Sprintf("%s!%s", user, token)

	case ticket != "":
		opts.Ticket = ticket
		opts.CSRFToken = csrf

	case username != "" && password != "":
		opts.Username = qualifiedUser(username, realm)
		opts.Password = password
	}

	if insecure {
		opts.SSLOptions = &pve.SSLOptions{
			VerifyMode:     pve.SSLVerifyNone,
			VerifyHostname: false,
		}
	}

	if fingerprint != "" {
		if opts.CachedFingerprints == nil {
			opts.CachedFingerprints = make(map[string]bool)
		}

		opts.CachedFingerprints[fingerprint] = true
	}

	return opts
}

// qualifiedUser returns "username@realm" if realm is non-empty and not already
// embedded in username (i.e. username does not already contain "@"), otherwise
// returns username unchanged.
func qualifiedUser(username, realm string) string {
	if realm == "" {
		return username
	}

	for _, c := range username {
		if c == '@' {
			return username // already qualified
		}
	}

	return username + "@" + realm
}
