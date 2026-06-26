package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/apiclient"
	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/config"
	"github.com/fivetwenty-io/pve-cli/internal/output"

	"github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/api/access"
)

// ticketLifetime is the PVE ticket validity window used to compute a session's
// expiry timestamp; PVE tickets are valid for two hours.
const ticketLifetime = 2 * time.Hour

// newAuthCmd builds `pve api auth` and its sub-commands.
func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate against a context",
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

// resolveContextName returns the context name to operate on: the --context flag
// if set, otherwise the config's current-context.
func resolveContextName(flagContext string, cfg *config.Config) (string, error) {
	if flagContext != "" {
		return flagContext, nil
	}
	if cfg.CurrentContext != "" {
		return cfg.CurrentContext, nil
	}
	return "", fmt.Errorf("no context specified: use --context or set a current context")
}

// lookupContext returns the named context or an error if it is absent.
func lookupContext(cfg *config.Config, name string) (*config.Context, error) {
	c, ok := cfg.Contexts[name]
	if !ok || c == nil {
		return nil, fmt.Errorf("context %q not found", name)
	}
	return c, nil
}

// newAuthLoginCmd builds `pve api auth login`.
func newAuthLoginCmd() *cobra.Command {
	var (
		contextName  string
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
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			name, err := resolveContextName(contextName, cfg)
			if err != nil {
				return err
			}
			ctx, err := lookupContext(cfg, name)
			if err != nil {
				return err
			}

			if oidc {
				return performOIDCLogin(cmd, deps, cfg, ctx, name, realm, redirectURL, code, state)
			}

			user := firstNonEmpty(username, ctx.Auth.Username)
			rlm := firstNonEmpty(realm, ctx.Realm, "pam")
			pw, err := resolvePassword(password, ctx)
			if err != nil {
				return err
			}

			resp, err := createTicket(cmd, ctx, user, rlm, pw, ticketOptions{
				otp:          otp,
				tfaChallenge: tfaChallenge,
				verifyPath:   verifyPath,
				verifyPrivs:  verifyPrivs,
			})
			if err != nil {
				return err
			}

			storeSession(ctx, resp)
			if err := config.SaveForce(configPath(cmd), cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			expires := time.Now().Add(ticketLifetime).Format(time.RFC3339)
			msg := fmt.Sprintf("Logged in as %s. Session valid until %s.",
				resp.Username, expires)
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: msg}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&contextName, "context", "", "context name (defaults to current context)")
	cmd.Flags().StringVar(&username, "username", "", "PVE username (defaults to context's username)")
	cmd.Flags().StringVar(&realm, "realm", "", "authentication realm (defaults to context's realm)")
	cmd.Flags().StringVar(&password, "password", "", "password (defaults to the context's resolved secret)")
	cmd.Flags().StringVar(&otp, "otp", "", "one-time password for TOTP-based two-factor authentication")
	cmd.Flags().StringVar(&tfaChallenge, "tfa-challenge", "", "signed challenge response for second-step two-factor authentication")
	cmd.Flags().StringVar(&verifyPath, "path", "", "ticket-verification mode: ACL path to check privileges on (requires --privs)")
	cmd.Flags().StringVar(&verifyPrivs, "privs", "", "ticket-verification mode: privileges to verify on --path")
	cmd.Flags().BoolVar(&oidc, "oidc", false, "authenticate via OpenID Connect instead of username/password")
	cmd.Flags().StringVar(&redirectURL, "redirect-url", "",
		"OIDC redirect URL sent to the identity provider (defaults to the configured PVE endpoint base URL; requires --oidc)")
	cmd.Flags().StringVar(&code, "code", "",
		"OIDC authorization code for non-interactive login (requires --oidc and --state)")
	cmd.Flags().StringVar(&state, "state", "",
		"OIDC state parameter for non-interactive login (requires --oidc and --code)")
	return noClient(cmd)
}

