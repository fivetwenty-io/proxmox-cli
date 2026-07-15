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

// notifMatchersPath is the base /config/notifications/matchers endpoint.
const notifMatchersPath = "/api2/json/config/notifications/matchers"

// --- matcher command tree -------------------------------------------------------

func TestNewNotifMatcherCmd_RegistersVerbs(t *testing.T) {
	cmd := newNotifMatcherCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"ls", "show", "add", "update", "delete", "fields", "field-values"} {
		require.True(t, names[want], "missing subcommand %q", want)
	}
}

// --- matcher ls -------------------------------------------------------------------

func TestNotifMatcherLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+notifMatchersPath, &rec, []map[string]any{
		{"name": "matcher-b", "target": []string{"target1"}, "mode": "any"},
		{"name": "matcher-a", "match-severity": []string{"warning", "error"}, "invert-match": true},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, notifMatchersPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "matcher-a")
	require.Contains(t, out, "matcher-b")
	require.Contains(t, out, "warning,error")
	require.Contains(t, out, "any")
}

func TestNotifMatcherLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+notifMatchersPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list notification matchers")
}

// --- matcher show -------------------------------------------------------------------

func TestNotifMatcherShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+notifMatchersPath+"/matcher-a", map[string]any{
		"name": "matcher-a", "mode": "all", "target": []string{"target1"}, "comment": "primary",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "show", "matcher-a")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "all")
	require.Contains(t, out, "primary")
}

func TestNotifMatcherShow_DefaultsTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+notifMatchersPath+"/matcher-a", map[string]any{
		"name": "matcher-a", "target": []string{"target1"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "show", "matcher-a", "--defaults")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "target1")
	require.Contains(t, out, "all (default)", "mode defaults to all")
}

func TestNotifMatcherShow_DefaultsJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+notifMatchersPath+"/matcher-a", map[string]any{
		"name": "matcher-a", "target": []string{"target1"},
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "show", "matcher-a", "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"set"`)
	require.Contains(t, buf.String(), `"defaults"`)

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "all", got.Defaults["mode"])
}

func TestNotifMatcherShow_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "show", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifMatcherShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+notifMatchersPath+"/matcher-a", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such matcher")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "show", "matcher-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "show notification matcher")
}

// --- matcher add -------------------------------------------------------------------

func TestNotifMatcherAdd_CreatesMatcher(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+notifMatchersPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "add", "matcher-a", "--target", "target1")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, notifMatchersPath, rec.path)
	require.Equal(t, "matcher-a", rec.form.Get("name"))
	require.Equal(t, []string{"target1"}, rec.form["target"])
	require.Contains(t, buf.String(), `Notification matcher "matcher-a" created.`)
}

func TestNotifMatcherAdd_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "add", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifMatcherAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+notifMatchersPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid matcher")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "add", "matcher-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create notification matcher")
}

// TestNotifMatcherAdd_AuditAllFlags asserts every field of
// CreateNotificationsMatchersParams is settable and forwarded by a flag.
func TestNotifMatcherAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+notifMatchersPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "add", "audit-matcher",
		"--comment", "audit comment",
		"--disable",
		"--invert-match",
		"--match-calendar", "mon..fri 8:00-16:30",
		"--match-field", "type=gc",
		"--match-severity", "warning",
		"--match-severity", "error",
		"--mode", "any",
		"--origin", "user-created",
		"--target", "target1",
		"--target", "target2",
	)
	require.NoError(t, err)

	want := map[string]string{
		"name":         "audit-matcher",
		"comment":      "audit comment",
		"disable":      "1",
		"invert-match": "1",
		"mode":         "any",
		"origin":       "user-created",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.Equal(t, []string{"mon..fri 8:00-16:30"}, rec.form["match-calendar"])
	require.Equal(t, []string{"type=gc"}, rec.form["match-field"])
	require.Equal(t, []string{"warning", "error"}, rec.form["match-severity"])
	require.Equal(t, []string{"target1", "target2"}, rec.form["target"])
}

// --- matcher update -------------------------------------------------------------------

