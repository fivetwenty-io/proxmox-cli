package context

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// addFlags holds the raw flag values for `pmx context add`.
type addFlags struct {
	host                string
	port                int
	protocol            string
	realm               string
	authType            string
	username            string
	tokenID             string
	secret              string
	insecure            bool
	fingerprint         string
	caCert              string
	tofu                bool
	defaultNode         string
	defaultOutput       string
	product             string
	sshUser             string
	sshPort             int
	sshIdentity         string
	sshJump             string
	proxyURL            string
	proxyUsername       string
	proxyPassword       string
	proxyFromEnv        bool
	timeoutConnect      string
	timeoutTLSHandshake string
	timeoutRequest      string
	selectCtx           bool
	force               bool
}

// newAddCmd builds `pmx context add <name>` (alias: create).
func newAddCmd() *cobra.Command {
	var f addFlags

	cmd := &cobra.Command{
		Use:     "add <name>",
		Aliases: []string{"create"},
		Short:   "Add a new named context to the config file",
		Long: `Add a new named context to the config file.

The --product flag selects which Proxmox product the context targets and,
unless --port is also given, its default API port.`,
		Example: `  # Proxmox VE (default product, port 8006)
  pmx context add lab --host pve.example.com \
  --username root@pam --token-id automation --secret '${PVE_TOKEN}' --select

  # Proxmox Backup Server (port 8007)
  pmx context add backup --product pbs --host pbs.example.com \
  --username root@pam --token-id automation --secret '${PBS_TOKEN}'

  # Proxmox Datacenter Manager (port 8443)
  pmx context add dcmgr --product pdm --host pdm.example.com \
  --username root@pam --token-id automation --secret '${PDM_TOKEN}'

  # Behind a bastion: ssh, rsync, and the API connection all tunnel through it
  pmx context add lab --host pve.example.com \
  --username root@pam --token-id automation --secret '${PVE_TOKEN}' \
  --ssh-jump admin@bastion.example.com

  # Through a proxy, with credentials kept out of the stored URL
  pmx context add lab --host pve.example.com \
  --username root@pam --token-id automation --secret '${PVE_TOKEN}' \
  --proxy-url socks5://proxy.example.com:1080 \
  --proxy-username relay --proxy-password '${PROXY_PASSWORD}'`,
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if name == "" {
				return fmt.Errorf("context name must not be empty")
			}

			// Validate required flag: --host.
			if f.host == "" {
				return fmt.Errorf("--host is required")
			}

			// Validate auth-type value.
			switch f.authType {
			case "token", "password":
			default:
				return fmt.Errorf("--auth-type must be \"token\" or \"password\", got %q", f.authType)
			}

			// Validate auth-type-specific required flags.
			if f.authType == "token" {
				if f.tokenID == "" {
					return fmt.Errorf("--token-id is required when --auth-type is \"token\"")
				}
				if f.secret == "" {
					return fmt.Errorf("--secret is required when --auth-type is \"token\"")
				}
				// Accept a full Proxmox token identifier (user@realm!tokenname) in
				// --token-id and split it, so operators can paste the value Proxmox
				// shows verbatim. The username derived here must not conflict with an
				// explicit --username.
				user, tokenID, err := splitTokenID(f.username, f.tokenID)
				if err != nil {
					return err
				}
				f.username, f.tokenID = user, tokenID

				// The API token header is USER@REALM!TOKENID=SECRET; without a
				// username the client would send "@realm!tokenid=secret" and the API
				// answers 401. Require it explicitly rather than writing a context
				// that cannot authenticate.
				if f.username == "" {
					return fmt.Errorf(
						"--username is required for token auth (user@realm, e.g. root@pam); " +
							"alternatively pass the full identifier to --token-id as user@realm!tokenname")
				}
			}
			if f.authType == "password" {
				if f.username == "" {
					return fmt.Errorf("--username is required when --auth-type is \"password\"")
				}
				if f.secret == "" {
					return fmt.Errorf("--secret is required when --auth-type is \"password\"")
				}
			}

			// Validate product.
			if !config.IsValidProduct(f.product) {
				return fmt.Errorf("--product must be one of: %s, got %q", strings.Join(config.Products(), ", "), f.product)
			}

			// The --port flag default (8006) targets Proxmox VE. When the operator
			// selects a product with a different default port without also giving
			// an explicit --port, switch to the product's standard port instead of
			// silently pointing a context at the wrong port.
			if !cmd.Flags().Changed("port") {
				f.port = config.DefaultPortForProduct(f.product)
			}

			// Validate port range.
			if f.port < 1 || f.port > 65535 {
				return fmt.Errorf("--port %d is out of range [1, 65535]", f.port)
			}

			// Validate protocol.
			switch f.protocol {
			case "https", "http":
			default:
				return fmt.Errorf("--protocol must be \"https\" or \"http\", got %q", f.protocol)
			}

			// Validate ssh.port and ssh.jump before writing anything, so a bad
			// --ssh-port or --ssh-jump never lands in a context that every
			// ssh/rsync command would then reject.
			if f.sshPort != 0 && (f.sshPort < 1 || f.sshPort > 65535) {
				return fmt.Errorf("ssh.port %d is out of range [1, 65535]", f.sshPort)
			}
			if f.sshJump != "" {
				if err := apiclient.CheckJumpChain("ssh.jump", f.sshJump); err != nil {
					return err
				}
			}

			// Build the proxy and timeout blocks and validate them with the
			// same checkers StrictValidateContext uses. add runs no strict
			// validation of its own, so these two calls are its only defense
			// against a malformed proxy URL or timeout ever reaching the
			// saved config.
			proxyBlock := config.ProxyBlock{
				URL:      f.proxyURL,
				Username: f.proxyUsername,
				Password: f.proxyPassword,
			}
			if cmd.Flags().Changed("proxy-from-env") {
				fromEnv := f.proxyFromEnv
				proxyBlock.FromEnv = &fromEnv
			}
			timeoutBlock := config.TimeoutBlock{
				Connect:      f.timeoutConnect,
				TLSHandshake: f.timeoutTLSHandshake,
				Request:      f.timeoutRequest,
			}
			var blockErrs []string
			blockErrs = append(blockErrs, config.ValidateProxyBlock(&proxyBlock)...)
			blockErrs = append(blockErrs, config.ValidateTimeoutBlock(&timeoutBlock)...)
			if len(blockErrs) > 0 {
				return errors.New(strings.Join(blockErrs, "; "))
			}

			deps := cli.GetDeps(cmd)

			cfg := deps.Cfg
			if cfg == nil {
				cfg = &config.Config{}
			}

			// Reject duplicate name unless --force.
			if cfg.Contexts != nil {
				if _, exists := cfg.Contexts[name]; exists && !f.force {
					return fmt.Errorf(
						"context %q already exists; use --force to overwrite",
						name,
					)
				}
			}

			// Warn to stderr if the secret looks like a literal (not an env ref or
			// keychain: prefix). This mirrors config.ResolveSecret's inline-literal
			// detection without resolving the secret here.
			if f.secret != "" && !config.IsSecretReference(f.secret) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"WARN: --secret looks like an inline literal; prefer ${ENV_VAR} or keychain:PATH")
			}
			if f.proxyPassword != "" && !config.IsSecretReference(f.proxyPassword) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"WARN: --proxy-password looks like an inline literal; prefer ${ENV_VAR} or keychain:PATH")
			}

			// Build the new Context.
			newCtx := &config.Context{
				Host:          f.host,
				Port:          f.port,
				Protocol:      f.protocol,
				Realm:         f.realm,
				DefaultNode:   f.defaultNode,
				DefaultOutput: f.defaultOutput,
				Product:       f.product,
				Auth: config.AuthBlock{
					Type:     f.authType,
					Username: f.username,
					TokenID:  f.tokenID,
					Secret:   f.secret,
				},
				TLS: config.TLSBlock{
					Insecure:    f.insecure,
					Fingerprint: f.fingerprint,
					CACert:      f.caCert,
					Tofu:        f.tofu,
				},
				SSH: config.SSHBlock{
					User:     f.sshUser,
					Port:     f.sshPort,
					Identity: f.sshIdentity,
					Jump:     f.sshJump,
				},
				Proxy:   proxyBlock,
				Timeout: timeoutBlock,
			}

			// Ensure the contexts map exists.
			if cfg.Contexts == nil {
				cfg.Contexts = make(map[string]*config.Context)
			}
			cfg.Contexts[name] = newCtx

			// If --select, update current-context.
			if f.selectCtx {
				if cfg.CurrentContext != "" && cfg.CurrentContext != name {
					cfg.PreviousContext = cfg.CurrentContext
				}
				cfg.CurrentContext = name
			}

			if err := config.Save(deps.ConfigPath, cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			msg := fmt.Sprintf("Context %q added.", name)
			if f.selectCtx {
				msg = fmt.Sprintf("Context %q added and selected.", name)
			}
			res := output.Result{Message: msg}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().StringVar(&f.host, "host", "", "Proxmox hostname or IP address (required)")
	cmd.Flags().IntVar(&f.port, "port", 8006,
		"API port (defaults to the --product's standard port: 8006 pve, 8007 pbs, 8443 pdm)")
	cmd.Flags().StringVar(&f.protocol, "protocol", "https", "connection scheme: https or http")
	cmd.Flags().StringVar(&f.realm, "realm", "pam", "authentication realm")
	cmd.Flags().StringVar(&f.authType, "auth-type", "token", "authentication type: token or password")
	cmd.Flags().StringVar(&f.username, "username", "",
		"Proxmox username (e.g. root@pam); required for token and password auth")
	cmd.Flags().StringVar(&f.tokenID, "token-id", "",
		"API token name (e.g. mytoken), or the full user@realm!mytoken identifier; required for --auth-type=token")
	cmd.Flags().StringVar(&f.secret, "secret", "", "token value or password; use ${ENV_VAR} or keychain:PATH to avoid inline literals")
	cmd.Flags().BoolVar(&f.insecure, "insecure", false, "disable TLS certificate verification")
	cmd.Flags().StringVar(&f.fingerprint, "fingerprint", "", "expected TLS certificate fingerprint (hex SHA-256)")
	cmd.Flags().StringVar(&f.caCert, "ca-cert", "", "path to a CA certificate (PEM) used to verify the server")
	cmd.Flags().BoolVar(&f.tofu, "tofu", false,
		"opt in to Trust-On-First-Use certificate pinning (TTY prompt on unknown cert; ignored with --insecure)")
	cmd.Flags().StringVar(&f.sshUser, "ssh-user", "", "default SSH login user for pmx ssh/rsync")
	cmd.Flags().IntVar(&f.sshPort, "ssh-port", 0, "default SSH port for pmx ssh/rsync")
	cmd.Flags().StringVar(&f.sshIdentity, "ssh-identity", "", "path to the SSH private key used by pmx ssh/rsync")
	cmd.Flags().StringVar(&f.sshJump, "ssh-jump", "",
		"jump host for ssh, rsync, and the API connection, as [user@]host[:port] (comma-separated for a chain)")
	cmd.Flags().StringVar(&f.proxyURL, "proxy-url", "",
		"proxy for API requests: socks5, socks5h, or http URL, without credentials")
	cmd.Flags().StringVar(&f.proxyUsername, "proxy-username", "", "username presented to the proxy")
	cmd.Flags().StringVar(&f.proxyPassword, "proxy-password", "",
		"proxy password; use ${ENV_VAR} or keychain:PATH to avoid inline literals")
	cmd.Flags().BoolVar(&f.proxyFromEnv, "proxy-from-env", false,
		"honour $HTTPS_PROXY (or $HTTP_PROXY) and $NO_PROXY for this context")
	cmd.Flags().StringVar(&f.timeoutConnect, "timeout-connect", "", "bound TCP connection setup for this context, e.g. 5s")
	cmd.Flags().StringVar(&f.timeoutTLSHandshake, "timeout-tls-handshake", "", "bound the TLS handshake for this context, e.g. 10s")
	cmd.Flags().StringVar(&f.timeoutRequest, "timeout-request", "", "bound one API request for this context, e.g. 30s")
	cmd.Flags().StringVar(&f.defaultNode, "default-node", "", "default Proxmox node for this context")
	cmd.Flags().StringVar(&f.defaultOutput, "default-output", "", "default output format for this context: table|ascii|plain|json|yaml")
	cmd.Flags().StringVar(&f.product, "product", config.ProductPVE,
		"Proxmox product this context targets: pve (Proxmox VE, port "+
			"8006), pbs (Proxmox Backup Server, 8007), or pdm (Proxmox "+
			"Datacenter Manager, 8443)")
	cmd.Flags().BoolVar(&f.selectCtx, "select", false, "make the new context the current context after adding")
	cmd.Flags().BoolVar(&f.force, "force", false, "overwrite an existing context with the same name")
	_ = cmd.RegisterFlagCompletionFunc("product", cli.ProductCompletion)

	cli.MustMarkRequired(cmd, "host")

	return cmd
}

