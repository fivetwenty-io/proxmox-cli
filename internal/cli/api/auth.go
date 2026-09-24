package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
)

// ticketLifetime is the PVE ticket validity window used to compute a session's
// expiry timestamp; PVE tickets are valid for two hours.
const ticketLifetime = 2 * time.Hour

// newAuthCmd builds the canonical `pmx auth` command and its sub-commands.
func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate against a context",
		Long: "Manage local credentials and session state for a context.\n\n" +
			"  status         what is configured, read from the config file\n" +
			"  set-token      store an API token id and secret\n" +
			"  set-password   store a username and password reference\n" +
			"  login          negotiate a session ticket with the server\n" +
			"  refresh        replace an existing session ticket\n" +
			"  logout         invalidate the ticket and drop it locally\n" +
			"  whoami         ask the server who the credentials are\n\n" +
			"Every verb works against any context, whether it targets Proxmox VE, Proxmox " +
			"Backup Server, or Proxmox Datacenter Manager.\n\n" +
			"Two-factor authentication differs by product. --otp, a one-time password for " +
			"TOTP, is PVE-only; PBS and PDM contexts use --tfa-challenge instead.",
		Example: `  pmx auth login --context lab
  pmx auth login --context lab --oidc --realm sso
  pmx auth whoami --context lab
  pmx auth status --context lab`,
	}
	cmd.AddCommand(
		newAuthLoginCmd(),
		newAuthLogoutCmd(),
		newAuthStatusCmd(),
		newAuthWhoamiCmd(),
		newAuthRefreshCmd(),
		newAuthSetTokenCmd(),
		newAuthSetPasswordCmd(),
	)
	return noClient(cmd)
}

// resolveContextName returns the context name the auth verbs operate on.
//
// deps.CtxName already carries the root's resolution, in --context/-c >
// $PMX_CONTEXT > current-context order. These verbs used to register their
// OWN --context flag and read that instead. A local flag of the same name
// shadows the root's persistent one and takes its shorthand with it, so
// `pmx -c lab auth status` failed outright with "unknown shorthand flag:
// 'c'", and $PMX_CONTEXT was ignored, so `pmx auth set-token` could store a
// credential against a context the user had not named while the rest of the
// invocation targeted the one they had.
func resolveContextName(deps *cli.Deps) (string, error) {
	if deps.CtxName != "" {
		return deps.CtxName, nil
	}
	return "", fmt.Errorf("no context specified: use --context or set a current context")
}

// lookupContext returns the context stored in cfg under name, or an error if
// it is absent. It applies no defaults and runs no validation, because the
// pointer it returns is the write handle: every change an auth verb makes,
// a new session, a cleared session, or a new credential, is written to it
// before config.SaveForce stores cfg. No verb reads a connection value off
// it. The verbs that dial pass it to contextOptions and read the endpoint
// and the route off the cli.Connection that returns, and auth status
// resolves it with cli.ResolveConnection, which works on a copy.
func lookupContext(cfg *config.Config, name string) (*config.Context, error) {
	if cfg == nil {
		return nil, fmt.Errorf("context %q not found: no configuration is loaded", name)
	}
	c, ok := cfg.Contexts[name]
	if !ok || c == nil {
		return nil, fmt.Errorf("context %q not found", name)
	}
	return c, nil
}

// refuseAmbientEndpointHost stops a verb that stores a session from storing
// one obtained from a host the operator did not name on the command line.
// When $PMX_API_ENDPOINT supplied the endpoint and its host differs from the
// stored host, once IPv6 brackets are stripped from both and case is
// ignored, the session would be written to the context while having come
// from somewhere else, so the verb refuses. The same host passed as
// --api-endpoint is the operator's explicit choice and passes, as does an
// environment value that changes only the port or the scheme. verb is the
// auth sub-command's name, as in "login".
func refuseAmbientEndpointHost(deps *cli.Deps, stored *config.Context, name, verb string) error {
	ov, err := deps.ConnectionOverrides()
	if err != nil {
		return err
	}
	// cli.OverridesFromCommand names an environment source with its "$",
	// as in "$PMX_API_ENDPOINT", and a flag source without one.
	if !strings.HasPrefix(ov.EndpointSource, "$") || ov.Host == "" || sameHost(ov.Host, stored.Host) {
		return nil
	}
	return fmt.Errorf("%s points context %q at %s instead of %s; auth %s stores a session, "+
		"so pass --api-endpoint on the command line to confirm",
		ov.EndpointSource, name, ov.Host, displayHost(stored.Host), verb)
}

// sameHost reports whether a and b name the same host, comparing them
// without IPv6 brackets and without regard to case, since host names are
// case-insensitive.
func sameHost(a, b string) bool {
	return strings.EqualFold(cli.UnbracketHost(a), cli.UnbracketHost(b))
}

// displayHost returns host for an error message, naming an empty one
// explicitly rather than printing nothing.
func displayHost(host string) string {
	if host == "" {
		return "no host"
	}
	return host
}

// endpointURL renders protocol, host, and port as scheme://host:port,
// bracketing an IPv6 literal whether or not host already carries brackets.
// It returns "" when host is empty, because there is no endpoint to render.
func endpointURL(protocol, host string, port int) string {
	h := cli.UnbracketHost(host)
	if h == "" {
		return ""
	}
	return protocol + "://" + net.JoinHostPort(h, strconv.Itoa(port))
}

