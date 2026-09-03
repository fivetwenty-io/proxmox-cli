package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

const s3AdminPath = "/api2/json/admin/s3/" + s3ID

func TestS3Check_PutsBucketAndPrefix(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+s3AdminPath+"/check", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "check", s3ID, "--bucket", "pbs-backups", "--store-prefix", "main")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, s3AdminPath+"/check", rec.path)
	require.Equal(t, "pbs-backups", rec.form.Get("bucket"))
	require.Equal(t, "main", rec.form.Get("store-prefix"))
	require.Contains(t, buf.String(), `S3 endpoint "minio-lab" check passed for bucket "pbs-backups".`)
}

func TestS3Check_OmitsPrefixWhenUnset(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+s3AdminPath+"/check", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "check", s3ID, "--bucket", "pbs-backups")
	require.NoError(t, err)
	require.Empty(t, rec.form.Get("store-prefix"))
}

func TestS3Check_RequiresBucket(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "check", s3ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "bucket")
}

func TestS3Check_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+s3AdminPath+"/check", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "access denied")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "check", s3ID, "--bucket", "pbs-backups")
	require.Error(t, err)
	require.Contains(t, err.Error(), `check s3 endpoint "minio-lab"`)
	require.Contains(t, err.Error(), "access denied")
}

func TestS3ResetCounters_PutsBucketAndPrefix(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+s3AdminPath+"/reset-counters", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "reset-counters", s3ID, "--bucket", "pbs-backups", "--store-prefix", "main")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, s3AdminPath+"/reset-counters", rec.path)
	require.Equal(t, "pbs-backups", rec.form.Get("bucket"))
	require.Equal(t, "main", rec.form.Get("store-prefix"))
	require.Contains(t, buf.String(), `Request counters reset on S3 endpoint "minio-lab" for bucket "pbs-backups".`)
}

func TestS3ResetCounters_RequiresBucket(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newS3Cmd(), "s3", "reset-counters", s3ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "bucket")
}

// --- datastore s3-refresh ------------------------------------------------------

const s3RefreshPath = "/api2/json/admin/datastore/store1/s3-refresh"

func TestDatastoreS3Refresh_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)
	var rec recordedRequest
	recordJSON(f, "PUT "+s3RefreshPath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newDatastoreCmd(), "datastore", "s3-refresh", "store1")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, s3RefreshPath, rec.path)
	require.Contains(t, buf.String(), `Datastore "store1" refreshed from S3.`)
}

func TestDatastoreS3Refresh_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+s3RefreshPath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newDatastoreCmd(), "datastore", "s3-refresh", "store1")
	require.NoError(t, err)
	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "refreshed")
}

func TestDatastoreS3Refresh_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+s3RefreshPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "not an s3 datastore")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newDatastoreCmd(), "datastore", "s3-refresh", "store1")
	require.Error(t, err)
	require.Contains(t, err.Error(), `refresh datastore "store1" from s3`)
}
