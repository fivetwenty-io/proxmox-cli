package context

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/config"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// addFlags holds the raw flag values for `pve context add`.
type addFlags struct {
	host          string
	port          int
	protocol      string
	realm         string
	authType      string
	username      string
	tokenID       string
	secret        string
	insecure      bool
	fingerprint   string
	defaultNode   string
	defaultOutput string
	selectCtx     bool
	force         bool
}

// newAddCmd builds `pve context add <name>` (alias: create).
func newAddCmd() *cobra.Command {
	var f addFlags

	cmd := &cobra.Command{
		Use:         "add <name>",
		Aliases:     []string{"create"},
		Short:       "Add a new named context to the config file",
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
			}
			if f.authType == "password" {
				if f.username == "" {
					return fmt.Errorf("--username is required when --auth-type is \"password\"")
				}
				if f.secret == "" {
					return fmt.Errorf("--secret is required when --auth-type is \"password\"")
				}
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

			deps := resolveDeps(cmd)

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
			if f.secret != "" && !isSecretRef(f.secret) {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
					"WARN: --secret looks like an inline literal; prefer ${ENV_VAR} or keychain:PATH")
			}

			// Build the new Context.
			newCtx := &config.Context{
				Host:          f.host,
				Port:          f.port,
				Protocol:      f.protocol,
				Realm:         f.realm,
				DefaultNode:   f.defaultNode,
				DefaultOutput: f.defaultOutput,
				Auth: config.AuthBlock{
					Type:     f.authType,
					Username: f.username,
					TokenID:  f.tokenID,
					Secret:   f.secret,
				},
				TLS: config.TLSBlock{
					Insecure:    f.insecure,
					Fingerprint: f.fingerprint,
				},
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

	cmd.Flags().StringVar(&f.host, "host", "", "Proxmox VE hostname or IP address (required)")
	cmd.Flags().IntVar(&f.port, "port", 8006, "API port")
	cmd.Flags().StringVar(&f.protocol, "protocol", "https", "connection scheme: https or http")
	cmd.Flags().StringVar(&f.realm, "realm", "pam", "authentication realm")
	cmd.Flags().StringVar(&f.authType, "auth-type", "token", "authentication type: token or password")
	cmd.Flags().StringVar(&f.username, "username", "", "PVE username (e.g. root@pam); required for --auth-type=password")
	cmd.Flags().StringVar(&f.tokenID, "token-id", "", "API token identifier (e.g. mytoken); required for --auth-type=token")
	cmd.Flags().StringVar(&f.secret, "secret", "", "token value or password; use ${ENV_VAR} or keychain:PATH to avoid inline literals")
	cmd.Flags().BoolVar(&f.insecure, "insecure", false, "disable TLS certificate verification")
	cmd.Flags().StringVar(&f.fingerprint, "fingerprint", "", "expected TLS certificate fingerprint (hex SHA-256)")
	cmd.Flags().StringVar(&f.defaultNode, "default-node", "", "default Proxmox node for this context")
	cmd.Flags().StringVar(&f.defaultOutput, "default-output", "", "default output format for this context: table|ascii|plain|json|yaml")
	cmd.Flags().BoolVar(&f.selectCtx, "select", false, "make the new context the current context after adding")
	cmd.Flags().BoolVar(&f.force, "force", false, "overwrite an existing context with the same name")

	_ = cmd.MarkFlagRequired("host")

	return cmd
}

// isSecretRef reports whether s is an environment-variable reference
// (${NAME} or $NAME) or a keychain reference (keychain:PATH). Literals
// trigger an inline-secret warning in the add verb.
func isSecretRef(s string) bool {
	return strings.HasPrefix(s, "${") ||
		strings.HasPrefix(s, "$") ||
		strings.HasPrefix(s, "keychain:")
}
