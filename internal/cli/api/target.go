package api

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/config"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// targetFlags holds every flag accepted by the target sub-commands. Because the
// target name precedes the action verb (`target <name> add`), all actions are
// dispatched from a single cobra command and share one flag set.
type targetFlags struct {
	host           string
	port           int
	realm          string
	token          string
	username       string
	tlsInsecure    bool
	tlsFingerprint string
	tlsCACert      string
	defaultNode    string
	doSwitch       bool
	yes            bool
}

// newTargetCmd builds `pve api target <name> <show|add|remove>`. The action verb
// follows the target name, so the command parses both positionally and routes to
// the matching handler.
func newTargetCmd() *cobra.Command {
	var f targetFlags

	cmd := &cobra.Command{
		Use:   "target <name> <show|add|remove>",
		Short: "Inspect or modify a single target",
		Long: "Inspect or modify a single named target.\n\n" +
			"  pve api target <name> show     show the target's configuration\n" +
			"  pve api target <name> add      add a new target\n" +
			"  pve api target <name> remove   remove the target",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, action := args[0], args[1]
			switch action {
			case "show":
				return runTargetShow(cmd, name)
			case "add":
				return runTargetAdd(cmd, name, &f)
			case "remove":
				return runTargetRemove(cmd, name, &f)
			default:
				return fmt.Errorf("unknown action %q: expected show, add, or remove", action)
			}
		},
	}

	cmd.Flags().StringVar(&f.host, "host", "", "PVE host or IP (required for add)")
	cmd.Flags().IntVar(&f.port, "port", 8006, "PVE API port")
	cmd.Flags().StringVar(&f.realm, "realm", "pam", "authentication realm")
	cmd.Flags().StringVar(&f.token, "token", "", "API token as tokenid=secret")
	cmd.Flags().StringVar(&f.username, "username", "", "PVE username (e.g. root@pam)")
	cmd.Flags().BoolVar(&f.tlsInsecure, "tls-insecure", false, "disable TLS verification")
	cmd.Flags().StringVar(&f.tlsFingerprint, "tls-fingerprint", "", "pinned TLS certificate fingerprint")
	cmd.Flags().StringVar(&f.tlsCACert, "tls-ca-cert", "", "path to a PEM CA certificate")
	cmd.Flags().StringVar(&f.defaultNode, "default-node", "", "default node for this target")
	cmd.Flags().BoolVar(&f.doSwitch, "switch", false, "switch to this target after adding")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "confirm removal without prompting")

	return noClient(cmd)
}

// runTargetShow renders a target's configuration as a key/value table.
func runTargetShow(cmd *cobra.Command, name string) error {
	deps := cli.GetDeps(cmd)

	t, ok := deps.Cfg.Targets[name]
	if !ok || t == nil {
		return fmt.Errorf("target %q not found", name)
	}

	single := map[string]string{
		"Name":           name,
		"Host":           t.Host,
		"Port":           portString(t.Port),
		"Protocol":       protocolOrDefault(t.Protocol),
		"Realm":          realmOrDefault(t.Realm),
		"Auth-type":      t.Auth.Type,
		"Username":       t.Auth.Username,
		"Token-ID":       t.Auth.TokenID,
		"Secret-source":  secretSource(t.Auth.Secret),
		"TLS":            tlsSummary(t.TLS),
		"Default-node":   t.DefaultNode,
		"Default-output": t.DefaultOutput,
		"Current":        boolMark(name == deps.Cfg.CurrentTarget),
	}

	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Single: single}, deps.Format)
}

// runTargetAdd writes a new token-auth target to the config.
func runTargetAdd(cmd *cobra.Command, name string, f *targetFlags) error {
	deps := cli.GetDeps(cmd)

	if f.host == "" {
		return fmt.Errorf("--host is required")
	}

	cfg := deps.Cfg
	if cfg.Targets == nil {
		cfg.Targets = map[string]*config.Target{}
	}

	target := &config.Target{
		Host:        f.host,
		Port:        f.port,
		Realm:       f.realm,
		DefaultNode: f.defaultNode,
		TLS: config.TLSBlock{
			Insecure:    f.tlsInsecure,
			Fingerprint: f.tlsFingerprint,
			CACert:      f.tlsCACert,
		},
	}

	tokenID, secret := splitToken(f.token)
	target.Auth = config.AuthBlock{
		Type:     "token",
		Username: f.username,
		TokenID:  tokenID,
		Secret:   secret,
	}

	cfg.Targets[name] = target
	if f.doSwitch {
		cfg.CurrentTarget = name
	}

	if err := config.SaveForce(configPath(cmd), cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	msg := fmt.Sprintf("Added target %q (%s).", name, f.host)
	if f.doSwitch {
		msg = fmt.Sprintf("Added target %q (%s) and switched to it.", name, f.host)
	}
	return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
}

// runTargetRemove deletes a target from the config, clearing current-target if
// it pointed at the removed entry.
func runTargetRemove(cmd *cobra.Command, name string, f *targetFlags) error {
	deps := cli.GetDeps(cmd)

	cfg := deps.Cfg
	if _, ok := cfg.Targets[name]; !ok {
		return fmt.Errorf("target %q not found", name)
	}

	if !f.yes {
		return fmt.Errorf("refusing to remove target %q without --yes/-y", name)
	}

	delete(cfg.Targets, name)
	if cfg.CurrentTarget == name {
		cfg.CurrentTarget = ""
	}

	if err := config.SaveForce(configPath(cmd), cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	return deps.Out.Render(cmd.OutOrStdout(),
		output.Result{Message: fmt.Sprintf("Removed target %q.", name)}, deps.Format)
}

// splitToken splits a "tokenid=secret" string into its parts. If no '=' is
// present the whole value is treated as the secret with an empty token id.
func splitToken(token string) (tokenID, secret string) {
	if token == "" {
		return "", ""
	}
	if i := strings.IndexByte(token, '='); i >= 0 {
		return token[:i], token[i+1:]
	}
	return "", token
}

// portString renders a port, substituting the default when zero.
func portString(port int) string {
	if port == 0 {
		port = 8006
	}
	return strconv.Itoa(port)
}

// realmOrDefault returns the realm or "pam" when empty.
func realmOrDefault(realm string) string {
	if realm == "" {
		return "pam"
	}
	return realm
}

// protocolOrDefault returns the protocol or "https" when empty.
func protocolOrDefault(protocol string) string {
	if protocol == "" {
		return "https"
	}
	return protocol
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

// tlsSummary renders a short TLS status string.
func tlsSummary(tls config.TLSBlock) string {
	if tls.Insecure {
		return "insecure (verification disabled)"
	}
	if tls.Fingerprint != "" {
		return "pinned fingerprint"
	}
	return "verify"
}

// boolMark renders a yes/no marker.
func boolMark(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
