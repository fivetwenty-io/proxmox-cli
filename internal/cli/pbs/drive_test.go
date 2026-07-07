package pbs

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// Repeated endpoint paths for `tape drive` tests, factored into consts so
// tests never repeat a raw string literal.
const (
	pathTapeDrive                    = "/api2/json/tape/drive"
	pathConfigDrive                  = "/api2/json/config/drive"
	pathConfigDriveFmt               = pathConfigDrive + "/%s"
	pathTapeScanDrives               = "/api2/json/tape/scan-drives"
	pathTapeDriveStatusFmt           = "/api2/json/tape/drive/%s/status"
	pathTapeDriveCartridgeMemoryFmt  = "/api2/json/tape/drive/%s/cartridge-memory"
	pathTapeDriveVolumeStatisticsFmt = "/api2/json/tape/drive/%s/volume-statistics"
	pathTapeDriveReadLabelFmt        = "/api2/json/tape/drive/%s/read-label"
	pathTapeDriveInventoryFmt        = "/api2/json/tape/drive/%s/inventory"
)

// --- drive ls ----------------------------------------------------------------

func TestTapeDriveLs_ListsDrivesSortedByName(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET "+pathTapeDrive, &recordedRequest{}, []map[string]any{
		{"name": "drive2", "path": "/dev/sg1", "vendor": "IBM", "model": "ULT3580", "serial": "SN2"},
		{"name": "drive1", "path": "/dev/sg0", "vendor": "HP", "model": "LTO8", "serial": "SN1", "state": "idle"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "ls")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "drive1")
	require.Contains(t, out, "drive2")
	require.Contains(t, out, "IBM")
	require.Contains(t, out, "HP")
	require.Less(t, strings.Index(out, "drive1"), strings.Index(out, "drive2"))
}

func TestTapeDriveLs_ScopesByChangerAndQueryActivity(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+pathTapeDrive, &rec, []map[string]any{})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "ls", "--changer", "changer0", "--query-activity")
	require.NoError(t, err)

	require.Equal(t, "changer0", rec.query.Get("changer"))
	require.Equal(t, "1", rec.query.Get("query-activity"))
}

func TestTapeDriveLs_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+pathTapeDrive, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list tape drives")
}

// --- drive show ----------------------------------------------------------------

func TestTapeDriveShow_RendersPopulatedFields(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+fmt.Sprintf(pathConfigDriveFmt, "drive1"), &rec, map[string]any{
		"name": "drive1", "path": "/dev/sg0", "changer": "changer0", "changer-drivenum": 1,
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "show", "drive1")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, fmt.Sprintf(pathConfigDriveFmt, "drive1"), rec.path)

	out := buf.String()
	require.Contains(t, out, "drive1")
	require.Contains(t, out, "/dev/sg0")
	require.Contains(t, out, "changer0")
}

func TestTapeDriveShow_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+fmt.Sprintf(pathConfigDriveFmt, "drive1"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such drive")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "show", "drive1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get tape drive")
}

// --- drive add ----------------------------------------------------------------

func TestTapeDriveAdd_RequiresPath(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "add", "drive1")
	require.Error(t, err)
}

func TestTapeDriveAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+pathConfigDrive, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "add", "drive1",
		"--path", "/dev/sg0",
		"--changer", "changer0",
		"--changer-drivenum", "2",
	)
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, pathConfigDrive, rec.path)
	require.Equal(t, "drive1", rec.form.Get("name"))
	require.Equal(t, "/dev/sg0", rec.form.Get("path"))
	require.Equal(t, "changer0", rec.form.Get("changer"))
	require.Equal(t, "2", rec.form.Get("changer-drivenum"))
	require.Contains(t, buf.String(), `Tape drive "drive1" created.`)
}

func TestTapeDriveAdd_OmitsUnsetOptionalFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+pathConfigDrive, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "add", "drive1", "--path", "/dev/sg0")
	require.NoError(t, err)

	require.Empty(t, rec.form.Get("changer"))
	require.Empty(t, rec.form.Get("changer-drivenum"))
}

func TestTapeDriveAdd_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+pathConfigDrive, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid drive")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "add", "drive1", "--path", "/dev/sg0")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create tape drive")
}

// --- drive update ----------------------------------------------------------------

func TestTapeDriveUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+fmt.Sprintf(pathConfigDriveFmt, "drive1"), &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "update", "drive1",
		"--path", "/dev/sg1",
		"--changer", "changer1",
		"--changer-drivenum", "3",
		"--delete", "changer",
		"--delete", "changer-drivenum",
		"--digest", "abc123",
	)
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, fmt.Sprintf(pathConfigDriveFmt, "drive1"), rec.path)
	require.Equal(t, "/dev/sg1", rec.form.Get("path"))
	require.Equal(t, "changer1", rec.form.Get("changer"))
	require.Equal(t, "3", rec.form.Get("changer-drivenum"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.Equal(t, []string{"changer", "changer-drivenum"}, rec.form["delete"])
	require.Contains(t, buf.String(), `Tape drive "drive1" updated.`)
}

func TestTapeDriveUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+fmt.Sprintf(pathConfigDriveFmt, "drive1"), &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "update", "drive1", "--path", "/dev/sg1")
	require.NoError(t, err)
	require.Equal(t, "/dev/sg1", rec.form.Get("path"))

	for _, key := range []string{"changer", "changer-drivenum", "delete", "digest"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestTapeDriveUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "update", "drive1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes given")
}

func TestTapeDriveUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "update", "drive1", "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestTapeDriveUpdate_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+fmt.Sprintf(pathConfigDriveFmt, "drive1"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "update", "drive1", "--path", "/dev/sg1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update tape drive")
}

// --- drive delete ----------------------------------------------------------------

func TestTapeDriveDelete_DeletesDrive(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+fmt.Sprintf(pathConfigDriveFmt, "drive1"), &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "delete", "drive1")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, fmt.Sprintf(pathConfigDriveFmt, "drive1"), rec.path)
	require.Contains(t, buf.String(), `Tape drive "drive1" deleted.`)
}

func TestTapeDriveDelete_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+fmt.Sprintf(pathConfigDriveFmt, "drive1"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "delete", "drive1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete tape drive")
}

// --- drive scan ----------------------------------------------------------------

func TestTapeDriveScan_ListsAutodetectedDrivesSortedByPath(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+pathTapeScanDrives, &rec, []map[string]any{
		{"kind": "tape", "major": 9, "minor": 1, "model": "ULT3580", "path": "/dev/sg1", "serial": "SN2", "vendor": "IBM"},
		{"kind": "tape", "major": 9, "minor": 0, "model": "LTO8", "path": "/dev/sg0", "serial": "SN1", "vendor": "HP"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "scan")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, pathTapeScanDrives, rec.path)

	out := buf.String()
	require.Contains(t, out, "/dev/sg0")
	require.Contains(t, out, "/dev/sg1")
	require.Less(t, strings.Index(out, "/dev/sg0"), strings.Index(out, "/dev/sg1"))
}

func TestTapeDriveScan_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+pathTapeScanDrives, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "scan")
	require.Error(t, err)
	require.Contains(t, err.Error(), "scan tape drives")
}

// --- drive status ----------------------------------------------------------------

func TestTapeDriveStatus_RendersFields(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+fmt.Sprintf(pathTapeDriveStatusFmt, "drive1"), &rec, map[string]any{
		"blocksize": 0, "buffer-mode": 1, "compression": true, "product": "ULT3580",
		"revision": "1.0", "vendor": "IBM", "write-protect": false,
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "status", "drive1")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, fmt.Sprintf(pathTapeDriveStatusFmt, "drive1"), rec.path)

	out := buf.String()
	require.Contains(t, out, "ULT3580")
	require.Contains(t, out, "IBM")
	require.Contains(t, out, "true")
}

func TestTapeDriveStatus_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+fmt.Sprintf(pathTapeDriveStatusFmt, "drive1"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "status", "drive1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get tape drive status")
}

// --- drive cartridge-memory ----------------------------------------------------------------

func TestTapeDriveCartridgeMemory_RendersEntries(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+fmt.Sprintf(pathTapeDriveCartridgeMemoryFmt, "drive1"), &rec, []map[string]any{
		{"id": 1, "name": "remaining-capacity", "value": "1000"},
		{"id": 2, "name": "load-count", "value": "42"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "cartridge-memory", "drive1")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, fmt.Sprintf(pathTapeDriveCartridgeMemoryFmt, "drive1"), rec.path)

	out := buf.String()
	require.Contains(t, out, "remaining-capacity")
	require.Contains(t, out, "load-count")
	require.Contains(t, out, "42")
}

