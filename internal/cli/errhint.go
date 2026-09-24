package cli

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	pveerrors "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/errors"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/exitcode"
)

// unauthorizedHint is printed after a 401 so the operator knows the request
// reached Proxmox but the credentials were rejected, and how to inspect them.
const unauthorizedHint = `hint: authentication failed (HTTP 401): Proxmox rejected the credentials.
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
const forbiddenHint = `hint: permission denied (HTTP 403): the credentials authenticated but lack privileges for this path.
  Grant an ACL role at the required path: Datacenter → Permissions.
  API tokens have "Privilege Separation" enabled by default, so the token needs its
  OWN ACL entry; granting the owning user access is not enough.`

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

// hintTarget is the endpoint a connection hint names: the host, the port,
// and the product whose default ports the port is judged against, together
// with the route the connection took and where an overridden endpoint came
// from.
type hintTarget struct {
	host    string
	port    int
	product string

	// via is the route Connection.Via names, or "direct" when the hint reads
	// a bare context.
	via string

	// source names the flag or variable that supplied an overridden
	// endpoint, such as "--api-endpoint", and is empty when the context
	// supplied it.
	source string

	// jump and proxy report whether the route went through a bastion and
	// through a proxy, so the advice names only the hops the route has.
	jump, proxy bool
}

// routeAdvice returns the sentence that asks the operator to check the hops
// between pmx and the API host, naming the bastion, the proxy, or both.
func (t hintTarget) routeAdvice() string {
	switch {
	case t.jump && t.proxy:
		return "confirm that the bastion and the proxy accept connections and can reach " + t.address() + "."
	case t.jump:
		return "confirm that the bastion accepts connections and can itself reach " + t.address() + "."
	default:
		return "confirm that the proxy accepts connections and can itself reach " + t.address() + "."
	}
}

// direct reports whether the connection dialled the API host itself, with no
// bastion or proxy in between. An environment proxy that does not apply to
// the API URL reads "direct (environment proxy not applicable)", which is a
// direct dial too.
func (t hintTarget) direct() bool {
	return strings.HasPrefix(t.via, "direct")
}

// address renders the target as host:port, with an IPv6 literal in
// brackets whether or not the host carried them.
func (t hintTarget) address() string {
	return net.JoinHostPort(UnbracketHost(t.host), strconv.Itoa(t.port))
}

// resolveHintTarget returns the endpoint a hint names. When route carries a
// host, which it does whenever the root built a client, the hint reads the
// resolved connection, so an endpoint override, a bastion, or a proxy is
// named as it was used. Otherwise, as for a noClient command or a Deps a
// test builds by hand, it reads ctx exactly as the hints always have. Either
// way the product's default port stands in for an unset one.
func resolveHintTarget(ctx *config.Context, route Connection) hintTarget {
	product := ctx.ProductOrDefault()

	target := hintTarget{host: ctx.Host, port: ctx.Port, product: product, via: "direct"}
	if route.Host != "" {
		target = hintTarget{
			host: route.Host, port: route.Port, product: product,
			via: route.Via(), source: route.EndpointSource,
			jump: route.hasJump(), proxy: route.usesProxy(),
		}
	}

	if target.port == 0 {
		target.port = config.DefaultPortForProduct(product)
	}

	return target
}

// PortConventionHint returns a one-line hint when err is a connection-level
// failure (dial, TLS handshake, timeout — never an HTTP-status error) and the
// port the invocation dialled is a DIFFERENT product's well-known default,
// the classic symptom of "right host, wrong product". It returns "" in every
// other case: hinting must never fire on an auth failure or an API error,
// where the connection itself worked. cmdPrefix is the persona-aware command
// prefix (see CommandPrefix) used to compose the follow-up command.
//
// route is the Connection the root's client dialled. When it carries a host,
// the port judged is route's, so a port an endpoint override chose is the
// one named, together with the flag or variable it came from. When route is
// the zero Connection, the hint reads ctx's port exactly as it always has.
func PortConventionHint(err error, ctx *config.Context, route Connection, contextName, cmdPrefix string) string {
	if err == nil || ctx == nil || !isConnectionError(err) {
		return ""
	}

	target := resolveHintTarget(ctx, route)
	if target.port == config.DefaultPortForProduct(target.product) {
		return ""
	}

	for _, other := range config.Products() {
		if other == target.product || target.port != config.DefaultPortForProduct(other) {
			continue
		}

		if target.source != "" {
			check := "the endpoint you passed"
			if isEnvSource(target.source) {
				check = "the endpoint in " + target.source
			}

			return fmt.Sprintf(
				"hint: port %d from %s is the %s default; context %q is set to product %s: check %s",
				target.port, target.source, ProductDisplayName(other), contextName, target.product, check,
			)
		}

		return fmt.Sprintf(
			"hint: port %d is the %s default; context %q is set to product %s: check '%s context show %s'",
			target.port, ProductDisplayName(other), contextName, target.product, cmdPrefix, contextName,
		)
	}

	return ""
}

// UnreachableHint returns a multi-line hint when err is a connection-level
// failure, naming the address that was actually dialed and how to check it. It
// is the general case behind PortConventionHint: callers should prefer the
// port hint when it fires, because "right host, wrong product port" is a more
// specific diagnosis than "unreachable".
//
// A hostname that resolves to a CDN or reverse proxy is a common cause: the
// name answers, but nothing accepts the API port, so the dial is dropped rather
// than refused and the failure looks like a hang.
//
// route is the Connection the root's client dialled. When it carries a host,
// the hint names route's host and port, so an endpoint override is named
// rather than the stored endpoint, along with the flag or variable it came
// from. When the route went through a bastion or a proxy, the hint names the
// route instead of suggesting dig and nc, which would test a path the
// connection never took, and when err holds an *apiclient.JumpError it
// prints the bastion's own account through Detail, which is never empty, so
// a silent ssh failure still names its exit status. When route is the zero
// Connection, the hint reads ctx exactly as it always has.
//
// The dig and nc commands take an IPv6 literal without its brackets, which
// neither tool accepts, while the hint's first line keeps them, as in
// "[::1]:8006".
func UnreachableHint(err error, ctx *config.Context, route Connection, contextName, cmdPrefix string) string {
	if err == nil || ctx == nil || !isConnectionError(err) {
		return ""
	}

	target := resolveHintTarget(ctx, route)

	var b strings.Builder

	if !target.direct() {
		fmt.Fprintf(&b, "hint: could not reach %s via %s: the connection was never established.\n",
			target.address(), target.via)

		if jumpErr, ok := errors.AsType[*apiclient.JumpError](err); ok {
			fmt.Fprintf(&b, "  The bastion reported: %s\n", jumpErr.Detail())
		}

		fmt.Fprintf(&b, "  The failure may lie in the route rather than the node:\n  %s\n", target.routeAdvice())
		writeEndpointAdvice(&b, target, contextName, cmdPrefix)

		return b.String()
	}

	shellHost := UnbracketHost(target.host)

	fmt.Fprintf(&b, "hint: could not reach %s: the connection was never established.\n", target.address())
	b.WriteString("  Nothing is listening there, or a firewall or proxy is dropping the connection.\n")
	b.WriteString("  Confirm the name resolves to the node itself, not a CDN or reverse proxy:\n")
	fmt.Fprintf(&b, "    dig +short %s\n", shellHost)
	b.WriteString("  Check that the API port accepts connections:\n")
	fmt.Fprintf(&b, "    nc -vz %s %d\n", shellHost, target.port)
	writeEndpointAdvice(&b, target, contextName, cmdPrefix)

	return b.String()
}

// writeEndpointAdvice ends an unreachable hint by saying where the endpoint
// came from: the flag or variable that overrode it, or the commands that
// inspect and repoint the context. It writes no trailing newline.
func writeEndpointAdvice(b *strings.Builder, target hintTarget, contextName, cmdPrefix string) {
	if target.source != "" {
		fmt.Fprintf(b, "  The endpoint came from %s rather than from context %q; inspect the context with:\n",
			target.source, contextName)
		fmt.Fprintf(b, "    %s context show %s", cmdPrefix, contextName)

		return
	}

	b.WriteString("  Inspect or repoint the context:\n")
	fmt.Fprintf(b, "    %s context show %s\n", cmdPrefix, contextName)
	fmt.Fprintf(b, "    %s context update %s --host <address>", cmdPrefix, contextName)
}

// UnbracketHost returns host without the brackets an IPv6 literal carries in
// a URL, which is the form dig, nc, and net.JoinHostPort each expect, and
// the form in which a bracketed and a bare spelling of one address compare
// equal. Any other host is returned unchanged.
func UnbracketHost(host string) string {
	if len(host) >= 2 && host[0] == '[' && host[len(host)-1] == ']' {
		return host[1 : len(host)-1]
	}

	return host
}

// isConnectionError reports whether err is a connection-level failure: a
// dial/socket error, a failure of an ssh jump, a TLS record-header failure
// (HTTPS spoken to a non-TLS or wrong-protocol port), or a network timeout.
// HTTP-status errors are deliberately excluded — they prove the connection
// worked.
func isConnectionError(err error) bool {
	if _, ok := errors.AsType[*net.OpError](err); ok {
		return true
	}

	if errors.Is(err, apiclient.ErrJump) {
		return true
	}

	if _, ok := errors.AsType[tls.RecordHeaderError](err); ok {
		return true
	}

	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