// performOIDCLogin carries out the OpenID Connect login flow for the given context.
// It calls CreateOpenidAuthUrl to obtain the authorization URL, then either reads
// code+state from the supplied flags (non-interactive) or prompts the user to
// authenticate in a browser and paste the redirect URL (interactive). On success it
// unmarshals the login response into the CreateTicketResponse shape, persists the
// session ticket via storeSession, and saves the config file.
func performOIDCLogin(
	cmd *cobra.Command,
	deps *cli.Deps,
	cfg *config.Config,
	ctx *config.Context,
	contextName string,
	realmFlag string,
	redirectURLFlag string,
	code string,
	state string,
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

	// Realm is required for OIDC; do NOT fall back to "pam" (which is not an OIDC realm).
	rlm := firstNonEmpty(realmFlag, ctx.Realm)
	if rlm == "" {
		return fmt.Errorf("OIDC login requires --realm or a realm configured in context %q", contextName)
	}

	// Build a client for the context. The OIDC auth-url and login endpoints are
	// public (PVE marks them noauthentication), so any credential satisfies the
	// HTTP transport — the server does not validate credentials on these paths.
	// Prefer an existing session ticket; fall back to a placeholder token that
	// passes the pve-apiclient-go options validator without real credentials.
	ac, err := buildClientForOIDC(ctx)
	if err != nil {
		return err
	}

	// Determine the redirect URL sent to the identity provider.
	redir := redirectURLFlag
	if redir == "" {
		redir = fmt.Sprintf("%s://%s:%d", ctx.Protocol, ctx.Host, ctx.Port)
	}

	// Step 1: obtain the OIDC authorization URL from PVE.
	authURLResp, err := ac.Access.CreateOpenidAuthUrl(cmd.Context(), &access.CreateOpenidAuthUrlParams{
		Realm:       rlm,
		RedirectUrl: redir,
	})
	if err != nil {
		return fmt.Errorf("get OIDC auth URL for realm %q: %w", rlm, err)
	}
	if authURLResp == nil {
		return fmt.Errorf("get OIDC auth URL for realm %q: server returned no data", rlm)
	}
	var authURL string
	if err := json.Unmarshal(*authURLResp, &authURL); err != nil {
		return fmt.Errorf("parse OIDC auth URL response: %w", err)
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
	loginResp, err := ac.Access.CreateOpenidLogin(cmd.Context(), &access.CreateOpenidLoginParams{
		Code:        oidcCode,
		State:       oidcState,
		RedirectUrl: redir,
	})
	if err != nil {
		return fmt.Errorf("OIDC login for realm %q: %w", rlm, err)
	}
	if loginResp == nil {
		return fmt.Errorf("OIDC login for realm %q: server returned no data", rlm)
	}

	// Unmarshal the raw JSON response into the ticket-response shape.
	// The PVE OIDC login endpoint returns the same fields as POST /access/ticket:
	// username, ticket, CSRFPreventionToken.
	var ticketResp access.CreateTicketResponse
	if err := json.Unmarshal(*loginResp, &ticketResp); err != nil {
		return fmt.Errorf("parse OIDC login response: %w", err)
	}
	if ticketResp.Ticket == nil || *ticketResp.Ticket == "" {
		return fmt.Errorf("OIDC login for realm %q: server returned no ticket", rlm)
	}
	if ticketResp.Username == "" {
		return fmt.Errorf("OIDC login for realm %q: server returned no username", rlm)
	}

	// Persist the session identically to password login.
	storeSession(ctx, &ticketResp)
	if err := config.SaveForce(configPath(cmd), cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	expires := time.Now().Add(ticketLifetime).Format(time.RFC3339)
	msg := fmt.Sprintf("Logged in as %s via OIDC. Session valid until %s.", ticketResp.Username, expires)
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
}

// parseOIDCRedirect extracts the code and state query parameters from a full
// OIDC redirect URL (the URL the identity provider sends the browser to after
// authentication). Returns an error if the URL cannot be parsed or if either
// required parameter is absent.
func parseOIDCRedirect(rawURL string) (code, state string, err error) {
	if rawURL == "" {
		return "", "", fmt.Errorf("redirect URL is empty")
	}
	u, parseErr := url.Parse(rawURL)
	if parseErr != nil {
		return "", "", fmt.Errorf("parse redirect URL %q: %w", rawURL, parseErr)
	}
	q := u.Query()
	code = q.Get("code")
	state = q.Get("state")
	if code == "" {
		return "", "", fmt.Errorf("redirect URL %q is missing the 'code' query parameter", rawURL)
	}
	if state == "" {
		return "", "", fmt.Errorf("redirect URL %q is missing the 'state' query parameter", rawURL)
	}
	return code, state, nil
}

// newAuthRefreshCmd builds `pve api auth refresh`, re-obtaining a session ticket
// for a password context.
func newAuthRefreshCmd() *cobra.Command {
	var (
		contextName  string
		tfaChallenge string
	)

	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Refresh the session ticket for a password context",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			name, err := resolveContextName(contextName, cfg)
			if err != nil {
				return err
			}
			ctx, err := lookupContext(cfg, name)
			if err != nil {
				return err
			}
			if ctx.Auth.Type != "password" {
				return fmt.Errorf("context %q uses %q auth; refresh applies only to password contexts",
					name, ctx.Auth.Type)
			}

			pw, err := resolvePassword("", ctx)
			if err != nil {
				return err
			}
			rlm := firstNonEmpty(ctx.Realm, "pam")

			resp, err := createTicket(cmd, ctx, ctx.Auth.Username, rlm, pw, ticketOptions{
				tfaChallenge: tfaChallenge,
			})
			if err != nil {
				return err
			}

			storeSession(ctx, resp)
			if err := config.SaveForce(configPath(cmd), cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			expires := time.Now().Add(ticketLifetime).Format(time.RFC3339)
			msg := fmt.Sprintf("Refreshed session for %s. Valid until %s.",
				resp.Username, expires)
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: msg}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&contextName, "context", "", "context name (defaults to current context)")
	cmd.Flags().StringVar(&tfaChallenge, "tfa-challenge", "", "signed challenge response for second-step two-factor authentication")
	return noClient(cmd)
}

// newAuthLogoutCmd builds `pve api auth logout`.
func newAuthLogoutCmd() *cobra.Command {
	var contextName string

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Invalidate the session ticket and remove it from the config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			name, err := resolveContextName(contextName, cfg)
			if err != nil {
				return err
			}
			ctx, err := lookupContext(cfg, name)
			if err != nil {
				return err
			}

			// If a live session exists, best-effort invalidate it server-side.
			if ctx.Auth.Session != nil && ctx.Auth.Session.Ticket != "" {
				if err := serverLogout(ctx); err != nil {
					return err
				}
			}

			ctx.Auth.Session = nil
			if err := config.SaveForce(configPath(cmd), cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Logged out from context %q.", name)},
				deps.Format)
		},
	}

	cmd.Flags().StringVar(&contextName, "context", "", "context name (defaults to current context)")
	return noClient(cmd)
}

