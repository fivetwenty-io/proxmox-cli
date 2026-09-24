package context

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/redact"
)

// newShowCmd builds `pmx context show [<name>]` (alias: info).
// When name is omitted it defaults to the current context; errors if none set.
func newShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "show [<name>]",
		Aliases: []string{"info"},
		Short:   "Show a named context (defaults to the current context)",
		Long: "Display the full configuration of a named context: host, port, protocol, " +
			"realm, product, auth type, username, token id, TLS settings, default node, " +
			"default output format, ssh user/port/identity, ssh jump host, proxy url/" +
			"username/password/from-env, connect/TLS-handshake/request timeouts, and the CA " +
			"certificate bundle.\n\n" +
			"With no name, the command shows the current context, and errors if none is " +
			"set.\n\n" +
			"Inline secret literals are always redacted to \"***\". Environment-variable and " +
			"keychain references are shown as they are, since a reference reveals nothing on " +
			"its own. A $NAME reference whose variable is unset is also shown as \"***\", " +
			"because pmx would use it as a literal password. A proxy URL's embedded password " +
			"is always redacted, whether or not the URL parses. An ssh jump chain that " +
			"ValidateJumpChain accepts prints as written; one it rejects has any embedded " +
			"password masked the same way.\n\n" +
			"Port, protocol, and realm render the stored value with pmx's defaults applied. " +
			"SSH user, port, and identity render the stored value only, since their fallbacks " +
			"belong to the ssh client, not to pmx. Each timeout renders the stored value, or " +
			"the built-in default marked \"(default)\", or the stored value marked " +
			"\"(invalid)\" when it does not parse; an invalid timeout never fails this command. " +
			"Every row reflects the stored context only. Root flags such as --api-endpoint " +
			"and --api-connect-timeout, and PMX_API_* environment variables, are not applied " +
			"here.",
		Example: `  pmx context show
  pmx context show lab`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{"noClient": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			cfg := deps.Cfg

			// Resolve name: explicit arg > --context/-c > $PMX_CONTEXT > current-context.
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			if name == "" {
				name = targetName(deps)
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

			// Resolve product, port, protocol, and realm from a defaults-applied
			// clone of the stored context, so every context — current or not —
			// renders the value the API connection would use, without mutating
			// the stored config.
			effective := config.CloneContext(ctx)
			config.ApplyDefaults(effective)
			product := effective.Product
			port := effective.Port
			protocol := effective.Protocol
			realm := effective.Realm

			// Redact the secret for all output formats to prevent accidental exposure
			// in logs, shell history, or shared terminal output.
			redactedSecret := redactSecret(ctx.Auth.Secret)

			// The jump chain goes through apiclient.RedactJumpChain, which is the
			// identity for a chain ValidateJumpChain accepts, so a well-formed
			// hop still shows as written. A rejected chain may carry an embedded
			// password, so RedactJumpChain masks it here too.
			jump := apiclient.RedactJumpChain(ctx.SSH.Jump)

			// The proxy URL goes through redact.ProxyURL directly on the stored
			// string, so an embedded password is masked even when the value does
			// not parse as a URL at all.
			proxyURL := redact.ProxyURL(ctx.Proxy.URL)
			redactedProxyPassword := redactSecret(ctx.Proxy.Password)
			proxyFromEnv := false
			if ctx.Proxy.FromEnv != nil {
				proxyFromEnv = *ctx.Proxy.FromEnv
			}

			timeoutDefaults := apiclient.DefaultTimeoutSpec()
			timeoutConnect := renderTimeoutRow("timeout.connect", ctx.Timeout.Connect, timeoutDefaults.Connect)
			timeoutTLSHandshake := renderTimeoutRow(
				"timeout.tls-handshake", ctx.Timeout.TLSHandshake, timeoutDefaults.TLSHandshake)
			timeoutRequest := renderTimeoutRow("timeout.request", ctx.Timeout.Request, timeoutDefaults.Request)

			single := map[string]string{
				"NAME":                  name,
				"HOST":                  ctx.Host,
				"PORT":                  fmt.Sprintf("%d", port),
				"PRODUCT":               product,
				"PROTOCOL":              protocol,
				"REALM":                 realm,
				"AUTH TYPE":             ctx.Auth.Type,
				"USERNAME":              ctx.Auth.Username,
				"TOKEN ID":              ctx.Auth.TokenID,
				"SECRET":                redactedSecret,
				"INSECURE":              fmt.Sprintf("%v", ctx.TLS.Insecure),
				"FINGERPRINT":           ctx.TLS.Fingerprint,
				"TOFU":                  fmt.Sprintf("%v", ctx.TLS.Tofu),
				"DEFAULT NODE":          ctx.DefaultNode,
				"DEFAULT OUTPUT":        ctx.DefaultOutput,
				"SSH USER":              ctx.SSH.User,
				"SSH PORT":              fmt.Sprintf("%d", ctx.SSH.Port),
				"SSH IDENTITY":          ctx.SSH.Identity,
				"JUMP":                  jump,
				"PROXY":                 proxyURL,
				"PROXY USERNAME":        ctx.Proxy.Username,
				"PROXY PASSWORD":        redactedProxyPassword,
				"PROXY FROM ENV":        fmt.Sprintf("%v", proxyFromEnv),
				"TIMEOUT CONNECT":       timeoutConnect,
				"TIMEOUT TLS HANDSHAKE": timeoutTLSHandshake,
				"TIMEOUT REQUEST":       timeoutRequest,
				"CA CERT":               ctx.TLS.CACert,
			}

			// raw carries structured data for json/yaml; every secret is also
			// redacted the same way the table cell is.
			type rawContext struct {
				Name                string `json:"name"`
				Host                string `json:"host"`
				Port                int    `json:"port"`
				Product             string `json:"product"`
				Protocol            string `json:"protocol"`
				Realm               string `json:"realm"`
				AuthType            string `json:"auth_type"`
				Username            string `json:"username"`
				TokenID             string `json:"token_id"`
				Secret              string `json:"secret"`
				Insecure            bool   `json:"insecure"`
				Fingerprint         string `json:"fingerprint"`
				Tofu                bool   `json:"tofu"`
				DefaultNode         string `json:"default_node"`
				DefaultOutput       string `json:"default_output"`
				SSHUser             string `json:"ssh_user"`
				SSHPort             int    `json:"ssh_port"`
				SSHIdentity         string `json:"ssh_identity"`
				Jump                string `json:"jump"`
				Proxy               string `json:"proxy"`
				ProxyUsername       string `json:"proxy_username"`
				ProxyPassword       string `json:"proxy_password"`
				ProxyFromEnv        bool   `json:"proxy_from_env"`
				TimeoutConnect      string `json:"timeout_connect"`
				TimeoutTLSHandshake string `json:"timeout_tls_handshake"`
				TimeoutRequest      string `json:"timeout_request"`
				CACert              string `json:"ca_cert"`
			}

			raw := rawContext{
				Name:                name,
				Host:                ctx.Host,
				Port:                port,
				Product:             product,
				Protocol:            protocol,
				Realm:               realm,
				AuthType:            ctx.Auth.Type,
				Username:            ctx.Auth.Username,
				TokenID:             ctx.Auth.TokenID,
				Secret:              redactedSecret,
				Insecure:            ctx.TLS.Insecure,
				Fingerprint:         ctx.TLS.Fingerprint,
				Tofu:                ctx.TLS.Tofu,
				DefaultNode:         ctx.DefaultNode,
				DefaultOutput:       ctx.DefaultOutput,
				SSHUser:             ctx.SSH.User,
				SSHPort:             ctx.SSH.Port,
				SSHIdentity:         ctx.SSH.Identity,
				Jump:                jump,
				Proxy:               proxyURL,
				ProxyUsername:       ctx.Proxy.Username,
				ProxyPassword:       redactedProxyPassword,
				ProxyFromEnv:        proxyFromEnv,
				TimeoutConnect:      timeoutConnect,
				TimeoutTLSHandshake: timeoutTLSHandshake,
				TimeoutRequest:      timeoutRequest,
				CACert:              ctx.TLS.CACert,
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

// renderTimeoutRow formats one timeout field for display. An unset field
// (empty string) renders the built-in default marked "(default)". A field
// that fails config.ParseTimeout renders the stored string marked
// "(invalid)" rather than failing the command — `context show` is the verb
// an operator reaches for on a broken context, and it must survive one bad
// value to show the rest. Anything else renders verbatim.
func renderTimeoutRow(field, raw string, def time.Duration) string {
	if raw == "" {
		return fmt.Sprintf("%s (default)", def)
	}
	if _, err := config.ParseTimeout(field, raw); err != nil {
		return fmt.Sprintf("%s (invalid)", raw)
	}
	return raw
}

// redactSecret replaces a secret value with a masked placeholder while
// preserving the reference type, so the operator can see how the secret is
// stored without exposing the value itself.
//
// Rules:
//   - Empty string → "" (no secret configured).
//   - ${NAME} → shown as-is, whether or not NAME is set: ResolveSecret fails
//     on an unset ${NAME} rather than falling back to using it as a literal,
//     so there is no literal value here to protect.
//   - $NAME, a syntactically valid variable name → shown as-is when NAME is
//     set in the current environment. When NAME is unset, ResolveSecret
//     falls through and uses the string as a literal password, so it is
//     masked here too, matching how it will be used.
//   - keychain:PATH → shown as-is (the path is not secret).
//   - Any other value (an inline literal, including one that merely starts
//     with "$", such as "$uper$ecret") → "***".
func redactSecret(s string) string {
	if s == "" {
		return ""
	}
	if !config.IsSecretReference(s) {
		return "***"
	}
	if strings.HasPrefix(s, "$") && !strings.HasPrefix(s, "${") {
		if _, ok := os.LookupEnv(s[1:]); !ok {
			return "***"
		}
	}
	return s
}
