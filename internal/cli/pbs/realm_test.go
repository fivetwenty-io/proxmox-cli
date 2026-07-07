package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pve-cli/internal/output"
	"github.com/fivetwenty-io/pve-cli/internal/testhelper"
)

// realmDomainsPath is the GET /access/domains endpoint (`realm ls`).
const realmDomainsPath = "/api2/json/access/domains"

// realmSyncPathFor builds the POST /access/domains/{realm}/sync path.
func realmSyncPathFor(realm string) string {
	return realmDomainsPath + "/" + realm + "/sync"
}

// --- realm ls ----------------------------------------------------------------

func TestRealmLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+realmDomainsPath, &rec, []map[string]any{
		{"realm": "pam", "type": "pam", "default": true},
		{"realm": "ad1", "type": "ad", "comment": "corp AD"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmCmd(), "realm", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, realmDomainsPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "pam")
	require.Contains(t, out, "ad1")
	require.Contains(t, out, "corp AD")
}

func TestRealmLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+realmDomainsPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmCmd(), "realm", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list realms")
}

// --- realm sync ----------------------------------------------------------------

func TestRealmSync_BlocksUntilTaskFinishes(t *testing.T) {
	f, pc := newFakeClient(t)
	handleTaskStatus(f, validUPID)

	var rec recordedRequest
	recordJSON(f, "POST "+realmSyncPathFor("ad1"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmCmd(), "realm", "sync", "ad1")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, realmSyncPathFor("ad1"), rec.path)
	require.Contains(t, buf.String(), `Sync of realm "ad1" finished.`)
}

func TestRealmSync_AsyncPrintsUPID(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+realmSyncPathFor("ad1"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmCmd(), "realm", "sync", "ad1")
	require.NoError(t, err)

	require.Contains(t, buf.String(), validUPID)
	require.NotContains(t, buf.String(), "finished")
}

func TestRealmSync_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+realmSyncPathFor("ad1"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmCmd(), "realm", "sync", "ad1",
		"--dry-run",
		"--enable-new",
		"--remove-vanished", "entry;properties",
	)
	require.NoError(t, err)

	require.Equal(t, "1", rec.form.Get("dry-run"))
	require.Equal(t, "1", rec.form.Get("enable-new"))
	require.Equal(t, "entry;properties", rec.form.Get("remove-vanished"))
}

func TestRealmSync_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+realmSyncPathFor("ad1"), &rec, validUPID)

	deps := depsFor(t, pc, output.FormatTable, true)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmCmd(), "realm", "sync", "ad1")
	require.NoError(t, err)

	for _, key := range []string{"dry-run", "enable-new", "remove-vanished"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestRealmSync_RejectsEmptyRemoveVanished(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmCmd(), "realm", "sync", "ad1", "--remove-vanished", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--remove-vanished")
}

func TestRealmSync_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+realmSyncPathFor("ad1"), func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "sync failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmCmd(), "realm", "sync", "ad1")
	require.Error(t, err)
	require.Contains(t, err.Error(), `sync realm "ad1"`)
}

func TestRealmSync_RejectsNonUPIDResponse(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("POST "+realmSyncPathFor("ad1"), "not-a-upid")

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newRealmCmd(), "realm", "sync", "ad1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a UPID")
}

// --- realm group wiring --------------------------------------------------------

func TestNewRealmCmd_RegistersAllSubcommands(t *testing.T) {
	cmd := newRealmCmd()
	names := make(map[string]bool)
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"ls", "sync", "ad", "ldap", "openid", "pam", "pbs"} {
		require.True(t, names[want], "expected `realm %s` to be registered", want)
	}
}
