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
	"golang.org/x/term"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
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
		Long: "Manage local credentials and session state for a context. status, set-token, " +
			"set-password, login, refresh, logout, and whoami all work with any context, Proxmox VE " +
			"(PVE), Proxmox Backup Server (PBS), or Proxmox Datacenter Manager (PDM). login and " +
			"refresh negotiate a session ticket with the server; --otp (one-time password for " +
			"TOTP-based two-factor authentication) is PVE-only — PBS and PDM contexts use " +
			"--tfa-challenge instead. whoami queries the server to verify the stored credentials.",
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

// newAuthLoginCmd builds `pmx auth login`.
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
		Long: "Authenticate against a context's realm (password, TOTP/two-factor, or OpenID " +
			"Connect via --oidc) and store the resulting session ticket in the config. " +
			"Works with Proxmox VE (PVE), Proxmox Backup Server (PBS), and Proxmox Datacenter " +
			"Manager (PDM) contexts.\n\n" +
			"--otp (one-time password for TOTP-based two-factor authentication) is PVE-only; " +
			"PBS and PDM contexts use --tfa-challenge for second-factor login instead. " +
			"--oidc works with PVE, PBS, and PDM contexts.",
		Example: `  pmx auth login --context lab
  pmx auth login --context lab --username alice@pve --realm pve
  pmx auth login --context lab --oidc --realm sso`,
		Args: cobra.NoArgs,
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

			resp, err := createTicket(cmd, ctx, name, user, rlm, pw, ticketOptions{
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
	cmd.Flags().StringVar(&username, "username", "", "username (defaults to context's username)")
	cmd.Flags().StringVar(&realm, "realm", "", "authentication realm (defaults to context's realm)")
	cmd.Flags().StringVar(&password, "password", "", "password (defaults to the context's resolved secret)")
	cmd.Flags().StringVar(&otp, "otp", "", "one-time password for TOTP-based two-factor authentication")
	cmd.Flags().StringVar(&tfaChallenge, "tfa-challenge", "", "signed challenge response for second-step two-factor authentication")
	cmd.Flags().StringVar(&verifyPath, "path", "", "ticket-verification mode: ACL path to check privileges on (requires --privs)")
	cmd.Flags().StringVar(&verifyPrivs, "privs", "", "ticket-verification mode: privileges to verify on --path")
	cmd.Flags().BoolVar(&oidc, "oidc", false, "authenticate via OpenID Connect instead of username/password")
	cmd.Flags().StringVar(&redirectURL, "redirect-url", "",
		"OIDC redirect URL sent to the identity provider (defaults to the context's endpoint base URL; requires --oidc)")
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

	// Build the product-appropriate auth client for the context. The OIDC
	// auth-url and login endpoints are public on PVE, PBS, and PDM alike (all
	// three mark them noauthentication), so any credential satisfies the HTTP
	// transport — the server does not validate credentials on these paths.
	// Prefer an existing session ticket; fall back to a placeholder token that
	// passes the proxmox-apiclient-go options validator without real credentials.
	ac, err := buildClientForOIDC(cmd, ctx, contextName)
	if err != nil {
		return err
	}

	// Determine the redirect URL sent to the identity provider.
	redir := redirectURLFlag
	if redir == "" {
		redir = fmt.Sprintf("%s://%s:%d", ctx.Protocol, ctx.Host, ctx.Port)
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
	storeSession(ctx, result)
	if err := config.SaveForce(configPath(cmd), cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	expires := time.Now().Add(ticketLifetime).Format(time.RFC3339)
	msg := fmt.Sprintf("Logged in as %s via OIDC. Session valid until %s.", result.Username, expires)
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

// newAuthRefreshCmd builds `pmx auth refresh`, re-obtaining a session ticket
// for a password context.
func newAuthRefreshCmd() *cobra.Command {
	var (
		contextName  string
		tfaChallenge string
	)

	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Refresh the session ticket for a password context",
		Long: "Re-authenticate against a password context's realm and replace the stored " +
			"session ticket, without changing the configured credentials. Applies only to " +
			"contexts using password authentication (auth.type == \"password\"); the command " +
			"errors on any other auth type, such as token contexts. --tfa-challenge supplies " +
			"a second-factor challenge response if the realm requires one. The refreshed " +
			"ticket and its expiry are persisted to the config file.",
		Example: `  pmx auth refresh --context lab
  pmx auth refresh --context lab --tfa-challenge <response>`,
		Args: cobra.NoArgs,
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

			resp, err := createTicket(cmd, ctx, name, ctx.Auth.Username, rlm, pw, ticketOptions{
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

// newAuthLogoutCmd builds `pmx auth logout`.
func newAuthLogoutCmd() *cobra.Command {
	var contextName string

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Invalidate the session ticket and remove it from the config",
		Long: "Invalidate a context's session ticket and remove it from the config file. If a " +
			"live session ticket is present, the server is asked to invalidate it first; on " +
			"failure the command errors and the local session is left untouched. Once server-side " +
			"invalidation succeeds (or there was no live ticket to invalidate), the local session " +
			"is cleared and the config saved. Configured API-token or password credentials on the " +
			"context are left intact — only the session ticket is removed.",
		Example: `  pmx auth logout --context lab`,
		Args:    cobra.NoArgs,
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
				if err := serverLogout(cmd, ctx, name); err != nil {
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

// newAuthStatusCmd builds `pmx auth status`.
func newAuthStatusCmd() *cobra.Command {
	var contextName string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show authentication status for a context",
		Long: "Display the authentication configuration and session state for a context: host, " +
			"product, configured auth type, username or token id, where the secret is sourced " +
			"from, whether that secret currently resolves, and the session ticket's validity. " +
			"Reads only the local config file and never contacts the Proxmox API — use " +
			"'auth whoami' to verify the credentials against the server. Secret values are never " +
			"displayed, only their source (inline literal, environment variable, or keychain " +
			"reference).",
		Example: `  pmx auth status --context lab`,
		Args:    cobra.NoArgs,
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

			// Display values only — never mutate the stored context.
			product := ctx.ProductOrDefault()
			port := ctx.Port
			if port == 0 {
				port = config.DefaultPortForProduct(product)
			}
			protocol := ctx.Protocol
			if protocol == "" {
				protocol = "https"
			}

			single := map[string]string{
				"Context":       name,
				"Host":          fmt.Sprintf("%s://%s:%d", protocol, ctx.Host, port),
				"Product":       fmt.Sprintf("%s (%s)", cli.ProductDisplayName(product), product),
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
		Long: "Query the server to confirm the stored credentials authenticate, calling " +
			"GET /access/permissions against whichever product — Proxmox VE, Proxmox Backup " +
			"Server, or Proxmox Datacenter Manager — the resolved context targets. Prints the " +
			"effective identity (username, or user!token-id for token authentication) plus the " +
			"full accessible-ACL-path payload the server returns. This makes a live API call " +
			"and requires a valid session ticket or resolvable credential.",
		Example:     `  pmx auth whoami --context lab`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{cli.ProductAnnotation: cli.ProductFromContext},
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			// The root resolved and built the client for this context name using
			// the same precedence (--context/-c > $PMX_CONTEXT > current-context).
			flagContext := ""
			if f := cmd.Flags().Lookup("context"); f != nil {
				flagContext = f.Value.String()
			}
			name := config.Resolve(flagContext, "PMX_CONTEXT", deps.Cfg.CurrentContext, "")
			ctx, _, err := config.ResolveContext(deps.Cfg, name)
			if err != nil {
				return err
			}

			var perms *json.RawMessage
			switch {
			case deps.PBS != nil:
				perms, err = deps.PBS.Access.ListPermissions(cmd.Context(), nil)
			case deps.PDM != nil:
				perms, err = deps.PDM.Access.ListPermissions(cmd.Context(), nil)
			default:
				perms, err = deps.API.Access.ListPermissions(cmd.Context(), nil)
			}
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

// newAuthSetTokenCmd builds `pmx auth set-token`.
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
		Long: "Configure API-token authentication for a context: store the token id (in " +
			"Proxmox's user@realm!token-id form) and its secret, switch the context's auth type " +
			"to \"token\", and clear any stored session ticket. --token-id and --secret are both " +
			"required; --username overrides the context's configured username when set. --secret " +
			"accepts an inline literal, a ${VAR} or $VAR environment-variable reference, or a " +
			"keychain:PATH reference — prefer a reference over a literal to avoid writing the " +
			"secret to the config file in cleartext.",
		Example: `  pmx auth set-token --context lab --token-id root@pam!ci --secret '${PMX_TOKEN_SECRET}'`,
		Args:    cobra.NoArgs,
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

// newAuthSetPasswordCmd builds `pmx auth set-password`.
func newAuthSetPasswordCmd() *cobra.Command {
	var (
		contextName string
		username    string
		secret      string
	)

	cmd := &cobra.Command{
		Use:   "set-password",
		Short: "Configure password authentication for a context",
		Long: "Configure password authentication for a context: store the username and password " +
			"reference, switch the context's auth type to \"password\", clear any configured API " +
			"token id, and clear any stored session ticket. --username and --secret are both " +
			"required. --secret accepts an inline literal, a ${VAR} or $VAR environment-variable " +
			"reference, or a keychain:PATH reference — prefer a reference over a literal to avoid " +
			"writing the password to the config file in cleartext.",
		Example: `  pmx auth set-password --context lab --username root@pam --secret '${PMX_PASSWORD}'`,
		Args:    cobra.NoArgs,
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

// createTicket builds the product-appropriate auth client for ctx and
// requests a session ticket using the given user, realm, password, and
// optional 2FA / ticket-verification inputs. contextName is the resolved
// context name (see resolveContextName), used only to derive the per-context
// TOFU fingerprint cache path (see contextOptions).
func createTicket(
	cmd *cobra.Command,
	ctx *config.Context,
	contextName, user, realm, password string,
	opts ticketOptions,
) (*ticketResult, error) {
	ac, err := newAuthClientForContext(cmd, ctx, contextName, user, realm, password, "", "", "")
	if err != nil {
		return nil, err
	}

	result, err := ac.CreateTicket(cmd.Context(), user, realm, password, opts)
	if err != nil {
		return nil, fmt.Errorf("login to %s: %w", ctx.Host, err)
	}
	return result, nil
}

// serverLogout builds a ticket-authenticated client and invalidates the session
// server-side. The session's CSRF token is supplied so the logout (a non-GET
// request) carries the required CSRF-prevention header.
// contextName is the resolved context name (see resolveContextName), used only
// to derive the per-context TOFU fingerprint cache path (see contextOptions).
func serverLogout(cmd *cobra.Command, ctx *config.Context, contextName string) error {
	ac, err := newAuthClientForContext(cmd, ctx, contextName, "", "", "", "",
		ctx.Auth.Session.Ticket, ctx.Auth.Session.CSRF)
	if err != nil {
		return err
	}
	if err := ac.Logout(); err != nil {
		return fmt.Errorf("logout from %s: %w", ctx.Host, err)
	}
	return nil
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
// only to derive the per-context TOFU fingerprint cache path (see
// contextOptions) and has no bearing on which credential is selected.
func buildClientForOIDC(cmd *cobra.Command, ctx *config.Context, contextName string) (authClient, error) {
	if ctx.Auth.Session != nil && ctx.Auth.Session.Ticket != "" {
		return newAuthClientForContext(cmd, ctx, contextName, "", "", "", "",
			ctx.Auth.Session.Ticket, ctx.Auth.Session.CSRF)
	}
	// No live session: build with a placeholder API token.
	return newAuthClientForContext(cmd, ctx, contextName, "", "", "",
		"dummy@pam!oidc=00000000-0000-0000-0000-000000000000", // placeholder: satisfies validation only
		"", "")
}

// contextOptions builds the pve.Options for ctx, applying the same
// Trust-On-First-Use (TOFU) certificate wiring the root command applies for
// regular commands (see cli.ApplyTOFUOptions): a context with tls.tofu set
// gets FingerprintCachePath and a manual-verify callback that prompts on a
// TTY and fails closed (no prompt, no read) otherwise; a context without
// tls.tofu set, or with tls.insecure set (see below), gets neither — options
// identical to pre-TOFU behavior. user/realm/token/password/ticket/csrf
// select which credential BuildOptions embeds; contextName is used only to
// derive the per-context fingerprint cache path.
//
// flagInsecure is the resolved global --insecure flag value (cli.Deps.Insecure,
// populated by the root command's PersistentPreRunE before its noClient
// early-return). It is OR'd with ctx.TLS.Insecure — exactly mirroring the
// "insecure := pf.insecure || ctx.TLS.Insecure" merge PersistentPreRunE
// performs for every other command (internal/cli/root.go) — so that
// --insecure on an auth sub-command both disables certificate verification
// AND suppresses TOFU prompting/pinning, never the reverse: passing
// --insecure can only turn insecure mode on, it can never force it off when
// the context config already sets tls.insecure: true.
func contextOptions(
	cmd *cobra.Command,
	ctx *config.Context,
	flagInsecure bool,
	contextName, user, realm, token, password, ticket, csrf string,
) pve.Options {
	insecure := flagInsecure || ctx.TLS.Insecure

	opts := apiclient.BuildOptions(
		ctx.Host,
		ctx.Port,
		ctx.Protocol,
		user,
		realm,
		token,
		password,
		ticket,
		csrf,
		insecure,
		ctx.TLS.Fingerprint,
	)

	return cli.ApplyTOFUOptions(
		opts,
		ctx.TLS.Tofu,
		insecure,
		configPath(cmd),
		contextName,
		cmd.ErrOrStderr(),
		cmd.InOrStdin(),
		func() bool { return isInteractiveInput(cmd.InOrStdin()) },
	)
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

// storeSession records the ticket, CSRF token, and expiry from result on ctx.
func storeSession(ctx *config.Context, result *ticketResult) {
	ctx.Auth.Session = &config.Session{
		ExpiresAt: time.Now().Add(ticketLifetime).Unix(),
		Ticket:    result.Ticket,
		CSRF:      result.CSRF,
	}
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
