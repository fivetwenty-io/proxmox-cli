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

// notifWebhookPath is the base /config/notifications/endpoints/webhook endpoint.
const notifWebhookPath = "/api2/json/config/notifications/endpoints/webhook"

// --- webhook command tree -------------------------------------------------------

func TestNewNotifEndpointWebhookCmd_RegistersVerbs(t *testing.T) {
	cmd := newNotifEndpointWebhookCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"ls", "show", "add", "update", "delete"} {
		require.True(t, names[want], "missing subcommand %q", want)
	}
}

// --- webhook ls -------------------------------------------------------------------

func TestNotifEndpointWebhookLs_RendersTable(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+notifWebhookPath, &rec, []map[string]any{
		{"name": "webhook-b", "method": "post", "url": "https://b.example.com/hook"},
		{"name": "webhook-a", "method": "put", "url": "https://a.example.com/hook", "disable": true},
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "ls")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, notifWebhookPath, rec.path)

	out := buf.String()
	require.Contains(t, out, "webhook-a")
	require.Contains(t, out, "webhook-b")
	require.Contains(t, out, "https://a.example.com/hook")
}

func TestNotifEndpointWebhookLs_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+notifWebhookPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "list failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "ls")
	require.Error(t, err)
	require.Contains(t, err.Error(), "list webhook endpoints")
}

// --- webhook show -------------------------------------------------------------------

func TestNotifEndpointWebhookShow_RendersSingle(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+notifWebhookPath+"/webhook-a", map[string]any{
		"name": "webhook-a", "method": "post", "url": "https://a.example.com/hook", "comment": "primary",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "show", "webhook-a")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "https://a.example.com/hook")
	require.Contains(t, out, "primary")
}

// TestNotifEndpointWebhookShow_DefaultsTable verifies --defaults lists an
// unset option. "method" has a schema default ("post") but the API always
// returns it (it is a required response field), so it is never actually
// unset; "comment" has no schema default at all and renders "(unset)".
func TestNotifEndpointWebhookShow_DefaultsTable(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+notifWebhookPath+"/webhook-a", map[string]any{
		"name": "webhook-a", "url": "https://a.example.com/hook", "method": "put",
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "show", "webhook-a", "--defaults")
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "https://a.example.com/hook")
	require.Contains(t, out, "(unset)", "comment has no schema default")
}

// TestNotifEndpointWebhookShow_DefaultsJSON verifies the JSON set/defaults
// shape and that write-only secret values are never resurrected as an
// "unset" default: "secret" is excluded from the schema table entirely.
func TestNotifEndpointWebhookShow_DefaultsJSON(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleJSON("GET "+notifWebhookPath+"/webhook-a", map[string]any{
		"name": "webhook-a", "url": "https://a.example.com/hook", "method": "put",
	})

	deps := depsFor(t, pc, output.FormatJSON, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "show", "webhook-a", "--defaults")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"set"`)
	require.Contains(t, buf.String(), `"defaults"`)

	var got struct {
		Set      map[string]any    `json:"set"`
		Defaults map[string]string `json:"defaults"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "put", got.Set["method"], "method is always present in the response, never merged from schema")
	require.NotContains(t, got.Defaults, "secret", "secret must not appear even as an unset default")
}

func TestNotifEndpointWebhookShow_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "show", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointWebhookShow_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+notifWebhookPath+"/webhook-a", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusNotFound, "no such endpoint")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "show", "webhook-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "show webhook endpoint")
}

// --- webhook add -------------------------------------------------------------------

func TestNotifEndpointWebhookAdd_CreatesEndpoint(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+notifWebhookPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "add", "webhook-a",
		"--method", "post", "--url", "https://a.example.com/hook")
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, rec.method)
	require.Equal(t, notifWebhookPath, rec.path)
	require.Equal(t, "webhook-a", rec.form.Get("name"))
	require.Equal(t, "post", rec.form.Get("method"))
	require.Equal(t, "https://a.example.com/hook", rec.form.Get("url"))
	require.Contains(t, buf.String(), `Webhook endpoint "webhook-a" created.`)
}

func TestNotifEndpointWebhookAdd_RequiresMethod(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "add", "webhook-a",
		"--url", "https://a.example.com/hook")
	require.Error(t, err)
}

func TestNotifEndpointWebhookAdd_RequiresURL(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "add", "webhook-a", "--method", "post")
	require.Error(t, err)
}

func TestNotifEndpointWebhookAdd_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "add", "",
		"--method", "post", "--url", "https://a.example.com/hook")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointWebhookAdd_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("POST "+notifWebhookPath, func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusBadRequest, "invalid endpoint")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "add", "webhook-a",
		"--method", "post", "--url", "https://a.example.com/hook")
	require.Error(t, err)
	require.Contains(t, err.Error(), "create webhook endpoint")
}

