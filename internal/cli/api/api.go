package api

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

// Group is the factory for the `pve api` command group. The placeholder deps
// are unused because every sub-command obtains live deps via cli.GetDeps at
// run time.
func Group(_ *cli.Deps) *cobra.Command {
	return NewCommand()
}

// AuthAlias is the factory for the hidden top-level `pve auth` alias, which
// behaves identically to `pve api auth`.
func AuthAlias(_ *cli.Deps) *cobra.Command { return hidden(newAuthCmd()) }

// hidden marks cmd as a hidden top-level alias so it works but is omitted from
// `pve --help` listings.
func hidden(cmd *cobra.Command) *cobra.Command {
	cmd.Hidden = true
	return cmd
}

// NewCommand builds the `pve api` command and its sub-commands: authentication
// (auth login/logout/status/refresh/set-token/set-password). Every sub-command
// operates only on the local config file (and, for login/refresh, the PVE
// ticket endpoint), so each carries the noClient annotation to skip API-client
// construction.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Manage CLI authentication",
		Long: "Manage authentication against named Proxmox VE contexts in the " +
			"local config file.",
	}

	cmd.AddCommand(
		newAuthCmd(),
	)

	return cmd
}

// noClient marks a command so the root PersistentPreRunE skips building an API
// client (api commands resolve everything from local config).
func noClient(cmd *cobra.Command) *cobra.Command {
	cmd.Annotations = map[string]string{"noClient": "true"}
	return cmd
}

// configPath returns the resolved --config flag value inherited from the root
// command. The flag is always registered on the root, so lookup cannot fail in
// normal operation; an empty string is returned defensively if it is absent.
func configPath(cmd *cobra.Command) string {
	if f := cmd.Flags().Lookup("config"); f != nil {
		return f.Value.String()
	}
	return ""
}
