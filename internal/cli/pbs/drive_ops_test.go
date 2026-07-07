package pbs

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// tapeDriveOpsDrive is the sample drive name reused across every test below.
const tapeDriveOpsDrive = "drive0"

// tapeDriveOpsPath builds the /api2/json/tape/drive/{drive}/{op} path used by
// every verb in drive_ops.go.
func tapeDriveOpsPath(op string) string {
	return fmt.Sprintf("/api2/json/tape/drive/%s/%s", tapeDriveOpsDrive, op)
}

// newTapeDriveOpsRoot builds a scratch "drive" parent command with every
// tape-drive media-operation verb registered, mirroring the real
// `pmx pbs tape drive` sub-tree that drive.go (owned by a different task)
// assembles by calling addTapeDriveOpCmds itself. Building the parent here
// keeps this file independently testable without depending on drive.go's
// existence.
func newTapeDriveOpsRoot() *cobra.Command {
	root := &cobra.Command{Use: "drive"}
	addTapeDriveOpCmds(root)

	return root
}

// --- load-media --------------------------------------------------------------

func TestTapeDriveOpLoadMedia_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+tapeDriveOpsPath("load-media"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "load-media", tapeDriveOpsDrive, "--label-text", "TAPE01")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapeDriveOpsPath("load-media"), rec.path)
	require.Equal(t, "TAPE01", rec.form.Get("label-text"))
	require.Contains(t, buf.String(), `Media "TAPE01" loaded into drive "drive0".`)
}

func TestTapeDriveOpLoadMedia_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "POST "+tapeDriveOpsPath("load-media"), &recordedRequest{}, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "load-media", tapeDriveOpsDrive, "--label-text", "TAPE01")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "loaded")
}

func TestTapeDriveOpLoadMedia_RequiresLabelText(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "load-media", tapeDriveOpsDrive)
	require.Error(t, err)
}

func TestTapeDriveOpLoadMedia_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapeDriveOpsPath("load-media"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "load failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "load-media", tapeDriveOpsDrive, "--label-text", "TAPE01")
	require.Error(t, err)
	require.Contains(t, err.Error(), "load media")
}

// --- load-slot (sync, null response) ------------------------------------------

func TestTapeDriveOpLoadSlot_LoadsFromSlot(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapeDriveOpsPath("load-slot"), &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "load-slot", tapeDriveOpsDrive, "--slot", "3")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapeDriveOpsPath("load-slot"), rec.path)
	require.Equal(t, "3", rec.form.Get("source-slot"))
	require.Contains(t, buf.String(), `Drive "drive0" loaded from slot 3.`)
}

func TestTapeDriveOpLoadSlot_RequiresSlot(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "load-slot", tapeDriveOpsDrive)
	require.Error(t, err)
}

func TestTapeDriveOpLoadSlot_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapeDriveOpsPath("load-slot"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "load-slot failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "load-slot", tapeDriveOpsDrive, "--slot", "1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "load slot")
}

// --- unload --------------------------------------------------------------------

func TestTapeDriveOpUnload_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+tapeDriveOpsPath("unload"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "unload", tapeDriveOpsDrive, "--slot", "5")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapeDriveOpsPath("unload"), rec.path)
	require.Equal(t, "5", rec.form.Get("target-slot"))
	require.Contains(t, buf.String(), `Drive "drive0" unloaded.`)
}

func TestTapeDriveOpUnload_OmitsSlotWhenUnset(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+tapeDriveOpsPath("unload"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "unload", tapeDriveOpsDrive)
	require.NoError(t, err)

	_, present := rec.form["target-slot"]
	require.False(t, present, "target-slot must be omitted from the body when --slot is unset")
}

func TestTapeDriveOpUnload_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "POST "+tapeDriveOpsPath("unload"), &recordedRequest{}, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "unload", tapeDriveOpsDrive)
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "unloaded")
}

func TestTapeDriveOpUnload_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapeDriveOpsPath("unload"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "unload failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "unload", tapeDriveOpsDrive)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unload drive")
}

// --- no-flag async verbs: eject, rewind, clean ----------------------------------

