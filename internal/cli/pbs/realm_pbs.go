package pbs

import (
	"fmt"

	"github.com/spf13/cobra"

	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newRealmPbsCmd builds `pmx pbs realm pbs` — inspect and update the
// built-in Proxmox Backup Server realm configuration
// (/config/access/pbs). GET /config/access/pbs returns a single object
// (there is exactly one system-wide PBS realm), not a list, so this exposes
// `show`/`update` rather than `ls`/`add`/`delete` — the PBS realm cannot be
// created or deleted, only its comment/default flag configured.
func newRealmPbsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pbs",
		Short: "Inspect and update the built-in Proxmox Backup Server realm",
		Long: "Show and update the built-in Proxmox Backup Server realm's " +
			"configuration (GET/PUT /config/access/pbs). This realm always " +
			"exists and cannot be created or deleted; only its comment and " +
			"default-realm flag can be changed.",
	}
	cmd.AddCommand(newRealmPbsShowCmd(), newRealmPbsUpdateCmd())
	return cmd
}

// newRealmPbsShowCmd builds `pmx pbs realm pbs show` — show the built-in PBS
// realm's configuration (GET /config/access/pbs).
func newRealmPbsShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the built-in PBS realm's configuration",
		Long:  "Show the built-in Proxmox Backup Server realm's configuration (GET /config/access/pbs).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListAccessPbs(cmd.Context())
			if err != nil {
				return fmt.Errorf("get PBS realm: %w", err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode PBS realm: %w", err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newRealmPbsUpdateCmd builds `pmx pbs realm pbs update` — update the
// built-in PBS realm's configuration (PUT /config/access/pbs). Only flags
// explicitly set are sent; use --delete to reset properties to their
// default.
func newRealmPbsUpdateCmd() *cobra.Command {
	var (
		comment   string
		isDefault bool
		del       []string
		digest    string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update the built-in PBS realm's configuration",
		Long: "Update the built-in Proxmox Backup Server realm's comment and " +
			"default-realm flag (PUT /config/access/pbs). Only flags explicitly " +
			"set are sent; use --delete to reset properties to their default instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update PBS realm: no changes given: pass at least one flag")
			}

			if cmd.Flags().Changed("delete") {
				for _, key := range del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateAccessPbsParams{}

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

			_, err := deps.PBS.Config.UpdateAccessPbs(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("update PBS realm: %w", err)
			}

			res := output.Result{Message: "PBS realm updated."}
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
