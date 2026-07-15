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

// notifSmtpPath is the base /config/notifications/endpoints/smtp endpoint.
const notifSmtpPath = "/api2/json/config/notifications/endpoints/smtp"

// --- smtp command tree -------------------------------------------------------

func TestNewNotifEndpointSmtpCmd_RegistersVerbs(t *testing.T) {
	cmd := newNotifEndpointSmtpCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"ls", "show", "add", "update", "delete"} {
		require.True(t, names[want], "missing subcommand %q", want)
	}
}

// --- smtp ls -------------------------------------------------------------------

func TestNotifEndpointSmtpLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+notifSmtpPath, &rec, []map[string]any{
		{"name": "smtp-b", "server": "smtp2.example.com", "from-address": "b@example.com"},
		{"name": "smtp-a", "server": "smtp1.example.com", "from-address": "a@example.com", "port": 587, "mode": "starttls"},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, notifSmtpPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "smtp-a")
	require.Contains(t, out, "smtp-b")
	require.Contains(t, out, "smtp1.example.com")
	require.Contains(t, out, "starttls")
}

func TestNotifEndpointSmtpLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+notifSmtpPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list smtp endpoints")
}

// --- smtp show -------------------------------------------------------------------

func TestNotifEndpointSmtpShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+notifSmtpPath+"/smtp-a", map[string]any{
		"name": "smtp-a", "server": "smtp1.example.com", "from-address": "a@example.com", "comment": "primary",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "show", "smtp-a")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "smtp1.example.com")
	require.Contains(t, out, "primary")
}

func TestNotifEndpointSmtpShow_DefaultsTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+notifSmtpPath+"/smtp-a", map[string]any{
		"name": "smtp-a", "server": "smtp1.example.com", "from-address": "a@example.com",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "show", "smtp-a", "--defaults")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "smtp1.example.com")
	require.Contains(t, out, "tls (default)", "mode defaults to tls")
}

// TestNotifEndpointSmtpShow_DefaultsJSON verifies the JSON set/defaults
// shape and that the write-only password is never resurrected as an
// "unset" default: it is excluded from the schema table entirely.
func TestNotifEndpointSmtpShow_DefaultsJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+notifSmtpPath+"/smtp-a", map[string]any{
		"name": "smtp-a", "server": "smtp1.example.com", "from-address": "a@example.com",
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "show", "smtp-a", "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"set"`)
	require.Contains(t, buf.String(), `"defaults"`)

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "tls", got.Defaults["mode"])
	require.NotContains(t, got.Defaults, "password", "password must not appear even as an unset default")
}

func TestNotifEndpointSmtpShow_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "show", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointSmtpShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+notifSmtpPath+"/smtp-a", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such endpoint")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "show", "smtp-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "show smtp endpoint")
}

// --- smtp add -------------------------------------------------------------------

func TestNotifEndpointSmtpAdd_CreatesEndpoint(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+notifSmtpPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "add", "smtp-a",
		"--server", "smtp1.example.com", "--from-address", "a@example.com")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, notifSmtpPath, rec.path)
	require.Equal(t, "smtp-a", rec.form.Get("name"))
	require.Equal(t, "smtp1.example.com", rec.form.Get("server"))
	require.Equal(t, "a@example.com", rec.form.Get("from-address"))
	require.Contains(t, buf.String(), `SMTP endpoint "smtp-a" created.`)
}

func TestNotifEndpointSmtpAdd_RequiresServer(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "add", "smtp-a", "--from-address", "a@example.com")
	require.Error(t, err)
}

func TestNotifEndpointSmtpAdd_RequiresFromAddress(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "add", "smtp-a", "--server", "smtp1.example.com")
	require.Error(t, err)
}

func TestNotifEndpointSmtpAdd_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "add", "",
		"--server", "smtp1.example.com", "--from-address", "a@example.com")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointSmtpAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+notifSmtpPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid endpoint")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "add", "smtp-a",
		"--server", "smtp1.example.com", "--from-address", "a@example.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create smtp endpoint")
}

