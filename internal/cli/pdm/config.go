package pdm

import (
	"github.com/spf13/cobra"
)

// newConfigCmd builds `pmx pdm config` — manage this Proxmox Datacenter
// Manager instance's own host configuration: ACME accounts and challenge
// plugins used to issue its own TLS certificate, the certificate/ACME-domain
// binding, the dashboard welcome notes, saved resource views, and the
// WebAuthn (TFA) relying-party configuration
// (/config/acme, /config/certificate, /config/notes, /config/views,
// /config/access/tfa/webauthn).
//
// GET /config, GET /config/access, and GET /config/access/tfa are not
// exposed as commands: per the PDM API schema each is a directory-index
// endpoint whose declared return type is "null" — it carries no data of its
// own, only routing to the children mounted below it. Rendering one as a
// data-bearing command would misrepresent an empty response as a real result.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage this Proxmox Datacenter Manager's own host configuration",
		Long: "Manage this Proxmox Datacenter Manager instance's own host " +
			"configuration: ACME accounts and challenge plugins, the " +
			"certificate/ACME-domain binding, the dashboard welcome notes, saved " +
			"resource views, and the WebAuthn (TFA) relying-party configuration. " +
			"This is distinct from 'pdm remote', which manages the PVE/PBS " +
			"remotes this instance is itself managing.",
	}
	cmd.AddCommand(
		newConfigAcmeCmd(),
		newConfigCertificateCmd(),
		newConfigNotesCmd(),
		newConfigViewCmd(),
		newConfigWebauthnCmd(),
	)
	return cmd
}
