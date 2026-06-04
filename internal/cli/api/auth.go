package api

import (
	"fmt"
	"os"
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
		Short: "Authenticate against a target",
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

// resolveTargetName returns the target name to operate on: the --target flag if
// set, otherwise the config's current-target.
func resolveTargetName(flagTarget string, cfg *config.Config) (string, error) {
	if flagTarget != "" {
		return flagTarget, nil
	}
	if cfg.CurrentTarget != "" {
		return cfg.CurrentTarget, nil
	}
	return "", fmt.Errorf("no target specified: use --target or set a current target")
}

// lookupTarget returns the named target or an error if it is absent.
func lookupTarget(cfg *config.Config, name string) (*config.Target, error) {
	t, ok := cfg.Targets[name]
	if !ok || t == nil {
		return nil, fmt.Errorf("target %q not found", name)
	}
	return t, nil
}

// newAuthLoginCmd builds `pve api auth login`.
func newAuthLoginCmd() *cobra.Command {
	var (
		targetName string
		username   string
		realm      string
		password   string
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Obtain a session ticket and store it in the config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			name, err := resolveTargetName(targetName, cfg)
			if err != nil {
				return err
			}
			target, err := lookupTarget(cfg, name)
			if err != nil {
				return err
			}

			user := firstNonEmpty(username, target.Auth.Username)
			rlm := firstNonEmpty(realm, target.Realm, "pam")
			pw, err := resolvePassword(password, target)
			if err != nil {
				return err
			}

			resp, err := createTicket(cmd, target, user, rlm, pw)
			if err != nil {
				return err
			}

			storeSession(target, resp)
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

	cmd.Flags().StringVar(&targetName, "target", "", "target name (defaults to current target)")
	cmd.Flags().StringVar(&username, "username", "", "PVE username (defaults to target's username)")
	cmd.Flags().StringVar(&realm, "realm", "", "authentication realm (defaults to target's realm)")
	cmd.Flags().StringVar(&password, "password", "", "password (defaults to the target's resolved secret)")
	return noClient(cmd)
}

// newAuthRefreshCmd builds `pve api auth refresh`, re-obtaining a session ticket
// for a password target.
func newAuthRefreshCmd() *cobra.Command {
	var targetName string

	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Refresh the session ticket for a password target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			name, err := resolveTargetName(targetName, cfg)
			if err != nil {
				return err
			}
			target, err := lookupTarget(cfg, name)
			if err != nil {
				return err
			}
			if target.Auth.Type != "password" {
				return fmt.Errorf("target %q uses %q auth; refresh applies only to password targets",
					name, target.Auth.Type)
			}

			pw, err := resolvePassword("", target)
			if err != nil {
				return err
			}
			rlm := firstNonEmpty(target.Realm, "pam")

			resp, err := createTicket(cmd, target, target.Auth.Username, rlm, pw)
			if err != nil {
				return err
			}

			storeSession(target, resp)
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

	cmd.Flags().StringVar(&targetName, "target", "", "target name (defaults to current target)")
	return noClient(cmd)
}

// newAuthLogoutCmd builds `pve api auth logout`.
func newAuthLogoutCmd() *cobra.Command {
	var targetName string

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Invalidate the session ticket and remove it from the config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			name, err := resolveTargetName(targetName, cfg)
			if err != nil {
				return err
			}
			target, err := lookupTarget(cfg, name)
			if err != nil {
				return err
			}

			// If a live session exists, best-effort invalidate it server-side.
			if target.Auth.Session != nil && target.Auth.Session.Ticket != "" {
				if err := serverLogout(target); err != nil {
					return err
				}
			}

			target.Auth.Session = nil
			if err := config.SaveForce(configPath(cmd), cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Logged out from target %q.", name)},
				deps.Format)
		},
	}

	cmd.Flags().StringVar(&targetName, "target", "", "target name (defaults to current target)")
	return noClient(cmd)
}

// newAuthStatusCmd builds `pve api auth status`.
func newAuthStatusCmd() *cobra.Command {
	var targetName string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show authentication status for a target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			name, err := resolveTargetName(targetName, cfg)
			if err != nil {
				return err
			}
			target, err := lookupTarget(cfg, name)
			if err != nil {
				return err
			}

			resolved := "yes"
			if _, err := config.ResolveSecret(target.Auth.Secret); err != nil {
				resolved = "no (" + err.Error() + ")"
			}

			single := map[string]string{
				"Target":        name,
				"Auth-type":     target.Auth.Type,
				"Username":      target.Auth.Username,
				"Token-ID":      target.Auth.TokenID,
				"Secret-source": secretSource(target.Auth.Secret),
				"Resolved":      resolved,
				"Session":       sessionStatus(target.Auth.Session),
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Single: single}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&targetName, "target", "", "target name (defaults to current target)")
	return noClient(cmd)
}

