package pbs

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/output"

	pbstape "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/pbs/tape"
)

// addTapeDriveOpCmds registers every tape-drive media-operation verb (load,
// unload, eject, rewind, clean, format, label, catalog, export, inventory,
// and restore-key) onto cmd. Called by newTapeDriveCmd (drive.go) so drive
// listing/CRUD and drive media operations stay in separate files while
// sharing one `pmx pbs tape drive` sub-tree.
//
// Every verb below takes the drive name as its sole positional argument
// (mirroring the /tape/drive/{drive}/... REST path shape) and resolves
// dependencies per-invocation via cli.GetDeps, exactly like every other pbs
// command in this package.
func addTapeDriveOpCmds(cmd *cobra.Command) {
	cmd.AddCommand(
		newTapeDriveOpLoadMediaCmd(),
		newTapeDriveOpLoadSlotCmd(),
		newTapeDriveOpUnloadCmd(),
		newTapeDriveOpEjectCmd(),
		newTapeDriveOpRewindCmd(),
		newTapeDriveOpCleanCmd(),
		newTapeDriveOpFormatCmd(),
		newTapeDriveOpLabelCmd(),
		newTapeDriveOpBarcodeLabelCmd(),
		newTapeDriveOpCatalogCmd(),
		newTapeDriveOpExportCmd(),
		newTapeDriveOpUpdateInventoryCmd(),
		newTapeDriveOpRestoreKeyCmd(),
	)
}

// tapeDriveOpArg validates and returns the sole positional <drive> argument
// shared by every verb in this file.
func tapeDriveOpArg(args []string) (string, error) {
	drive := args[0]
	if drive == "" {
		return "", fmt.Errorf("drive name must not be empty")
	}

	return drive, nil
}

// newTapeDriveOpLoadMediaCmd builds `pmx pbs tape drive load-media <drive>` —
// load a specific piece of media (by label/barcode) into the drive from
// whichever slot currently holds it (POST /tape/drive/{drive}/load-media).
// Asynchronous; blocks until the task finishes unless --async is set.
func newTapeDriveOpLoadMediaCmd() *cobra.Command {
	var labelText string
	cmd := &cobra.Command{
		Use:   "load-media <drive>",
		Short: "Load media into a drive by label",
		Long: "Load a specific piece of media into a drive, locating it by its " +
			"label text/barcode (POST /tape/drive/{drive}/load-media). --label-text " +
			"is required. Runs as an asynchronous task; the command blocks until it " +
			"finishes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive, err := tapeDriveOpArg(args)
			if err != nil {
				return err
			}

			params := &pbstape.CreateDriveLoadMediaParams{LabelText: labelText}

			resp, err := deps.PBS.Tape.CreateDriveLoadMedia(cmd.Context(), drive, params)
			if err != nil {
				return fmt.Errorf("load media %q into drive %q: %w", labelText, drive, err)
			}

			if resp == nil {
				return fmt.Errorf("load media %q into drive %q: nil response from PBS", labelText, drive)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Media %q loaded into drive %q.", labelText, drive))
		},
	}
	cmd.Flags().StringVar(&labelText, "label-text", "", "media label text/barcode to load (required)")
	cli.MustMarkRequired(cmd, "label-text")

	return cmd
}

