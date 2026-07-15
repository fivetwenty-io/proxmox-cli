package pdm

import (
	"fmt"

	"github.com/spf13/cobra"

	pdmconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/config"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newConfigWebauthnCmd builds `pmx pdm config webauthn` — inspect and
// update the WebAuthn relying-party configuration used for second-factor
// (TFA) authentication (/config/access/tfa/webauthn). GET returns a single
// object (config_gen.go's ListAccessTfaWebauthnResponse, v3.6.0 — not a
// []json.RawMessage list), so this exposes `show`/`update` rather than
// `ls`/`add`/`delete`, matching realm_pam.go/realm_pdm.go's singleton
// pattern. None of its properties declare a schema default
// (pdm-apidoc.json PUT /config/access/tfa/webauthn parameters, verified
// 2026-07-08: every "default" key is absent), so there is no `--defaults`
// flag here.
func newConfigWebauthnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webauthn",
		Short: "Inspect and update the WebAuthn relying-party configuration",
		Long: "Show and update the WebAuthn relying-party configuration used for " +
			"second-factor (TFA) authentication (GET/PUT " +
			"/config/access/tfa/webauthn). Changing the relying-party ID may break " +
			"existing WebAuthn credentials.",
	}
	cmd.AddCommand(newConfigWebauthnShowCmd(), newConfigWebauthnUpdateCmd())
	return cmd
}

// newConfigWebauthnShowCmd builds `pmx pdm config webauthn show` — show the
// WebAuthn configuration (GET /config/access/tfa/webauthn).
func newConfigWebauthnShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the WebAuthn relying-party configuration",
		Long:  "Show the WebAuthn relying-party configuration (GET /config/access/tfa/webauthn).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Config.ListAccessTfaWebauthn(cmd.Context())
			if err != nil {
				return fmt.Errorf("get webauthn configuration: %w", err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode webauthn configuration: %w", err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newConfigWebauthnUpdateCmd builds `pmx pdm config webauthn update` —
// update the WebAuthn configuration (PUT /config/access/tfa/webauthn). Only
// flags explicitly set are sent; use --delete to reset properties to their
// default instead.
func newConfigWebauthnUpdateCmd() *cobra.Command {
	var (
		id              string
		rp              string
		origin          string
		allowSubdomains bool
		del             []string
		digest          string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the WebAuthn relying-party configuration",
		Long: "Update the WebAuthn relying-party configuration (PUT " +
			"/config/access/tfa/webauthn). Only flags explicitly set are sent; use " +
			"--delete to reset properties to their default instead. Changing --id " +
			"*will* break existing WebAuthn credentials; changing --origin or --rp " +
			"*may* break them.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			fl := cmd.Flags()
			if !anyFlagChanged(fl) {
				return fmt.Errorf("update webauthn configuration: no changes requested: pass at least one flag")
			}

			if fl.Changed("delete") {
				for _, key := range del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pdmconfig.UpdateAccessTfaWebauthnParams{}
			if fl.Changed("id") {
				params.Id = strPtr(id)
			}
			if fl.Changed("rp") {
				params.Rp = strPtr(rp)
			}
			if fl.Changed("origin") {
				params.Origin = strPtr(origin)
			}
			if fl.Changed("allow-subdomains") {
				params.AllowSubdomains = boolPtr(allowSubdomains)
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PDM.Config.UpdateAccessTfaWebauthn(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("update webauthn configuration: %w", err)
			}

			res := output.Result{Message: "Webauthn configuration updated."}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&id, "id", "", "relying party ID: the domain name, without protocol, port, or location")
	f.StringVar(&rp, "rp", "", "relying party name: any text identifier")
	f.StringVar(&origin, "origin", "", "site origin: an https:// URL (or http://localhost)")
	f.BoolVar(&allowSubdomains, "allow-subdomains", false, "consider subdomains of --origin valid as well")
	f.StringArrayVar(&del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&digest, "digest", "", "only update if the current config digest matches")
	return cmd
}