// newAuthStatusCmd builds `pve api auth status`.
func newAuthStatusCmd() *cobra.Command {
	var contextName string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show authentication status for a context",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			name, err := resolveContextName(contextName, cfg)
			if err != nil {
				return err
			}
			ctx, err := lookupContext(cfg, name)
			if err != nil {
				return err
			}

			resolved := "yes"
			if _, err := config.ResolveSecret(ctx.Auth.Secret); err != nil {
				resolved = "no (" + err.Error() + ")"
			}

			single := map[string]string{
				"Context":       name,
				"Auth-type":     ctx.Auth.Type,
				"Username":      ctx.Auth.Username,
				"Token-ID":      ctx.Auth.TokenID,
				"Secret-source": secretSource(ctx.Auth.Secret),
				"Resolved":      resolved,
				"Session":       sessionStatus(ctx.Auth.Session),
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&contextName, "context", "", "context name (defaults to current context)")
	return noClient(cmd)
}

// newAuthWhoamiCmd builds `pve api auth whoami`. Unlike the other auth
// sub-commands it requires a live API client (built by the root from the
// resolved context), so it is NOT annotated noClient: it calls
// GET /access/permissions to confirm the stored credentials authenticate and
// reports the effective identity plus the accessible ACL paths.
func newAuthWhoamiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show the identity the current credentials authenticate as",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			// The root resolved and built the client for this context name using
			// the same precedence (--context/-c > $PVE_CONTEXT > current-context).
			flagContext := ""
			if f := cmd.Flags().Lookup("context"); f != nil {
				flagContext = f.Value.String()
			}
			name := config.Resolve(flagContext, "PVE_CONTEXT", deps.Cfg.CurrentContext, "")
			ctx, _, err := config.ResolveContext(deps.Cfg, name)
			if err != nil {
				return err
			}

			perms, err := deps.API.Access.ListPermissions(cmd.Context(), nil)
			if err != nil {
				return fmt.Errorf("verify credentials for context %q: %w", name, err)
			}

			single := map[string]string{
				"Context":   name,
				"Auth-type": ctx.Auth.Type,
				"Identity":  authIdentity(ctx),
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single, Raw: perms}, deps.Format)
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

