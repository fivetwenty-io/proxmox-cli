package pdm

import (
	"fmt"

	"github.com/spf13/cobra"

	pdmconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/config"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newRealmPdmCmd builds `pmx pdm realm pdm` — inspect and update the
// built-in Proxmox Datacenter Manager authentication-server realm
// configuration (/config/access/pdm). GET /config/access/pdm returns a
// single object (config_gen.go's ListAccessPdmResponse, v3.6.0 — not a
// []json.RawMessage list), so this exposes `show`/`update` rather than
// `ls`/`add`/`delete` — this realm cannot be created or deleted, only its
// comment/default flag configured.
func newRealmPdmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pdm",
		Short: "Inspect and update the built-in PDM realm",
		Long: "Show and update the built-in Proxmox Datacenter Manager authentication " +
			"server realm's configuration (GET/PUT /config/access/pdm). This realm " +
			"always exists and cannot be created or deleted; only its comment and " +
			"default-realm flag can be changed.",
	}
	cmd.AddCommand(newRealmPdmShowCmd(), newRealmPdmUpdateCmd())
	return cmd
}

// newRealmPdmShowCmd builds `pmx pdm realm pdm show` — show the built-in PDM
// realm's configuration (GET /config/access/pdm).
func newRealmPdmShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the built-in PDM realm's configuration",
		Long:  "Show the built-in Proxmox Datacenter Manager authentication server realm's configuration (GET /config/access/pdm).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Config.ListAccessPdm(cmd.Context())
			if err != nil {
				return fmt.Errorf("get PDM realm: %w", err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode PDM realm: %w", err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newRealmPdmUpdateCmd builds `pmx pdm realm pdm update` — update the
// built-in PDM realm's configuration (PUT /config/access/pdm). Only flags
// explicitly set are sent; use --delete to reset properties to their
// default.
func newRealmPdmUpdateCmd() *cobra.Command {
	var (
		comment   string
		isDefault bool
		del       []string
		digest    string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the built-in PDM realm's configuration",
		Long: "Update the built-in Proxmox Datacenter Manager authentication server " +
			"realm's comment and default-realm flag (PUT /config/access/pdm). Only " +
			"flags explicitly set are sent; use --delete to reset properties to " +
			"their default instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update PDM realm: no changes requested: pass at least one flag")
			}

			if cmd.Flags().Changed("delete") {
				for _, key := range del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pdmconfig.UpdateAccessPdmParams{}

			fl := cmd.Flags()
			if fl.Changed("comment") {
				params.Comment = strPtr(comment)
			}
			if fl.Changed("default") {
				params.Default = boolPtr(isDefault)
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}

			_, err := deps.PDM.Config.UpdateAccessPdm(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("update PDM realm: %w", err)
			}

			res := output.Result{Message: "PDM realm updated."}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&comment, "comment", "", "comment")
	f.BoolVar(&isDefault, "default", false, "select this realm by default on login")
	f.StringArrayVar(&del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&digest, "digest", "", "only update if the current config digest matches")
	return cmd
}
