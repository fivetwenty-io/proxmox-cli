package context

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/config"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newShowCmd builds `pmx context show [<name>]` (alias: info).
// When name is omitted it defaults to the current context; errors if none set.
func newShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "show [<name>]",
		Aliases:     []string{"info"},
		Short:       "Show a named context (defaults to the current context)",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			// Resolve name: explicit arg > current-context.
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if name == "" {
				name = cfg.CurrentContext
			}
			if name == "" {
				return fmt.Errorf(
					"no context name specified and no current-context is set; " +
						"use 'pmx context show <name>' or 'pmx context select <name>' first",
				)
			}

			if cfg.Contexts == nil {
				return fmt.Errorf("context %q not found: config has no contexts", name)
			}
			ctx, ok := cfg.Contexts[name]
			if !ok || ctx == nil {
				return fmt.Errorf("context %q not found in config", name)
			}

			// Resolve display product (default "pve" for display only — do not
			// mutate the stored config).
			product := ctx.Product
			if product == "" {
				product = config.ProductPVE
			}

			// Resolve display port (apply the product-aware default for display
			// only — do not mutate the stored config).
			port := ctx.Port
			if port == 0 {
				port = config.DefaultPortForProduct(product)
			}

			// Redact the secret for all output formats to prevent accidental exposure
			// in logs, shell history, or shared terminal output.
			redactedSecret := redactSecret(ctx.Auth.Secret)

			single := map[string]string{
				"NAME":           name,
				"HOST":           ctx.Host,
				"PORT":           fmt.Sprintf("%d", port),
				"PRODUCT":        product,
				"PROTOCOL":       ctx.Protocol,
				"REALM":          ctx.Realm,
				"AUTH TYPE":      ctx.Auth.Type,
				"USERNAME":       ctx.Auth.Username,
				"TOKEN ID":       ctx.Auth.TokenID,
				"SECRET":         redactedSecret,
				"INSECURE":       fmt.Sprintf("%v", ctx.TLS.Insecure),
				"FINGERPRINT":    ctx.TLS.Fingerprint,
				"TOFU":           fmt.Sprintf("%v", ctx.TLS.Tofu),
				"DEFAULT NODE":   ctx.DefaultNode,
				"DEFAULT OUTPUT": ctx.DefaultOutput,
			}

			// raw carries structured data for json/yaml; secret is also redacted.
			type rawContext struct {
				Name          string `json:"name"`
				Host          string `json:"host"`
				Port          int    `json:"port"`
				Product       string `json:"product"`
				Protocol      string `json:"protocol"`
				Realm         string `json:"realm"`
				AuthType      string `json:"auth_type"`
				Username      string `json:"username"`
				TokenID       string `json:"token_id"`
				Secret        string `json:"secret"`
				Insecure      bool   `json:"insecure"`
				Fingerprint   string `json:"fingerprint"`
				Tofu          bool   `json:"tofu"`
				DefaultNode   string `json:"default_node"`
				DefaultOutput string `json:"default_output"`
			}

			raw := rawContext{
				Name:          name,
				Host:          ctx.Host,
				Port:          port,
				Product:       product,
				Protocol:      ctx.Protocol,
				Realm:         ctx.Realm,
				AuthType:      ctx.Auth.Type,
				Username:      ctx.Auth.Username,
				TokenID:       ctx.Auth.TokenID,
				Secret:        redactedSecret,
				Insecure:      ctx.TLS.Insecure,
				Fingerprint:   ctx.TLS.Fingerprint,
				Tofu:          ctx.TLS.Tofu,
				DefaultNode:   ctx.DefaultNode,
				DefaultOutput: ctx.DefaultOutput,
			}

			res := output.Result{
				Single: single,
				Raw:    raw,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.ValidArgsFunction = cli.FirstArgContextNames
	return cmd
}

// redactSecret replaces the secret value with a masked placeholder while
// preserving the reference type so the operator can see how the secret is
// stored without exposing the value itself.
//
// Rules:
//   - Empty string → "" (no secret configured).
//   - ${NAME} or $NAME env ref → shown as-is (the variable name is not secret).
//   - keychain:PATH → shown as-is (the path is not secret).
//   - Any other value (inline literal) → "***".
func redactSecret(s string) string {
	if s == "" {
		return ""
	}
	// Environment-variable reference — safe to display; value is not here.
	if len(s) > 1 && s[0] == '$' {
		return s
	}
	// keychain reference — safe to display the path.
	if len(s) > 9 && s[:9] == "keychain:" {
		return s
	}
	// Inline literal — redact.
	return "***"
}