func TestTapeDriveOp_NoFlagAsyncVerbs(t *testing.T) {
	cases := []struct {
		verb       string
		op         string
		method     string
		finishWord string
	}{
		{verb: "eject", op: "eject-media", method: http.MethodPost, finishWord: "ejected"},
		{verb: "rewind", op: "rewind", method: http.MethodPost, finishWord: "rewound"},
		{verb: "clean", op: "clean", method: http.MethodPut, finishWord: "cleaned"},
	}

	for _, tc := range cases {
		t.Run(tc.verb+"/blocks-until-finished", func(t *testing.T) {
			f, pc := newFakeClient(t)
			handleTaskStatus(f, validUPID)

			var rec recordedRequest
			recordJSON(f, tc.method+" "+tapeDriveOpsPath(tc.op), &rec, validUPID)

			deps := depsFor(t, pc, output.FormatTable, false)
			var buf bytes.Buffer
			err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", tc.verb, tapeDriveOpsDrive)
			require.NoError(t, err)

			require.Equal(t, tc.method, rec.method)
			require.Equal(t, tapeDriveOpsPath(tc.op), rec.path)
			require.Contains(t, buf.String(), tc.finishWord)
		})

		t.Run(tc.verb+"/async-prints-upid", func(t *testing.T) {
			f, pc := newFakeClient(t)
			recordJSON(f, tc.method+" "+tapeDriveOpsPath(tc.op), &recordedRequest{}, validUPID)

			deps := depsFor(t, pc, output.FormatTable, true)
			var buf bytes.Buffer
			err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", tc.verb, tapeDriveOpsDrive)
			require.NoError(t, err)
			require.Contains(t, buf.String(), validUPID)
			require.NotContains(t, buf.String(), tc.finishWord)
		})

		t.Run(tc.verb+"/surfaces-api-error", func(t *testing.T) {
			f, pc := newFakeClient(t)
			f.HandleFunc(tc.method+" "+tapeDriveOpsPath(tc.op), func(w http.ResponseWriter, _ *http.Request) {
				testhelper.WriteError(w, http.StatusInternalServerError, tc.verb+" failed")
			})

			deps := depsFor(t, pc, output.FormatTable, false)
			var buf bytes.Buffer
			err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", tc.verb, tapeDriveOpsDrive)
			require.Error(t, err)
		})
	}
}

// --- format ----------------------------------------------------------------

func TestTapeDriveOpFormat_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+tapeDriveOpsPath("format-media"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "format", tapeDriveOpsDrive,
		"--fast", "--label-text", "TAPE02", "--load-barcode", "BC02", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapeDriveOpsPath("format-media"), rec.path)
	require.Equal(t, "1", rec.form.Get("fast"))
	require.Equal(t, "TAPE02", rec.form.Get("label-text"))
	require.Equal(t, "BC02", rec.form.Get("load-barcode"))
	require.Contains(t, buf.String(), `Media in drive "drive0" formatted.`)
}

func TestTapeDriveOpFormat_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+tapeDriveOpsPath("format-media"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "format", tapeDriveOpsDrive, "--yes")
	require.NoError(t, err)

	for _, key := range []string{"fast", "label-text", "load-barcode"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestTapeDriveOpFormat_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "POST "+tapeDriveOpsPath("format-media"), &recordedRequest{}, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "format", tapeDriveOpsDrive, "--yes")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "formatted")
}

func TestTapeDriveOpFormat_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapeDriveOpsPath("format-media"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "format failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "format", tapeDriveOpsDrive, "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "format media")
}

func TestTapeDriveOpFormat_RequiresYes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapeDriveOpsPath("format-media"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "format", tapeDriveOpsDrive)
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Contains(t, err.Error(), "--yes/-y")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

// --- label -------------------------------------------------------------------

func TestTapeDriveOpLabel_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+tapeDriveOpsPath("label-media"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "label", tapeDriveOpsDrive,
		"--label-text", "TAPE03", "--pool", "pool1")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapeDriveOpsPath("label-media"), rec.path)
	require.Equal(t, "TAPE03", rec.form.Get("label-text"))
	require.Equal(t, "pool1", rec.form.Get("pool"))
	require.Contains(t, buf.String(), `Media in drive "drive0" labeled "TAPE03".`)
}