// newTapeDriveOpLoadSlotCmd builds `pmx pbs tape drive load-slot <drive>` —
// load media from a specific changer slot into the drive (POST
// /tape/drive/{drive}/load-slot). Synchronous: the endpoint returns no data
// on success, so this reports plain success once the request completes.
func newTapeDriveOpLoadSlotCmd() *cobra.Command {
	var slot int64
	cmd := &cobra.Command{
		Use:   "load-slot <drive>",
		Short: "Load media from a changer slot into a drive",
		Long: "Load media from a specific changer slot into a drive (POST " +
			"/tape/drive/{drive}/load-slot). --slot is required.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive, err := tapeDriveOpArg(args)
			if err != nil {
				return err
			}

			params := &pbstape.CreateDriveLoadSlotParams{SourceSlot: slot}

			err = deps.PBS.Tape.CreateDriveLoadSlot(cmd.Context(), drive, params)
			if err != nil {
				return fmt.Errorf("load slot %d into drive %q: %w", slot, drive, err)
			}

			res := output.Result{Message: fmt.Sprintf("Drive %q loaded from slot %d.", drive, slot)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().Int64Var(&slot, "slot", 0, "source changer slot number (required)")
	cli.MustMarkRequired(cmd, "slot")

	return cmd
}

// newTapeDriveOpUnloadCmd builds `pmx pbs tape drive unload <drive>` — unload
// the drive's current media back into the media changer (POST
// /tape/drive/{drive}/unload). --slot is optional; when omitted the media
// returns to the slot it was loaded from. Asynchronous; blocks until the
// task finishes unless --async is set.
func newTapeDriveOpUnloadCmd() *cobra.Command {
	var slot int64
	cmd := &cobra.Command{
		Use:   "unload <drive>",
		Short: "Unload media from a drive back into the changer",
		Long: "Unload the drive's current media back into the media changer " +
			"(POST /tape/drive/{drive}/unload). --slot targets a specific slot; " +
			"omit it to return the media to the slot it was loaded from. Runs as " +
			"an asynchronous task; the command blocks until it finishes unless " +
			"--async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive, err := tapeDriveOpArg(args)
			if err != nil {
				return err
			}

			params := &pbstape.CreateDriveUnloadParams{}
			if cmd.Flags().Changed("slot") {
				params.TargetSlot = int64Ptr(slot)
			}

			resp, err := deps.PBS.Tape.CreateDriveUnload(cmd.Context(), drive, params)
			if err != nil {
				return fmt.Errorf("unload drive %q: %w", drive, err)
			}

			if resp == nil {
				return fmt.Errorf("unload drive %q: nil response from PBS", drive)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Drive %q unloaded.", drive))
		},
	}
	cmd.Flags().Int64Var(&slot, "slot", 0,
		"target changer slot number; defaults to the slot the media was loaded from")

	return cmd
}

// newTapeDriveOpEjectCmd builds `pmx pbs tape drive eject <drive>` — eject
// the drive's current media (POST /tape/drive/{drive}/eject-media).
// Asynchronous; blocks until the task finishes unless --async is set.
func newTapeDriveOpEjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eject <drive>",
		Short: "Eject a drive's current media",
		Long: "Eject the media currently loaded in a drive (POST " +
			"/tape/drive/{drive}/eject-media). Runs as an asynchronous task; the " +
			"command blocks until it finishes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive, err := tapeDriveOpArg(args)
			if err != nil {
				return err
			}

			resp, err := deps.PBS.Tape.CreateDriveEjectMedia(cmd.Context(), drive)
			if err != nil {
				return fmt.Errorf("eject media from drive %q: %w", drive, err)
			}

			if resp == nil {
				return fmt.Errorf("eject media from drive %q: nil response from PBS", drive)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Media ejected from drive %q.", drive))
		},
	}

	return cmd
}

// newTapeDriveOpRewindCmd builds `pmx pbs tape drive rewind <drive>` —
// rewind the drive's current media to the start (POST
// /tape/drive/{drive}/rewind). Asynchronous; blocks until the task finishes
// unless --async is set.
func newTapeDriveOpRewindCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rewind <drive>",
		Short: "Rewind a drive's current media",
		Long: "Rewind the media currently loaded in a drive to the start (POST " +
			"/tape/drive/{drive}/rewind). Runs as an asynchronous task; the command " +
			"blocks until it finishes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive, err := tapeDriveOpArg(args)
			if err != nil {
				return err
			}

			resp, err := deps.PBS.Tape.CreateDriveRewind(cmd.Context(), drive)
			if err != nil {
				return fmt.Errorf("rewind drive %q: %w", drive, err)
			}

			if resp == nil {
				return fmt.Errorf("rewind drive %q: nil response from PBS", drive)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Drive %q rewound.", drive))
		},
	}

	return cmd
}

// newTapeDriveOpCleanCmd builds `pmx pbs tape drive clean <drive>` — run the
// drive's cleaning cycle using a loaded cleaning cartridge (PUT
// /tape/drive/{drive}/clean). Asynchronous; blocks until the task finishes
// unless --async is set.
func newTapeDriveOpCleanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clean <drive>",
		Short: "Run a drive's cleaning cycle",
		Long: "Run the cleaning cycle on a drive using a loaded cleaning cartridge " +
			"(PUT /tape/drive/{drive}/clean). Runs as an asynchronous task; the " +
			"command blocks until it finishes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive, err := tapeDriveOpArg(args)
			if err != nil {
				return err
			}

			resp, err := deps.PBS.Tape.UpdateDriveClean(cmd.Context(), drive)
			if err != nil {
				return fmt.Errorf("clean drive %q: %w", drive, err)
			}

			if resp == nil {
				return fmt.Errorf("clean drive %q: nil response from PBS", drive)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Drive %q cleaned.", drive))
		},
	}

	return cmd
}

