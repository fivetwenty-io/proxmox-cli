package pbs

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

func TestStatusDatastoreUsage_ListsSortedByStore(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	var rec recordedRequest
	recordJSON(f, "GET /api2/json/status/datastore-usage", &rec, []map[string]any{
		{
			"store": "zstore", "backend-type": "filesystem", "mount-status": "mounted",
			"total": 1000000, "used": 500000, "avail": 500000,
		},
		{
			"store": "astore", "backend-type": "s3", "mount-status": "mounted",
			"total": 2000000, "used": 100000, "avail": 1900000, "estimated-full-date": 1999999999,
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsStatusDatastoreUsageCmd(), "datastore-usage")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/status/datastore-usage", rec.path)

	out := buf.String()
	require.Contains(t, out, "astore")
	require.Contains(t, out, "zstore")
	require.Contains(t, out, "s3")
	require.Contains(t, out, "1999999999")

	aIdx := strings.Index(out, "astore")
	zIdx := strings.Index(out, "zstore")
	require.Less(t, aIdx, zIdx, "entries must be sorted by store name")
}

func TestStatusDatastoreUsage_HandlesErrorAndMissingOptionalFields(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/status/datastore-usage", &recordedRequest{}, []map[string]any{
		{
			"store": "brokenstore", "backend-type": "filesystem", "mount-status": "not-mounted",
			"error": "device not found",
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsStatusDatastoreUsageCmd(), "datastore-usage")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "device not found")
	require.Contains(t, buf.String(), "not-mounted")
}

func TestStatusDatastoreUsage_EmptyList(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	recordJSON(f, "GET /api2/json/status/datastore-usage", &recordedRequest{}, []map[string]any{})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsStatusDatastoreUsageCmd(), "datastore-usage")
	require.NoError(t, err)
}

func TestStatusDatastoreUsage_ServerErrorSurfaced(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)

	f.HandleFunc("GET /api2/json/status/datastore-usage", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "internal error listing datastores")
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsStatusDatastoreUsageCmd(), "datastore-usage")
	require.Error(t, err)
	require.ErrorContains(t, err, "internal error listing datastores")
}

func TestStatusDatastoreUsage_RawPreservesHistory(t *testing.T) {
	f, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatJSON, false)

	recordJSON(f, "GET /api2/json/status/datastore-usage", &recordedRequest{}, []map[string]any{
		{
			"store": "s1", "backend-type": "filesystem", "mount-status": "mounted",
			"history": []int64{1, 2, 3}, "history-delta": 3600, "history-start": 1700000000,
		},
	})

	var buf bytes.Buffer
	err := run(deps, &buf, newPbsStatusDatastoreUsageCmd(), "datastore-usage")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"history"`)
	require.Contains(t, buf.String(), "3600")
}

func TestNewStatusCmd_RegistersDatastoreUsage(t *testing.T) {
	cmd := newStatusCmd()
	found := false
	for _, c := range cmd.Commands() {
		if c.Name() == "datastore-usage" {
			found = true
		}
	}
	require.True(t, found, "status command must register datastore-usage")
}
