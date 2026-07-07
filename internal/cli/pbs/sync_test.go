package pbs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
	"github.com/fivetwenty-io/pmx-cli/internal/testhelper"
)

// syncAdminListPath is the GET /admin/sync endpoint (`sync ls`).
const syncAdminListPath = "/api2/json/admin/sync"

// syncConfigPath is the base /config/sync endpoint (`sync job` CRUD).
const syncConfigPath = "/api2/json/config/sync"

// syncJobID is the sample job ID reused across `sync job` tests.
const syncJobID = "job1"

// syncPullPath and syncPushPath are the one-shot sync endpoints.
const syncPullPath = "/api2/json/pull"
const syncPushPath = "/api2/json/push"

// --- sync ls (admin, status-rich) -------------------------------------------

func TestSyncLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+syncAdminListPath, &rec, []map[string]any{
		{
			"id": "job2", "store": "store2", "remote": "r1", "remote-store": "rs2",
			"sync-direction": "pull", "schedule": "daily", "last-run-state": "OK", "next-run": 1700000000,
		},
		{
			"id": "job1", "store": "store1", "remote": "r1", "remote-store": "rs1",
			"sync-direction": "push", "schedule": "hourly", "last-run-state": "OK", "next-run": 1690000000,
		},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, syncAdminListPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "job1")
	require.Contains(t, out, "job2")
	require.Contains(t, out, "push")
	require.Contains(t, out, "pull")
}

func TestSyncLs_FiltersByStoreAndDirection(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+syncAdminListPath, &rec, []map[string]any{})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "ls", "--store", "store1", "--sync-direction", "pull")
	require.NoError(t, err)

	require.Equal(t, "store1", rec.query.Get("store"))
	require.Equal(t, "pull", rec.query.Get("sync-direction"))
}

func TestSyncLs_RejectsInvalidSyncDirection(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "ls", "--sync-direction", "sideways")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--sync-direction")
}

func TestSyncLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+syncAdminListPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list sync job status")
}

// --- sync job ls (config, plain) --------------------------------------------

func TestSyncJobLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+syncConfigPath, []map[string]any{
		{"id": "job2", "store": "store2", "remote-store": "rs2", "sync-direction": "pull", "schedule": "daily"},
		{"id": "job1", "store": "store1", "remote-store": "rs1", "sync-direction": "push", "schedule": "hourly"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "ls")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "job1")
	require.Contains(t, out, "job2")
}

func TestSyncJobLs_FiltersByDirection(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+syncConfigPath, &rec, []map[string]any{})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "ls", "--sync-direction", "push")
	require.NoError(t, err)
	require.Equal(t, "push", rec.query.Get("sync-direction"))
}

func TestSyncJobLs_RejectsInvalidSyncDirection(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "ls", "--sync-direction", "bogus")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--sync-direction")
}

func TestSyncJobLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+syncConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list sync jobs")
}

// --- sync job show -----------------------------------------------------------

func TestSyncJobShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+syncConfigPath+"/"+syncJobID, map[string]any{
		"id": syncJobID, "store": "store1", "remote-store": "rs1", "schedule": "daily", "comment": "nightly sync",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "show", syncJobID)
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "nightly sync")
	require.Contains(t, out, "daily")
}

func TestSyncJobShow_DefaultsTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+syncConfigPath+"/"+syncJobID, map[string]any{
		"id": syncJobID, "store": "store1", "remote-store": "rs1",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "show", syncJobID, "--defaults")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "rs1")
	require.Contains(t, out, "pull (default)", "sync-direction defaults to pull")
}

func TestSyncJobShow_DefaultsJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+syncConfigPath+"/"+syncJobID, map[string]any{
		"id": syncJobID, "store": "store1", "remote-store": "rs1",
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "show", syncJobID, "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"set"`)
	require.Contains(t, buf.String(), `"defaults"`)

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "store1", got.Set["store"])
	require.Equal(t, "pull", got.Defaults["sync-direction"])
}

func TestSyncJobShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+syncConfigPath+"/"+syncJobID, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such job")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "show", syncJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "show sync job")
}

// --- sync job add --------------------------------------------------------

func TestSyncJobAdd_CreatesJob(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+syncConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "add", syncJobID,
		"--store", "store1", "--remote-store", "rs1", "--remote", "r1", "--schedule", "daily")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, syncConfigPath, rec.path)
	require.Equal(t, syncJobID, rec.form.Get("id"))
	require.Equal(t, "store1", rec.form.Get("store"))
	require.Equal(t, "rs1", rec.form.Get("remote-store"))
	require.Equal(t, "r1", rec.form.Get("remote"))
	require.Equal(t, "daily", rec.form.Get("schedule"))
	require.Contains(t, buf.String(), "Sync job \"job1\" created.")
}

func TestSyncJobAdd_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "add", syncJobID, "--remote-store", "rs1")
	require.Error(t, err)
}

func TestSyncJobAdd_RequiresRemoteStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "add", syncJobID, "--store", "store1")
	require.Error(t, err)
}

func TestSyncJobAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+syncConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid job")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "add", syncJobID, "--store", "store1", "--remote-store", "rs1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create sync job")
}

func TestSyncJobAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+syncConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "add", "audit-job",
		"--store", "store1",
		"--remote-store", "rs1",
		"--active-encryption-key", "key1",
		"--associated-key", "key1", "--associated-key", "key2",
		"--burst-in", "10MB",
		"--burst-out", "20MB",
		"--comment", "audit comment",
		"--encrypted-only",
		"--group-filter", "type:vm", "--group-filter", "type:ct",
		"--max-depth", "3",
		"--ns", "myns",
		"--owner", "root@pam",
		"--rate-in", "1MB",
		"--rate-out", "2MB",
		"--remote", "r1",
		"--remote-ns", "remotens",
		"--remove-vanished",
		"--resync-corrupt",
		"--run-on-mount",
		"--schedule", "daily",
		"--sync-direction", "pull",
		"--transfer-last", "5",
		"--unmount-on-done",
		"--verified-only",
		"--worker-threads", "4",
	)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, rec.method)

	want := map[string]string{
		"id":                    "audit-job",
		"store":                 "store1",
		"remote-store":          "rs1",
		"active-encryption-key": "key1",
		"burst-in":              "10MB",
		"burst-out":             "20MB",
		"comment":               "audit comment",
		"encrypted-only":        "1",
		"max-depth":             "3",
		"ns":                    "myns",
		"owner":                 "root@pam",
		"rate-in":               "1MB",
		"rate-out":              "2MB",
		"remote":                "r1",
		"remote-ns":             "remotens",
		"remove-vanished":       "1",
		"resync-corrupt":        "1",
		"run-on-mount":          "1",
		"schedule":              "daily",
		"sync-direction":        "pull",
		"transfer-last":         "5",
		"unmount-on-done":       "1",
		"verified-only":         "1",
		"worker-threads":        "4",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.ElementsMatch(t, []string{"key1", "key2"}, rec.form["associated-key"])
	require.ElementsMatch(t, []string{"type:vm", "type:ct"}, rec.form["group-filter"])
}

// --- sync job update -----------------------------------------------------

func TestSyncJobUpdate_UpdatesJob(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+syncConfigPath+"/"+syncJobID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "update", syncJobID,
		"--schedule", "weekly", "--digest", "abc123", "--delete", "comment", "--delete", "ns")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, syncConfigPath+"/"+syncJobID, rec.path)
	require.Equal(t, "weekly", rec.form.Get("schedule"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"comment", "ns"}, rec.form["delete"])
	require.Contains(t, buf.String(), "Sync job \"job1\" updated.")
}

func TestSyncJobUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+syncConfigPath+"/"+syncJobID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "update", syncJobID, "--schedule", "weekly")
	require.NoError(t, err)
	require.Equal(t, "weekly", rec.form.Get("schedule"))

	for _, key := range []string{
		"store", "remote", "remote-store", "remote-ns", "ns", "comment", "max-depth",
		"remove-vanished", "resync-corrupt", "run-on-mount", "unmount-on-done", "verified-only",
		"encrypted-only", "delete", "digest",
	} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestSyncJobUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "update", syncJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes given")
}

func TestSyncJobUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "update", syncJobID, "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestSyncJobUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+syncConfigPath+"/"+syncJobID, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "update", syncJobID, "--schedule", "weekly")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update sync job")
}

// --- sync job delete -------------------------------------------------------

func TestSyncJobDelete_RequiresYes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+syncConfigPath+"/"+syncJobID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "delete", syncJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

func TestSyncJobDelete_DeletesJob(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+syncConfigPath+"/"+syncJobID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "delete", syncJobID, "--digest", "abc123", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, syncConfigPath+"/"+syncJobID, rec.path)
	require.Equal(t, "abc123", rec.query.Get("digest"))
	require.Contains(t, buf.String(), "Sync job \"job1\" deleted.")
}

func TestSyncJobDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+syncConfigPath+"/"+syncJobID, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "delete", syncJobID, "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete sync job")
}

// --- sync job run (raw call, UPID-capturing) --------------------------------

func TestSyncJobRun_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+syncAdminListPath+"/"+syncJobID+"/run", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "run", syncJobID)
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, syncAdminListPath+"/"+syncJobID+"/run", rec.path)
	require.Contains(t, buf.String(), "Sync job \"job1\" finished.")
}

func TestSyncJobRun_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+syncAdminListPath+"/"+syncJobID+"/run", &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "run", syncJobID)
	require.NoError(t, err)

	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "finished")
}

func TestSyncJobRun_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+syncAdminListPath+"/"+syncJobID+"/run", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "run failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "run", syncJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "run sync job")
}

func TestSyncJobRun_RejectsNonUPIDResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("POST "+syncAdminListPath+"/"+syncJobID+"/run", "not-a-upid")

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "job", "run", syncJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a UPID")
}

// --- sync pull ---------------------------------------------------------------