// storedEndpointURL renders the endpoint the stored context names, with the
// product's default port, the https default, and the other defaults applied
// to a copy, so stored is never modified. It deliberately ignores every
// --api-* override: the OpenID redirect it feeds is the identity registered
// with the provider, not an address the browser must reach.
func storedEndpointURL(stored *config.Context) string {
	d := config.CloneContext(stored)
	config.ApplyDefaults(d)
	return endpointURL(d.Protocol, d.Host, d.Port)
}

// renderResult renders res through the invocation's renderer, falling back
// to the default renderer and the table format when a hand-built Deps left
// them unset, so a verb never dereferences a nil renderer.
func renderResult(cmd *cobra.Command, deps *cli.Deps, res output.Result) error {
	out := deps.Out
	if out == nil {
		out = output.New()
	}
	format := deps.Format
	if format == "" {
		format = output.FormatTable
	}
	return out.Render(cmd.OutOrStdout(), res, format)
}

// newAuthLoginCmd builds `pmx auth login`.
func newAuthLoginCmd() *cobra.Command {
	var (
		username     string
		realm        string
		password     string
		otp          string
		tfaChallenge string
		verifyPath   string
		verifyPrivs  string
		// OIDC flags
		oidc        bool
		redirectURL string
		code        string
		state       string
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Obtain a session ticket and store it in the config",
		Long: "Authenticate against a context's realm (password, TOTP/two-factor, or OpenID " +
			"Connect via --oidc) and store the resulting session ticket in the config. " +
			"Works with Proxmox VE (PVE), Proxmox Backup Server (PBS), and Proxmox Datacenter " +
			"Manager (PDM) contexts.\n\n" +
			"--otp (one-time password for TOTP-based two-factor authentication) is PVE-only; " +
			"PBS and PDM contexts use --tfa-challenge for second-factor login instead. " +
			"--oidc works with PVE, PBS, and PDM contexts.\n\n" +
			"The --api-* flags and PMX_API_* variables change where this login connects, " +
			"and the session is still stored on the context. When $PMX_API_ENDPOINT names " +
			"a different host from the one the context stores, the login refuses, because " +
			"it would store a session from a host you did not name on the command line; " +
			"pass that host as --api-endpoint to confirm it.",
		Example: `  pmx auth login --context lab
  pmx auth login --context lab --username alice@pve --realm pve
  pmx auth login --context lab --oidc --realm sso`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{cli.AnnotationUsesConnection: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			var conn cli.Connection
			err := func() error {
				deps := cli.GetDeps(cmd)
				cfg := deps.Cfg

				name, err := resolveContextName(deps)
				if err != nil {
					return err
				}
				stored, err := lookupContext(cfg, name)
				if err != nil {
					return err
				}
				if err := refuseAmbientEndpointHost(deps, stored, name, "login"); err != nil {
					return err
				}

				if oidc {
					return performOIDCLogin(cmd, deps, cfg, stored, name, realm, redirectURL, code, state, &conn)
				}

				user := firstNonEmpty(username, stored.Auth.Username)
				rlm := firstNonEmpty(realm, stored.Realm, "pam")
				pw, err := resolvePassword(password, stored)
				if err != nil {
					return err
				}

				var resp *ticketResult
				resp, conn, err = createTicket(cmd, stored, name, user, rlm, pw, ticketOptions{
					otp:          otp,
					tfaChallenge: tfaChallenge,
					verifyPath:   verifyPath,
					verifyPrivs:  verifyPrivs,
				})
				if err != nil {
					return err
				}

				storeSession(stored, resp)
				if err := config.SaveForce(configPath(cmd), cfg); err != nil {
					return fmt.Errorf("save config: %w", err)
				}

				expires := time.Now().Add(ticketLifetime).Format(time.RFC3339)
				msg := fmt.Sprintf("Logged in as %s. Session valid until %s.",
					resp.Username, expires)
				return renderResult(cmd, deps, output.Result{Message: msg})
			}()
			return conn.WrapPinMismatch(err)
		},
	}

	cmd.Flags().StringVar(&username, "username", "", "username (defaults to context's username)")
	cmd.Flags().StringVar(&realm, "realm", "", "authentication realm (defaults to context's realm)")
	cmd.Flags().StringVar(&password, "password", "", "password (defaults to the context's resolved secret)")
	cmd.Flags().StringVar(&otp, "otp", "", "one-time password for TOTP-based two-factor authentication")
	cmd.Flags().StringVar(&tfaChallenge, "tfa-challenge", "", "signed challenge response for second-step two-factor authentication")
	cmd.Flags().StringVar(&verifyPath, "path", "", "ticket-verification mode: ACL path to check privileges on (requires --privs)")
	cmd.Flags().StringVar(&verifyPrivs, "privs", "", "ticket-verification mode: privileges to verify on --path")
	cmd.Flags().BoolVar(&oidc, "oidc", false, "authenticate via OpenID Connect instead of username/password")
	cmd.Flags().StringVar(&redirectURL, "redirect-url", "",
		"OIDC redirect URL sent to the identity provider; it defaults to the context's stored endpoint "+
			"even under --api-endpoint, so a tunnelled login needs no extra flag, and --redirect-url "+
			"overrides it (requires --oidc)")
	cmd.Flags().StringVar(&code, "code", "",
		"OIDC authorization code for non-interactive login (requires --oidc and --state)")
	cmd.Flags().StringVar(&state, "state", "",
		"OIDC state parameter for non-interactive login (requires --oidc and --code)")
	return noClient(cmd)
}

