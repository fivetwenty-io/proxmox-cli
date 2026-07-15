package api

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
)

// Group is the factory for the `pmx api` command group (raw GET/POST/PUT/
// DELETE passthrough). The placeholder deps are unused because every
// sub-command obtains live deps via cli.GetDeps at run time.
func Group(_ *cli.Deps) *cobra.Command {
	return NewCommand()
}

// Auth is the factory for the canonical top-level `auth` command
// (login/logout/status/whoami/set-token/set-password). status, set-token,
// set-password, logout, login, refresh, and whoami all work with any
// context — Proxmox VE, Proxmox Backup Server, or Proxmox Datacenter
// Manager. whoami queries the server to verify credentials via
// Access.ListPermissions, selecting whichever of PVE, PBS, or PDM the root
// built for the resolved context.
func Auth(_ *cli.Deps) *cobra.Command { return newAuthCmd() }

// NewCommand builds the `pmx api` command and its sub-commands: raw
// GET/POST/PUT/DELETE passthrough requests against the active context's
// Proxmox VE, Proxmox Backup Server, or Proxmox Datacenter Manager API. The
// command carries the product:context annotation so the root resolves
// whichever client (PVE, PBS, or PDM) the active context targets; each raw
// sub-command then selects deps.PBS or deps.PDM when set, falling back to
// deps.API otherwise (see rawClient).
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Make raw Proxmox API requests against the active context",
		Long: "Issue raw GET/POST/PUT/DELETE requests against the active context's Proxmox VE, " +
			"Proxmox Backup Server, or Proxmox Datacenter Manager API, for endpoints this CLI does " +
			"not (yet) expose as a typed command. The response is rendered generically: as a " +
			"key/value table for a single JSON object, a table for an array of objects, and a " +
			"plain value otherwise; every format always preserves the full response losslessly via " +
			"--output json or --output yaml.",
		Example: `  pmx api get /nodes
  pmx api get /cluster/resources -d type=vm
  pmx api post /nodes/pve1/qemu/100/status/start
  pmx api put /nodes/pve1/qemu/100/config -d memory=4096`,
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
