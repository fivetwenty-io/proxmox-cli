package api

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
)

// Group is the factory for the `pmx api` command group (raw GET/POST/PUT/
// DELETE passthrough). The placeholder deps are unused because every
// sub-command obtains live deps via cli.GetDeps at run time.
func Group(_ *cli.Deps) *cobra.Command {
	return NewCommand()
}

// AuthAlias is the factory for the hidden top-level `pmx auth` alias
// (login/logout/status/refresh/set-token/set-password). It is the only way
// to reach authentication commands now that `pmx api` means raw passthrough;
// a later task promotes it to the canonical, non-hidden `pmx auth` command.
func AuthAlias(_ *cli.Deps) *cobra.Command { return hidden(newAuthCmd()) }

// hidden marks cmd as a hidden top-level alias so it works but is omitted from
// `pmx --help` listings.
func hidden(cmd *cobra.Command) *cobra.Command {
	cmd.Hidden = true
	return cmd
}

// NewCommand builds the `pmx api` command and its sub-commands: raw
// GET/POST/PUT/DELETE passthrough requests against the active context's
// Proxmox VE or Proxmox Backup Server API. The command carries the
// product:context annotation so the root resolves whichever client (PVE or
// PBS) the active context targets; each raw sub-command then selects
// deps.PBS when set, falling back to deps.API otherwise (see rawClient).
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Make raw Proxmox API requests against the active context",
		Long: "Issue raw GET/POST/PUT/DELETE requests against the active context's Proxmox VE or " +
			"Proxmox Backup Server API, for endpoints this CLI does not (yet) expose as a typed " +
			"command. The response is rendered generically: as a key/value table for a single JSON " +
			"object, a table for an array of objects, and a plain value otherwise; every format " +
			"always preserves the full response losslessly via --output json or --output yaml.",
		Annotations: map[string]string{cli.ProductAnnotation: cli.ProductFromContext},
	}

	cmd.AddCommand(newRawGetCmd(), newRawPostCmd(), newRawPutCmd(), newRawDeleteCmd())

	return cmd
}

// noClient marks a command so the root PersistentPreRunE skips building an API
// client (auth commands resolve everything from local config, building their
// own client on demand for login/refresh/whoami).
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
