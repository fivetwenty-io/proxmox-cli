package pbs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// verifyStore is the sample datastore name reused across verify tests.
const verifyStore = "store1"

// verifyDatastoreVerifyPath is the POST /admin/datastore/{store}/verify endpoint.
const verifyDatastoreVerifyPath = "/api2/json/admin/datastore/" + verifyStore + "/verify"

// verifyConfigPath is the base /config/verify endpoint.
const verifyConfigPath = "/api2/json/config/verify"

// verifyAdminListPath is the GET /admin/verify endpoint.
const verifyAdminListPath = "/api2/json/admin/verify"

// verifyJobID is the sample job ID reused across `verify job` tests.
const verifyJobID = "job1"

func TestVerifyRun_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+verifyDatastoreVerifyPath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "run", "--store", verifyStore,
		"--ns", "ns1", "--backup-type", "vm", "--backup-id", "100", "--backup-time", "1700000000",
		"--ignore-verified", "--outdated-after", "30", "--max-depth", "2",
		"--read-threads", "4", "--verify-threads", "8")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, verifyDatastoreVerifyPath, rec.path)
	require.Equal(t, "ns1", rec.form.Get("ns"))
	require.Equal(t, "vm", rec.form.Get("backup-type"))
	require.Equal(t, "100", rec.form.Get("backup-id"))
	require.Equal(t, "1700000000", rec.form.Get("backup-time"))
	require.Equal(t, "1", rec.form.Get("ignore-verified"))
	require.Equal(t, "30", rec.form.Get("outdated-after"))
	require.Equal(t, "2", rec.form.Get("max-depth"))
	require.Equal(t, "4", rec.form.Get("read-threads"))
	require.Equal(t, "8", rec.form.Get("verify-threads"))
	require.Contains(t, buf.String(), "Verification on datastore \"store1\" finished.")
}

func TestVerifyRun_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+verifyDatastoreVerifyPath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "run", "--store", verifyStore)
	require.NoError(t, err)

	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "finished")
}

func TestVerifyRun_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "run")
	require.Error(t, err)
}

func TestVerifyRun_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+verifyDatastoreVerifyPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "verify failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "run", "--store", verifyStore)
	require.Error(t, err)
	require.Contains(t, err.Error(), "run verify on datastore")
}

func TestVerifyJobLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+verifyAdminListPath, []map[string]any{
		{"id": "job2", "store": "store2", "ns": "", "schedule": "daily", "last-run-state": "OK", "next-run": 1700000000},
		{"id": "job1", "store": "store1", "ns": "ns1", "schedule": "hourly", "last-run-state": "OK", "next-run": 1690000000},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "ls")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "job1")
	require.Contains(t, out, "job2")
	require.Contains(t, out, "hourly")
	require.Contains(t, out, "daily")
}

func TestVerifyJobLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+verifyAdminListPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list verify jobs")
}

func TestVerifyJobShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+verifyConfigPath+"/"+verifyJobID, map[string]any{
		"id": verifyJobID, "store": verifyStore, "schedule": "daily",
		"ignore-verified": true, "comment": "nightly verify",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "show", verifyJobID)
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "nightly verify")
	require.Contains(t, out, "daily")
}

func TestVerifyJobShow_DefaultsTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+verifyConfigPath+"/"+verifyJobID, map[string]any{
		"id": verifyJobID, "store": verifyStore, "schedule": "daily",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "show", verifyJobID, "--defaults")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "daily")
	require.Contains(t, out, "true (default)", "ignore-verified defaults to true")
}

func TestVerifyJobShow_DefaultsJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+verifyConfigPath+"/"+verifyJobID, map[string]any{
		"id": verifyJobID, "store": verifyStore, "schedule": "daily",
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "show", verifyJobID, "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"set"`)
	require.Contains(t, buf.String(), `"defaults"`)

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "daily", got.Set["schedule"])
	require.Equal(t, "true", got.Defaults["ignore-verified"])
}

func TestVerifyJobShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+verifyConfigPath+"/"+verifyJobID, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such job")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "show", verifyJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "show verify job")
}

func TestVerifyJobAdd_CreatesJob(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+verifyConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "add", verifyJobID,
		"--store", verifyStore, "--schedule", "daily", "--ns", "ns1",
		"--max-depth", "3", "--outdated-after", "30", "--read-threads", "2",
		"--verify-threads", "4", "--ignore-verified", "--comment", "nightly")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, verifyConfigPath, rec.path)
	require.Equal(t, verifyJobID, rec.form.Get("id"))
	require.Equal(t, verifyStore, rec.form.Get("store"))
	require.Equal(t, "daily", rec.form.Get("schedule"))
	require.Equal(t, "ns1", rec.form.Get("ns"))
	require.Equal(t, "3", rec.form.Get("max-depth"))
	require.Equal(t, "30", rec.form.Get("outdated-after"))
	require.Equal(t, "2", rec.form.Get("read-threads"))
	require.Equal(t, "4", rec.form.Get("verify-threads"))
	require.Equal(t, "1", rec.form.Get("ignore-verified"))
	require.Equal(t, "nightly", rec.form.Get("comment"))
	require.Contains(t, buf.String(), "Verify job \"job1\" created.")
}

func TestVerifyJobAdd_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "add", verifyJobID)
	require.Error(t, err)
}

func TestVerifyJobAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+verifyConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid store")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "add", verifyJobID, "--store", verifyStore)
	require.Error(t, err)
	require.Contains(t, err.Error(), "create verify job")
}

func TestVerifyJobUpdate_UpdatesJob(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+verifyConfigPath+"/"+verifyJobID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "update", verifyJobID,
		"--schedule", "weekly", "--digest", "abc123", "--delete", "comment,ns")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, verifyConfigPath+"/"+verifyJobID, rec.path)
	require.Equal(t, "weekly", rec.form.Get("schedule"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"comment", "ns"}, rec.form["delete"])
	require.Contains(t, buf.String(), "Verify job \"job1\" updated.")
}

func TestVerifyJobUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "update", verifyJobID, "--delete", "comment,")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestVerifyJobUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+verifyConfigPath+"/"+verifyJobID, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "update", verifyJobID, "--schedule", "weekly")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update verify job")
}

func TestVerifyJobDelete_RequiresYes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+verifyConfigPath+"/"+verifyJobID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "delete", verifyJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

func TestVerifyJobDelete_DeletesJob(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+verifyConfigPath+"/"+verifyJobID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "delete", verifyJobID, "--digest", "abc123", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, verifyConfigPath+"/"+verifyJobID, rec.path)
	require.Equal(t, "abc123", rec.query.Get("digest"))
	require.Contains(t, buf.String(), "Verify job \"job1\" deleted.")
}

func TestVerifyJobDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+verifyConfigPath+"/"+verifyJobID, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "delete", verifyJobID, "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete verify job")
}

func TestVerifyJobRun_RunsJob(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+verifyAdminListPath+"/"+verifyJobID+"/run", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "run", verifyJobID)
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, verifyAdminListPath+"/"+verifyJobID+"/run", rec.path)
	require.Contains(t, buf.String(), "Verify job \"job1\" started.")
}

func TestVerifyJobRun_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+verifyAdminListPath+"/"+verifyJobID+"/run", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "run failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newVerifyCmd(), "verify", "job", "run", verifyJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "run verify job")
}