func TestTapeDriveOpLabel_OmitsPoolWhenUnset(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+tapeDriveOpsPath("label-media"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "label", tapeDriveOpsDrive, "--label-text", "TAPE03")
	require.NoError(t, err)

	_, present := rec.form["pool"]
	require.False(t, present, "pool must be omitted from the body when unset")
}

func TestTapeDriveOpLabel_RequiresLabelText(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "label", tapeDriveOpsDrive)
	require.Error(t, err)
}

func TestTapeDriveOpLabel_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "POST "+tapeDriveOpsPath("label-media"), &recordedRequest{}, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "label", tapeDriveOpsDrive, "--label-text", "TAPE03")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "labeled")
}

func TestTapeDriveOpLabel_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapeDriveOpsPath("label-media"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "label failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "label", tapeDriveOpsDrive, "--label-text", "TAPE03")
	require.Error(t, err)
	require.Contains(t, err.Error(), "label media")
}

// --- barcode-label -------------------------------------------------------------

func TestTapeDriveOpBarcodeLabel_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+tapeDriveOpsPath("barcode-label-media"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "barcode-label", tapeDriveOpsDrive, "--pool", "pool2")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapeDriveOpsPath("barcode-label-media"), rec.path)
	require.Equal(t, "pool2", rec.form.Get("pool"))
	require.Contains(t, buf.String(), `Media in drive "drive0" barcode-labeled.`)
}

func TestTapeDriveOpBarcodeLabel_OmitsPoolWhenUnset(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+tapeDriveOpsPath("barcode-label-media"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "barcode-label", tapeDriveOpsDrive)
	require.NoError(t, err)

	_, present := rec.form["pool"]
	require.False(t, present, "pool must be omitted from the body when unset")
}

func TestTapeDriveOpBarcodeLabel_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "POST "+tapeDriveOpsPath("barcode-label-media"), &recordedRequest{}, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "barcode-label", tapeDriveOpsDrive)
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "barcode-labeled")
}

func TestTapeDriveOpBarcodeLabel_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapeDriveOpsPath("barcode-label-media"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "barcode-label failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "barcode-label", tapeDriveOpsDrive)
	require.Error(t, err)
	require.Contains(t, err.Error(), "barcode-label media")
}

// --- catalog -------------------------------------------------------------------

func TestTapeDriveOpCatalog_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+tapeDriveOpsPath("catalog"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "catalog", tapeDriveOpsDrive,
		"--force", "--scan", "--verbose")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapeDriveOpsPath("catalog"), rec.path)
	require.Equal(t, "1", rec.form.Get("force"))
	require.Equal(t, "1", rec.form.Get("scan"))
	require.Equal(t, "1", rec.form.Get("verbose"))
	require.Contains(t, buf.String(), `Media in drive "drive0" cataloged.`)
}

func TestTapeDriveOpCatalog_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+tapeDriveOpsPath("catalog"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "catalog", tapeDriveOpsDrive)
	require.NoError(t, err)

	for _, key := range []string{"force", "scan", "verbose"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestTapeDriveOpCatalog_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "POST "+tapeDriveOpsPath("catalog"), &recordedRequest{}, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "catalog", tapeDriveOpsDrive)
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "cataloged")
}

func TestTapeDriveOpCatalog_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapeDriveOpsPath("catalog"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "catalog failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "catalog", tapeDriveOpsDrive)
	require.Error(t, err)
	require.Contains(t, err.Error(), "catalog media")
}

// --- export (integer slot decode, not a UPID) -----------------------------------

func TestTapeDriveOpExport_DecodesSlotNumber(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+tapeDriveOpsPath("export-media"), &rec, 7)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "export", tapeDriveOpsDrive, "--label-text", "TAPE04")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, tapeDriveOpsPath("export-media"), rec.path)
	require.Equal(t, "TAPE04", rec.form.Get("label-text"))
	// output.Result.Single wins over Message in the table renderer (see
	// group.go / snapshot.go's identical Single+Raw+Message "notes" pattern),
	// so assert on the rendered key/value pair rather than the message text.
	require.Contains(t, buf.String(), "slot")
	require.Contains(t, buf.String(), "7")
}