// performOIDCLogin carries out the OpenID Connect login flow for the given context.
// It calls OpenidAuthUrl to obtain the authorization URL, then either reads
// code+state from the supplied flags (non-interactive) or prompts the user to
// authenticate in a browser and paste the redirect URL (interactive). On success it
// persists the resulting ticketResult via storeSession and saves the config file.
// Works with PVE, PBS, and PDM contexts via the authClient seam (see
// buildClientForOIDC and authproducts.go).
//
// stored is the write handle lookupContext returned. The realm and the
// default redirect URL are read off it, and the session is written to it.
// conn receives the connection the client was built on as soon as it is
// resolved, so the caller can pass any later error through
// conn.WrapPinMismatch.
func performOIDCLogin(
	cmd *cobra.Command,
	deps *cli.Deps,
	cfg *config.Config,
	stored *config.Context,
	contextName string,
	realmFlag string,
	redirectURLFlag string,
	code string,
	state string,
	conn *cli.Connection,
) error {
	fl := cmd.Flags()

	// Flags incompatible with OIDC.
	if fl.Changed("password") {
		return fmt.Errorf("--password cannot be used with --oidc")
	}
	if fl.Changed("otp") {
		return fmt.Errorf("--otp cannot be used with --oidc")
	}
	if fl.Changed("tfa-challenge") {
		return fmt.Errorf("--tfa-challenge cannot be used with --oidc")
	}

	// --code and --state must be supplied together for non-interactive login.
	if fl.Changed("code") != fl.Changed("state") {
		return fmt.Errorf("--code and --state must both be supplied for non-interactive OIDC login")
	}

	// Realm is required for OIDC; do NOT fall back to "pam" (which is not an
	// OIDC realm). stored carries no defaults, so an empty stored realm stays
	// empty here and the login refuses.
	rlm := firstNonEmpty(realmFlag, stored.Realm)
	if rlm == "" {
		return fmt.Errorf("OIDC login requires --realm or a realm configured in context %q", contextName)
	}

	// Determine the redirect URL sent to the identity provider. It is the
	// stored endpoint with its defaults applied, never an --api-* override:
	// it is the identity registered with the provider, and the operator
	// pastes the result back from the address bar rather than the browser
	// having to reach it.
	redir := redirectURLFlag
	if redir == "" {
		redir = storedEndpointURL(stored)
		if redir == "" {
			return fmt.Errorf("context %q stores no host, so the OIDC redirect URL has no default; "+
				"pass --redirect-url", contextName)
		}
	}

	// Build the product-appropriate auth client for the context. The OIDC
	// auth-url and login endpoints are public on PVE, PBS, and PDM alike (all
	// three mark them noauthentication), so any credential satisfies the HTTP
	// transport — the server does not validate credentials on these paths.
	// Prefer an existing session ticket; fall back to a placeholder token that
	// passes the proxmox-apiclient-go options validator without real credentials.
	ac, c, err := buildClientForOIDC(cmd, stored, contextName)
	*conn = c
	if err != nil {
		return err
	}

	// Step 1: obtain the OIDC authorization URL.
	authURL, err := ac.OpenidAuthUrl(cmd.Context(), rlm, redir)
	if err != nil {
		return fmt.Errorf("get OIDC auth URL for realm %q: %w", rlm, err)
	}
	if authURL == "" {
		return fmt.Errorf("get OIDC auth URL for realm %q: server returned empty URL", rlm)
	}

	// Step 2: obtain code + state.
	var oidcCode, oidcState string
	if fl.Changed("code") {
		// Non-interactive path: use the supplied flags directly.
		oidcCode = code
		oidcState = state
	} else {
		// Interactive path: print the URL, ask the user to authenticate and paste the redirect.
		_, _ = fmt.Fprintf(cmd.OutOrStdout(),
			"Open the following URL in your browser to authenticate:\n\n  %s\n\nAfter authenticating, paste the full redirect URL here and press Enter:\n",
			authURL)
		reader := bufio.NewReader(cmd.InOrStdin())
		pasted, readErr := reader.ReadString('\n')
		pasted = strings.TrimSpace(pasted)
		if pasted == "" && readErr != nil {
			return fmt.Errorf("read redirect URL from stdin: %w", readErr)
		}
		// io.EOF on last line without trailing newline is acceptable if we got data.
		if readErr != nil && readErr != io.EOF {
			return fmt.Errorf("read redirect URL from stdin: %w", readErr)
		}
		oidcCode, oidcState, err = parseOIDCRedirect(pasted)
		if err != nil {
			return err
		}
	}

	// Step 3: complete the OIDC login. RedirectUrl must equal the one from step 1.
	result, err := ac.OpenidLogin(cmd.Context(), oidcCode, oidcState, redir)
	if err != nil {
		return fmt.Errorf("OIDC login for realm %q: %w", rlm, err)
	}
	if result.Ticket == "" {
		return fmt.Errorf("OIDC login for realm %q: server returned no ticket", rlm)
	}
	if result.Username == "" {
		return fmt.Errorf("OIDC login for realm %q: server returned no username", rlm)
	}

	// Persist the session identically to password login.
	storeSession(stored, result)
	if err := config.SaveForce(configPath(cmd), cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	expires := time.Now().Add(ticketLifetime).Format(time.RFC3339)
	msg := fmt.Sprintf("Logged in as %s via OIDC. Session valid until %s.", result.Username, expires)
	return renderResult(cmd, deps, output.Result{Message: msg})
}

// parseOIDCRedirect extracts the code and state query parameters from a full
// OIDC redirect URL (the URL the identity provider sends the browser to after
// authentication). Returns an error if the URL cannot be parsed or if either
// required parameter is absent.
//
// No error names the raw URL: its query string carries the authorization
// code, which is a single-use credential, and these errors reach both the
// terminal and the JSONL exit record. redactRedirectURL keeps the part an
// operator needs to recognise which URL they pasted.
func parseOIDCRedirect(rawURL string) (code, state string, err error) {
	if rawURL == "" {
		return "", "", fmt.Errorf("redirect URL is empty")
	}
	u, parseErr := url.Parse(rawURL)
	if parseErr != nil {
		// *url.Error embeds the offending URL in its own message, so only its
		// wrapped reason is safe to surface here.
		reason := parseErr
		if urlErr, ok := errors.AsType[*url.Error](parseErr); ok {
			reason = urlErr.Err
		}
		return "", "", fmt.Errorf("parse redirect URL %q: %w", redactRedirectURL(rawURL), reason)
	}
	q := u.Query()
	code = q.Get("code")
	state = q.Get("state")
	if code == "" {
		return "", "", fmt.Errorf("redirect URL %q is missing the 'code' query parameter",
			redactRedirectURL(rawURL))
	}
	if state == "" {
		return "", "", fmt.Errorf("redirect URL %q is missing the 'state' query parameter",
			redactRedirectURL(rawURL))
	}
	return code, state, nil
}

// redactRedirectURL strips the query and fragment from an OIDC redirect URL,
// leaving scheme, host, and path. A URL that will not parse is reported as
// its portion before the first "?", since nothing beyond that can be trusted
// to be free of the code.
func redactRedirectURL(rawURL string) string {
	if i := strings.IndexAny(rawURL, "?#"); i >= 0 {
		return rawURL[:i] + "?<redacted>"
	}
	return rawURL
}

// newAuthRefreshCmd builds `pmx auth refresh`, re-obtaining a session ticket
// for a password context.
func newAuthRefreshCmd() *cobra.Command {
	var (
		tfaChallenge string
	)

	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Refresh the session ticket for a password context",
		Long: "Re-authenticate against a password context's realm and replace the stored " +
			"session ticket. The configured credentials are left as they are.\n\n" +
			"This applies only to contexts using password authentication, meaning " +
			"auth.type is \"password\". On any other type, token contexts included, the " +
			"command errors out.\n\n" +
			"Pass --tfa-challenge to answer a second-factor challenge when the realm asks " +
			"for one.\n\n" +
			"The refreshed ticket and its expiry are written to the config file.\n\n" +
			"The --api-* flags and PMX_API_* variables change where the refresh connects. " +
			"As with auth login, a $PMX_API_ENDPOINT that names a different host from the " +
			"stored one is refused; pass that host as --api-endpoint to confirm it.",
		Example: `  pmx auth refresh --context lab
  pmx auth refresh --context lab --tfa-challenge <response>`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{cli.AnnotationUsesConnection: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			var conn cli.Connection
			err := func() error {
				deps := cli.GetDeps(cmd)
				cfg := deps.Cfg

				name, err := resolveContextName(deps)
				if err != nil {
					return err
				}
				stored, err := lookupContext(cfg, name)
				if err != nil {
					return err
				}
				if stored.Auth.Type != "password" {
					return fmt.Errorf("context %q uses %q auth; refresh applies only to password contexts",
						name, stored.Auth.Type)
				}
				if err := refuseAmbientEndpointHost(deps, stored, name, "refresh"); err != nil {
					return err
				}

				pw, err := resolvePassword("", stored)
				if err != nil {
					return err
				}
				rlm := firstNonEmpty(stored.Realm, "pam")

				var resp *ticketResult
				resp, conn, err = createTicket(cmd, stored, name, stored.Auth.Username, rlm, pw, ticketOptions{
					tfaChallenge: tfaChallenge,
				})
				if err != nil {
					return err
				}

				storeSession(stored, resp)
				if err := config.SaveForce(configPath(cmd), cfg); err != nil {
					return fmt.Errorf("save config: %w", err)
				}

				expires := time.Now().Add(ticketLifetime).Format(time.RFC3339)
				msg := fmt.Sprintf("Refreshed session for %s. Valid until %s.",
					resp.Username, expires)
				return renderResult(cmd, deps, output.Result{Message: msg})
			}()
			return conn.WrapPinMismatch(err)
		},
	}

	cmd.Flags().StringVar(&tfaChallenge, "tfa-challenge", "", "signed challenge response for second-step two-factor authentication")
	return noClient(cmd)
}

