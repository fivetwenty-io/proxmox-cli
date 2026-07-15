package pbs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
	pbstape "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/tape"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

// newTapeChangerCmd builds `pmx pbs tape changer` — manage tape changer
// (autoloader/library) device configuration (/config/changer CRUD), and
// inspect the runtime device: attached-device scan, per-drive/slot status,
// and slot-to-slot media transfer (/tape/changer, /tape/scan-changers).
func newTapeChangerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "changer",
		Short: "Manage tape changer devices",
		Long: "List, inspect, create, update, and delete tape changer " +
			"(autoloader/library) configurations, scan for attached SCSI changer " +
			"devices, read a changer's drive/slot status, and transfer media " +
			"between two slots.",
	}
	cmd.AddCommand(
		newTapeChangerLsCmd(),
		newTapeChangerShowCmd(),
		newTapeChangerAddCmd(),
		newTapeChangerUpdateCmd(),
		newTapeChangerDeleteCmd(),
		newTapeChangerScanCmd(),
		newTapeChangerStatusCmd(),
		newTapeChangerTransferCmd(),
	)
	return cmd
}

// tapeChangerListEntry is the decoded shape of one element of GET
// /tape/changer: the configured changer plus autodetected model info.
type tapeChangerListEntry struct {
	EjectBeforeUnload *bool   `json:"eject-before-unload,omitempty"`
	ExportSlots       *string `json:"export-slots,omitempty"`
	Model             *string `json:"model,omitempty"`
	Name              string  `json:"name"`
	Path              string  `json:"path"`
	Serial            *string `json:"serial,omitempty"`
	Vendor            *string `json:"vendor,omitempty"`
}

