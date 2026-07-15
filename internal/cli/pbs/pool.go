package pbs

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"

	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
)

// newTapePoolCmd builds `pmx pbs tape pool` — create, inspect, update, and
// delete tape media pool configurations (GET/POST/PUT/DELETE
// /config/media-pool).
func newTapePoolCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pool",
		Short: "Manage tape media pool configurations",
		Long: "Create, inspect, update, and delete tape media pool configurations " +
			"(GET/POST/PUT/DELETE /config/media-pool).",
	}
	cmd.AddCommand(
		newTapePoolLsCmd(),
		newTapePoolShowCmd(),
		newTapePoolAddCmd(),
		newTapePoolUpdateCmd(),
		newTapePoolDeleteCmd(),
	)
	return cmd
}

// tapePoolEntry is the decoded shape of one element of GET /config/media-pool
// and of the full response of GET /config/media-pool/{name}.
type tapePoolEntry struct {
	Allocation *string `json:"allocation,omitempty"`
	Comment    *string `json:"comment,omitempty"`
	Encrypt    *string `json:"encrypt,omitempty"`
	Name       string  `json:"name"`
	Retention  *string `json:"retention,omitempty"`
	Template   *string `json:"template,omitempty"`
}

// decodeTapePoolEntries decodes a Config.ListMediaPool response into typed
// entries, skipping any element that fails to decode.
func decodeTapePoolEntries(resp *pbsconfig.ListMediaPoolResponse) []tapePoolEntry {
	entries := make([]tapePoolEntry, 0)
	if resp == nil {
		return entries
	}

	for _, raw := range *resp {
		var e tapePoolEntry

		err := json.Unmarshal(raw, &e)
		if err != nil {
			continue
		}

		entries = append(entries, e)
	}

	return entries
}

// newTapePoolLsCmd builds `pmx pbs tape pool ls` — list every tape media pool
// configuration (GET /config/media-pool).
func newTapePoolLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Short:   "List tape media pool configurations",
		Long:    "List every tape media pool configuration visible to the caller (GET /config/media-pool).",
		Example: "  pmx pbs tape pool ls",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Config.ListMediaPool(cmd.Context())
			if err != nil {
				return fmt.Errorf("list tape media pools: %w", err)
			}

			entries := decodeTapePoolEntries(resp)
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{"NAME", "ALLOCATION", "RETENTION", "ENCRYPT", "TEMPLATE", "COMMENT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Name, pbsFormatOptionalString(e.Allocation), pbsFormatOptionalString(e.Retention),
					pbsFormatOptionalString(e.Encrypt), pbsFormatOptionalString(e.Template),
					pbsFormatOptionalString(e.Comment),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: entries}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newTapePoolShowCmd builds `pmx pbs tape pool show <name>` — show one tape
// media pool's full configuration (GET /config/media-pool/{name}).
func newTapePoolShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show one tape media pool's configuration",
		Long: "Show every populated field of a single tape media pool configuration " +
			"(GET /config/media-pool/{name}).",
		Example: "  pmx pbs tape pool show weekly-tapes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("media pool name must not be empty")
			}

			resp, err := deps.PBS.Config.GetMediaPool(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("show tape media pool %q: %w", name, err)
			}

			if resp == nil {
				return fmt.Errorf("show tape media pool %q: nil response from PBS", name)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode tape media pool %q: %w", name, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// tapePoolFlags collects the media-pool attribute flags shared by `pool add`
// and `pool update`. Every field maps directly onto a CreateMediaPoolParams /
// UpdateMediaPoolParams field of the same name.
type tapePoolFlags struct {
	allocation string
	comment    string
	encrypt    string
	retention  string
	template   string

	// update-only
	del []string
}

// registerCommon binds the attribute flags accepted by both `pool add` and
// `pool update`.
func (pf *tapePoolFlags) registerCommon(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&pf.allocation, "allocation", "",
		"media set allocation policy: 'continue', 'always', or a calendar event")
	f.StringVar(&pf.comment, "comment", "", "comment")
	f.StringVar(&pf.encrypt, "encrypt", "", "tape encryption key fingerprint (sha256)")
	f.StringVar(&pf.retention, "retention", "", "media retention policy: 'overwrite', 'keep', or a time span")
	f.StringVar(&pf.template, "template", "",
		"media set naming template (may contain strftime() time format specifications)")
}

