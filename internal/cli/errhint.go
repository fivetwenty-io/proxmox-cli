package cli

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"

	pveerrors "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/errors"

	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/exitcode"
)

// unauthorizedHint is printed after a 401 so the operator knows the request
// reached Proxmox but the credentials were rejected, and how to inspect them.
const unauthorizedHint = `hint: authentication failed (HTTP 401) — Proxmox rejected the credentials.
  Inspect the active context:   pmx context show
  For token auth the API header is USER@REALM!TOKENID=SECRET, split across three fields:
    - auth.username : the user and realm, e.g. root@pam
    - auth.token-id : the token NAME only, e.g. backup  (no "@" or "!")
    - auth.secret   : the token's secret UUID value
  Confirm the token exists and is enabled: Datacenter → Permissions → API Tokens.
  Run 'pmx context validate' to catch a malformed context before it hits the API.`

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

// PortConventionHint returns a one-line hint when err is a connection-level
// failure (dial, TLS handshake, timeout — never an HTTP-status error) and the
// resolved context's port is a DIFFERENT product's well-known default, the
// classic symptom of "right host, wrong product". It returns "" in every
// other case: hinting must never fire on an auth failure or an API error,
// where the connection itself worked. cmdPrefix is the persona-aware command
// prefix (see CommandPrefix) used to compose the follow-up command.
func PortConventionHint(err error, ctx *config.Context, contextName, cmdPrefix string) string {
	if err == nil || ctx == nil || !isConnectionError(err) {
		return ""
	}

	product := ctx.Product
	if product == "" {
		product = config.ProductPVE
	}
	if ctx.Port == 0 || ctx.Port == config.DefaultPortForProduct(product) {
		return ""
	}

	for _, other := range config.Products() {
		if other == product {
			continue
		}
		if ctx.Port == config.DefaultPortForProduct(other) {
			return fmt.Sprintf(
				"hint: port %d is the %s default; context %q is set to product %s — check '%s context show %s'",
				ctx.Port, ProductDisplayName(other), contextName, product, cmdPrefix, contextName,
			)
		}
	}

	return ""
}

// isConnectionError reports whether err is a connection-level failure: a
// dial/socket error, a TLS record-header failure (HTTPS spoken to a non-TLS
// or wrong-protocol port), or a network timeout. HTTP-status errors are
// deliberately excluded — they prove the connection worked.
func isConnectionError(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	var recErr tls.RecordHeaderError
	if errors.As(err, &recErr) {
		return true
	}

	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