func TestNotifMatcherUpdate_UpdatesMatcher(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifMatchersPath+"/matcher-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "update", "matcher-a",
		"--mode", "any", "--digest", "abc123", "--delete", "comment")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, notifMatchersPath+"/matcher-a", rec.path)
	require.Equal(t, "any", rec.form.Get("mode"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.Equal(t, []string{"comment"}, rec.form["delete"])
	require.Contains(t, buf.String(), `Notification matcher "matcher-a" updated.`)
}

// TestNotifMatcherUpdate_AuditAllFlags asserts every field of
// UpdateNotificationsMatchersParams is settable and forwarded by a flag.
func TestNotifMatcherUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifMatchersPath+"/matcher-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "update", "matcher-a",
		"--comment", "updated comment",
		"--disable",
		"--invert-match",
		"--match-calendar", "daily",
		"--match-field", "type=prune",
		"--match-severity", "info",
		"--mode", "all",
		"--target", "target3",
		"--delete", "match-field",
		"--digest", "abc123",
	)
	require.NoError(t, err)

	want := map[string]string{
		"comment":      "updated comment",
		"disable":      "1",
		"invert-match": "1",
		"mode":         "all",
		"digest":       "abc123",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.Equal(t, []string{"daily"}, rec.form["match-calendar"])
	require.Equal(t, []string{"type=prune"}, rec.form["match-field"])
	require.Equal(t, []string{"info"}, rec.form["match-severity"])
	require.Equal(t, []string{"target3"}, rec.form["target"])
	require.Equal(t, []string{"match-field"}, rec.form["delete"])
}

func TestNotifMatcherUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifMatchersPath+"/matcher-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "update", "matcher-a", "--comment", "only comment")
	require.NoError(t, err)
	require.Equal(t, "only comment", rec.form.Get("comment"))

	for _, key := range []string{
		"disable", "invert-match", "match-calendar", "match-field", "match-severity",
		"mode", "target", "delete", "digest",
	} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestNotifMatcherUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "update", "matcher-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestNotifMatcherUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "update", "matcher-a", "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestNotifMatcherUpdate_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "update", "", "--comment", "x")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifMatcherUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+notifMatchersPath+"/matcher-a", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "update", "matcher-a", "--comment", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update notification matcher")
}

// --- matcher delete -------------------------------------------------------------------

func TestNotifMatcherDelete_RequiresYes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+notifMatchersPath+"/matcher-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "delete", "matcher-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

func TestNotifMatcherDelete_DeletesMatcher(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+notifMatchersPath+"/matcher-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "delete", "matcher-a", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, notifMatchersPath+"/matcher-a", rec.path)
	require.Contains(t, buf.String(), `Notification matcher "matcher-a" deleted.`)
}

func TestNotifMatcherDelete_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "delete", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifMatcherDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+notifMatchersPath+"/matcher-a", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "delete", "matcher-a", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete notification matcher")
}

// --- matcher fields ls -------------------------------------------------------------------

func TestNotifMatcherFieldsLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/notifications/matcher-fields", &rec, []map[string]any{
		{"name": "type"},
		{"name": "datastore"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "fields", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/config/notifications/matcher-fields", rec.path)

	out := buf.String()
	require.Contains(t, out, "type")
	require.Contains(t, out, "datastore")
}

func TestNotifMatcherFieldsLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET /api2/json/config/notifications/matcher-fields", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "fields", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list notification matcher fields")
}

// --- matcher field-values ls -------------------------------------------------------------------

func TestNotifMatcherFieldValuesLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET /api2/json/config/notifications/matcher-field-values", &rec, []map[string]any{
		{"field": "type", "value": "gc"},
		{"field": "type", "value": "prune", "comment": "prune jobs"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "field-values", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/config/notifications/matcher-field-values", rec.path)

	out := buf.String()
	require.Contains(t, out, "gc")
	require.Contains(t, out, "prune")
	require.Contains(t, out, "prune jobs")
}

func TestNotifMatcherFieldValuesLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET /api2/json/config/notifications/matcher-field-values", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "field-values", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list notification matcher field values")
}