// newAuthLogoutCmd builds `pmx auth logout`.
func newAuthLogoutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Invalidate the session ticket and remove it from the config",
		Long: "Invalidate a context's session ticket and remove it from the config file.\n\n" +
			"When a live ticket is present, the server is asked to invalidate it first. If " +
			"that fails, the command errors and leaves the local session untouched, so the " +
			"two never drift apart.\n\n" +
			"Once the server has invalidated the ticket, or there was none to invalidate, " +
			"the local session is cleared and the config saved.\n\n" +
			"Only the session ticket goes. Configured API-token and password credentials on " +
			"the context stay intact.\n\n" +
			"The --api-* flags and PMX_API_* variables change where the server-side " +
			"invalidation connects. Clearing the local session never needs a connection.",
		Example:     `  pmx auth logout --context lab`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{cli.AnnotationUsesConnection: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			var conn cli.Connection
			err := func() error {
				deps := cli.GetDeps(cmd)
				cfg := deps.Cfg

				name, err := resolveContextName(deps)
				if err != nil {
					return err
				}
				stored, err := lookupContext(cfg, name)
				if err != nil {
					return err
				}

				// If a live session exists, invalidate it server-side first.
				if stored.Auth.Session != nil && stored.Auth.Session.Ticket != "" {
					conn, err = serverLogout(cmd, stored, name)
					if err != nil {
						return err
					}
				}

				stored.Auth.Session = nil
				if err := config.SaveForce(configPath(cmd), cfg); err != nil {
					return fmt.Errorf("save config: %w", err)
				}

				return renderResult(cmd, deps,
					output.Result{Message: fmt.Sprintf("Logged out from context %q.", name)})
			}()
			return conn.WrapPinMismatch(err)
		},
	}

	return noClient(cmd)
}