// newTapeChangerLsCmd builds `pmx pbs tape changer ls` — list configured
// changers with runtime model information (GET /tape/changer).
func newTapeChangerLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List tape changers with runtime model information",
		Long: "List the configured tape changers together with the autodetected " +
			"model, vendor, and serial number (GET /tape/changer).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Tape.ListChanger(cmd.Context())
			if err != nil {
				return fmt.Errorf("list tape changers: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]tapeChangerListEntry, 0, len(items))

			for _, raw := range items {
				var e tapeChangerListEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode tape changer entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{"NAME", "PATH", "MODEL", "VENDOR", "SERIAL", "EXPORT-SLOTS", "EJECT-BEFORE-UNLOAD"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Name, e.Path, pbsFormatOptionalString(e.Model), pbsFormatOptionalString(e.Vendor),
					pbsFormatOptionalString(e.Serial), pbsFormatOptionalString(e.ExportSlots),
					pbsFormatOptionalBool(e.EjectBeforeUnload),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newTapeChangerShowCmd builds `pmx pbs tape changer show <name>` — show a
// single changer's configuration (GET /config/changer/{name}).
func newTapeChangerShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a single tape changer's configuration",
		Long:  "Show every populated field of a single tape changer configuration (GET /config/changer/{name}).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			resp, err := deps.PBS.Config.GetChanger(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("get tape changer %q: %w", name, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode tape changer %q: %w", name, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// tapeChangerFlags collects the changer attribute flags shared by `add` and
// `update`. Every field maps directly onto a CreateChangerParams /
// UpdateChangerParams field of the same name.
type tapeChangerFlags struct {
	ejectBeforeUnload bool
	exportSlots       string
	path              string

	// update-only
	del    []string
	digest string
}

// registerCommon binds the attribute flags accepted by both `add` and `update`.
func (cf *tapeChangerFlags) registerCommon(cmd *cobra.Command) {
	f := cmd.Flags()
	f.BoolVar(&cf.ejectBeforeUnload, "eject-before-unload", false,
		"eject tapes manually before unloading, instead of relying on the changer to do it")
	f.StringVar(&cf.exportSlots, "export-slots", "",
		"comma-separated slot numbers reserved for import/export (media in them are considered offline)")
	f.StringVar(&cf.path, "path", "", "path to the Linux generic SCSI device, e.g. '/dev/sg4'")
}

// registerUpdate binds every flag `update` accepts, including the
// update-only delete/digest fields.
func (cf *tapeChangerFlags) registerUpdate(cmd *cobra.Command) {
	cf.registerCommon(cmd)
	f := cmd.Flags()
	f.StringArrayVar(&cf.del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&cf.digest, "digest", "", "only update if the current config digest matches")
}

// applyUpdate builds the update payload, forwarding optional flags only when set.
func (cf *tapeChangerFlags) applyUpdate(cmd *cobra.Command, p *pbsconfig.UpdateChangerParams) {
	fl := cmd.Flags()
	if fl.Changed("eject-before-unload") {
		p.EjectBeforeUnload = &cf.ejectBeforeUnload
	}
	if fl.Changed("export-slots") {
		p.ExportSlots = &cf.exportSlots
	}
	if fl.Changed("path") {
		p.Path = &cf.path
	}
	if fl.Changed("delete") {
		p.Delete = cf.del
	}
	if fl.Changed("digest") {
		p.Digest = &cf.digest
	}
}

// newTapeChangerAddCmd builds `pmx pbs tape changer add <name>` — create a
// tape changer configuration (POST /config/changer). --path is required;
// every other option is optional.
func newTapeChangerAddCmd() *cobra.Command {
	var cf tapeChangerFlags
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a tape changer configuration",
		Long: "Create a new tape changer (autoloader/library) configuration (POST " +
			"/config/changer). --path is required; every other option is optional " +
			"and only forwarded when explicitly set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			if cf.path == "" {
				return fmt.Errorf("--path is required")
			}

			params := &pbsconfig.CreateChangerParams{Name: name, Path: cf.path}

			fl := cmd.Flags()
			if fl.Changed("eject-before-unload") {
				params.EjectBeforeUnload = &cf.ejectBeforeUnload
			}

			if fl.Changed("export-slots") {
				params.ExportSlots = &cf.exportSlots
			}

			err := deps.PBS.Config.CreateChanger(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create tape changer %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Tape changer %q created.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cf.registerCommon(cmd)
	cli.MustMarkRequired(cmd, "path")
	return cmd
}

// newTapeChangerUpdateCmd builds `pmx pbs tape changer update <name>` —
// update a tape changer configuration (PUT /config/changer/{name}). Only
// flags explicitly set are sent; use --delete to reset properties to their
// default instead.
func newTapeChangerUpdateCmd() *cobra.Command {
	var cf tapeChangerFlags
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update a tape changer configuration",
		Long: "Update an existing tape changer configuration (PUT /config/changer/{name}). " +
			"Only flags explicitly set are sent; use --delete to reset properties to their " +
			"default instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			if !anyFlagChanged(cmd.Flags()) {
				return fmt.Errorf("update tape changer %q: no changes requested: pass at least one flag", name)
			}

			if cmd.Flags().Changed("delete") {
				for _, key := range cf.del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateChangerParams{}
			cf.applyUpdate(cmd, params)

			err := deps.PBS.Config.UpdateChanger(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("update tape changer %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Tape changer %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cf.registerUpdate(cmd)
	return cmd
}

// newTapeChangerDeleteCmd builds `pmx pbs tape changer delete <name>` —
// remove a tape changer configuration (DELETE /config/changer/{name}). The
// generated binding takes no parameters (unlike most delete endpoints in
// this API, this one has no --digest guard): the PBS API schema declares no
// request body for it.
func newTapeChangerDeleteCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a tape changer configuration",
		Long: "Remove a tape changer configuration (DELETE /config/changer/{name}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete tape changer %q without confirmation: pass --yes/-y", name)
			}

			err := deps.PBS.Config.DeleteChanger(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("delete tape changer %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Tape changer %q deleted.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")

	return cmd
}

// tapeChangerScanEntry is the decoded shape of one element of GET
// /tape/scan-changers: an autodetected SCSI changer device.
type tapeChangerScanEntry struct {
	Kind   string `json:"kind"`
	Major  int64  `json:"major"`
	Minor  int64  `json:"minor"`
	Model  string `json:"model"`
	Path   string `json:"path"`
	Serial string `json:"serial"`
	Vendor string `json:"vendor"`
}

// newTapeChangerScanCmd builds `pmx pbs tape changer scan` — scan for
// attached SCSI tape changer devices (GET /tape/scan-changers).
func newTapeChangerScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan for attached SCSI tape changer devices",
		Long:  "List autodetected SCSI tape changer devices attached to this host (GET /tape/scan-changers).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Tape.ListScanChangers(cmd.Context())
			if err != nil {
				return fmt.Errorf("scan tape changers: %w", err)
			}

			items := rawItemsOf(resp)
			entries := make([]tapeChangerScanEntry, 0, len(items))

			for _, raw := range items {
				var e tapeChangerScanEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode tape changer scan entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

			headers := []string{"PATH", "KIND", "MODEL", "VENDOR", "SERIAL", "MAJOR", "MINOR"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					e.Path, e.Kind, e.Model, e.Vendor, e.Serial,
					strconv.FormatInt(e.Major, 10), strconv.FormatInt(e.Minor, 10),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// tapeChangerStatusEntry is the decoded shape of one element of GET
// /tape/changer/{name}/status: a status entry for one drive or slot.
type tapeChangerStatusEntry struct {
	EntryId    int64   `json:"entry-id"`
	EntryKind  string  `json:"entry-kind"`
	LabelText  *string `json:"label-text,omitempty"`
	LoadedSlot *int64  `json:"loaded-slot,omitempty"`
	State      *string `json:"state,omitempty"`
}

// newTapeChangerStatusCmd builds `pmx pbs tape changer status <name>` —
// show a changer's per-drive/slot status (GET /tape/changer/{name}/status).
func newTapeChangerStatusCmd() *cobra.Command {
	var cache bool
	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show a tape changer's drive and slot status",
		Long: "Show a status entry for every drive and slot on a tape changer (GET " +
			"/tape/changer/{name}/status). Pass --cache to use the last cached value " +
			"instead of querying the device directly.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			params := &pbstape.ListChangerStatusParams{}
			if cmd.Flags().Changed("cache") {
				params.Cache = &cache
			}

			resp, err := deps.PBS.Tape.ListChangerStatus(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("get tape changer status %q: %w", name, err)
			}

			items := rawItemsOf(resp)
			entries := make([]tapeChangerStatusEntry, 0, len(items))

			for _, raw := range items {
				var e tapeChangerStatusEntry

				err := json.Unmarshal(raw, &e)
				if err != nil {
					return fmt.Errorf("decode tape changer status entry: %w", err)
				}

				entries = append(entries, e)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].EntryId < entries[j].EntryId })

			headers := []string{"ENTRY-ID", "KIND", "STATE", "LOADED-SLOT", "LABEL-TEXT"}
			rows := make([][]string, 0, len(entries))

			for _, e := range entries {
				rows = append(rows, []string{
					strconv.FormatInt(e.EntryId, 10), e.EntryKind, pbsFormatOptionalString(e.State),
					pbsFormatOptionalInt64(e.LoadedSlot), pbsFormatOptionalString(e.LabelText),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&cache, "cache", false, "use the last cached status instead of querying the device")
	return cmd
}

// newTapeChangerTransferCmd builds `pmx pbs tape changer transfer <name>` —
// move media from one changer slot to another (POST
// /tape/changer/{name}/transfer). This endpoint's PBS API schema declares a
// "null" return type: it carries no UPID, so this command reports success
// directly instead of going through finishAsync/--async task-wait semantics.
func newTapeChangerTransferCmd() *cobra.Command {
	var from, to int64
	cmd := &cobra.Command{
		Use:   "transfer <name>",
		Short: "Transfer media between two changer slots",
		Long: "Move media from one changer slot to another (POST " +
			"/tape/changer/{name}/transfer). --from and --to are required slot " +
			"numbers. This is a synchronous operation: the PBS API returns no task " +
			"ID for it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			if from <= 0 {
				return fmt.Errorf("--from must be a positive slot number")
			}

			if to <= 0 {
				return fmt.Errorf("--to must be a positive slot number")
			}

			params := &pbstape.CreateChangerTransferParams{From: from, To: to}

			err := deps.PBS.Tape.CreateChangerTransfer(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("transfer media on tape changer %q: %w", name, err)
			}

			res := output.Result{
				Message: fmt.Sprintf("Media transferred from slot %d to slot %d on tape changer %q.", from, to, name),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.Int64Var(&from, "from", 0, "source slot number (required)")
	f.Int64Var(&to, "to", 0, "destination slot number (required)")
	cli.MustMarkRequired(cmd, "from")
	cli.MustMarkRequired(cmd, "to")
	return cmd
}