// newAuthSetTokenCmd builds `pve api auth set-token`.
func newAuthSetTokenCmd() *cobra.Command {
	var (
		contextName string
		tokenID     string
		secret      string
		username    string
	)

	cmd := &cobra.Command{
		Use:   "set-token",
		Short: "Configure token authentication for a context",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			name, err := resolveContextName(contextName, cfg)
			if err != nil {
				return err
			}
			ctx, err := lookupContext(cfg, name)
			if err != nil {
				return err
			}

			if tokenID == "" {
				return fmt.Errorf("--token-id is required")
			}
			if secret == "" {
				return fmt.Errorf("--secret is required")
			}

			ctx.Auth.Type = "token"
			ctx.Auth.TokenID = tokenID
			ctx.Auth.Secret = secret
			if username != "" {
				ctx.Auth.Username = username
			}
			ctx.Auth.Session = nil

			if err := config.SaveForce(configPath(cmd), cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Set token authentication for context %q.", name)},
				deps.Format)
		},
	}

	cmd.Flags().StringVar(&contextName, "context", "", "context name (defaults to current context)")
	cmd.Flags().StringVar(&tokenID, "token-id", "", "API token id (required)")
	cmd.Flags().StringVar(&secret, "secret", "", "token secret reference (required)")
	cmd.Flags().StringVar(&username, "username", "", "PVE username for the token")
	return noClient(cmd)
}

// newAuthSetPasswordCmd builds `pve api auth set-password`.
func newAuthSetPasswordCmd() *cobra.Command {
	var (
		contextName string
		username    string
		secret      string
	)

	cmd := &cobra.Command{
		Use:   "set-password",
		Short: "Configure password authentication for a context",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			name, err := resolveContextName(contextName, cfg)
			if err != nil {
				return err
			}
			ctx, err := lookupContext(cfg, name)
			if err != nil {
				return err
			}

			if username == "" {
				return fmt.Errorf("--username is required")
			}
			if secret == "" {
				return fmt.Errorf("--secret is required")
			}

			ctx.Auth.Type = "password"
			ctx.Auth.Username = username
			ctx.Auth.Secret = secret
			ctx.Auth.TokenID = ""
			ctx.Auth.Session = nil

			if err := config.SaveForce(configPath(cmd), cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Set password authentication for context %q.", name)},
				deps.Format)
		},
	}

	cmd.Flags().StringVar(&contextName, "context", "", "context name (defaults to current context)")
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

// createTicket builds a password-authenticated client for ctx and requests a
// session ticket using the given user, realm, password, and optional 2FA /
// ticket-verification inputs.
func createTicket(
	cmd *cobra.Command,
	ctx *config.Context,
	user, realm, password string,
	opts ticketOptions,
) (*access.CreateTicketResponse, error) {
	ac, err := clientForContext(ctx, user, realm, password, "", "")
	if err != nil {
		return nil, err
	}

	params := &access.CreateTicketParams{
		Username: user,
		Password: password,
	}
	if realm != "" {
		r := realm
		params.Realm = &r
	}
	if opts.otp != "" {
		params.Otp = &opts.otp
	}
	if opts.tfaChallenge != "" {
		params.TfaChallenge = &opts.tfaChallenge
	}
	if opts.verifyPath != "" {
		params.Path = &opts.verifyPath
	}
	if opts.verifyPrivs != "" {
		params.Privs = &opts.verifyPrivs
	}

	resp, err := ac.Access.CreateTicket(cmd.Context(), params)
	if err != nil {
		return nil, fmt.Errorf("login to %s: %w", ctx.Host, err)
	}
	return resp, nil
}