// newAuthStatusCmd builds `pmx auth status`.
func newAuthStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show authentication status for a context",
		Long: "Display the authentication configuration and session state for a " +
			"context.\n\n" +
			"  * Host, the endpoint this invocation would connect to, marked with its source " +
			"when an --api-endpoint flag or $PMX_API_ENDPOINT overrides the stored one\n" +
			"  * Product\n" +
			"  * Via, the route to the API, marked with the source of any jump or proxy override. " +
			"It is shown when the route is not direct, and also when the context asks for an " +
			"environment proxy that does not apply to the API URL\n" +
			"  * Auth-type, the configured auth type\n" +
			"  * Username or Token-ID\n" +
			"  * Secret-source and Resolved, where the secret comes from and whether it resolves\n" +
			"  * Proxy-secret-source and Proxy-resolved, the same two answers for " +
			"proxy.password, shown only when the context sets one\n" +
			"  * Session, how much validity the session ticket has left\n" +
			"  * Connection, shown only when the context's connection settings or the " +
			"--api-* overrides cannot be resolved, carrying the reason\n\n" +
			"This reads the local config file and never contacts the Proxmox API. To verify " +
			"the credentials against the server, use `auth whoami`. A connection that cannot " +
			"be resolved is reported in the Connection row, and the command still exits 0. Any " +
			"note about a PMX_API_* variable that overrides the context goes to standard error.\n\n" +
			"Secret values are never displayed, only their source: an inline literal, an " +
			"environment variable, a keychain reference, or a $NAME reference whose variable " +
			"is unset and which therefore resolves as a literal.",
		Example:     `  pmx auth status --context lab`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{cli.AnnotationUsesConnection: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			name, err := resolveContextName(deps)
			if err != nil {
				return err
			}
			stored, err := lookupContext(cfg, name)
			if err != nil {
				return err
			}

			// Display values come from a defaults-applied copy, so stored is
			// never modified.
			shown := config.CloneContext(stored)
			config.ApplyDefaults(shown)

			single := map[string]string{
				"Context":       name,
				"Host":          firstNonEmpty(endpointURL(shown.Protocol, shown.Host, shown.Port), "(none)"),
				"Product":       fmt.Sprintf("%s (%s)", cli.ProductDisplayName(shown.Product), shown.Product),
				"Auth-type":     stored.Auth.Type,
				"Username":      stored.Auth.Username,
				"Token-ID":      stored.Auth.TokenID,
				"Secret-source": secretSource(stored.Auth.Secret),
				"Resolved":      secretResolves(stored.Auth.Secret),
				"Session":       sessionStatus(stored.Auth.Session),
			}

			if stored.Proxy.Password != "" {
				single["Proxy-secret-source"] = secretSource(stored.Proxy.Password)
				single["Proxy-resolved"] = secretResolves(stored.Proxy.Password)
			}

			conn, err := statusConnection(deps, stored, name)
			if err != nil {
				single["Connection"] = err.Error()
			} else {
				single["Host"] = markSource(endpointURL(conn.Protocol, conn.Host, conn.Port), conn.EndpointSource)
				if via := conn.Via(); via != "direct" {
					single["Via"] = markSource(via, routeOverrideSources(conn)...)
				}
				for _, note := range conn.Notes {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), note)
				}
			}

			return renderResult(cmd, deps, output.Result{Single: single})
		},
	}

	return noClient(cmd)
}

// statusConnection resolves the connection auth status reports. It runs the
// proxy and timeout block checks on stored first, as contextOptions does,
// and skips the resolve when either reports a message. It builds no client
// and resolves no secret, because cli.ResolveConnection is pure.
func statusConnection(deps *cli.Deps, stored *config.Context, name string) (cli.Connection, error) {
	if err := checkStoredBlocks(name, stored); err != nil {
		return cli.Connection{}, err
	}
	ov, err := deps.ConnectionOverrides()
	if err != nil {
		return cli.Connection{}, err
	}
	return cli.ResolveConnection(name, stored, ov)
}

// checkStoredBlocks runs config.ValidateProxyBlock and
// config.ValidateTimeoutBlock on stored and returns their messages joined
// with "; " under a `context %q: ` prefix, the shape config.ResolveContext
// gives the same checks on the root path, or nil when both pass. Every URL
// in a message has already passed through redact.ProxyURL, so no message
// can carry a proxy password.
func checkStoredBlocks(name string, stored *config.Context) error {
	var msgs []string
	msgs = append(msgs, config.ValidateProxyBlock(&stored.Proxy)...)
	msgs = append(msgs, config.ValidateTimeoutBlock(&stored.Timeout)...)
	if len(msgs) == 0 {
		return nil
	}
	return fmt.Errorf("context %q: %s", name, strings.Join(msgs, "; "))
}

// routeOverrideSources returns the sources of the per-invocation overrides
// that shaped conn's route: the jump's when an override supplied the chain
// in use, and the proxy's when an override supplied the proxy in use. A
// source that only removed a jump or a proxy is left out, because the route
// no longer carries anything it supplied.
func routeOverrideSources(conn cli.Connection) []string {
	var sources []string
	if strings.TrimSpace(conn.Jump.Chain) != "" && conn.JumpSource != "" {
		sources = append(sources, conn.JumpSource)
	}
	if (conn.Proxy.URL != nil || conn.Proxy.FromEnv) && conn.ProxySource != "" &&
		!slices.Contains(sources, conn.ProxySource) {
		sources = append(sources, conn.ProxySource)
	}
	return sources
}