// TestNotifEndpointWebhookAdd_AuditAllFlags asserts every field of
// CreateNotificationsEndpointsWebhookParams is settable and forwarded by a
// flag, including repeated form entries for --header and --secret.
func TestNotifEndpointWebhookAdd_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+notifWebhookPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "add", "audit-webhook",
		"--method", "post",
		"--url", "https://a.example.com/hook",
		"--body", "{\"text\":\"{{ message }}\"}",
		"--comment", "audit comment",
		"--disable",
		"--header", "name=Content-Type,value=YXBwbGljYXRpb24vanNvbg==",
		"--header", "name=X-Custom,value=Zm9v",
		"--origin", "user-created",
		"--secret", "name=api-key,value=c2VjcmV0",
	)
	require.NoError(t, err)

	want := map[string]string{
		"name":    "audit-webhook",
		"method":  "post",
		"url":     "https://a.example.com/hook",
		"body":    "{\"text\":\"{{ message }}\"}",
		"comment": "audit comment",
		"disable": "1",
		"origin":  "user-created",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.Equal(t, []string{
		"name=Content-Type,value=YXBwbGljYXRpb24vanNvbg==",
		"name=X-Custom,value=Zm9v",
	}, rec.form["header"])
	require.Equal(t, []string{"name=api-key,value=c2VjcmV0"}, rec.form["secret"])
}

// --- webhook update -------------------------------------------------------------------

func TestNotifEndpointWebhookUpdate_UpdatesEndpoint(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifWebhookPath+"/webhook-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "update", "webhook-a",
		"--url", "https://new.example.com/hook", "--digest", "abc123", "--delete", "comment")
	require.NoError(t, err)

	require.Equal(t, http.MethodPut, rec.method)
	require.Equal(t, notifWebhookPath+"/webhook-a", rec.path)
	require.Equal(t, "https://new.example.com/hook", rec.form.Get("url"))
	require.Equal(t, "abc123", rec.form.Get("digest"))
	require.Equal(t, []string{"comment"}, rec.form["delete"])
	require.Contains(t, buf.String(), `Webhook endpoint "webhook-a" updated.`)
}

// TestNotifEndpointWebhookUpdate_AuditAllFlags asserts every field of
// UpdateNotificationsEndpointsWebhookParams is settable and forwarded by a
// flag, including repeated form entries for --header and --secret.
func TestNotifEndpointWebhookUpdate_AuditAllFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifWebhookPath+"/webhook-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "update", "webhook-a",
		"--method", "get",
		"--url", "https://new.example.com/hook",
		"--body", "new-body",
		"--comment", "updated comment",
		"--disable",
		"--header", "name=X-New,value=bmV3",
		"--secret", "name=api-key",
		"--delete", "body",
		"--digest", "abc123",
	)
	require.NoError(t, err)

	want := map[string]string{
		"method":  "get",
		"url":     "https://new.example.com/hook",
		"body":    "new-body",
		"comment": "updated comment",
		"disable": "1",
		"digest":  "abc123",
	}
	for key, val := range want {
		require.Equal(t, val, rec.form.Get(key), "body key %q", key)
	}
	require.Equal(t, []string{"name=X-New,value=bmV3"}, rec.form["header"])
	require.Equal(t, []string{"name=api-key"}, rec.form["secret"])
	require.Equal(t, []string{"body"}, rec.form["delete"])
}

func TestNotifEndpointWebhookUpdate_OmitsUnsetFlags(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifWebhookPath+"/webhook-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "update", "webhook-a", "--comment", "only comment")
	require.NoError(t, err)
	require.Equal(t, "only comment", rec.form.Get("comment"))

	for _, key := range []string{"method", "url", "body", "header", "secret", "disable", "delete", "digest"} {
		_, present := rec.form[key]
		require.False(t, present, "%s must be omitted from the body when unset", key)
	}
}

func TestNotifEndpointWebhookUpdate_RequiresAtLeastOneFlag(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "update", "webhook-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes given")
}

func TestNotifEndpointWebhookUpdate_RejectsEmptyDeleteEntry(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "update", "webhook-a", "--delete", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--delete")
}

func TestNotifEndpointWebhookUpdate_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "update", "", "--comment", "x")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointWebhookUpdate_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("PUT "+notifWebhookPath+"/webhook-a", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "update failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "update", "webhook-a", "--comment", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "update webhook endpoint")
}

// --- webhook delete -------------------------------------------------------------------

func TestNotifEndpointWebhookDelete_RequiresYes(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+notifWebhookPath+"/webhook-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "delete", "webhook-a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "without confirmation")
	require.Empty(t, rec.method, "no request must be issued without --yes")
}

func TestNotifEndpointWebhookDelete_DeletesEndpoint(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "DELETE "+notifWebhookPath+"/webhook-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "delete", "webhook-a", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, notifWebhookPath+"/webhook-a", rec.path)
	require.Contains(t, buf.String(), `Webhook endpoint "webhook-a" deleted.`)
}

func TestNotifEndpointWebhookDelete_EmptyNameRejected(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "delete", "")
	require.Error(t, err)
	require.ErrorContains(t, err, "must not be empty")
}

func TestNotifEndpointWebhookDelete_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("DELETE "+notifWebhookPath+"/webhook-a", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "delete failed")
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifEndpointWebhookCmd(), "webhook", "delete", "webhook-a", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete webhook endpoint")
}
