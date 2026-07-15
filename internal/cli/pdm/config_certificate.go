package pdm

import (
	"fmt"

	"github.com/spf13/cobra"

	pdmconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/config"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newConfigCertificateCmd builds `pmx pdm config certificate` — inspect and
// update the ACME account and per-domain configuration used to issue this
// Proxmox Datacenter Manager's own TLS certificate (/config/certificate).
// GET /config/certificate returns a single object (config_gen.go's
// ListCertificateResponse, v3.6.0 — not a []json.RawMessage list), so this
// exposes `show`/`update` rather than `ls`/`add`/`delete`; none of its
// properties declare a schema default (pdm-apidoc.json PUT
// /config/certificate parameters, verified 2026-07-08: every "default" key
// is absent), so unlike remote.go/realm_openid.go there is no `--defaults`
// flag here.
func newConfigCertificateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "certificate",
		Short: "Inspect and update the certificate/ACME-domain configuration",
		Long: "Show and update the ACME account and per-domain configuration used to " +
			"issue this Proxmox Datacenter Manager's own TLS certificate " +
			"(GET/PUT /config/certificate). This configures which ACME account and " +
			"domains to use; it does not itself order or renew a certificate.",
	}
	cmd.AddCommand(newConfigCertificateShowCmd(), newConfigCertificateUpdateCmd())
	return cmd
}

// newConfigCertificateShowCmd builds `pmx pdm config certificate show` —
// show the certificate/ACME-domain configuration (GET /config/certificate).
func newConfigCertificateShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the certificate/ACME-domain configuration",
		Long:  "Show the ACME account and per-domain configuration used to issue this instance's own TLS certificate (GET /config/certificate).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Config.ListCertificate(cmd.Context())
			if err != nil {
				return fmt.Errorf("get certificate configuration: %w", err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode certificate configuration: %w", err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newConfigCertificateUpdateCmd builds `pmx pdm config certificate update` —
// update the certificate/ACME-domain configuration (PUT
// /config/certificate). Only flags explicitly set are sent; use --delete to
// reset properties to their default instead.
func newConfigCertificateUpdateCmd() *cobra.Command {
	var (
		acme                                                            string
		acmedomain0, acmedomain1, acmedomain2, acmedomain3, acmedomain4 string
		del                                                             []string
		digest                                                          string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the certificate/ACME-domain configuration",
		Long: "Update the ACME account and per-domain configuration used to issue " +
			"this instance's own TLS certificate (PUT /config/certificate). Only " +
			"flags explicitly set are sent; use --delete to reset properties to " +
			"their default instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			fl := cmd.Flags()
			if !anyFlagChanged(fl) {
				return fmt.Errorf("update certificate configuration: no changes requested: pass at least one flag")
			}

			if fl.Changed("delete") {
				for _, key := range del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pdmconfig.UpdateCertificateParams{}
			if fl.Changed("acme") {
				params.Acme = strPtr(acme)
			}
			if fl.Changed("acmedomain0") {
				params.Acmedomain0 = strPtr(acmedomain0)
			}
			if fl.Changed("acmedomain1") {
				params.Acmedomain1 = strPtr(acmedomain1)
			}
			if fl.Changed("acmedomain2") {
				params.Acmedomain2 = strPtr(acmedomain2)
			}
			if fl.Changed("acmedomain3") {
				params.Acmedomain3 = strPtr(acmedomain3)
			}
			if fl.Changed("acmedomain4") {
				params.Acmedomain4 = strPtr(acmedomain4)
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PDM.Config.UpdateCertificate(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("update certificate configuration: %w", err)
			}

			res := output.Result{Message: "Certificate configuration updated."}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&acme, "acme", "", "name of the ACME account to use")
	f.StringVar(&acmedomain0, "acmedomain0", "", "ACME domain configuration string for slot 0")
	f.StringVar(&acmedomain1, "acmedomain1", "", "ACME domain configuration string for slot 1")
	f.StringVar(&acmedomain2, "acmedomain2", "", "ACME domain configuration string for slot 2")
	f.StringVar(&acmedomain3, "acmedomain3", "", "ACME domain configuration string for slot 3")
	f.StringVar(&acmedomain4, "acmedomain4", "", "ACME domain configuration string for slot 4")
	f.StringArrayVar(&del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&digest, "digest", "", "only update if the current config digest matches")
	return cmd
}