func TestSyncPull_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+syncPullPath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "pull",
		"--remote", "r1", "--remote-store", "rs1", "--store", "store1")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, syncPullPath, rec.path)
	require.Equal(t, "r1", rec.form.Get("remote"))
	require.Equal(t, "rs1", rec.form.Get("remote-store"))
	require.Equal(t, "store1", rec.form.Get("store"))
	require.Contains(t, buf.String(), "Pull of datastore \"store1\" from remote \"r1\" finished.")
}

func TestSyncPull_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+syncPullPath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "pull",
		"--remote", "r1", "--remote-store", "rs1", "--store", "store1")
	require.NoError(t, err)

	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "finished")
}

func TestSyncPull_LocalWithoutRemote(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+syncPullPath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "pull", "--remote-store", "rs1", "--store", "store1")
	require.NoError(t, err)

	require.False(t, rec.form.Has("remote"))
	require.Equal(t, "rs1", rec.form.Get("remote-store"))
	require.Contains(t, buf.String(), "Pull of datastore \"store1\" from local datastore \"rs1\" finished.")
}

func TestSyncPull_RequiresRemoteStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "pull", "--remote", "r1", "--store", "store1")
	require.Error(t, err)
}

func TestSyncPull_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "pull", "--remote", "r1", "--remote-store", "rs1")
	require.Error(t, err)
}

func TestSyncPull_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+syncPullPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "pull failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "pull",
		"--remote", "r1", "--remote-store", "rs1", "--store", "store1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "pull datastore")
}

func TestSyncPull_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+syncPullPath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "pull",
		"--remote", "r1",
		"--remote-store", "rs1",
		"--store", "store1",
		"--burst-in", "10MB",
		"--burst-out", "20MB",
		"--decryption-keys", "key1", "--decryption-keys", "key2",
		"--encrypted-only",
		"--group-filter", "type:vm",
		"--max-depth", "2",
		"--ns", "myns",
		"--rate-in", "1MB",
		"--rate-out", "2MB",
		"--remote-ns", "remotens",
		"--remove-vanished",
		"--resync-corrupt",
		"--transfer-last", "5",
		"--verified-only",
		"--worker-threads", "4",
	)
	require.NoError(t, err)

	want := map[string]string{
		"remote":          "r1",
		"remote-store":    "rs1",
		"store":           "store1",
		"burst-in":        "10MB",
		"burst-out":       "20MB",
		"encrypted-only":  "1",
		"max-depth":       "2",
		"ns":              "myns",
		"rate-in":         "1MB",
		"rate-out":        "2MB",
		"remote-ns":       "remotens",
		"remove-vanished": "1",
		"resync-corrupt":  "1",
		"transfer-last":   "5",
		"verified-only":   "1",
		"worker-threads":  "4",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.ElementsMatch(t, []string{"key1", "key2"}, rec.form["decryption-keys"])
	require.ElementsMatch(t, []string{"type:vm"}, rec.form["group-filter"])
}

// --- sync push ---------------------------------------------------------------

func TestSyncPush_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+syncPushPath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "push",
		"--remote", "r1", "--remote-store", "rs1", "--store", "store1")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, syncPushPath, rec.path)
	require.Contains(t, buf.String(), "Push of datastore \"store1\" to remote \"r1\" finished.")
}

func TestSyncPush_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+syncPushPath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "push",
		"--remote", "r1", "--remote-store", "rs1", "--store", "store1")
	require.NoError(t, err)

	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "finished")
}

func TestSyncPush_RequiresRemote(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "push", "--remote-store", "rs1", "--store", "store1")
	require.Error(t, err)
}

func TestSyncPush_RequiresRemoteStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "push", "--remote", "r1", "--store", "store1")
	require.Error(t, err)
}

func TestSyncPush_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "push", "--remote", "r1", "--remote-store", "rs1")
	require.Error(t, err)
}

func TestSyncPush_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+syncPushPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "push failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "push",
		"--remote", "r1", "--remote-store", "rs1", "--store", "store1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "push datastore")
}

func TestSyncPush_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+syncPushPath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newSyncCmd(), "sync", "push",
		"--remote", "r1",
		"--remote-store", "rs1",
		"--store", "store1",
		"--burst-in", "10MB",
		"--burst-out", "20MB",
		"--encrypted-only",
		"--encryption-key", "key1",
		"--group-filter", "type:vm",
		"--max-depth", "2",
		"--ns", "myns",
		"--rate-in", "1MB",
		"--rate-out", "2MB",
		"--remote-ns", "remotens",
		"--remove-vanished",
		"--transfer-last", "5",
		"--verified-only",
		"--worker-threads", "4",
	)
	require.NoError(t, err)

	want := map[string]string{
		"remote":          "r1",
		"remote-store":    "rs1",
		"store":           "store1",
		"burst-in":        "10MB",
		"burst-out":       "20MB",
		"encrypted-only":  "1",
		"encryption-key":  "key1",
		"max-depth":       "2",
		"ns":              "myns",
		"rate-in":         "1MB",
		"rate-out":        "2MB",
		"remote-ns":       "remotens",
		"remove-vanished": "1",
		"transfer-last":   "5",
		"verified-only":   "1",
		"worker-threads":  "4",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.ElementsMatch(t, []string{"type:vm"}, rec.form["group-filter"])
}