func TestTapeDriveOpExport_RequiresLabelText(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "export", tapeDriveOpsDrive)
	require.Error(t, err)
}

func TestTapeDriveOpExport_IgnoresAsyncFlag(t *testing.T) {
	// export-media returns a plain slot integer, never a UPID, so --async
	// must not change its behavior or attempt UPID parsing.
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+tapeDriveOpsPath("export-media"), &rec, 2)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "export", tapeDriveOpsDrive, "--label-text", "TAPE05")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "slot")
	require.Contains(t, buf.String(), "2")
}

func TestTapeDriveOpExport_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+tapeDriveOpsPath("export-media"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "export failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "export", tapeDriveOpsDrive, "--label-text", "TAPE04")
	require.Error(t, err)
	require.Contains(t, err.Error(), "export media")
}

func TestTapeDriveOpExport_SurfacesDecodeError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("PUT "+tapeDriveOpsPath("export-media"), "not-a-number")

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "export", tapeDriveOpsDrive, "--label-text", "TAPE04")
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode import-export slot")
}

// --- update-inventory ------------------------------------------------------------

func TestTapeDriveOpUpdateInventory_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "PUT "+tapeDriveOpsPath("inventory"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "update-inventory", tapeDriveOpsDrive,
		"--catalog", "--read-all-labels")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, tapeDriveOpsPath("inventory"), rec.path)
	require.Equal(t, "1", rec.form.Get("catalog"))
	require.Equal(t, "1", rec.form.Get("read-all-labels"))
	require.Contains(t, buf.String(), `Inventory updated via drive "drive0".`)
}

func TestTapeDriveOpUpdateInventory_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "PUT "+tapeDriveOpsPath("inventory"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "update-inventory", tapeDriveOpsDrive)
	require.NoError(t, err)

	for _, key := range []string{"catalog", "read-all-labels"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestTapeDriveOpUpdateInventory_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "PUT "+tapeDriveOpsPath("inventory"), &recordedRequest{}, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "update-inventory", tapeDriveOpsDrive)
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "updated")
}

func TestTapeDriveOpUpdateInventory_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+tapeDriveOpsPath("inventory"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "inventory failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "update-inventory", tapeDriveOpsDrive)
	require.Error(t, err)
	require.Contains(t, err.Error(), "update inventory")
}

// --- restore-key (sync, null response) ------------------------------------------

func TestTapeDriveOpRestoreKey_RestoresKey(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+tapeDriveOpsPath("restore-key"), &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "restore-key", tapeDriveOpsDrive, "--password", "s3cr3t")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, tapeDriveOpsPath("restore-key"), rec.path)
	require.Equal(t, "s3cr3t", rec.form.Get("password"))
	require.Contains(t, buf.String(), `Encryption key restored from drive "drive0".`)
}

func TestTapeDriveOpRestoreKey_RequiresPassword(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "restore-key", tapeDriveOpsDrive)
	require.Error(t, err)
}

func TestTapeDriveOpRestoreKey_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+tapeDriveOpsPath("restore-key"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "bad password")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "restore-key", tapeDriveOpsDrive, "--password", "wrong")
	require.Error(t, err)
	require.Contains(t, err.Error(), "restore encryption key")
}

// --- positional argument validation -----------------------------------------

func TestTapeDriveOp_RejectsEmptyDriveArg(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveOpsRoot(), "drive", "rewind", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "drive name must not be empty")
}

// --- command tree ------------------------------------------------------------

func TestAddTapeDriveOpCmds_RegistersAllThirteenVerbs(t *testing.T) {
	root := newTapeDriveOpsRoot()

	want := []string{
		"load-media", "load-slot", "unload", "eject", "rewind", "clean", "format",
		"label", "barcode-label", "catalog", "export", "update-inventory", "restore-key",
	}

	got := make(map[string]bool, len(root.Commands()))
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}

	require.Len(t, root.Commands(), len(want))
	for _, name := range want {
		require.True(t, got[name], "expected verb %q to be registered", name)
	}
}
