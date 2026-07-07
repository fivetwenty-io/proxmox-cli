package pbs

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	pbsconfig "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/config"
	pbstape "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/tape"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// newTapeDriveCmd builds `pmx pbs tape drive` and its verbs: manage tape
// drive configurations (GET/POST/PUT/DELETE /config/drive) and inspect
// runtime drive state (GET /tape/drive, /tape/scan-drives, and the
// /tape/drive/{drive}/* status/inventory endpoints).
func newTapeDriveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drive",
		Short: "Manage tape drives",
		Long: "List, inspect, create, update, and delete tape drive configurations, and " +
			"read their runtime status, cartridge memory, volume statistics, and media " +
			"labels/inventory.",
	}
	cmd.AddCommand(
		newTapeDriveLsCmd(),
		newTapeDriveShowCmd(),
		newTapeDriveAddCmd(),
		newTapeDriveUpdateCmd(),
		newTapeDriveDeleteCmd(),
		newTapeDriveScanCmd(),
		newTapeDriveStatusCmd(),
		newTapeDriveCartridgeMemoryCmd(),
		newTapeDriveVolumeStatisticsCmd(),
		newTapeDriveReadLabelCmd(),
		newTapeDriveInventoryCmd(),
	)
	// drive media operations (load/unload/label/format/...) live in drive_ops.go
	addTapeDriveOpCmds(cmd)
	return cmd
}

// tapeDriveListEntry is one element of GET /tape/drive, per the PBS API's
// documented "Drive list entry" schema. Only name and path are required;
// every other field is populated from autodetection or changer association
// and may be absent.
type tapeDriveListEntry struct {
	Activity        *string `json:"activity,omitempty"`
	Changer         *string `json:"changer,omitempty"`
	ChangerDrivenum *int64  `json:"changer-drivenum,omitempty"`
	Model           *string `json:"model,omitempty"`
	Name            string  `json:"name"`
	Path            string  `json:"path"`
	Serial          *string `json:"serial,omitempty"`
	State           *string `json:"state,omitempty"`
	Vendor          *string `json:"vendor,omitempty"`
}