// markSource appends "(from <sources>)" to value when any source is given,
// as in "https://pve9:9999 (from --api-endpoint)".
func markSource(value string, sources ...string) string {
	var named []string
	for _, s := range sources {
		if s != "" {
			named = append(named, s)
		}
	}
	if len(named) == 0 {
		return value
	}
	return value + " (from " + strings.Join(named, ", ") + ")"
}

// secretResolves reports whether ref resolves through config.ResolveSecret,
// as "yes" or "no (<reason>)". The reason names the variable or keychain
// path that failed and never the secret.
func secretResolves(ref string) string {
	if _, err := config.ResolveSecret(ref); err != nil {
		return "no (" + err.Error() + ")"
	}
	return "yes"
}

// newAuthWhoamiCmd builds `pmx auth whoami`. Unlike the other auth
// sub-commands it requires a live API client (built by the root from the
// resolved context), so it is NOT annotated noClient: it calls
// GET /access/permissions to confirm the stored credentials authenticate and
// reports the effective identity plus the accessible ACL paths. The
// ProductFromContext annotation lets the root build whichever of PVE, PBS,
// or PDM the resolved context targets (see cli.ProductFromContext), and
// RunE selects the client the root populated.
func newAuthWhoamiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show the identity the current credentials authenticate as",
		Long: "Query the server to confirm the stored credentials actually authenticate. " +
			"The call is GET /access/permissions, made against whichever product the " +
			"resolved context targets: Proxmox VE, Proxmox Backup Server, or Proxmox " +
			"Datacenter Manager.\n\n" +
			"The output is the effective identity, a username or a user!token-id under token " +
			"authentication, together with the full accessible-ACL-path payload the server " +
			"returns.\n\n" +
			"This makes a live API call, so it needs a valid session ticket or a resolvable " +
			"credential.",
		Example:     `  pmx auth whoami --context lab`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{cli.ProductAnnotation: cli.ProductFromContext},
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			// The root resolved and built the client for this same name.
			ctx, _, err := config.ResolveContext(deps.Cfg, deps.CtxName)
			if err != nil {
				return err
			}

			var perms *json.RawMessage
			switch {
			case deps.PBS != nil:
				perms, err = deps.PBS.Access.ListPermissions(cmd.Context(), nil)
			case deps.PDM != nil:
				perms, err = deps.PDM.Access.ListPermissions(cmd.Context(), nil)
			case deps.API != nil:
				perms, err = deps.API.Access.ListPermissions(cmd.Context(), nil)
			default:
				return fmt.Errorf("verify credentials for context %q: no API client was built for it", deps.CtxName)
			}
			if err != nil {
				return fmt.Errorf("verify credentials for context %q: %w", deps.CtxName, err)
			}

			single := map[string]string{
				"Context":   deps.CtxName,
				"Auth-type": ctx.Auth.Type,
				"Identity":  authIdentity(ctx),
			}

			return renderResult(cmd, deps, output.Result{Single: single, Raw: perms})
		},
	}

	return cmd
}

// authIdentity returns the display identity for a context: the API token id
// (user!tokenid) for token auth, otherwise the username.
func authIdentity(ctx *config.Context) string {
	if ctx.Auth.Type == "token" && ctx.Auth.TokenID != "" {
		if ctx.Auth.Username != "" {
			return ctx.Auth.Username + "!" + ctx.Auth.TokenID
		}
		return ctx.Auth.TokenID
	}
	return ctx.Auth.Username
}

// newAuthSetTokenCmd builds `pmx auth set-token`.
func newAuthSetTokenCmd() *cobra.Command {
	var (
		tokenID  string
		secret   string
		username string
	)

	cmd := &cobra.Command{
		Use:   "set-token",
		Short: "Configure token authentication for a context",
		Long: "Configure API-token authentication for a context. The command stores the " +
			"token id, in Proxmox's user@realm!token-id form, and its secret; switches the " +
			"context's auth type to \"token\"; and clears any stored session ticket.\n\n" +
			"--token-id and --secret are both required. --username overrides the context's " +
			"configured username when you set it.\n\n" +
			"--secret takes an inline literal, a ${VAR} or $VAR environment-variable " +
			"reference, or a keychain:PATH reference. Prefer a reference: a literal ends up " +
			"in the config file in cleartext.",
		Example: `  pmx auth set-token --context lab --token-id root@pam!ci --secret '${PMX_TOKEN_SECRET}'`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			name, err := resolveContextName(deps)
			if err != nil {
				return err
			}
			stored, err := lookupContext(cfg, name)
			if err != nil {
				return err
			}

			if tokenID == "" {
				return fmt.Errorf("--token-id is required")
			}
			if secret == "" {
				return fmt.Errorf("--secret is required")
			}

			stored.Auth.Type = "token"
			stored.Auth.TokenID = tokenID
			stored.Auth.Secret = secret
			if username != "" {
				stored.Auth.Username = username
			}
			stored.Auth.Session = nil

			if err := config.SaveForce(configPath(cmd), cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			return renderResult(cmd, deps,
				output.Result{Message: fmt.Sprintf("Set token authentication for context %q.", name)})
		},
	}

	cmd.Flags().StringVar(&tokenID, "token-id", "", "API token id (required)")
	cmd.Flags().StringVar(&secret, "secret", "", "token secret reference (required)")
	cmd.Flags().StringVar(&username, "username", "", "PVE username for the token")
	return noClient(cmd)
}

