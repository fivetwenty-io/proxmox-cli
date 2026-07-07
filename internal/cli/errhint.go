package cli

import (
	"errors"

	pveerrors "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/errors"

	"github.com/fivetwenty-io/pve-cli/internal/exitcode"
)

// unauthorizedHint is printed after a 401 so the operator knows the request
// reached Proxmox but the credentials were rejected, and how to inspect them.
const unauthorizedHint = `hint: authentication failed (HTTP 401) — Proxmox rejected the credentials.
  Inspect the active context:   pve context show
  For token auth the API header is USER@REALM!TOKENID=SECRET, split across three fields:
    - auth.username : the user and realm, e.g. root@pam
    - auth.token-id : the token NAME only, e.g. backup  (no "@" or "!")
    - auth.secret   : the token's secret UUID value
  Confirm the token exists and is enabled: Datacenter → Permissions → API Tokens.
  Run 'pve context validate' to catch a malformed context before it hits the API.`

// forbiddenHint is printed after a 403 so the operator knows the credentials
// authenticated but lack the privilege for the requested path — most often the
// token's own ACL, not the owning user's.
const forbiddenHint = `hint: permission denied (HTTP 403) — the credentials authenticated but lack privileges for this path.
  Grant an ACL role at the required path: Datacenter → Permissions.
  API tokens have "Privilege Separation" enabled by default, so the token needs its
  OWN ACL entry — granting the owning user access is not enough.`

// AuthHint returns an actionable, multi-line hint for authentication and
// authorisation failures, or "" when err is not an auth error. The auth
// classification is shared with the process exit code via exitcode.FromError,
// so the hint appears for exactly the errors that map to exitcode.Auth.
func AuthHint(err error) string {
	if err == nil || exitcode.FromError(err) != exitcode.Auth {
		return ""
	}

	if isForbidden(err) {
		return forbiddenHint
	}

	return unauthorizedHint
}

// isForbidden reports whether err represents a 403 (as opposed to a 401). The
// library maps every 403 to either the ErrForbidden sentinel or a typed
// PermissionError, so those two checks are exhaustive.
func isForbidden(err error) bool {
	if errors.Is(err, pveerrors.ErrForbidden) {
		return true
	}

	var permErr *pveerrors.PermissionError

	return errors.As(err, &permErr)
}
