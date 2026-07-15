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

// notifGotifyPath is the base /config/notifications/endpoints/gotify endpoint.
const notifGotifyPath = "/api2/json/config/notifications/endpoints/gotify"

// --- gotify command tree -------------------------------------------------------

func TestNewNotifEndpointGotifyCmd_RegistersVerbs(t *testing.T) {
	cmd := newNotifEndpointGotifyCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"ls", "show", "add", "update", "delete"} {
		require.True(t, names[want], "missing subcommand %q", want)
	}
}

// --- gotify ls -------------------------------------------------------------------

func TestNotifEndpointGotifyLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+notifGotifyPath, &rec, []map[string]any{
		{"name": "gotify-b", "server": "https://gotify.example.com", "comment": "second"},
		{"name": "gotify-a", "server": "https://gotify-a.example.com", "disable": true},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, notifGotifyPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "gotify-a")
	require.Contains(t, out, "gotify-b")
	require.Contains(t, out, "https://gotify-a.example.com")
}

func TestNotifEndpointGotifyLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+notifGotifyPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list gotify endpoints")
}

// --- gotify show -------------------------------------------------------------------

func TestNotifEndpointGotifyShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+notifGotifyPath+"/gotify-a", map[string]any{
		"name": "gotify-a", "server": "https://gotify.example.com", "comment": "primary",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "show", "gotify-a")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "https://gotify.example.com")
	require.Contains(t, out, "primary")
}

// TestNotifEndpointGotifyShow_DefaultsTable verifies --defaults lists an
// unset option (the PBS gotify endpoint schema declares no built-in
// defaults, so unset options render "(unset)" rather than "(default)").
func TestNotifEndpointGotifyShow_DefaultsTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+notifGotifyPath+"/gotify-a", map[string]any{
		"name": "gotify-a", "server": "https://gotify.example.com",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "show", "gotify-a", "--defaults")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "https://gotify.example.com")
	require.Contains(t, out, "(unset)", "comment has no schema default")
}

// TestNotifEndpointGotifyShow_DefaultsJSON verifies the JSON set/defaults
// shape and that the write-only token is never resurrected as an "unset"
// default: it is excluded from the schema table entirely.
func TestNotifEndpointGotifyShow_DefaultsJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+notifGotifyPath+"/gotify-a", map[string]any{
		"name": "gotify-a", "server": "https://gotify.example.com",
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "show", "gotify-a", "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"set"`)
	require.Contains(t, buf.String(), `"defaults"`)

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "https://gotify.example.com", got.Set["server"])
	require.NotContains(t, got.Defaults, "token", "token must not appear even as an unset default")
}

func TestNotifEndpointGotifyShow_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "show", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointGotifyShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+notifGotifyPath+"/gotify-a", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such endpoint")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "show", "gotify-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "show gotify endpoint")
}

// --- gotify add -------------------------------------------------------------------

func TestNotifEndpointGotifyAdd_CreatesEndpoint(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+notifGotifyPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "add", "gotify-a",
		"--server", "https://gotify.example.com", "--token", "tok123")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, notifGotifyPath, rec.path)
	require.Equal(t, "gotify-a", rec.form.Get("name"))
	require.Equal(t, "https://gotify.example.com", rec.form.Get("server"))
	require.Equal(t, "tok123", rec.form.Get("token"))
	require.Contains(t, buf.String(), `Gotify endpoint "gotify-a" created.`)
}

func TestNotifEndpointGotifyAdd_RequiresServer(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "add", "gotify-a", "--token", "tok123")
	require.Error(t, err)
}

func TestNotifEndpointGotifyAdd_RequiresToken(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "add", "gotify-a",
		"--server", "https://gotify.example.com")
	require.Error(t, err)
}

func TestNotifEndpointGotifyAdd_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "add", "",
		"--server", "https://gotify.example.com", "--token", "tok123")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointGotifyAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+notifGotifyPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid endpoint")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "add", "gotify-a",
		"--server", "https://gotify.example.com", "--token", "tok123")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create gotify endpoint")
}

// TestNotifEndpointGotifyAdd_AuditAllFlags asserts every field of
// CreateNotificationsEndpointsGotifyParams is settable and forwarded by a flag.
func TestNotifEndpointGotifyAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+notifGotifyPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "add", "audit-gotify",
		"--server", "https://gotify.example.com",
		"--token", "tok123",
		"--comment", "audit comment",
		"--disable",
		"--filter", "old-filter",
		"--origin", "user-created",
	)
	require.NoError(t, err)

	want := map[string]string{
		"name":    "audit-gotify",
		"server":  "https://gotify.example.com",
		"token":   "tok123",
		"comment": "audit comment",
		"disable": "1",
		"filter":  "old-filter",
		"origin":  "user-created",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
}

// --- gotify update -------------------------------------------------------------------

func TestNotifEndpointGotifyUpdate_UpdatesEndpoint(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifGotifyPath+"/gotify-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "update", "gotify-a",
		"--server", "https://gotify-new.example.com", "--digest", "abc123", "--delete", "comment")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, notifGotifyPath+"/gotify-a", rec.path)
	require.Equal(t, "https://gotify-new.example.com", rec.form.Get("server"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.Equal(t, []string{"comment"}, rec.form["delete"])
	require.Contains(t, buf.String(), `Gotify endpoint "gotify-a" updated.`)
}

// TestNotifEndpointGotifyUpdate_AuditAllFlags asserts every field of
// UpdateNotificationsEndpointsGotifyParams is settable and forwarded by a flag.
func TestNotifEndpointGotifyUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifGotifyPath+"/gotify-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "update", "gotify-a",
		"--server", "https://gotify-new.example.com",
		"--token", "tok456",
		"--comment", "updated comment",
		"--disable",
		"--delete", "filter",
		"--digest", "abc123",
	)
	require.NoError(t, err)

	want := map[string]string{
		"server":  "https://gotify-new.example.com",
		"token":   "tok456",
		"comment": "updated comment",
		"disable": "1",
		"digest":  "abc123",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.Equal(t, []string{"filter"}, rec.form["delete"])
}

func TestNotifEndpointGotifyUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifGotifyPath+"/gotify-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "update", "gotify-a", "--comment", "only comment")
	require.NoError(t, err)
	require.Equal(t, "only comment", rec.form.Get("comment"))

	for _, key := range []string{"server", "token", "disable", "delete", "digest"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestNotifEndpointGotifyUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "update", "gotify-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestNotifEndpointGotifyUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "update", "gotify-a", "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestNotifEndpointGotifyUpdate_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "update", "", "--comment", "x")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointGotifyUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+notifGotifyPath+"/gotify-a", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "update", "gotify-a", "--comment", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update gotify endpoint")
}

// --- gotify delete -------------------------------------------------------------------

func TestNotifEndpointGotifyDelete_RequiresYes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+notifGotifyPath+"/gotify-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "delete", "gotify-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

func TestNotifEndpointGotifyDelete_DeletesEndpoint(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+notifGotifyPath+"/gotify-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "delete", "gotify-a", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, notifGotifyPath+"/gotify-a", rec.path)
	require.Contains(t, buf.String(), `Gotify endpoint "gotify-a" deleted.`)
}

func TestNotifEndpointGotifyDelete_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "delete", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointGotifyDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+notifGotifyPath+"/gotify-a", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointGotifyCmd(), "gotify", "delete", "gotify-a", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete gotify endpoint")
}
