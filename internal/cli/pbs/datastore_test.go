package pbs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// Repeated endpoint paths, factored into consts so tests never repeat a raw
// string literal.
const (
	pathConfigDatastore    = "/api2/json/config/datastore"
	pathConfigDatastoreFmt = pathConfigDatastore + "/%s"
	pathAdminStatusFmt     = "/api2/json/admin/datastore/%s/status"
	pathAdminRrdFmt        = "/api2/json/admin/datastore/%s/rrd"
	pathStatusUsage        = "/api2/json/status/datastore-usage"
)

// --- ls -----------------------------------------------------------------

func TestDatastoreLs_ListsDatastoresSortedByName(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET "+pathConfigDatastore, &recordedRequest{}, []map[string]any{
		{"name": "store2", "path": "/mnt/store2"},
		{"name": "store1", "path": "/mnt/store1", "comment": "primary", "gc-schedule": "daily", "prune-schedule": "weekly"},
	})

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreLsCmd(), "ls")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "store1")
	require.Contains(t, out, "store2")
	require.Contains(t, out, "/mnt/store1")
	require.Contains(t, out, "primary")
	require.Contains(t, out, "daily")
	require.Contains(t, out, "weekly")
	// store1 must be listed before store2 (sorted by name).
	require.Less(t, strings.Index(out, "store1"), strings.Index(out, "store2"))
}

func TestDatastoreLs_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+pathConfigDatastore, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreLsCmd(), "ls")
	require.Error(t, err)
}

// --- show -----------------------------------------------------------------

func TestDatastoreShow_RendersPopulatedFields(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+fmt.Sprintf(pathConfigDatastoreFmt, "store1"), &rec, map[string]any{
		"name":          "store1",
		"path":          "/mnt/store1",
		"comment":       "hello world",
		"keep-last":     5,
		"gc-on-unmount": true,
	})

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreShowCmd(), "show", "store1")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, fmt.Sprintf(pathConfigDatastoreFmt, "store1"), rec.path)

	out := buf.String()
	require.Contains(t, out, "hello world")
	require.Contains(t, out, "store1")
	require.Contains(t, out, "/mnt/store1")
	require.Contains(t, out, "true")
}

func TestDatastoreShow_DefaultsTable(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET "+fmt.Sprintf(pathConfigDatastoreFmt, "store1"), &recordedRequest{}, map[string]any{
		"name": "store1", "path": "/mnt/store1",
	})

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreShowCmd(), "show", "store1", "--defaults")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "/mnt/store1")
	require.Contains(t, out, "notification-system (default)", "notification-mode defaults to notification-system")
}

func TestDatastoreShow_DefaultsJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET "+fmt.Sprintf(pathConfigDatastoreFmt, "store1"), &recordedRequest{}, map[string]any{
		"name": "store1", "path": "/mnt/store1",
	})

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatJSON, false)
	err := run(deps, &buf, newDatastoreShowCmd(), "show", "store1", "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"set"`)
	require.Contains(t, buf.String(), `"defaults"`)

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "/mnt/store1", got.Set["path"])
	require.Equal(t, "notification-system", got.Defaults["notification-mode"])
}

func TestDatastoreShow_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+fmt.Sprintf(pathConfigDatastoreFmt, "store1"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such datastore")
	})

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreShowCmd(), "show", "store1")
	require.Error(t, err)
}

// --- create -----------------------------------------------------------------

func TestDatastoreCreate_RequiresPath(t *testing.T) {
	_, pc := newFakeClient(t)

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreCreateCmd(), "create", "store1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--path is required")
}

