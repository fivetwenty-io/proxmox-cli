package pdm

import (
	"fmt"

	"github.com/spf13/cobra"

	pdmconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/config"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newRealmPamCmd builds `pmx pdm realm pam` — inspect and update the
// built-in PAM realm configuration (/config/access/pam). GET
// /config/access/pam returns a single object (config_gen.go's
// ListAccessPamResponse, v3.6.0 — not a []json.RawMessage list, unlike
// ListAccessAd/Ldap/Openid), so this exposes `show`/`update` rather than
// `ls`/`add`/`delete` — the PAM realm cannot be created or deleted, only its
// comment/default flag configured.
func newRealmPamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pam",
		Short: "Inspect and update the built-in PAM realm",
		Long: "Show and update the built-in PAM authentication realm's configuration " +
			"(GET/PUT /config/access/pam). The PAM realm always exists and cannot be " +
			"created or deleted; only its comment and default-realm flag can be changed.",
	}
	cmd.AddCommand(newRealmPamShowCmd(), newRealmPamUpdateCmd())
	return cmd
}

// newRealmPamShowCmd builds `pmx pdm realm pam show` — show the PAM realm's
// configuration (GET /config/access/pam).
func newRealmPamShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the PAM realm's configuration",
		Long:  "Show the built-in PAM realm's configuration (GET /config/access/pam).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Config.ListAccessPam(cmd.Context())
			if err != nil {
				return fmt.Errorf("get PAM realm: %w", err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode PAM realm: %w", err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newRealmPamUpdateCmd builds `pmx pdm realm pam update` — update the PAM
// realm's configuration (PUT /config/access/pam). Only flags explicitly set
// are sent; use --delete to reset properties to their default.
func newRealmPamUpdateCmd() *cobra.Command {
	var (
		comment   string
		isDefault bool
		del       []string
		digest    string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the PAM realm's configuration",
		Long: "Update the built-in PAM realm's comment and default-realm flag " +
			"(PUT /config/access/pam). Only flags explicitly set are sent; use " +
			"--delete to reset properties to their default instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update PAM realm: no changes requested: pass at least one flag")
			}

			if cmd.Flags().Changed("delete") {
				for _, key := range del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pdmconfig.UpdateAccessPamParams{}

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

			_, err := deps.PDM.Config.UpdateAccessPam(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("update PAM realm: %w", err)
			}

			res := output.Result{Message: "PAM realm updated."}
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
