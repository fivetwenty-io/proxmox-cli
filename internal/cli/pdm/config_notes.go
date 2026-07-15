package pdm

import (
	"fmt"

	"github.com/spf13/cobra"

	pdmconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pdm/config"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newConfigNotesCmd builds `pmx pdm config notes` — inspect and update the
// free-text notes shown on this Proxmox Datacenter Manager's dashboard
// (/config/notes). GET /config/notes returns a raw JSON string
// (ListNotesResponse is a `= json.RawMessage` alias, config_gen.go:1972,
// v3.6.0, and the PDM API schema declares its GET returns.type "string"), so
// this exposes `show`/`update` rather than `ls`/`add`/`delete`.
func newConfigNotesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notes",
		Short: "Inspect and update the dashboard welcome notes",
		Long: "Show and replace the free-text notes shown on this Proxmox " +
			"Datacenter Manager's dashboard (GET/PUT /config/notes).",
	}
	cmd.AddCommand(newConfigNotesShowCmd(), newConfigNotesUpdateCmd())
	return cmd
}

// newConfigNotesShowCmd builds `pmx pdm config notes show` — show the
// dashboard notes (GET /config/notes).
func newConfigNotesShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the dashboard welcome notes",
		Long:  "Show the free-text notes shown on this instance's dashboard (GET /config/notes).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PDM.Config.ListNotes(cmd.Context())
			if err != nil {
				return fmt.Errorf("get notes: %w", err)
			}

			if resp == nil || len(*resp) == 0 {
				res := output.Result{Message: "No notes are set."}
				return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
			}

			notes, err := decodeRawString(*resp)
			if err != nil {
				return fmt.Errorf("decode notes: %w", err)
			}

			res := output.Result{
				Single:  map[string]string{"notes": notes},
				Raw:     map[string]string{"notes": notes},
				Message: notes,
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newConfigNotesUpdateCmd builds `pmx pdm config notes update` — replace the
// dashboard notes (PUT /config/notes). --notes is required:
// UpdateNotesParams.Notes has no omitempty (config_gen.go:2005-2010,
// v3.6.0), so PDM always requires a (possibly empty) notes value on update.
func newConfigNotesUpdateCmd() *cobra.Command {
	var (
		notes  string
		digest string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Replace the dashboard welcome notes",
		Long: "Replace the free-text notes shown on this instance's dashboard (PUT " +
			"/config/notes). --notes is required; pass an empty string to clear it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			params := &pdmconfig.UpdateNotesParams{Notes: notes}
			if cmd.Flags().Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PDM.Config.UpdateNotes(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("update notes: %w", err)
			}

			res := output.Result{Message: "Notes updated."}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&notes, "notes", "", "new notes text (required; pass an empty string to clear)")
	f.StringVar(&digest, "digest", "", "only update if the current config digest matches")
	cli.MustMarkRequired(cmd, "notes")
	return cmd
}