// newAuthSetPasswordCmd builds `pmx auth set-password`.
func newAuthSetPasswordCmd() *cobra.Command {
	var (
		username string
		secret   string
	)

	cmd := &cobra.Command{
		Use:   "set-password",
		Short: "Configure password authentication for a context",
		Long: "Configure password authentication for a context. The command stores the " +
			"username and password reference; switches the context's auth type to " +
			"\"password\"; and clears any configured API token id along with any stored " +
			"session ticket.\n\n" +
			"--username and --secret are both required.\n\n" +
			"--secret takes an inline literal, a ${VAR} or $VAR environment-variable " +
			"reference, or a keychain:PATH reference. Prefer a reference: a literal ends up " +
			"in the config file in cleartext.",
		Example: `  pmx auth set-password --context lab --username root@pam --secret '${PMX_PASSWORD}'`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			name, err := resolveContextName(deps)
			if err != nil {
				return err
			}
			stored, err := lookupContext(cfg, name)
			if err != nil {
				return err
			}

			if username == "" {
				return fmt.Errorf("--username is required")
			}
			if secret == "" {
				return fmt.Errorf("--secret is required")
			}

			stored.Auth.Type = "password"
			stored.Auth.Username = username
			stored.Auth.Secret = secret
			stored.Auth.TokenID = ""
			stored.Auth.Session = nil

			if err := config.SaveForce(configPath(cmd), cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			return renderResult(cmd, deps,
				output.Result{Message: fmt.Sprintf("Set password authentication for context %q.", name)})
		},
	}

	cmd.Flags().StringVar(&username, "username", "", "PVE username (required)")
	cmd.Flags().StringVar(&secret, "secret", "", "password reference (required)")
	return noClient(cmd)
}

// ticketOptions carries the optional two-factor and ticket-verification inputs
// for a CreateTicket request. Empty fields are omitted from the API payload.
type ticketOptions struct {
	otp          string // one-time password for TOTP-based 2FA
	tfaChallenge string // signed challenge response for second-step TFA
	verifyPath   string // ticket-verification: ACL path to check
	verifyPrivs  string // ticket-verification: privileges to check on verifyPath
}

// createTicket builds the product-appropriate auth client for stored and
// requests a session ticket using the given user, realm, password, and
// optional 2FA / ticket-verification inputs. contextName is the resolved
// context name (see resolveContextName), used for the per-context TOFU
// fingerprint cache path and in error text. It returns the connection the
// client was built on, including on a failed request, so the caller can
// pass the error through its WrapPinMismatch, and the "login to" wrapper
// names the host that was dialled, which an endpoint override may have
// changed.
func createTicket(
	cmd *cobra.Command,
	stored *config.Context,
	contextName, user, realm, password string,
	opts ticketOptions,
) (*ticketResult, cli.Connection, error) {
	ac, conn, err := newAuthClientForContext(cmd, stored, contextName, user, realm, password, "", "", "")
	if err != nil {
		return nil, conn, err
	}

	result, err := ac.CreateTicket(cmd.Context(), user, realm, password, opts)
	if err != nil {
		return nil, conn, fmt.Errorf("login to %s: %w", conn.Host, err)
	}
	return result, conn, nil
}

// serverLogout builds a ticket-authenticated client and invalidates the session
// server-side. The session's CSRF token is supplied so the logout (a non-GET
// request) carries the required CSRF-prevention header. contextName is the
// resolved context name (see resolveContextName). It returns the connection
// the client was built on, as createTicket does, and the "logout from"
// wrapper names the host that was dialled.
func serverLogout(cmd *cobra.Command, stored *config.Context, contextName string) (cli.Connection, error) {
	if stored.Auth.Session == nil || stored.Auth.Session.Ticket == "" {
		return cli.Connection{}, fmt.Errorf("context %q has no session to invalidate", contextName)
	}
	ac, conn, err := newAuthClientForContext(cmd, stored, contextName, "", "", "", "",
		stored.Auth.Session.Ticket, stored.Auth.Session.CSRF)
	if err != nil {
		return conn, err
	}
	if err := ac.Logout(); err != nil {
		return conn, fmt.Errorf("logout from %s: %w", conn.Host, err)
	}
	return conn, nil
}

// buildClientForOIDC constructs the product-appropriate authClient (see
// authproducts.go) suitable for calling the two public OIDC endpoints
// (auth-url and login). Those endpoints are marked noauthentication by PVE,
// PBS, and PDM alike, meaning the server does not validate credentials on
// those paths. However, the proxmox-apiclient-go options validator requires
// at least one credential to be set, so:
//   - If the context carries a live session ticket, that ticket is used.
//   - Otherwise a placeholder API token is constructed to pass validation; the
//     token value is never checked by the server on these public endpoints.
//     The tolerant JSON decoding in proxmox-apiclient-go v3.8.1+ accepts this
//     placeholder's "=" form on PBS/PDM contexts too.
//
// contextName is the resolved context name (see resolveContextName); it is used
// for the per-context TOFU fingerprint cache path and in error text, and has
// no bearing on which credential is selected. The connection the client was
// built on is returned alongside it.
func buildClientForOIDC(
	cmd *cobra.Command, stored *config.Context, contextName string,
) (authClient, cli.Connection, error) {
	if stored.Auth.Session != nil && stored.Auth.Session.Ticket != "" {
		return newAuthClientForContext(cmd, stored, contextName, "", "", "", "",
			stored.Auth.Session.Ticket, stored.Auth.Session.CSRF)
	}
	// No live session: build with a placeholder API token.
	return newAuthClientForContext(cmd, stored, contextName, "", "", "",
		"dummy@pam!oidc=00000000-0000-0000-0000-000000000000", // placeholder: satisfies validation only
		"", "")
}