func TestTapeDriveCartridgeMemory_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+fmt.Sprintf(pathTapeDriveCartridgeMemoryFmt, "drive1"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "cartridge-memory", "drive1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get cartridge memory")
}

// --- drive volume-statistics ----------------------------------------------------------------

func TestTapeDriveVolumeStatistics_RendersFields(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+fmt.Sprintf(pathTapeDriveVolumeStatisticsFmt, "drive1"), &rec, map[string]any{
		"beginning-of-medium-passes": 5, "last-load-read-compression-ratio": 100,
		"last-load-write-compression-ratio": 100, "last-mount-bytes-read": 0,
		"last-mount-bytes-written": 0, "last-mount-unrecovered-read-errors": 0,
		"last-mount-unrecovered-write-errors": 0, "lifetime-bytes-read": 0,
		"lifetime-bytes-written": 0, "medium-mount-time": 0, "medium-ready-time": 0,
		"middle-of-tape-passes": 0, "serial": "SN1", "total-native-capacity": 0,
		"total-used-native-capacity": 0, "volume-datasets-read": 0, "volume-datasets-written": 0,
		"volume-mounts": 1, "volume-recovered-read-errors": 0, "volume-recovered-write-data-errors": 0,
		"volume-unrecovered-read-errors": 0, "volume-unrecovered-write-data-errors": 0,
		"volume-unrecovered-write-servo-errors": 0, "volume-write-servo-errors": 0,
		"worm": false, "write-protect": false,
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "volume-statistics", "drive1")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, fmt.Sprintf(pathTapeDriveVolumeStatisticsFmt, "drive1"), rec.path)
	require.Contains(t, buf.String(), "SN1")
}

func TestTapeDriveVolumeStatistics_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+fmt.Sprintf(pathTapeDriveVolumeStatisticsFmt, "drive1"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "volume-statistics", "drive1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "get volume statistics")
}

// --- drive read-label ----------------------------------------------------------------

func TestTapeDriveReadLabel_RendersFieldsAndSendsInventorize(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+fmt.Sprintf(pathTapeDriveReadLabelFmt, "drive1"), &rec, map[string]any{
		"ctime": 1700000000, "label-text": "TAPE001", "uuid": "11111111-1111-1111-1111-111111111111",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "read-label", "drive1", "--inventorize")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, fmt.Sprintf(pathTapeDriveReadLabelFmt, "drive1"), rec.path)
	require.Equal(t, "1", rec.query.Get("inventorize"))
	require.Contains(t, buf.String(), "TAPE001")
}

func TestTapeDriveReadLabel_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+fmt.Sprintf(pathTapeDriveReadLabelFmt, "drive1"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "read-label", "drive1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "read media label")
}

// --- drive inventory ----------------------------------------------------------------

func TestTapeDriveInventory_ListsEntriesSortedByLabel(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+fmt.Sprintf(pathTapeDriveInventoryFmt, "drive1"), &rec, []map[string]any{
		{"label-text": "TAPE002"},
		{"label-text": "TAPE001", "uuid": "11111111-1111-1111-1111-111111111111"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "inventory", "drive1")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, fmt.Sprintf(pathTapeDriveInventoryFmt, "drive1"), rec.path)

	out := buf.String()
	require.Contains(t, out, "TAPE001")
	require.Contains(t, out, "TAPE002")
	require.Less(t, strings.Index(out, "TAPE001"), strings.Index(out, "TAPE002"))
}

func TestTapeDriveInventory_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+fmt.Sprintf(pathTapeDriveInventoryFmt, "drive1"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newTapeDriveCmd(), "drive", "inventory", "drive1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list media inventory")
}

// --- command tree ----------------------------------------------------------------

func TestNewTapeDriveCmd_RegistersAllVerbs(t *testing.T) {
	cmd := newTapeDriveCmd()
	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{
		"ls", "show", "add", "update", "delete", "scan", "status",
		"cartridge-memory", "volume-statistics", "read-label", "inventory",
	} {
		require.True(t, names[want], "tape drive command must expose a %q sub-command", want)
	}
}
