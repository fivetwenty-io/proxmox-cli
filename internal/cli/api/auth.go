package api

import (
	"fmt"
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
		contextName string
		username    string
		realm       string
		password    string
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

			user := firstNonEmpty(username, ctx.Auth.Username)
			rlm := firstNonEmpty(realm, ctx.Realm, "pam")
			pw, err := resolvePassword(password, ctx)
			if err != nil {
				return err
			}

			resp, err := createTicket(cmd, ctx, user, rlm, pw)
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
	return noClient(cmd)
}

// newAuthRefreshCmd builds `pve api auth refresh`, re-obtaining a session ticket
// for a password context.
func newAuthRefreshCmd() *cobra.Command {
	var contextName string

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

			resp, err := createTicket(cmd, ctx, ctx.Auth.Username, rlm, pw)
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

// createTicket builds a password-authenticated client for ctx and requests a
// session ticket using the given user, realm, and password.
func createTicket(
	cmd *cobra.Command,
	ctx *config.Context,
	user, realm, password string,
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
