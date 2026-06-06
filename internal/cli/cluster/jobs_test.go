package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// TestJobsRealmSync_List verifies `pve cluster jobs realm-sync list` reads
// GET /cluster/jobs/realm-sync and renders the focused columns.
func TestJobsRealmSync_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/jobs/realm-sync", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, []any{
			map[string]any{"id": "sync-ldap", "realm": "ldap", "schedule": "daily", "enabled": 1},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "jobs", "realm-sync", "list"))
	out := buf.String()
	require.Contains(t, out, "sync-ldap")
	require.Contains(t, out, "ldap")
}

// TestJobsRealmSync_Get verifies get reads GET /cluster/jobs/realm-sync/{id}.
func TestJobsRealmSync_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/jobs/realm-sync/sync-ldap", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, map[string]any{
			"id": "sync-ldap", "realm": "ldap", "schedule": "daily", "scope": "both",
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "jobs", "realm-sync", "get", "sync-ldap"))
	require.Contains(t, buf.String(), "both")
}

// TestJobsRealmSync_CreateForwardsFields verifies create posts the required
// schedule plus changed optionals, and omits unset ones.
func TestJobsRealmSync_CreateForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/jobs/realm-sync/sync-ldap", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "jobs", "realm-sync", "create", "sync-ldap",
		"--schedule", "daily", "--realm", "ldap", "--scope", "both", "--enabled"))
	require.Equal(t, "daily", gotForm.Get("schedule"))
	require.Equal(t, "ldap", gotForm.Get("realm"))
	require.Equal(t, "both", gotForm.Get("scope"))
	require.Equal(t, "1", gotForm.Get("enabled"))
	_, hasComment := gotForm["comment"]
	require.False(t, hasComment, "unset --comment must be omitted from the request body")
}

// TestJobsRealmSync_CreateRequiresSchedule verifies create rejects a missing schedule.
func TestJobsRealmSync_CreateRequiresSchedule(t *testing.T) {
	_, ac := newFakeClient(t)
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "jobs", "realm-sync", "create", "sync-ldap", "--realm", "ldap")
	require.Error(t, err)
	require.Contains(t, err.Error(), "schedule")
}

// TestJobsRealmSync_SetRequiresSchedule verifies set rejects a missing schedule,
// because the API rewrites the full schedule on every update.
func TestJobsRealmSync_SetRequiresSchedule(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/cluster/jobs/realm-sync/sync-ldap", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "jobs", "realm-sync", "set", "sync-ldap", "--comment", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "schedule")
	require.False(t, called, "set must not issue a PUT without the required --schedule")
}

// TestJobsRealmSync_SetForwardsChanged verifies set forwards the required schedule
// plus changed optionals, and omits unset ones.
func TestJobsRealmSync_SetForwardsChanged(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/jobs/realm-sync/sync-ldap", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "jobs", "realm-sync", "set", "sync-ldap",
		"--schedule", "weekly", "--comment", "nightly"))
	require.Equal(t, "weekly", gotForm.Get("schedule"))
	require.Equal(t, "nightly", gotForm.Get("comment"))
	_, hasScope := gotForm["scope"]
	require.False(t, hasScope, "unset --scope must be omitted from the request body")
}

// TestJobsRealmSync_DeleteRequiresYes verifies delete refuses without --yes.
func TestJobsRealmSync_DeleteRequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("DELETE /api2/json/cluster/jobs/realm-sync/sync-ldap", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "jobs", "realm-sync", "delete", "sync-ldap")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "delete must not issue a DELETE without --yes")
}

// TestJobsRealmSync_DeleteWithYes verifies delete issues the DELETE with --yes.
func TestJobsRealmSync_DeleteWithYes(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod string
	f.HandleFunc("DELETE /api2/json/cluster/jobs/realm-sync/sync-ldap", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "jobs", "realm-sync", "delete", "sync-ldap", "--yes"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Contains(t, buf.String(), "deleted")
}

// TestJobsCommandTree verifies the jobs realm-sync verb set is registered.
func TestJobsCommandTree(t *testing.T) {
	root := newClusterCmd(&cli.Deps{})
	jobs := childCommands(root)["jobs"]
	require.NotNil(t, jobs, "cluster must have a jobs command")

	realmSync := childCommands(jobs)["realm-sync"]
	require.NotNil(t, realmSync, "jobs must have a realm-sync command")

	verbs := childCommands(realmSync)
	for _, v := range []string{"list", "get", "create", "set", "delete"} {
		require.Containsf(t, verbs, v, "realm-sync must have a %s command", v)
	}
}