// registerUpdate binds every flag `pool update` accepts, including the
// update-only --delete flag.
func (pf *tapePoolFlags) registerUpdate(cmd *cobra.Command) {
	pf.registerCommon(cmd)
	cmd.Flags().StringArrayVar(&pf.del, "delete", nil, "property name to reset to its default (repeatable)")
}

// applyAdd builds the create payload, forwarding optional flags only when set.
func (pf *tapePoolFlags) applyAdd(cmd *cobra.Command, p *pbsconfig.CreateMediaPoolParams) {
	fl := cmd.Flags()
	if fl.Changed("allocation") {
		p.Allocation = strPtr(pf.allocation)
	}

	if fl.Changed("comment") {
		p.Comment = strPtr(pf.comment)
	}

	if fl.Changed("encrypt") {
		p.Encrypt = strPtr(pf.encrypt)
	}

	if fl.Changed("retention") {
		p.Retention = strPtr(pf.retention)
	}

	if fl.Changed("template") {
		p.Template = strPtr(pf.template)
	}
}

// applyUpdate builds the update payload, forwarding optional flags only when set.
func (pf *tapePoolFlags) applyUpdate(cmd *cobra.Command, p *pbsconfig.UpdateMediaPoolParams) {
	fl := cmd.Flags()
	if fl.Changed("allocation") {
		p.Allocation = strPtr(pf.allocation)
	}

	if fl.Changed("comment") {
		p.Comment = strPtr(pf.comment)
	}

	if fl.Changed("encrypt") {
		p.Encrypt = strPtr(pf.encrypt)
	}

	if fl.Changed("retention") {
		p.Retention = strPtr(pf.retention)
	}

	if fl.Changed("template") {
		p.Template = strPtr(pf.template)
	}

	if fl.Changed("delete") {
		p.Delete = pf.del
	}
}

// newTapePoolAddCmd builds `pmx pbs tape pool add <name>` — create a new tape
// media pool configuration (POST /config/media-pool). Every attribute is
// optional and only forwarded when explicitly set.
func newTapePoolAddCmd() *cobra.Command {
	var pf tapePoolFlags
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a tape media pool",
		Long: "Create a new tape media pool configuration (POST /config/media-pool). " +
			"Every attribute is optional and only forwarded when explicitly set.",
		Example: `  pmx pbs tape pool add weekly-tapes --allocation continue --retention keep`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("media pool name must not be empty")
			}

			params := &pbsconfig.CreateMediaPoolParams{Name: name}
			pf.applyAdd(cmd, params)

			err := deps.PBS.Config.CreateMediaPool(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create tape media pool %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Tape media pool %q created.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	pf.registerCommon(cmd)
	return cmd
}

// newTapePoolUpdateCmd builds `pmx pbs tape pool update <name>` — update a
// tape media pool configuration (PUT /config/media-pool/{name}). Only flags
// explicitly set are sent; use --delete to reset properties to their default.
func newTapePoolUpdateCmd() *cobra.Command {
	var pf tapePoolFlags
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update a tape media pool",
		Long: "Update an existing tape media pool configuration (PUT " +
			"/config/media-pool/{name}). Only flags explicitly set are sent; use " +
			"--delete to reset properties to their default instead.",
		Example: "  pmx pbs tape pool update weekly-tapes --retention keep",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("media pool name must not be empty")
			}

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update tape media pool %q: no changes requested: pass at least one flag", name)
			}

			if cmd.Flags().Changed("delete") {
				for _, prop := range pf.del {
					if prop == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateMediaPoolParams{}
			pf.applyUpdate(cmd, params)

			err := deps.PBS.Config.UpdateMediaPool(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("update tape media pool %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Tape media pool %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	pf.registerUpdate(cmd)
	return cmd
}

// newTapePoolDeleteCmd builds `pmx pbs tape pool delete <name>` — remove a
// tape media pool configuration (DELETE /config/media-pool/{name}). This
// endpoint accepts no parameters beyond the pool name; there is no digest
// guard.
func newTapePoolDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a tape media pool",
		Long: "Remove a tape media pool configuration (DELETE /config/media-pool/{name}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Example: "  pmx pbs tape pool delete weekly-tapes --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			if name == "" {
				return fmt.Errorf("media pool name must not be empty")
			}

			if !yes {
				return fmt.Errorf("refusing to delete tape media pool %q without confirmation: pass --yes/-y", name)
			}

			err := deps.PBS.Config.DeleteMediaPool(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("delete tape media pool %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Tape media pool %q deleted.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")
	return cmd
}
