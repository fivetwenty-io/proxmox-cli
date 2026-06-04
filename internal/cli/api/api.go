package api

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
)

func init() {
	cli.RegisterGroup(newGroupCmd)
	// Hidden top-level aliases for the api/* sub-tree (A-03): `pve targets`,
	// `pve target`, `pve switch`, and `pve auth ...` behave identically to their
	// `pve api ...` forms. Each is registered as its own hidden top-level command
	// so it does not clutter help yet remains a working invocation.
	cli.RegisterGroup(func(_ *cli.Deps) *cobra.Command { return hidden(newTargetsCmd()) })
	cli.RegisterGroup(func(_ *cli.Deps) *cobra.Command { return hidden(newTargetCmd()) })
	cli.RegisterGroup(func(_ *cli.Deps) *cobra.Command { return hidden(newSwitchCmd()) })
	cli.RegisterGroup(func(_ *cli.Deps) *cobra.Command { return hidden(newAuthCmd()) })
}

// newGroupCmd is the registry factory; the placeholder deps are unused because
// every sub-command obtains live deps via cli.GetDeps at run time.
func newGroupCmd(_ *cli.Deps) *cobra.Command {
	return NewCommand()
}

// hidden marks cmd as a hidden top-level alias so it works but is omitted from
// `pve --help` listings.
func hidden(cmd *cobra.Command) *cobra.Command {
	cmd.Hidden = true
	return cmd
}

// NewCommand builds the `pve api` command and all of its sub-commands: target
// management (targets/target/switch) and authentication (auth login/logout/
// status/refresh/set-token/set-password). Every sub-command operates only on the
// local config file (and, for login/refresh, the PVE ticket endpoint), so each
// carries the noClient annotation to skip API-client construction.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Manage CLI targets and authentication",
		Long: "Manage named Proxmox VE targets in the local config file and " +
			"authenticate against them.",
	}

	cmd.AddCommand(
		newTargetsCmd(),
		newTargetCmd(),
		newSwitchCmd(),
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