// serverLogout builds a ticket-authenticated client and invalidates the session
// server-side. The session's CSRF token is supplied so the logout (a non-GET
// request) carries the PVECSRFPreventionToken header Proxmox requires.
func serverLogout(ctx *config.Context) error {
	ac, err := clientForContext(ctx, "", "", "", ctx.Auth.Session.Ticket, ctx.Auth.Session.CSRF)
	if err != nil {
		return err
	}
	if err := ac.Raw.Logout(); err != nil {
		return fmt.Errorf("logout from %s: %w", ctx.Host, err)
	}
	return nil
}

// buildClientForOIDC constructs an API client suitable for calling the two public
// OIDC endpoints (POST /access/openid/auth-url and POST /access/openid/login).
// Both endpoints are marked noauthentication by PVE, meaning the server does not
// validate credentials on those paths. However, the pve-apiclient-go options
// validator requires at least one credential to be set, so:
//   - If the context carries a live session ticket, that ticket is used.
//   - Otherwise a placeholder API token is constructed to pass validation; the
//     token value is never checked by the server on these public endpoints.
func buildClientForOIDC(ctx *config.Context) (*apiclient.APIClient, error) {
	if ctx.Auth.Session != nil && ctx.Auth.Session.Ticket != "" {
		return clientForContext(ctx, "", "", "", ctx.Auth.Session.Ticket, ctx.Auth.Session.CSRF)
	}
	// No live session: build with a placeholder API token.
	if ctx.TLS.Insecure {
		cli.WarnInsecureTLS(os.Stderr)
	}
	opts := apiclient.BuildOptions(
		ctx.Host,
		ctx.Port,
		ctx.Protocol,
		"", "",
		"dummy@pam!oidc=00000000-0000-0000-0000-000000000000", // placeholder: satisfies validation only
		"", "", "",
		ctx.TLS.Insecure,
		ctx.TLS.Fingerprint,
	)
	ac, err := apiclient.NewAPIClient(opts)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", ctx.Host, err)
	}
	return ac, nil
}

// clientForContext constructs an APIClient for the given context. Exactly one of
// (user+password) or (ticket+csrf) should be supplied so the underlying client
// has valid credentials. The csrf token is required alongside a ticket for
// non-GET requests under session authentication.
func clientForContext(ctx *config.Context, user, realm, password, ticket, csrf string) (*apiclient.APIClient, error) {
	if ctx.TLS.Insecure {
		cli.WarnInsecureTLS(os.Stderr)
	}
	rlm := realm
	if rlm == "" {
		rlm = ctx.Realm
	}
	opts := apiclient.BuildOptions(
		ctx.Host,
		ctx.Port,
		ctx.Protocol,
		user,
		rlm,
		"", // no token: login/logout use ticket or password
		password,
		ticket,
		csrf,
		ctx.TLS.Insecure,
		ctx.TLS.Fingerprint,
	)
	ac, err := apiclient.NewAPIClient(opts)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", ctx.Host, err)
	}
	return ac, nil
}

// storeSession records the ticket, CSRF token, and expiry from resp on ctx.
func storeSession(ctx *config.Context, resp *access.CreateTicketResponse) {
	session := &config.Session{
		ExpiresAt: time.Now().Add(ticketLifetime).Unix(),
	}
	if resp.Ticket != nil {
		session.Ticket = *resp.Ticket
	}
	if resp.CSRFPreventionToken != nil {
		session.CSRF = *resp.CSRFPreventionToken
	}
	ctx.Auth.Session = session
}

// resolvePassword returns the explicit password if provided, otherwise resolves
// the context's secret reference.
func resolvePassword(explicit string, ctx *config.Context) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	pw, err := config.ResolveSecret(ctx.Auth.Secret)
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

// secretSource classifies a secret reference without revealing its value.
func secretSource(secret string) string {
	switch {
	case secret == "":
		return "(none)"
	case strings.HasPrefix(secret, "${") || (strings.HasPrefix(secret, "$") && !strings.HasPrefix(secret, "${")):
		return secret + " (env)"
	case strings.HasPrefix(secret, "keychain:"):
		return secret + " (keychain)"
	default:
		return "(inline literal)"
	}
}