func TestDatastoreCreate_SendsAllForwardedFields(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+pathConfigDatastore, &rec, nil)

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreCreateCmd(), "create", "store1",
		"--path", "/mnt/store1",
		"--comment", "test store",
		"--gc-schedule", "daily",
		"--prune-schedule", "weekly",
		"--keep-last", "5",
		"--keep-daily", "7",
		"--notify", "always",
		"--verify-new",
		"--backend", "type=filesystem",
	)
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, pathConfigDatastore, rec.path)
	require.Equal(t, "store1", rec.form.Get("name"))
	require.Equal(t, "/mnt/store1", rec.form.Get("path"))
	require.Equal(t, "test store", rec.form.Get("comment"))
	require.Equal(t, "daily", rec.form.Get("gc-schedule"))
	require.Equal(t, "weekly", rec.form.Get("prune-schedule"))
	require.Equal(t, "5", rec.form.Get("keep-last"))
	require.Equal(t, "7", rec.form.Get("keep-daily"))
	require.Equal(t, "always", rec.form.Get("notify"))
	require.Equal(t, "1", rec.form.Get("verify-new"))
	require.Equal(t, "type=filesystem", rec.form.Get("backend"))
	require.Contains(t, buf.String(), `Datastore "store1" created.`)
}

func TestDatastoreCreate_OmitsUnsetOptionalFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+pathConfigDatastore, &rec, nil)

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreCreateCmd(), "create", "store1", "--path", "/mnt/store1")
	require.NoError(t, err)

	require.Empty(t, rec.form.Get("comment"))
	require.Empty(t, rec.form.Get("gc-schedule"))
	require.Empty(t, rec.form.Get("keep-last"))
}

func TestDatastoreCreate_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+pathConfigDatastore, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid path")
	})

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreCreateCmd(), "create", "store1", "--path", "/mnt/store1")
	require.Error(t, err)
}

// --- update -----------------------------------------------------------------

func TestDatastoreUpdate_SendsChangedFieldsAndDelete(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+fmt.Sprintf(pathConfigDatastoreFmt, "store1"), &rec, nil)

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreUpdateCmd(), "update", "store1",
		"--comment", "updated",
		"--keep-weekly", "3",
		"--delete", "gc-schedule",
		"--delete", "notify",
		"--digest", "abc123",
	)
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, fmt.Sprintf(pathConfigDatastoreFmt, "store1"), rec.path)
	require.Equal(t, "updated", rec.form.Get("comment"))
	require.Equal(t, "3", rec.form.Get("keep-weekly"))
	require.Equal(t, []string{"gc-schedule", "notify"}, rec.form["delete"])
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.Contains(t, buf.String(), `Datastore "store1" updated.`)
}

func TestDatastoreUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreUpdateCmd(), "update", "store1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes given")
}

func TestDatastoreUpdate_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+fmt.Sprintf(pathConfigDatastoreFmt, "store1"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreUpdateCmd(), "update", "store1", "--comment", "x")
	require.Error(t, err)
}

// --- delete -----------------------------------------------------------------

func TestDatastoreDelete_BlocksUntilComplete(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+fmt.Sprintf(pathConfigDatastoreFmt, "store1"), &rec, validUPID)
	handleTaskStatus(f, validUPID)

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreDeleteCmd(), "delete", "store1", "--destroy-data", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, fmt.Sprintf(pathConfigDatastoreFmt, "store1"), rec.path)
	// DELETE requests carry their parameters on the query string, not the form body.
	require.Equal(t, "1", rec.query.Get("destroy-data"))
	require.Contains(t, buf.String(), `Datastore "store1" deleted.`)
}

func TestDatastoreDelete_AsyncReturnsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "DELETE "+fmt.Sprintf(pathConfigDatastoreFmt, "store1"), &recordedRequest{}, validUPID)

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, true)
	err := run(deps, &buf, newDatastoreDeleteCmd(), "delete", "store1", "--yes")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "deleted.")
}

func TestDatastoreDelete_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+fmt.Sprintf(pathConfigDatastoreFmt, "store1"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreDeleteCmd(), "delete", "store1", "--yes")
	require.Error(t, err)
}

func TestDatastoreDelete_WithoutConfirmation(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+fmt.Sprintf(pathConfigDatastoreFmt, "store1"), &rec, validUPID)

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreDeleteCmd(), "delete", "store1")
	require.Error(t, err)
	require.ErrorContains(t, err, "without confirmation")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