// newTapeDriveLsCmd builds `pmx pbs tape drive ls` — list configured tape
// drives together with autodetected model/vendor/serial information
// (GET /tape/drive). Scope with --changer and/or --query-activity.
func newTapeDriveLsCmd() *cobra.Command {
	var (
		changer       string
		queryActivity bool
	)
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List tape drives with runtime model/vendor/serial information",
		Long: "List every configured tape drive together with its autodetected model, " +
			"vendor, and serial number (GET /tape/drive). Pass --changer to scope to drives " +
			"associated with a tape changer, and --query-activity to also report each " +
			"drive's current activity (slower, as it queries the drive directly).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			fl := cmd.Flags()

			params := &pbstape.ListDriveParams{}
			if fl.Changed("changer") {
				params.Changer = strPtr(changer)
			}
			if fl.Changed("query-activity") {
				params.QueryActivity = boolPtr(queryActivity)
			}

			resp, err := deps.PBS.Tape.ListDrive(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("list tape drives: %w", err)
			}

			items := rawItemsOf(resp)
			entries, err := nodeDecodeArray[tapeDriveListEntry](items)
			if err != nil {
				return fmt.Errorf("decode tape drive entry: %w", err)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

			headers := []string{"NAME", "PATH", "CHANGER", "MODEL", "VENDOR", "SERIAL", "STATE", "ACTIVITY"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					e.Name, e.Path, pbsFormatOptionalString(e.Changer), pbsFormatOptionalString(e.Model),
					pbsFormatOptionalString(e.Vendor), pbsFormatOptionalString(e.Serial),
					pbsFormatOptionalString(e.State), pbsFormatOptionalString(e.Activity),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&changer, "changer", "", "only list drives associated with this tape changer")
	f.BoolVar(&queryActivity, "query-activity", false, "also query and report each drive's current activity")
	return cmd
}

// newTapeDriveShowCmd builds `pmx pbs tape drive show <name>` — show a
// single tape drive configuration (GET /config/drive/{name}).
func newTapeDriveShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a single tape drive configuration",
		Long:  "Show every populated field of a single tape drive configuration.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			resp, err := deps.PBS.Config.GetDrive(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("get tape drive %q: %w", name, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode tape drive %q: %w", name, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newTapeDriveAddCmd builds `pmx pbs tape drive add <name>` — create a tape
// drive configuration (POST /config/drive). --path is required.
func newTapeDriveAddCmd() *cobra.Command {
	var (
		path            string
		changer         string
		changerDrivenum int64
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a tape drive configuration",
		Long: "Create a new tape drive configuration. --path is required: the path to a " +
			"LTO SCSI-generic tape device (i.e. '/dev/sg0').",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			fl := cmd.Flags()

			params := &pbsconfig.CreateDriveParams{Name: name, Path: path}
			if fl.Changed("changer") {
				params.Changer = strPtr(changer)
			}
			if fl.Changed("changer-drivenum") {
				params.ChangerDrivenum = int64Ptr(changerDrivenum)
			}

			err := deps.PBS.Config.CreateDrive(cmd.Context(), params)
			if err != nil {
				return fmt.Errorf("create tape drive %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Tape drive %q created.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&path, "path", "", "path to a LTO SCSI-generic tape device, e.g. '/dev/sg0' (required)")
	f.StringVar(&changer, "changer", "", "tape changer identifier to associate with this drive")
	f.Int64Var(&changerDrivenum, "changer-drivenum", 0, "associated changer drive number (requires --changer)")
	cli.MustMarkRequired(cmd, "path")
	return cmd
}

// newTapeDriveUpdateCmd builds `pmx pbs tape drive update <name>` — update a
// tape drive configuration (PUT /config/drive/{name}). Only flags explicitly
// set are sent; use --delete to reset properties to their default.
func newTapeDriveUpdateCmd() *cobra.Command {
	var (
		path            string
		changer         string
		changerDrivenum int64
		del             []string
		digest          string
	)
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update a tape drive configuration",
		Long: "Update settings on an existing tape drive configuration. Only flags " +
			"explicitly set are sent; use --delete to reset properties to their default instead.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]
			fl := cmd.Flags()

			if !anyFlagChanged(fl) {
				return fmt.Errorf("update tape drive %q: no changes given: pass at least one flag", name)
			}

			if fl.Changed("delete") {
				for _, key := range del {
					if key == "" {
						return fmt.Errorf("--delete: property name must not be empty")
					}
				}
			}

			params := &pbsconfig.UpdateDriveParams{}
			if fl.Changed("path") {
				params.Path = strPtr(path)
			}
			if fl.Changed("changer") {
				params.Changer = strPtr(changer)
			}
			if fl.Changed("changer-drivenum") {
				params.ChangerDrivenum = int64Ptr(changerDrivenum)
			}
			if fl.Changed("delete") {
				params.Delete = del
			}
			if fl.Changed("digest") {
				params.Digest = strPtr(digest)
			}

			err := deps.PBS.Config.UpdateDrive(cmd.Context(), name, params)
			if err != nil {
				return fmt.Errorf("update tape drive %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Tape drive %q updated.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	f := cmd.Flags()
	f.StringVar(&path, "path", "", "path to a LTO SCSI-generic tape device, e.g. '/dev/sg0'")
	f.StringVar(&changer, "changer", "", "tape changer identifier to associate with this drive")
	f.Int64Var(&changerDrivenum, "changer-drivenum", 0, "associated changer drive number (requires --changer)")
	f.StringArrayVar(&del, "delete", nil, "property name to reset to its default (repeatable)")
	f.StringVar(&digest, "digest", "", "only update if the current config digest matches")
	return cmd
}

// newTapeDriveDeleteCmd builds `pmx pbs tape drive delete <name>` — remove a
// tape drive configuration (DELETE /config/drive/{name}).
//
// The generated Config.DeleteDrive binding accepts no parameters (unlike
// most other /config DELETE endpoints, this one has no digest-guard option
// in the PBS API schema), so this command has no --digest flag.
func newTapeDriveDeleteCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a tape drive configuration",
		Long: "Remove a tape drive configuration (DELETE /config/drive/{name}). " +
			"This is destructive: pass --yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			if !yes {
				return fmt.Errorf("refusing to delete tape drive %q without confirmation: pass --yes/-y", name)
			}

			err := deps.PBS.Config.DeleteDrive(cmd.Context(), name)
			if err != nil {
				return fmt.Errorf("delete tape drive %q: %w", name, err)
			}

			res := output.Result{Message: fmt.Sprintf("Tape drive %q deleted.", name)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")

	return cmd
}

// tapeScanDriveEntry is one element of GET /tape/scan-drives, per the PBS
// API's documented "Tape device information" schema. Every field is
// required (autodetected directly from the SCSI device, not configuration).
type tapeScanDriveEntry struct {
	Kind   string `json:"kind"`
	Major  int64  `json:"major"`
	Minor  int64  `json:"minor"`
	Model  string `json:"model"`
	Path   string `json:"path"`
	Serial string `json:"serial"`
	Vendor string `json:"vendor"`
}

// newTapeDriveScanCmd builds `pmx pbs tape drive scan` — scan for locally
// attached SCSI tape drives (GET /tape/scan-drives).
func newTapeDriveScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan for locally attached tape drives",
		Long:  "Scan the host for autodetected SCSI tape drives (GET /tape/scan-drives).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)

			resp, err := deps.PBS.Tape.ListScanDrives(cmd.Context())
			if err != nil {
				return fmt.Errorf("scan tape drives: %w", err)
			}

			items := rawItemsOf(resp)
			entries, err := nodeDecodeArray[tapeScanDriveEntry](items)
			if err != nil {
				return fmt.Errorf("decode scanned tape drive entry: %w", err)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

			headers := []string{"PATH", "KIND", "MODEL", "VENDOR", "SERIAL", "MAJOR", "MINOR"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{
					e.Path, e.Kind, e.Model, e.Vendor, e.Serial,
					pbsFormatOptionalInt64(&e.Major), pbsFormatOptionalInt64(&e.Minor),
				})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newTapeDriveStatusCmd builds `pmx pbs tape drive status <drive>` — read
// drive and loaded-media status (GET /tape/drive/{drive}/status).
func newTapeDriveStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <drive>",
		Short: "Show drive and loaded-media status",
		Long:  "Read live drive and loaded-media status (GET /tape/drive/{drive}/status).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive := args[0]

			resp, err := deps.PBS.Tape.ListDriveStatus(cmd.Context(), drive)
			if err != nil {
				return fmt.Errorf("get tape drive status %q: %w", drive, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode tape drive status %q: %w", drive, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// tapeCartridgeMemoryEntry is one element of
// GET /tape/drive/{drive}/cartridge-memory, per the PBS API's documented
// "Medium auxiliary memory attributes (MAM)" schema. Every field is required.
type tapeCartridgeMemoryEntry struct {
	Id    int64  `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// newTapeDriveCartridgeMemoryCmd builds
// `pmx pbs tape drive cartridge-memory <drive>` — read Cartridge Memory
// (medium auxiliary memory attributes) from the tape currently loaded in a
// drive (GET /tape/drive/{drive}/cartridge-memory).
func newTapeDriveCartridgeMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cartridge-memory <drive>",
		Short: "Read the loaded tape's cartridge memory attributes",
		Long: "Read Cartridge Memory (medium auxiliary memory attributes) from the tape " +
			"currently loaded in a drive (GET /tape/drive/{drive}/cartridge-memory).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive := args[0]

			resp, err := deps.PBS.Tape.ListDriveCartridgeMemory(cmd.Context(), drive)
			if err != nil {
				return fmt.Errorf("get cartridge memory for drive %q: %w", drive, err)
			}

			items := rawItemsOf(resp)
			entries, err := nodeDecodeArray[tapeCartridgeMemoryEntry](items)
			if err != nil {
				return fmt.Errorf("decode cartridge memory entry for drive %q: %w", drive, err)
			}

			headers := []string{"ID", "NAME", "VALUE"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{pbsFormatOptionalInt64(&e.Id), e.Name, e.Value})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newTapeDriveVolumeStatisticsCmd builds
// `pmx pbs tape drive volume-statistics <drive>` — read Volume Statistics
// (SCSI log page 17h) from the tape currently loaded in a drive
// (GET /tape/drive/{drive}/volume-statistics).
func newTapeDriveVolumeStatisticsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "volume-statistics <drive>",
		Short: "Read the loaded tape's volume statistics",
		Long: "Read Volume Statistics (SCSI log page 17h) from the tape currently loaded " +
			"in a drive (GET /tape/drive/{drive}/volume-statistics).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive := args[0]

			resp, err := deps.PBS.Tape.ListDriveVolumeStatistics(cmd.Context(), drive)
			if err != nil {
				return fmt.Errorf("get volume statistics for drive %q: %w", drive, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode volume statistics for drive %q: %w", drive, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}

// newTapeDriveReadLabelCmd builds `pmx pbs tape drive read-label <drive>` —
// read the media label of the tape currently loaded in a drive
// (GET /tape/drive/{drive}/read-label). Pass --inventorize to also record
// the result in the media inventory.
func newTapeDriveReadLabelCmd() *cobra.Command {
	var inventorize bool
	cmd := &cobra.Command{
		Use:   "read-label <drive>",
		Short: "Read the loaded tape's media label",
		Long: "Read the media label of the tape currently loaded in a drive " +
			"(GET /tape/drive/{drive}/read-label). Pass --inventorize to also record the " +
			"result in the media inventory.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive := args[0]

			params := &pbstape.ListDriveReadLabelParams{}
			if cmd.Flags().Changed("inventorize") {
				params.Inventorize = boolPtr(inventorize)
			}

			resp, err := deps.PBS.Tape.ListDriveReadLabel(cmd.Context(), drive, params)
			if err != nil {
				return fmt.Errorf("read media label for drive %q: %w", drive, err)
			}

			fields, err := flattenToMap(resp)
			if err != nil {
				return fmt.Errorf("decode media label for drive %q: %w", drive, err)
			}

			res := output.Result{Single: stringMap(fields), Raw: fields}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().BoolVar(&inventorize, "inventorize", false, "also record the read label in the media inventory")
	return cmd
}

// tapeInventoryEntry is one element of GET /tape/drive/{drive}/inventory,
// per the PBS API's documented "Label with optional Uuid" schema. Uuid is
// absent for media that were scanned but never registered in the catalog.
type tapeInventoryEntry struct {
	LabelText string  `json:"label-text"`
	Uuid      *string `json:"uuid,omitempty"`
}

// newTapeDriveInventoryCmd builds `pmx pbs tape drive inventory <drive>` —
// list known media labels via the drive's associated changer (Changer
// Inventory; GET /tape/drive/{drive}/inventory). Only useful for drives with
// an associated changer device; this also updates the media online status.
func newTapeDriveInventoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inventory <drive>",
		Short: "List known media labels via the drive's changer",
		Long: "List known media labels (Changer Inventory) by querying the changer " +
			"associated with the given drive (GET /tape/drive/{drive}/inventory). Only " +
			"useful for drives with an associated changer device; this also updates the " +
			"media online status.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive := args[0]

			resp, err := deps.PBS.Tape.ListDriveInventory(cmd.Context(), drive)
			if err != nil {
				return fmt.Errorf("list media inventory for drive %q: %w", drive, err)
			}

			items := rawItemsOf(resp)
			entries, err := nodeDecodeArray[tapeInventoryEntry](items)
			if err != nil {
				return fmt.Errorf("decode media inventory entry for drive %q: %w", drive, err)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].LabelText < entries[j].LabelText })

			headers := []string{"LABEL-TEXT", "UUID"}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{e.LabelText, pbsFormatOptionalString(e.Uuid)})
			}

			res := output.Result{Headers: headers, Rows: rows, Raw: decodeRawList(items)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	return cmd
}
