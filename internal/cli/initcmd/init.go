package initcmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/config"
	"github.com/fivetwenty-io/pve-cli/internal/output"
)

// Group is the factory for the `pve init` command group. Deps are unused
// because the sub-commands resolve everything from flags and the local
// filesystem at run time.
func Group(_ *cli.Deps) *cobra.Command {
	return NewCommand()
}

// NewCommand builds `pve init` and its sub-commands. Everything here operates on
// the local config file only, so the group carries the noClient annotation to
// skip API-client construction.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold local CLI configuration",
		Long:  "Scaffold local Proxmox VE CLI configuration files.",
	}
	cmd.AddCommand(newConfigCmd())
	return cmd
}

// configTemplate is the commented config.yml emitted by `pve init config`. It
// documents every supported field so the user can fill it in by hand. The
// rendered file still parses as valid YAML once a context's placeholders are set.
const configTemplate = `# pve CLI configuration.
# Location: ~/.config/pve/config.yml (override with --config or $XDG_CONFIG_HOME).
# This file is written 0600; keep it that way — it may hold credentials.

# Name of the context used when --context/-c is not given. Set this to one of
# the keys under "contexts:" below once you have filled a context in.
current-context: lab

# Default output format for every command: table | plain | json | yaml.
default-output: table

contexts:
  # A context is one Proxmox VE endpoint. Rename "lab" to anything you like; the
  # name is what you pass to --context and "current-context" above. Add as many
  # contexts as you need.
  lab:
    host: pve.example.com    # hostname or IP of any node in the cluster
    port: 8006               # HTTPS API port (default 8006)
    protocol: https          # https (default) or http
    realm: pam               # authentication realm (pam, pve, ...)
    default-node: pve1       # node used when --node is omitted (optional)

    auth:
      # type: token (recommended) or password.
      type: token

      # PVE user the token or password belongs to, e.g. root@pam or ci@pve.
      username: root@pam

      # For type: token only — the token identifier (the part after the '!').
      token-id: automation

      # The token secret or password. Resolution, in order:
      #   ${VAR} or $VAR  read from the environment ( ${VAR} errors if unset )
      #   keychain:path   read from the system keychain
      #   any other value used verbatim (plaintext; emits a one-time warning)
      secret: ${PVE_TOKEN}

    tls:
      insecure: false        # true disables certificate verification (lab only)
      fingerprint: ""        # pin a hex SHA-256 cert fingerprint instead
      ca-cert: ""            # path to a PEM CA bundle for custom trust
`

// newConfigCmd builds `pve init config`, which writes the commented template to
// the resolved config path. It refuses to clobber an existing file without
// --force so a populated config is never silently overwritten.
func newConfigCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "config",
		Short: "Write a config.yml template to fill in",
		Long: "Write a commented config.yml template to the config path " +
			"(default ~/.config/pve/config.yml) for you to fill in.\n\n" +
			"Refuses to overwrite an existing file unless --force is given.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			path := configPath(cmd)

			if !force {
				if _, err := os.Stat(path); err == nil {
					return fmt.Errorf(
						"config file %s already exists; pass --force to overwrite it", path)
				} else if !os.IsNotExist(err) {
					return fmt.Errorf("stat config file %s: %w", path, err)
				}
			}

			if err := config.WriteRaw(path, []byte(configTemplate), force); err != nil {
				return fmt.Errorf("write config template: %w", err)
			}

			msg := fmt.Sprintf(
				"Wrote config template to %s. Edit it, then run `pve cluster status` to test.",
				path)
			return deps.Out.Render(cmd.OutOrStdout(),
				output.Result{Message: msg}, deps.Format)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config file")

	cmd.Annotations = map[string]string{"noClient": "true"}
	return cmd
}

// configPath returns the resolved --config flag value inherited from the root
// command. The flag is always registered on the root; an empty string is
// returned defensively if it is somehow absent.
func configPath(cmd *cobra.Command) string {
	if f := cmd.Flags().Lookup("config"); f != nil {
		return f.Value.String()
	}
	return ""
}