// newAuthSetTokenCmd builds `pve api auth set-token`.
func newAuthSetTokenCmd() *cobra.Command {
	var (
		targetName string
		tokenID    string
		secret     string
		username   string
	)

	cmd := &cobra.Command{
		Use:   "set-token",
		Short: "Configure token authentication for a target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			name, err := resolveTargetName(targetName, cfg)
			if err != nil {
				return err
			}
			target, err := lookupTarget(cfg, name)
			if err != nil {
				return err
			}

			if tokenID == "" {
				return fmt.Errorf("--token-id is required")
			}
			if secret == "" {
				return fmt.Errorf("--secret is required")
			}

			target.Auth.Type = "token"
			target.Auth.TokenID = tokenID
			target.Auth.Secret = secret
			if username != "" {
				target.Auth.Username = username
			}
			target.Auth.Session = nil

			if err := config.SaveForce(configPath(cmd), cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Set token authentication for target %q.", name)},
				deps.Format)
		},
	}

	cmd.Flags().StringVar(&targetName, "target", "", "target name (defaults to current target)")
	cmd.Flags().StringVar(&tokenID, "token-id", "", "API token id (required)")
	cmd.Flags().StringVar(&secret, "secret", "", "token secret reference (required)")
	cmd.Flags().StringVar(&username, "username", "", "PVE username for the token")
	return noClient(cmd)
}

// newAuthSetPasswordCmd builds `pve api auth set-password`.
func newAuthSetPasswordCmd() *cobra.Command {
	var (
		targetName string
		username   string
		secret     string
	)

	cmd := &cobra.Command{
		Use:   "set-password",
		Short: "Configure password authentication for a target",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			name, err := resolveTargetName(targetName, cfg)
			if err != nil {
				return err
			}
			target, err := lookupTarget(cfg, name)
			if err != nil {
				return err
			}

			if username == "" {
				return fmt.Errorf("--username is required")
			}
			if secret == "" {
				return fmt.Errorf("--secret is required")
			}

			target.Auth.Type = "password"
			target.Auth.Username = username
			target.Auth.Secret = secret
			target.Auth.TokenID = ""
			target.Auth.Session = nil

			if err := config.SaveForce(configPath(cmd), cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: fmt.Sprintf("Set password authentication for target %q.", name)},
				deps.Format)
		},
	}

	cmd.Flags().StringVar(&targetName, "target", "", "target name (defaults to current target)")
	cmd.Flags().StringVar(&username, "username", "", "PVE username (required)")
	cmd.Flags().StringVar(&secret, "secret", "", "password reference (required)")
	return noClient(cmd)
}

// createTicket builds a password-authenticated client for target and requests a
// session ticket using the given user, realm, and password.
func createTicket(
	cmd *cobra.Command,
	target *config.Target,
	user, realm, password string,
) (*access.CreateTicketResponse, error) {
	ac, err := clientForTarget(target, user, realm, password, "", "")
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
		return nil, fmt.Errorf("login to %s: %w", target.Host, err)
	}
	return resp, nil
}

// serverLogout builds a ticket-authenticated client and invalidates the session
// server-side. The session's CSRF token is supplied so the logout (a non-GET
// request) carries the PVECSRFPreventionToken header Proxmox requires.
func serverLogout(target *config.Target) error {
	ac, err := clientForTarget(target, "", "", "", target.Auth.Session.Ticket, target.Auth.Session.CSRF)
	if err != nil {
		return err
	}
	if err := ac.Raw.Logout(); err != nil {
		return fmt.Errorf("logout from %s: %w", target.Host, err)
	}
	return nil
}

// clientForTarget constructs an APIClient for the given target. Exactly one of
// (user+password) or (ticket+csrf) should be supplied so the underlying client
// has valid credentials. The csrf token is required alongside a ticket for
// non-GET requests under session authentication.
func clientForTarget(target *config.Target, user, realm, password, ticket, csrf string) (*apiclient.APIClient, error) {
	if target.TLS.Insecure {
		cli.WarnInsecureTLS(os.Stderr)
	}
	rlm := realm
	if rlm == "" {
		rlm = target.Realm
	}
	opts := apiclient.BuildOptions(
		target.Host,
		target.Port,
		target.Protocol,
		user,
		rlm,
		"", // no token: login/logout use ticket or password
		password,
		ticket,
		csrf,
		target.TLS.Insecure,
		target.TLS.Fingerprint,
	)
	ac, err := apiclient.NewAPIClient(opts)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", target.Host, err)
	}
	return ac, nil
}

// storeSession records the ticket, CSRF token, and expiry from resp on target.
func storeSession(target *config.Target, resp *access.CreateTicketResponse) {
	session := &config.Session{
		ExpiresAt: time.Now().Add(ticketLifetime).Unix(),
	}
	if resp.Ticket != nil {
		session.Ticket = *resp.Ticket
	}
	if resp.CSRFPreventionToken != nil {
		session.CSRF = *resp.CSRFPreventionToken
	}
	target.Auth.Session = session
}

// resolvePassword returns the explicit password if provided, otherwise resolves
// the target's secret reference.
func resolvePassword(explicit string, target *config.Target) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	pw, err := config.ResolveSecret(target.Auth.Secret)
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
