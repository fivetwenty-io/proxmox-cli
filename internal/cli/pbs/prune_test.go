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

// pruneStore is the sample datastore name reused across prune tests.
const pruneStore = "store1"

// prunePruneDatastorePath is the POST endpoint hit by `prune run`.
const prunePruneDatastorePath = "/api2/json/admin/datastore/" + pruneStore + "/prune-datastore"

// pruneGroupPrunePath is the POST endpoint hit by `prune simulate`.
const pruneGroupPrunePath = "/api2/json/admin/datastore/" + pruneStore + "/prune"

// pruneConfigPath is the base /config/prune endpoint.
const pruneConfigPath = "/api2/json/config/prune"

// pruneAdminListPath is the GET /admin/prune endpoint.
const pruneAdminListPath = "/api2/json/admin/prune"

// pruneJobID is the sample job ID reused across `prune job` tests.
const pruneJobID = "job1"

func TestPruneRun_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+prunePruneDatastorePath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "run", "--store", pruneStore,
		"--ns", "myns", "--max-depth", "2", "--keep-last", "3", "--keep-daily", "7", "--dry-run")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, prunePruneDatastorePath, rec.path)
	require.Equal(t, "myns", rec.form.Get("ns"))
	require.Equal(t, "2", rec.form.Get("max-depth"))
	require.Equal(t, "3", rec.form.Get("keep-last"))
	require.Equal(t, "7", rec.form.Get("keep-daily"))
	require.Equal(t, "1", rec.form.Get("dry-run"))
	require.Contains(t, buf.String(), "Prune of datastore \"store1\" finished.")
}

func TestPruneRun_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+prunePruneDatastorePath, &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "run", "--store", pruneStore)
	require.NoError(t, err)

	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "finished")
}

func TestPruneRun_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "run")
	require.Error(t, err)
}

func TestPruneRun_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+prunePruneDatastorePath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "prune failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "run", "--store", pruneStore)
	require.Error(t, err)
	require.Contains(t, err.Error(), "run prune on datastore")
}

func TestPruneSimulate_RendersPlan(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+pruneGroupPrunePath, &rec, []map[string]any{
		{"backup-type": "vm", "backup-id": "100", "backup-time": 1700000000, "keep": true},
		{"backup-type": "vm", "backup-id": "100", "backup-time": 1690000000, "keep": false},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "simulate", "vm/100", "--store", pruneStore, "--keep-last", "1")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, pruneGroupPrunePath, rec.path)
	require.Equal(t, "vm", rec.form.Get("backup-type"))
	require.Equal(t, "100", rec.form.Get("backup-id"))
	require.Equal(t, "1", rec.form.Get("dry-run"))
	require.Equal(t, "1", rec.form.Get("keep-last"))

	require.Contains(t, buf.String(), "keep")
	require.Contains(t, buf.String(), "remove")
}

func TestPruneSimulate_RejectsInvalidGroupRef(t *testing.T) {
	tests := []struct {
		name string
		ref  string
	}{
		{name: "no separator", ref: "vm100"},
		{name: "unknown type", ref: "docker/100"},
		{name: "empty id", ref: "vm/"},
		{name: "id with extra segment", ref: "vm/100/200"},
		{name: "id with bad character", ref: "vm/100!"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, pc := newFakeClient(t)
			deps := depsFor(t, pc, output.FormatTable, false)
			var buf bytes.Buffer
			err := run(deps, &buf, newPruneCmd(), "prune", "simulate", tc.ref, "--store", pruneStore)
			require.Error(t, err)
			require.Contains(t, err.Error(), "invalid group reference")
		})
	}
}

func TestPruneSimulate_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "simulate", "vm/100")
	require.Error(t, err)
}

func TestPruneSimulate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+pruneGroupPrunePath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "simulate failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "simulate", "vm/100", "--store", pruneStore)
	require.Error(t, err)
	require.Contains(t, err.Error(), "simulate prune of group")
}

func TestPruneJobLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+pruneAdminListPath, []map[string]any{
		{
			"id": "job2", "store": "store2", "ns": "", "schedule": "daily",
			"disable": false, "last-run-state": "OK", "next-run": 1700000000,
		},
		{
			"id": "job1", "store": "store1", "ns": "ns1", "schedule": "hourly",
			"disable": true, "last-run-state": "OK", "next-run": 1690000000,
		},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "ls")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "job1")
	require.Contains(t, out, "job2")
	require.Contains(t, out, "hourly")
	require.Contains(t, out, "daily")
}

func TestPruneJobLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+pruneAdminListPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list prune jobs")
}

func TestPruneJobShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+pruneConfigPath+"/"+pruneJobID, map[string]any{
		"id": pruneJobID, "store": pruneStore, "schedule": "daily",
		"keep-last": 5, "disable": false, "comment": "nightly prune",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "show", pruneJobID)
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "nightly prune")
	require.Contains(t, out, "daily")
}

func TestPruneJobShow_DefaultsTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+pruneConfigPath+"/"+pruneJobID, map[string]any{
		"id": pruneJobID, "store": pruneStore, "schedule": "daily",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "show", pruneJobID, "--defaults")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "daily")
	require.Contains(t, out, "false (default)", "disable defaults to false")
}

func TestPruneJobShow_DefaultsJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+pruneConfigPath+"/"+pruneJobID, map[string]any{
		"id": pruneJobID, "store": pruneStore, "schedule": "daily",
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "show", pruneJobID, "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"set"`)
	require.Contains(t, buf.String(), `"defaults"`)

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "daily", got.Set["schedule"])
	require.Equal(t, "false", got.Defaults["disable"])
}

func TestPruneJobShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+pruneConfigPath+"/"+pruneJobID, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such job")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "show", pruneJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "show prune job")
}

func TestPruneJobAdd_CreatesJob(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+pruneConfigPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "add", pruneJobID,
		"--store", pruneStore, "--schedule", "daily", "--ns", "ns1",
		"--max-depth", "3", "--keep-last", "5", "--disable", "--comment", "nightly")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, pruneConfigPath, rec.path)
	require.Equal(t, pruneJobID, rec.form.Get("id"))
	require.Equal(t, pruneStore, rec.form.Get("store"))
	require.Equal(t, "daily", rec.form.Get("schedule"))
	require.Equal(t, "ns1", rec.form.Get("ns"))
	require.Equal(t, "3", rec.form.Get("max-depth"))
	require.Equal(t, "5", rec.form.Get("keep-last"))
	require.Equal(t, "1", rec.form.Get("disable"))
	require.Equal(t, "nightly", rec.form.Get("comment"))
	require.Contains(t, buf.String(), "Prune job \"job1\" created.")
}

func TestPruneJobAdd_RequiresStore(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "add", pruneJobID, "--schedule", "daily")
	require.Error(t, err)
}

func TestPruneJobAdd_RequiresSchedule(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "add", pruneJobID, "--store", pruneStore)
	require.Error(t, err)
}

func TestPruneJobAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+pruneConfigPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid schedule")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "add", pruneJobID, "--store", pruneStore, "--schedule", "daily")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create prune job")
}

func TestPruneJobUpdate_UpdatesJob(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+pruneConfigPath+"/"+pruneJobID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "update", pruneJobID,
		"--schedule", "weekly", "--digest", "abc123", "--delete", "comment,ns")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, pruneConfigPath+"/"+pruneJobID, rec.path)
	require.Equal(t, "weekly", rec.form.Get("schedule"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.ElementsMatch(t, []string{"comment", "ns"}, rec.form["delete"])
	require.Contains(t, buf.String(), "Prune job \"job1\" updated.")
}

func TestPruneJobUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "update", pruneJobID, "--delete", "comment,")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestPruneJobUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+pruneConfigPath+"/"+pruneJobID, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "update", pruneJobID, "--schedule", "weekly")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update prune job")
}

func TestPruneJobDelete_RequiresYes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+pruneConfigPath+"/"+pruneJobID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "delete", pruneJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

func TestPruneJobDelete_DeletesJob(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+pruneConfigPath+"/"+pruneJobID, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "delete", pruneJobID, "--digest", "abc123", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, pruneConfigPath+"/"+pruneJobID, rec.path)
	require.Equal(t, "abc123", rec.query.Get("digest"))
	require.Contains(t, buf.String(), "Prune job \"job1\" deleted.")
}

func TestPruneJobDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+pruneConfigPath+"/"+pruneJobID, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "delete", pruneJobID, "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete prune job")
}

func TestPruneJobRun_RunsJob(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+pruneAdminListPath+"/"+pruneJobID+"/run", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "run", pruneJobID)
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, pruneAdminListPath+"/"+pruneJobID+"/run", rec.path)
	require.Contains(t, buf.String(), "Prune job \"job1\" started.")
}

func TestPruneJobRun_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+pruneAdminListPath+"/"+pruneJobID+"/run", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "run failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newPruneCmd(), "prune", "job", "run", pruneJobID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "run prune job")
}