// --- status -----------------------------------------------------------------

func TestDatastoreStatus_RendersUsageFields(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+fmt.Sprintf(pathAdminStatusFmt, "store1"), &rec, map[string]any{
		"avail":        1000,
		"total":        2000,
		"used":         1000,
		"backend-type": "filesystem",
	})

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreStatusCmd(), "status", "store1", "--verbose")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, fmt.Sprintf(pathAdminStatusFmt, "store1"), rec.path)
	require.Equal(t, "1", rec.query.Get("verbose"))

	out := buf.String()
	require.Contains(t, out, "2000")
	require.Contains(t, out, "filesystem")
}

func TestDatastoreStatus_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+fmt.Sprintf(pathAdminStatusFmt, "store1"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreStatusCmd(), "status", "store1")
	require.Error(t, err)
}

// --- usage -----------------------------------------------------------------

func TestDatastoreUsage_ListsEntriesSortedByStore(t *testing.T) {
	f, pc := newFakeClient(t)
	recordJSON(f, "GET "+pathStatusUsage, &recordedRequest{}, []map[string]any{
		{"store": "store2", "backend-type": "filesystem", "mount-status": "mounted", "total": 100, "used": 50, "avail": 50},
		{"store": "store1", "backend-type": "s3", "mount-status": "mounted", "error": "disk full"},
	})

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreUsageCmd(), "usage")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "store1")
	require.Contains(t, out, "store2")
	require.Contains(t, out, "disk full")
	require.Contains(t, out, "s3")
	require.Less(t, strings.Index(out, "store1"), strings.Index(out, "store2"))
}

func TestDatastoreUsage_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+pathStatusUsage, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreUsageCmd(), "usage")
	require.Error(t, err)
}

// --- rrd -----------------------------------------------------------------

func TestDatastoreRrd_RendersRawData(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+fmt.Sprintf(pathAdminRrdFmt, "store1"), &rec, map[string]any{
		"time": 1700000000, "total": 1000, "used": 500,
	})

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatJSON, false)
	err := run(deps, &buf, newDatastoreRrdCmd(), "rrd", "store1", "--timeframe", "hour", "--cf", "MAX")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, fmt.Sprintf(pathAdminRrdFmt, "store1"), rec.path)
	require.Equal(t, "hour", rec.query.Get("timeframe"))
	require.Equal(t, "MAX", rec.query.Get("cf"))

	out := buf.String()
	require.Contains(t, out, "1700000000")
	require.Contains(t, out, "500")
}

func TestDatastoreRrd_RequiresTimeframeFlag(t *testing.T) {
	_, pc := newFakeClient(t)

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreRrdCmd(), "rrd", "store1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeframe")
}

func TestDatastoreRrd_RejectsInvalidTimeframe(t *testing.T) {
	_, pc := newFakeClient(t)

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreRrdCmd(), "rrd", "store1", "--timeframe", "fortnight")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--timeframe must be one of")
}

func TestDatastoreRrd_RejectsInvalidCf(t *testing.T) {
	_, pc := newFakeClient(t)

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreRrdCmd(), "rrd", "store1", "--timeframe", "hour", "--cf", "MEDIAN")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--cf must be one of")
}

func TestDatastoreRrd_ServerError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+fmt.Sprintf(pathAdminRrdFmt, "store1"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	var buf bytes.Buffer
	deps := depsFor(t, pc, output.FormatTable, false)
	err := run(deps, &buf, newDatastoreRrdCmd(), "rrd", "store1", "--timeframe", "hour")
	require.Error(t, err)
}

// --- command tree -----------------------------------------------------------------

func TestNewDatastoreCmd_RegistersAllVerbs(t *testing.T) {
	cmd := newDatastoreCmd()
	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"ls", "show", "create", "update", "delete", "status", "usage", "rrd"} {
		require.True(t, names[want], "datastore command must expose a %q sub-command", want)
	}
}
