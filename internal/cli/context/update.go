package context

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// updateFlags holds the raw flag values for `pmx context update`.
type updateFlags struct {
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
}

// updateFieldFlags lists every flag on `context update` that maps to a
// persisted config.Context field; at least one must be set for the command
// to do anything.
var updateFieldFlags = []string{
	"host", "port", "protocol", "realm", "auth-type", "username", "token-id",
	"secret", "insecure", "fingerprint", "ca-cert", "tofu", "default-node",
	"default-output", "product", "ssh-user", "ssh-port", "ssh-identity", "ssh-jump",
	"proxy-url", "proxy-username", "proxy-password", "proxy-from-env",
	"timeout-connect", "timeout-tls-handshake", "timeout-request",
}

// newUpdateCmd builds `pmx context update [<name>]`: change individual fields
// of an existing context without opening $EDITOR.
func newUpdateCmd() *cobra.Command {
	var f updateFlags

	cmd := &cobra.Command{
		Use:   "update [<name>]",
		Short: "Update individual fields of an existing context",
		Long: `Update individual fields of an existing context without opening $EDITOR.

Only fields whose flags are given change; everything else is preserved.
Without a name the current context is updated. The result is validated with
the same rules as 'context add' and 'context validate' before it is saved,
so an update can never write a context that fails validation.

Changing --product without --port re-defaults a port that still equals the
old product's default to the new product's port (8006 pve, 8007 pbs,
8443 pdm); a customized port is kept with a note.`,
		Example: `  # Rotate a token secret to a keychain reference
  pmx context update lab --secret 'keychain:pve-cli/lab'

  # Fix the token identity by pasting the full identifier Proxmox shows
  pmx context update backup --token-id 'pmx@pbs!admin'

  # Move a context to another host and pin its certificate
  pmx context update lab --host pve2.example.com --fingerprint AA:BB:...`,
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
						"use 'pmx context update <name>' or 'pmx context select <name>' first",
				)
			}

			if cfg.Contexts == nil {
				return fmt.Errorf("context %q not found: config has no contexts", name)
			}
			ctx, ok := cfg.Contexts[name]
			if !ok {
				return fmt.Errorf("context %q not found", name)
			}

			flags := cmd.Flags()
			if !slices.ContainsFunc(updateFieldFlags, flags.Changed) {
				return fmt.Errorf(
					"no fields to update: pass at least one field flag, e.g. --host or --secret; " +
						"use 'context edit' to edit the whole context in $EDITOR")
			}

			// Mutate a copy so a failed validation leaves the in-memory config
			// untouched.
			updated := *ctx

			// Inline-literal warnings are collected here and printed only after
			// strict validation passes, so a rejected update never warns about a
			// value it never saved.
			var warnings []string

			// Product first: its port re-default rule must see the old port
			// before an explicit --port (applied below) overrides it.
			if flags.Changed("product") {
				if !config.IsValidProduct(f.product) {
					return fmt.Errorf("--product must be one of: %s, got %q",
						strings.Join(config.Products(), ", "), f.product)
				}
				if !flags.Changed("port") {
					oldDefault := config.DefaultPortForProduct(updated.ProductOrDefault())
					newDefault := config.DefaultPortForProduct(f.product)
					switch {
					case updated.Port == 0 || updated.Port == oldDefault:
						updated.Port = newDefault
					case updated.Port != newDefault:
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
							"note: port %d kept; %s default is %d\n", updated.Port, f.product, newDefault)
					}
				}
				updated.Product = f.product
			}

			if flags.Changed("host") {
				updated.Host = f.host
			}
			if flags.Changed("port") {
				updated.Port = f.port
			}
			if flags.Changed("protocol") {
				updated.Protocol = f.protocol
			}
			if flags.Changed("realm") {
				updated.Realm = f.realm
			}
			if flags.Changed("default-node") {
				updated.DefaultNode = f.defaultNode
			}
			if flags.Changed("default-output") {
				updated.DefaultOutput = f.defaultOutput
			}

			if flags.Changed("auth-type") {
				updated.Auth.Type = f.authType
			}
			// A full user@realm!tokenname pasted into --token-id also carries
			// the username; splitTokenID extracts it and rejects a conflicting
			// explicit --username. A bare token name leaves the username alone.
			if flags.Changed("token-id") {
				explicitUser := ""
				if flags.Changed("username") {
					explicitUser = f.username
				}
				user, tokenID, err := splitTokenID(explicitUser, f.tokenID)
				if err != nil {
					return err
				}
				if user != "" {
					updated.Auth.Username = user
				}
				updated.Auth.TokenID = tokenID
			} else if flags.Changed("username") {
				updated.Auth.Username = f.username
			}
			if flags.Changed("secret") {
				updated.Auth.Secret = f.secret
				if f.secret != "" && !config.IsSecretReference(f.secret) {
					warnings = append(warnings,
						"WARN: --secret looks like an inline literal; prefer ${ENV_VAR} or keychain:PATH")
				}
			}

			if flags.Changed("insecure") {
				updated.TLS.Insecure = f.insecure
			}
			if flags.Changed("fingerprint") {
				updated.TLS.Fingerprint = f.fingerprint
			}
			if flags.Changed("ca-cert") {
				updated.TLS.CACert = f.caCert
			}
			if flags.Changed("tofu") {
				updated.TLS.Tofu = f.tofu
			}

			if flags.Changed("ssh-user") {
				updated.SSH.User = f.sshUser
			}
			if flags.Changed("ssh-port") {
				updated.SSH.Port = f.sshPort
			}
			if flags.Changed("ssh-identity") {
				updated.SSH.Identity = f.sshIdentity
			}
			if flags.Changed("ssh-jump") {
				updated.SSH.Jump = f.sshJump
			}

			if flags.Changed("proxy-url") {
				updated.Proxy.URL = f.proxyURL
				if f.proxyURL == "" {
					// Clearing the URL also clears the credentials tied to
					// it, because leaving them would fail validation with
					// "proxy.username is set but proxy.url is empty".
					updated.Proxy.Username = ""
					updated.Proxy.Password = ""
				}
			}
			if flags.Changed("proxy-username") {
				updated.Proxy.Username = f.proxyUsername
			}
			if flags.Changed("proxy-password") {
				updated.Proxy.Password = f.proxyPassword
				if f.proxyPassword != "" && !config.IsSecretReference(f.proxyPassword) {
					warnings = append(warnings,
						"WARN: --proxy-password looks like an inline literal; prefer ${ENV_VAR} or keychain:PATH")
				}
			}
			if flags.Changed("proxy-from-env") {
				// A fresh pointer is assigned rather than writing through the
				// stored one, so a context loaded before this update cannot
				// have its from-env value mutated by reference elsewhere.
				fromEnv := f.proxyFromEnv
				updated.Proxy.FromEnv = &fromEnv
			}

			if flags.Changed("timeout-connect") {
				updated.Timeout.Connect = f.timeoutConnect
			}
			if flags.Changed("timeout-tls-handshake") {
				updated.Timeout.TLSHandshake = f.timeoutTLSHandshake
			}
			if flags.Changed("timeout-request") {
				updated.Timeout.Request = f.timeoutRequest
			}

			// Same write-time rule set as add/edit/validate.
			config.ApplyDefaults(&updated)
			strictErrs := config.StrictValidateContext(&updated)
			// StrictValidateContext checks ssh.port's range but not ssh.jump's
			// syntax (internal/config cannot import internal/apiclient), so a
			// changed --ssh-jump is checked here and its message joins the
			// strict ones, so one run reports every problem under one prefix.
			if flags.Changed("ssh-jump") && updated.SSH.Jump != "" {
				if err := apiclient.CheckJumpChain("ssh.jump", updated.SSH.Jump); err != nil {
					strictErrs = append(strictErrs, err.Error())
				}
			}
			if len(strictErrs) > 0 {
				return fmt.Errorf("context %q fails validation after update: %s",
					name, strings.Join(strictErrs, "; "))
			}

			for _, w := range warnings {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), w)
			}

			cfg.Contexts[name] = &updated

			if err := config.Save(deps.ConfigPath, cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			res := output.Result{Message: fmt.Sprintf("Context %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Flags().StringVar(&f.host, "host", "", "Proxmox hostname or IP address")
	cmd.Flags().IntVar(&f.port, "port", 0, "API port")
	cmd.Flags().StringVar(&f.protocol, "protocol", "", "connection scheme: https or http")
	cmd.Flags().StringVar(&f.realm, "realm", "", "authentication realm")
	cmd.Flags().StringVar(&f.authType, "auth-type", "", "authentication type: token or password")
	cmd.Flags().StringVar(&f.username, "username", "", "Proxmox username (e.g. root@pam)")
	cmd.Flags().StringVar(&f.tokenID, "token-id", "",
		"API token name (e.g. mytoken), or the full user@realm!mytoken identifier (also updates the username)")
	cmd.Flags().StringVar(&f.secret, "secret", "",
		"token value or password; use ${ENV_VAR} or keychain:PATH to avoid inline literals")
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
	cmd.Flags().StringVar(&f.defaultOutput, "default-output", "",
		"default output format for this context: table|ascii|plain|json|yaml")
	cmd.Flags().StringVar(&f.product, "product", "",
		"Proxmox product this context targets: pve, pbs, or pdm; without --port the "+
			"old product's default port is re-defaulted to the new product's port")
	_ = cmd.RegisterFlagCompletionFunc("product", cli.ProductCompletion)
	cmd.ValidArgsFunction = cli.FirstArgContextNames

	return cmd
}