// TestNotifEndpointSmtpAdd_AuditAllFlags asserts every field of
// CreateNotificationsEndpointsSmtpParams is settable and forwarded by a flag.
func TestNotifEndpointSmtpAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+notifSmtpPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "add", "audit-smtp",
		"--server", "smtp1.example.com",
		"--from-address", "a@example.com",
		"--author", "PBS Audit",
		"--comment", "audit comment",
		"--disable",
		"--mailto", "x@example.com",
		"--mailto", "y@example.com",
		"--mailto-user", "root@pam",
		"--mode", "starttls",
		"--origin", "user-created",
		"--password", "secret",
		"--port", "587",
		"--username", "smtpuser",
	)
	require.NoError(t, err)

	want := map[string]string{
		"name":         "audit-smtp",
		"server":       "smtp1.example.com",
		"from-address": "a@example.com",
		"author":       "PBS Audit",
		"comment":      "audit comment",
		"disable":      "1",
		"mode":         "starttls",
		"origin":       "user-created",
		"password":     "secret",
		"port":         "587",
		"username":     "smtpuser",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.Equal(t, []string{"x@example.com", "y@example.com"}, rec.form["mailto"])
	require.Equal(t, []string{"root@pam"}, rec.form["mailto-user"])
}

// --- smtp update -------------------------------------------------------------------

func TestNotifEndpointSmtpUpdate_UpdatesEndpoint(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifSmtpPath+"/smtp-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "update", "smtp-a",
		"--port", "465", "--digest", "abc123", "--delete", "comment")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, notifSmtpPath+"/smtp-a", rec.path)
	require.Equal(t, "465", rec.form.Get("port"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.Equal(t, []string{"comment"}, rec.form["delete"])
	require.Contains(t, buf.String(), `SMTP endpoint "smtp-a" updated.`)
}

// TestNotifEndpointSmtpUpdate_AuditAllFlags asserts every field of
// UpdateNotificationsEndpointsSmtpParams is settable and forwarded by a flag.
func TestNotifEndpointSmtpUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifSmtpPath+"/smtp-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "update", "smtp-a",
		"--server", "smtp-new.example.com",
		"--from-address", "new@example.com",
		"--author", "New Author",
		"--comment", "updated comment",
		"--disable",
		"--mailto", "z@example.com",
		"--mailto-user", "user@pam",
		"--mode", "tls",
		"--password", "newsecret",
		"--port", "465",
		"--username", "newuser",
		"--delete", "mode",
		"--digest", "abc123",
	)
	require.NoError(t, err)

	want := map[string]string{
		"server":       "smtp-new.example.com",
		"from-address": "new@example.com",
		"author":       "New Author",
		"comment":      "updated comment",
		"disable":      "1",
		"mode":         "tls",
		"password":     "newsecret",
		"port":         "465",
		"username":     "newuser",
		"digest":       "abc123",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.Equal(t, []string{"z@example.com"}, rec.form["mailto"])
	require.Equal(t, []string{"user@pam"}, rec.form["mailto-user"])
	require.Equal(t, []string{"mode"}, rec.form["delete"])
}

func TestNotifEndpointSmtpUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifSmtpPath+"/smtp-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "update", "smtp-a", "--comment", "only comment")
	require.NoError(t, err)
	require.Equal(t, "only comment", rec.form.Get("comment"))

	for _, key := range []string{
		"server", "from-address", "author", "mailto", "mailto-user", "mode",
		"password", "port", "username", "disable", "delete", "digest",
	} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestNotifEndpointSmtpUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "update", "smtp-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
}

func TestNotifEndpointSmtpUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "update", "smtp-a", "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestNotifEndpointSmtpUpdate_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "update", "", "--comment", "x")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointSmtpUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+notifSmtpPath+"/smtp-a", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "update", "smtp-a", "--comment", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update smtp endpoint")
}

// --- smtp delete -------------------------------------------------------------------

func TestNotifEndpointSmtpDelete_RequiresYes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+notifSmtpPath+"/smtp-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "delete", "smtp-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

func TestNotifEndpointSmtpDelete_DeletesEndpoint(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+notifSmtpPath+"/smtp-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "delete", "smtp-a", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, notifSmtpPath+"/smtp-a", rec.path)
	require.Contains(t, buf.String(), `SMTP endpoint "smtp-a" deleted.`)
}

func TestNotifEndpointSmtpDelete_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "delete", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointSmtpDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+notifSmtpPath+"/smtp-a", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointSmtpCmd(), "smtp", "delete", "smtp-a", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete smtp endpoint")
}