// splitTokenID normalises the identity fields for token auth.
//
// A Proxmox API token is fully identified by "user@realm!tokenname". Operators
// commonly copy that whole string, so when tokenID contains "!" it is split into
// the embedded user (before the first "!") and the token name (after it):
//
//	splitTokenID("",         "root@pam!backup") → ("root@pam", "backup", nil)
//	splitTokenID("root@pam", "backup")          → ("root@pam", "backup", nil)
//
// When tokenID has no "!" the inputs pass through unchanged. An explicit
// username that disagrees with an embedded one is an error, as is a token name
// that still contains "@" or "!" after the split.
func splitTokenID(username, tokenID string) (user, token string, err error) {
	user, token = username, tokenID

	if strings.Contains(tokenID, "!") {
		embeddedUser, name, _ := strings.Cut(tokenID, "!")
		if embeddedUser == "" {
			return "", "", fmt.Errorf(
				"--token-id %q is missing the user@realm before %q", tokenID, "!")
		}
		if name == "" {
			return "", "", fmt.Errorf(
				"--token-id %q is missing the token name after %q", tokenID, "!")
		}
		if username != "" && username != embeddedUser {
			return "", "", fmt.Errorf(
				"--username %q conflicts with the user %q embedded in --token-id", username, embeddedUser)
		}
		user, token = embeddedUser, name
	}

	if strings.Contains(token, "!") {
		return "", "", fmt.Errorf(
			"--token-id token name %q must not contain %q; expected user@realm!tokenname", token, "!")
	}
	if strings.Contains(token, "@") {
		return "", "", fmt.Errorf(
			"--token-id token name %q must not contain %q; put user@realm in --username", token, "@")
	}
	if strings.Contains(user, "!") {
		return "", "", fmt.Errorf(
			`--username %q must not contain "!"; the token name belongs in --token-id`, user)
	}

	return user, token, nil
}