// newTapeDriveOpFormatCmd builds `pmx pbs tape drive format <drive>` — format
// (erase) the drive's current media (POST /tape/drive/{drive}/format-media).
// Every CreateDriveFormatMediaParams field is optional and forwarded only
// when explicitly set. Asynchronous; blocks until the task finishes unless
// --async is set.
func newTapeDriveOpFormatCmd() *cobra.Command {
	var (
		fast        bool
		labelText   string
		loadBarcode string
		yes         bool
	)
	cmd := &cobra.Command{
		Use:   "format <drive>",
		Short: "Format (erase) a drive's current media",
		Long: "Format (erase) the media currently loaded in a drive (POST " +
			"/tape/drive/{drive}/format-media). Every option is optional and only " +
			"forwarded when explicitly set. Runs as an asynchronous task; the " +
			"command blocks until it finishes unless --async is set. This is " +
			"destructive: all data on the media is erased and unrecoverable. Pass " +
			"--yes/-y to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive, err := tapeDriveOpArg(args)
			if err != nil {
				return err
			}

			if !yes {
				return fmt.Errorf("refusing to format media in drive %q without confirmation: pass --yes/-y", drive)
			}

			fl := cmd.Flags()
			params := &pbstape.CreateDriveFormatMediaParams{}
			if fl.Changed("fast") {
				params.Fast = boolPtr(fast)
			}

			if fl.Changed("label-text") {
				params.LabelText = strPtr(labelText)
			}

			if fl.Changed("load-barcode") {
				params.LoadBarcode = strPtr(loadBarcode)
			}

			resp, err := deps.PBS.Tape.CreateDriveFormatMedia(cmd.Context(), drive, params)
			if err != nil {
				return fmt.Errorf("format media in drive %q: %w", drive, err)
			}

			if resp == nil {
				return fmt.Errorf("format media in drive %q: nil response from PBS", drive)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Media in drive %q formatted.", drive))
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&fast, "fast", false, "use fast erase")
	fl.StringVar(&labelText, "label-text", "", "media label text/barcode to write after formatting")
	fl.StringVar(&loadBarcode, "load-barcode", "", "load media with this label/barcode before formatting")
	fl.BoolVarP(&yes, "yes", "y", false, "confirm the destructive operation without prompting")

	return cmd
}

// newTapeDriveOpLabelCmd builds `pmx pbs tape drive label <drive>` — write a
// new label onto the drive's current media (POST
// /tape/drive/{drive}/label-media). --label-text is required; --pool is
// optional. Asynchronous; blocks until the task finishes unless --async is
// set.
func newTapeDriveOpLabelCmd() *cobra.Command {
	var (
		labelText string
		pool      string
	)
	cmd := &cobra.Command{
		Use:   "label <drive>",
		Short: "Write a label onto a drive's current media",
		Long: "Write a new label onto the media currently loaded in a drive " +
			"(POST /tape/drive/{drive}/label-media). --label-text is required; " +
			"--pool optionally assigns the media to a pool. Runs as an " +
			"asynchronous task; the command blocks until it finishes unless " +
			"--async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive, err := tapeDriveOpArg(args)
			if err != nil {
				return err
			}

			params := &pbstape.CreateDriveLabelMediaParams{LabelText: labelText}
			if cmd.Flags().Changed("pool") {
				params.Pool = strPtr(pool)
			}

			resp, err := deps.PBS.Tape.CreateDriveLabelMedia(cmd.Context(), drive, params)
			if err != nil {
				return fmt.Errorf("label media %q in drive %q: %w", labelText, drive, err)
			}

			if resp == nil {
				return fmt.Errorf("label media %q in drive %q: nil response from PBS", labelText, drive)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Media in drive %q labeled %q.", drive, labelText))
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&labelText, "label-text", "", "media label text/barcode to write (required)")
	fl.StringVar(&pool, "pool", "", "media pool name to assign the media to")
	cli.MustMarkRequired(cmd, "label-text")

	return cmd
}

// newTapeDriveOpBarcodeLabelCmd builds `pmx pbs tape drive barcode-label
// <drive>` — read the media's barcode via the changer's barcode reader and
// use it as the label (POST /tape/drive/{drive}/barcode-label-media). --pool
// is optional. Asynchronous; blocks until the task finishes unless --async
// is set.
func newTapeDriveOpBarcodeLabelCmd() *cobra.Command {
	var pool string
	cmd := &cobra.Command{
		Use:   "barcode-label <drive>",
		Short: "Label a drive's current media using its barcode",
		Long: "Read the media's barcode via the changer's barcode reader and " +
			"write it as the media label (POST " +
			"/tape/drive/{drive}/barcode-label-media). --pool optionally assigns " +
			"the media to a pool. Runs as an asynchronous task; the command " +
			"blocks until it finishes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive, err := tapeDriveOpArg(args)
			if err != nil {
				return err
			}

			params := &pbstape.CreateDriveBarcodeLabelMediaParams{}
			if cmd.Flags().Changed("pool") {
				params.Pool = strPtr(pool)
			}

			resp, err := deps.PBS.Tape.CreateDriveBarcodeLabelMedia(cmd.Context(), drive, params)
			if err != nil {
				return fmt.Errorf("barcode-label media in drive %q: %w", drive, err)
			}

			if resp == nil {
				return fmt.Errorf("barcode-label media in drive %q: nil response from PBS", drive)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Media in drive %q barcode-labeled.", drive))
		},
	}
	cmd.Flags().StringVar(&pool, "pool", "", "media pool name to assign the media to")

	return cmd
}

// newTapeDriveOpCatalogCmd builds `pmx pbs tape drive catalog <drive>` —
// (re)build the catalog for the drive's current media (POST
// /tape/drive/{drive}/catalog). Every CreateDriveCatalogParams field is
// optional and forwarded only when explicitly set. Asynchronous; blocks
// until the task finishes unless --async is set.
func newTapeDriveOpCatalogCmd() *cobra.Command {
	var (
		force   bool
		scan    bool
		verbose bool
	)
	cmd := &cobra.Command{
		Use:   "catalog <drive>",
		Short: "(Re)build the catalog for a drive's current media",
		Long: "(Re)build the catalog for the media currently loaded in a drive " +
			"(POST /tape/drive/{drive}/catalog). Every option is optional and " +
			"only forwarded when explicitly set. Runs as an asynchronous task; " +
			"the command blocks until it finishes unless --async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive, err := tapeDriveOpArg(args)
			if err != nil {
				return err
			}

			fl := cmd.Flags()
			params := &pbstape.CreateDriveCatalogParams{}
			if fl.Changed("force") {
				params.Force = boolPtr(force)
			}

			if fl.Changed("scan") {
				params.Scan = boolPtr(scan)
			}

			if fl.Changed("verbose") {
				params.Verbose = boolPtr(verbose)
			}

			resp, err := deps.PBS.Tape.CreateDriveCatalog(cmd.Context(), drive, params)
			if err != nil {
				return fmt.Errorf("catalog media in drive %q: %w", drive, err)
			}

			if resp == nil {
				return fmt.Errorf("catalog media in drive %q: nil response from PBS", drive)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Media in drive %q cataloged.", drive))
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&force, "force", false, "force overriding an existing index")
	fl.BoolVar(&scan, "scan", false,
		"re-read the whole tape to reconstruct the catalog instead of restoring saved versions")
	fl.BoolVar(&verbose, "verbose", false, "log every chunk found (verbose mode)")

	return cmd
}

// tapeDriveOpExportSlot is the decoded shape of UpdateDriveExportMedia's
// response: a bare JSON integer naming the import-export slot the media was
// moved to. Unlike every other verb in this file, this endpoint is neither a
// UPID-bearing async task nor a null-returning sync call — its RawMessage
// response holds a plain number, so it is decoded and rendered directly
// instead of going through finishAsync.
type tapeDriveOpExportSlot int64

// newTapeDriveOpExportCmd builds `pmx pbs tape drive export <drive>` — move
// the drive's current media to the media changer's import-export slot (PUT
// /tape/drive/{drive}/export-media). --label-text is required. The response
// is the destination slot number, not a UPID; this command decodes and
// reports it directly rather than treating it as an asynchronous task.
func newTapeDriveOpExportCmd() *cobra.Command {
	var labelText string
	cmd := &cobra.Command{
		Use:   "export <drive>",
		Short: "Export a drive's current media to the import-export slot",
		Long: "Move the media currently loaded in a drive to the media " +
			"changer's import-export slot (PUT /tape/drive/{drive}/export-media). " +
			"--label-text is required. The response reports the destination slot " +
			"number.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive, err := tapeDriveOpArg(args)
			if err != nil {
				return err
			}

			params := &pbstape.UpdateDriveExportMediaParams{LabelText: labelText}

			resp, err := deps.PBS.Tape.UpdateDriveExportMedia(cmd.Context(), drive, params)
			if err != nil {
				return fmt.Errorf("export media %q from drive %q: %w", labelText, drive, err)
			}

			if resp == nil {
				return fmt.Errorf("export media %q from drive %q: nil response from PBS", labelText, drive)
			}

			var slot tapeDriveOpExportSlot

			decodeErr := json.Unmarshal(*resp, &slot)
			if decodeErr != nil {
				return fmt.Errorf("export media %q from drive %q: decode import-export slot: %w",
					labelText, drive, decodeErr)
			}

			res := output.Result{
				Single:  map[string]string{"slot": strconv.FormatInt(int64(slot), 10)},
				Raw:     map[string]int64{"slot": int64(slot)},
				Message: fmt.Sprintf("Media exported to import-export slot %d.", slot),
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&labelText, "label-text", "", "media label text/barcode to export (required)")
	cli.MustMarkRequired(cmd, "label-text")

	return cmd
}

// newTapeDriveOpUpdateInventoryCmd builds `pmx pbs tape drive
// update-inventory <drive>` — update the media online status by scanning the
// changer's slots via the drive (PUT /tape/drive/{drive}/inventory). Every
// UpdateDriveInventoryParams field is optional and forwarded only when
// explicitly set. Asynchronous; blocks until the task finishes unless
// --async is set.
func newTapeDriveOpUpdateInventoryCmd() *cobra.Command {
	var (
		catalog       bool
		readAllLabels bool
	)
	cmd := &cobra.Command{
		Use:   "update-inventory <drive>",
		Short: "Update the changer's media inventory via a drive",
		Long: "Update the media online status by scanning the media changer's " +
			"slots via this drive (PUT /tape/drive/{drive}/inventory). Every " +
			"option is optional and only forwarded when explicitly set. Runs as " +
			"an asynchronous task; the command blocks until it finishes unless " +
			"--async is set.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive, err := tapeDriveOpArg(args)
			if err != nil {
				return err
			}

			fl := cmd.Flags()
			params := &pbstape.UpdateDriveInventoryParams{}
			if fl.Changed("catalog") {
				params.Catalog = boolPtr(catalog)
			}

			if fl.Changed("read-all-labels") {
				params.ReadAllLabels = boolPtr(readAllLabels)
			}

			resp, err := deps.PBS.Tape.UpdateDriveInventory(cmd.Context(), drive, params)
			if err != nil {
				return fmt.Errorf("update inventory via drive %q: %w", drive, err)
			}

			if resp == nil {
				return fmt.Errorf("update inventory via drive %q: nil response from PBS", drive)
			}

			return finishAsync(cmd, deps, *resp, fmt.Sprintf("Inventory updated via drive %q.", drive))
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&catalog, "catalog", false, "restore the catalog from tape")
	fl.BoolVar(&readAllLabels, "read-all-labels", false, "load every tape and read its label, even if already inventoried")

	return cmd
}

// newTapeDriveOpRestoreKeyCmd builds `pmx pbs tape drive restore-key
// <drive>` — restore an encryption key from the drive's current media (POST
// /tape/drive/{drive}/restore-key). --password is required. Synchronous:
// the endpoint returns no data on success, so this reports plain success
// once the request completes.
func newTapeDriveOpRestoreKeyCmd() *cobra.Command {
	var password string
	cmd := &cobra.Command{
		Use:   "restore-key <drive>",
		Short: "Restore an encryption key from a drive's current media",
		Long: "Restore an encryption key stored on the media currently loaded " +
			"in a drive (POST /tape/drive/{drive}/restore-key). --password, the " +
			"password the key was encrypted with, is required.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			drive, err := tapeDriveOpArg(args)
			if err != nil {
				return err
			}

			params := &pbstape.CreateDriveRestoreKeyParams{Password: password}

			err = deps.PBS.Tape.CreateDriveRestoreKey(cmd.Context(), drive, params)
			if err != nil {
				return fmt.Errorf("restore encryption key from drive %q: %w", drive, err)
			}

			res := output.Result{Message: fmt.Sprintf("Encryption key restored from drive %q.", drive)}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}
	cmd.Flags().StringVar(&password, "password", "", "password the encryption key was encrypted with (required)")
	cli.MustMarkRequired(cmd, "password")

	return cmd
}
