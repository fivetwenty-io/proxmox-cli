package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// gcStore is the sample datastore name reused across gc tests.
const gcStore = "store1"

// gcDatastoreGcPath is the /admin/datastore/{store}/gc endpoint.
const gcDatastoreGcPath = "/api2/json/admin/datastore/" + gcStore + "/gc"

// gcListPath is the GET /admin/gc endpoint.
const gcListPath = "/api2/json/admin/gc"

func TestGcRun_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+gcDatastoreGcPath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newGcCmd(), "gc", "run", "--store", gcStore)
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, gcDatastoreGcPath, rec.path)
	require.Contains(t, buf.String(), "Garbage collection on datastore \"store1\" finished.")
}

func TestGcRun_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+gcDatastoreGcPath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newGcCmd(), "gc", "run", "--store", gcStore)
	require.NoError(t, err)

	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "finished")
}

func TestGcRun_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newGcCmd(), "gc", "run")
	require.Error(t, err)
}

func TestGcRun_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+gcDatastoreGcPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "gc failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newGcCmd(), "gc", "run", "--store", gcStore)
	require.Error(t, err)
	require.Contains(t, err.Error(), "run gc on datastore")
}

func TestGcStatus_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+gcDatastoreGcPath, &rec, map[string]any{
		"store":            gcStore,
		"disk-bytes":       1048576,
		"disk-chunks":      10,
		"index-data-bytes": 2097152,
		"index-file-count": 5,
		"pending-bytes":    0,
		"pending-chunks":   0,
		"removed-bad":      0,
		"removed-bytes":    4096,
		"removed-chunks":   1,
		"still-bad":        0,
		"schedule":         "daily",
		"last-run-state":   "OK",
		"cache-stats":      map[string]any{"hits": 42, "misses": 3},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newGcCmd(), "gc", "status", "--store", gcStore)
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, gcDatastoreGcPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "daily")
	require.Contains(t, out, "1048576")
	require.Contains(t, out, "42")
}

func TestGcStatus_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newGcCmd(), "gc", "status")
	require.Error(t, err)
}

func TestGcStatus_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+gcDatastoreGcPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "status failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newGcCmd(), "gc", "status", "--store", gcStore)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get gc status for datastore")
}

func TestGcLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+gcListPath, &rec, []map[string]any{
		{
			"store": "store2", "disk-bytes": 200, "disk-chunks": 2, "index-data-bytes": 400,
			"index-file-count": 4, "pending-bytes": 0, "pending-chunks": 0, "removed-bad": 0,
			"removed-bytes": 10, "removed-chunks": 1, "still-bad": 0, "schedule": "daily",
			"last-run-state": "OK",
		},
		{
			"store": "store1", "disk-bytes": 100, "disk-chunks": 1, "index-data-bytes": 200,
			"index-file-count": 2, "pending-bytes": 0, "pending-chunks": 0, "removed-bad": 0,
			"removed-bytes": 5, "removed-chunks": 1, "still-bad": 0, "schedule": "hourly",
			"last-run-state": "OK",
		},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newGcCmd(), "gc", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, gcListPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "store1")
	require.Contains(t, out, "store2")
	require.Contains(t, out, "hourly")
	require.Contains(t, out, "daily")
}

func TestGcLs_FiltersByStore(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+gcListPath, &rec, []map[string]any{})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newGcCmd(), "gc", "ls", "--store", gcStore)
	require.NoError(t, err)

	require.Equal(t, gcStore, rec.query.Get("store"))
}

func TestGcLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+gcListPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newGcCmd(), "gc", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list gc status")
}
