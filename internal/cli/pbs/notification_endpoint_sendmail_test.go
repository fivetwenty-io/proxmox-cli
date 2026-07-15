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

// notifSendmailPath is the base /config/notifications/endpoints/sendmail endpoint.
const notifSendmailPath = "/api2/json/config/notifications/endpoints/sendmail"

// --- sendmail command tree -------------------------------------------------------

func TestNewNotifEndpointSendmailCmd_RegistersVerbs(t *testing.T) {
	cmd := newNotifEndpointSendmailCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"ls", "show", "add", "update", "delete"} {
		require.True(t, names[want], "missing subcommand %q", want)
	}
}

// --- sendmail ls -------------------------------------------------------------------

func TestNotifEndpointSendmailLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+notifSendmailPath, &rec, []map[string]any{
		{"name": "sendmail-b", "mailto": []string{"b@example.com"}},
		{"name": "sendmail-a", "mailto-user": []string{"root@pam"}, "from-address": "pbs@example.com"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, notifSendmailPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "sendmail-a")
	require.Contains(t, out, "sendmail-b")
	require.Contains(t, out, "root@pam")
	require.Contains(t, out, "pbs@example.com")
}

func TestNotifEndpointSendmailLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+notifSendmailPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list sendmail endpoints")
}

// --- sendmail show -------------------------------------------------------------------

func TestNotifEndpointSendmailShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+notifSendmailPath+"/sendmail-a", map[string]any{
		"name": "sendmail-a", "mailto": []string{"admin@example.com"}, "comment": "primary",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "show", "sendmail-a")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "admin@example.com")
	require.Contains(t, out, "primary")
}

// TestNotifEndpointSendmailShow_DefaultsTable verifies --defaults lists an
// unset option (the PBS sendmail endpoint schema declares no built-in
// defaults, so unset options render "(unset)" rather than "(default)").
func TestNotifEndpointSendmailShow_DefaultsTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+notifSendmailPath+"/sendmail-a", map[string]any{
		"name": "sendmail-a", "mailto": []string{"admin@example.com"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "show", "sendmail-a", "--defaults")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "admin@example.com")
	require.Contains(t, out, "(unset)", "comment has no schema default")
}

func TestNotifEndpointSendmailShow_DefaultsJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+notifSendmailPath+"/sendmail-a", map[string]any{
		"name": "sendmail-a", "mailto": []string{"admin@example.com"},
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "show", "sendmail-a", "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"set"`)
	require.Contains(t, buf.String(), `"defaults"`)

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Empty(t, got.Defaults, "sendmail endpoint schema declares no built-in defaults")
}

func TestNotifEndpointSendmailShow_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "show", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointSendmailShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+notifSendmailPath+"/sendmail-a", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such endpoint")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "show", "sendmail-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "show sendmail endpoint")
}

// --- sendmail add -------------------------------------------------------------------

func TestNotifEndpointSendmailAdd_CreatesEndpoint(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+notifSendmailPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "add", "sendmail-a",
		"--mailto", "admin@example.com")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, notifSendmailPath, rec.path)
	require.Equal(t, "sendmail-a", rec.form.Get("name"))
	require.Equal(t, []string{"admin@example.com"}, rec.form["mailto"])
	require.Contains(t, buf.String(), `Sendmail endpoint "sendmail-a" created.`)
}

func TestNotifEndpointSendmailAdd_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "add", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointSendmailAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+notifSendmailPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid endpoint")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "add", "sendmail-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create sendmail endpoint")
}

// TestNotifEndpointSendmailAdd_AuditAllFlags asserts every field of
// CreateNotificationsEndpointsSendmailParams is settable and forwarded by a flag.
func TestNotifEndpointSendmailAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+notifSendmailPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "add", "audit-sendmail",
		"--author", "PBS Audit",
		"--comment", "audit comment",
		"--disable",
		"--filter", "old-filter",
		"--from-address", "pbs@example.com",
		"--mailto", "a@example.com",
		"--mailto", "b@example.com",
		"--mailto-user", "root@pam",
		"--mailto-user", "admin@pam",
		"--origin", "user-created",
	)
	require.NoError(t, err)

	want := map[string]string{
		"name":         "audit-sendmail",
		"author":       "PBS Audit",
		"comment":      "audit comment",
		"disable":      "1",
		"filter":       "old-filter",
		"from-address": "pbs@example.com",
		"origin":       "user-created",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.Equal(t, []string{"a@example.com", "b@example.com"}, rec.form["mailto"])
	require.Equal(t, []string{"root@pam", "admin@pam"}, rec.form["mailto-user"])
}

// --- sendmail update -------------------------------------------------------------------

func TestNotifEndpointSendmailUpdate_UpdatesEndpoint(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifSendmailPath+"/sendmail-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "update", "sendmail-a",
		"--from-address", "new@example.com", "--digest", "abc123", "--delete", "comment")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, notifSendmailPath+"/sendmail-a", rec.path)
	require.Equal(t, "new@example.com", rec.form.Get("from-address"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.Equal(t, []string{"comment"}, rec.form["delete"])
	require.Contains(t, buf.String(), `Sendmail endpoint "sendmail-a" updated.`)
}

// TestNotifEndpointSendmailUpdate_AuditAllFlags asserts every field of
// UpdateNotificationsEndpointsSendmailParams is settable and forwarded by a flag.
func TestNotifEndpointSendmailUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifSendmailPath+"/sendmail-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "update", "sendmail-a",
		"--author", "New Author",
		"--comment", "updated comment",
		"--disable",
		"--from-address", "new@example.com",
		"--mailto", "c@example.com",
		"--mailto-user", "user@pam",
		"--delete", "filter",
		"--digest", "abc123",
	)
	require.NoError(t, err)

	want := map[string]string{
		"author":       "New Author",
		"comment":      "updated comment",
		"disable":      "1",
		"from-address": "new@example.com",
		"digest":       "abc123",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.Equal(t, []string{"c@example.com"}, rec.form["mailto"])
	require.Equal(t, []string{"user@pam"}, rec.form["mailto-user"])
	require.Equal(t, []string{"filter"}, rec.form["delete"])
}

func TestNotifEndpointSendmailUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifSendmailPath+"/sendmail-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "update", "sendmail-a", "--comment", "only comment")
	require.NoError(t, err)
	require.Equal(t, "only comment", rec.form.Get("comment"))

	for _, key := range []string{"author", "from-address", "mailto", "mailto-user", "disable", "delete", "digest"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestNotifEndpointSendmailUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "update", "sendmail-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestNotifEndpointSendmailUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "update", "sendmail-a", "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestNotifEndpointSendmailUpdate_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "update", "", "--comment", "x")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointSendmailUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+notifSendmailPath+"/sendmail-a", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "update", "sendmail-a", "--comment", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update sendmail endpoint")
}

// --- sendmail delete -------------------------------------------------------------------

func TestNotifEndpointSendmailDelete_RequiresYes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+notifSendmailPath+"/sendmail-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "delete", "sendmail-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

func TestNotifEndpointSendmailDelete_DeletesEndpoint(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+notifSendmailPath+"/sendmail-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "delete", "sendmail-a", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, notifSendmailPath+"/sendmail-a", rec.path)
	require.Contains(t, buf.String(), `Sendmail endpoint "sendmail-a" deleted.`)
}

func TestNotifEndpointSendmailDelete_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "delete", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointSendmailDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+notifSendmailPath+"/sendmail-a", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSendmailCmd(), "sendmail", "delete", "sendmail-a", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete sendmail endpoint")
}