// contextOptions builds the pve.Options an auth verb's client is built from,
// through cli.ContextOptions, the builder every other command uses, so the
// auth path gets the same endpoint and product defaults, the same TLS
// trust wiring (tls.insecure, tls.fingerprint, tls.ca-cert, and trust on
// first use), the same --api-* and PMX_API_* overrides, and the same proxy,
// timeout, and jump transport. user, realm, token, password, ticket, and
// csrf select exactly which credential BuildOptions embeds, so the stored
// auth secret is never read here. contextName names the per-context
// fingerprint cache and appears in error text.
//
// stored is the raw context lookupContext returned. ContextOptions resolves
// it on a copy, so nothing here writes to it. Before resolving, the proxy
// and timeout blocks are checked with config.ValidateProxyBlock and
// config.ValidateTimeoutBlock, and their messages come back joined with
// "; " under a `context %q: ` prefix, the shape config.ResolveContext gives
// the same checks on the root path.
//
// flagInsecure is the resolved global --insecure flag value (cli.Deps.Insecure,
// populated by the root command's PersistentPreRunE before its noClient
// early-return). It is OR'd into the overrides' Insecure, which
// cli.ResolveConnection ORs with tls.insecure in turn, so --insecure on an
// auth sub-command both disables certificate verification and suppresses
// trust on first use, and it can only turn insecure mode on, never off.
//
// It returns the resolved cli.Connection alongside the options, and the
// caller reads the endpoint and the route from it.
func contextOptions(
	cmd *cobra.Command,
	stored *config.Context,
	flagInsecure bool,
	contextName, user, realm, token, password, ticket, csrf string,
) (pve.Options, cli.Connection, error) {
	if stored == nil {
		return pve.Options{}, cli.Connection{}, fmt.Errorf("context %q not found", contextName)
	}
	if err := checkStoredBlocks(contextName, stored); err != nil {
		return pve.Options{}, cli.Connection{}, err
	}

	ov, err := cli.GetDeps(cmd).ConnectionOverrides()
	if err != nil {
		return pve.Options{}, cli.Connection{}, err
	}
	ov.Insecure = ov.Insecure || flagInsecure

	creds := cli.Credentials{
		Override: true,
		Username: user,
		Realm:    realm,
		Token:    token,
		Password: password,
		Ticket:   ticket,
		CSRF:     csrf,
	}

	return cli.ContextOptions(cmd, stored, contextName, configPath(cmd), ov, creds,
		func() bool { return isInteractiveInput(cmd.InOrStdin()) })
}

// isInteractiveInput reports whether in is an interactive terminal, used to
// decide whether the TOFU manual-verify callback (see contextOptions and
// cli.ApplyTOFUOptions) may prompt for a trust decision. Only a live *os.File
// that the terminal package recognises as a TTY counts as interactive; pipes,
// redirected files, and the in-memory readers/buffers used by tests are
// always treated as non-interactive, so the callback fails closed for them
// exactly as it does for a genuinely non-interactive process. Mirrors
// internal/cli.isInteractiveInput, duplicated here because that helper is
// unexported and this package builds its own clients independently of the
// root command's PersistentPreRunE.
func isInteractiveInput(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}

	return term.IsTerminal(int(f.Fd()))
}

// storeSession records the ticket, CSRF token, and expiry from result on
// stored, the write handle lookupContext returned.
func storeSession(stored *config.Context, result *ticketResult) {
	stored.Auth.Session = &config.Session{
		ExpiresAt: time.Now().Add(ticketLifetime).Unix(),
		Ticket:    result.Ticket,
		CSRF:      result.CSRF,
	}
}

// resolvePassword returns the explicit password if provided, otherwise resolves
// the stored context's secret reference.
func resolvePassword(explicit string, stored *config.Context) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	pw, err := config.ResolveSecret(stored.Auth.Secret)
	if err != nil {
		return "", fmt.Errorf("resolve password: %w", err)
	}
	return pw, nil
}

// sessionStatus summarises a session's validity for display.
func sessionStatus(s *config.Session) string {
	if s == nil || s.Ticket == "" {
		return "none"
	}
	if s.ExpiresAt == 0 {
		return "active"
	}
	expiry := time.Unix(s.ExpiresAt, 0)
	if time.Now().After(expiry) {
		return "expired " + expiry.Format(time.RFC3339)
	}
	return "valid until " + expiry.Format(time.RFC3339)
}

// firstNonEmpty returns the first non-empty string from vals, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// secretSource classifies a secret reference without revealing its value,
// using config.IsSecretReference, which mirrors the syntax
// config.ResolveSecret dispatches on. A value that is not a reference, such
// as "$uper$ecret", is an inline literal and is never printed. A $NAME
// reference whose variable is unset is reported without its text, because
// ResolveSecret then uses the text itself as the secret.
func secretSource(secret string) string {
	switch {
	case secret == "":
		return "(none)"
	case !config.IsSecretReference(secret):
		return "(inline literal)"
	case strings.HasPrefix(secret, "keychain:"):
		return secret + " (keychain)"
	case strings.HasPrefix(secret, "${"):
		return secret + " (env)"
	default:
		if _, set := os.LookupEnv(secret[1:]); set {
			return secret + " (env)"
		}
		return "(unset reference, resolves as a literal)"
	}
}
