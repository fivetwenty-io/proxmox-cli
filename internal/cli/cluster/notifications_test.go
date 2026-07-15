package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func TestNotifications_Targets(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/cluster/notifications/targets", []any{
		map[string]any{"name": "mail-to-root", "type": "sendmail", "origin": "builtin"},
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "targets"))
	out := buf.String()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "ORIGIN")
	require.Contains(t, out, "mail-to-root")
}

// TestNotifications_EndpointsMergesTypes verifies `notifications endpoints`
// aggregates the four typed endpoint lists (GET /cluster/notifications/endpoints
// is only a directory index of the types) and labels each row with its type.
func TestNotifications_EndpointsMergesTypes(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/cluster/notifications/endpoints/sendmail", []any{
		map[string]any{"name": "mail-to-root", "mailto-user": "root@pam"},
	})
	f.HandleJSON("GET /api2/json/cluster/notifications/endpoints/gotify", []any{
		map[string]any{"name": "g1", "server": "https://gotify.example"},
	})
	f.HandleJSON("GET /api2/json/cluster/notifications/endpoints/smtp", []any{})
	f.HandleJSON("GET /api2/json/cluster/notifications/endpoints/webhook", []any{
		map[string]any{"name": "w1", "url": "https://hooks.example"},
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "endpoints"))
	out := buf.String()
	require.Contains(t, out, "TYPE")
	require.Contains(t, out, "mail-to-root")
	require.Contains(t, out, "sendmail")
	require.Contains(t, out, "g1")
	require.Contains(t, out, "gotify")
	require.Contains(t, out, "w1")
	require.Contains(t, out, "webhook")
}

// TestNotifications_EndpointsTypeError verifies a failure fetching one typed
// list surfaces with the type name.
func TestNotifications_EndpointsTypeError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/cluster/notifications/endpoints/sendmail", []any{})
	f.HandleFunc("GET /api2/json/cluster/notifications/endpoints/gotify",
		func(w http.ResponseWriter, _ *http.Request) {
			testhelper.WriteError(w, http.StatusInternalServerError, "boom")
		})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "notifications", "endpoints")
	require.Error(t, err)
	require.Contains(t, err.Error(), "gotify")
}

func TestGotify_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/cluster/notifications/endpoints/gotify", []any{
		map[string]any{"name": "g1", "server": "https://gotify.example", "disable": 0},
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "gotify", "list"))
	require.Contains(t, buf.String(), "g1")
}

func TestGotify_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/notifications/endpoints/gotify/g1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{"name": "g1", "server": "https://gotify.example"})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "gotify", "get", "g1"))
	require.Equal(t, "/api2/json/cluster/notifications/endpoints/gotify/g1", gotPath)
	require.Contains(t, buf.String(), "gotify.example")
}

// TestGotify_CreateTokenNotEchoed verifies the Gotify secret token reaches the
// API but is never written to command output.
func TestGotify_CreateTokenNotEchoed(t *testing.T) {
	f, ac := newFakeClient(t)
	const secret = "AzdfGotifyTok"
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/notifications/endpoints/gotify", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "gotify", "create", "g1",
		"--server", "https://gotify.example", "--token", secret, "--comment", "test"))
	require.Equal(t, "g1", gotForm.Get("name"))
	require.Equal(t, secret, gotForm.Get("token"), "token must reach the API")
	require.NotContains(t, buf.String(), secret, "token must never be echoed to output")
}

func TestGotify_SetRequiresFlag(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/cluster/notifications/endpoints/gotify/g1", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "notifications", "gotify", "set", "g1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
	require.False(t, called)
}

func TestGotify_DeleteRequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("DELETE /api2/json/cluster/notifications/endpoints/gotify/g1", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "notifications", "gotify", "delete", "g1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called)
}

func TestGotify_DeleteWithYes(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotMethod string
	f.HandleFunc("DELETE /api2/json/cluster/notifications/endpoints/gotify/g1", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "gotify", "delete", "g1", "--yes"))
	require.Equal(t, http.MethodDelete, gotMethod)
	require.Contains(t, buf.String(), "deleted")
}

// TestSMTP_CreatePasswordNotEchoed verifies the SMTP password reaches the API
// but is never echoed.
func TestSMTP_CreatePasswordNotEchoed(t *testing.T) {
	f, ac := newFakeClient(t)
	const secret = "smtpPassw0rd"
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/notifications/endpoints/smtp", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "smtp", "create", "s1",
		"--server", "smtp.example", "--from-address", "noc@example.com",
		"--username", "noc", "--password", secret, "--mailto", "root@example.com"))
	require.Equal(t, "smtp.example", gotForm.Get("server"))
	require.Equal(t, secret, gotForm.Get("password"), "password must reach the API")
	require.NotContains(t, buf.String(), secret, "password must never be echoed")
	require.NotContains(t, gotForm, "port", "unset --port must be omitted from the request body")
}

// TestWebhook_CreateSecretNotEchoed verifies webhook secret property strings
// reach the API but are never echoed.
func TestWebhook_CreateSecretNotEchoed(t *testing.T) {
	f, ac := newFakeClient(t)
	const secret = "name=apikey,value=Zm9v"
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/notifications/endpoints/webhook", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "webhook", "create", "w1",
		"--url", "https://hook.example", "--method", "post", "--secret", secret))
	require.Equal(t, "https://hook.example", gotForm.Get("url"))
	require.Equal(t, secret, gotForm.Get("secret"), "secret must reach the API")
	require.NotContains(t, buf.String(), "Zm9v", "secret value must never be echoed")
	require.NotContains(t, gotForm, "body", "unset --body must be omitted from the request body")
}

func TestSendmail_CreateForwardsArrays(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/notifications/endpoints/sendmail", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "sendmail", "create", "m1",
		"--mailto", "a@example.com", "--mailto", "b@example.com", "--comment", "ops"))
	require.Equal(t, "m1", gotForm.Get("name"))
	require.Contains(t, gotForm["mailto"], "a@example.com")
	require.Contains(t, gotForm["mailto"], "b@example.com")
	// Unset optionals omitted.
	require.NotContains(t, gotForm, "author")
}

func TestMatcher_CreateForwardsFields(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/notifications/matchers", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "matcher", "create", "match1",
		"--match-severity", "warning", "--match-severity", "error",
		"--notify-target", "mail-to-root", "--mode", "any"))
	require.Equal(t, "match1", gotForm.Get("name"))
	require.Contains(t, gotForm["match-severity"], "warning")
	require.Contains(t, gotForm["match-severity"], "error")
	require.Equal(t, "mail-to-root", gotForm.Get("target"))
	require.Equal(t, "any", gotForm.Get("mode"))
}

func TestMatcher_SetRequiresFlag(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/cluster/notifications/matchers/match1", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "notifications", "matcher", "set", "match1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes requested")
	require.False(t, called)
}

// --- set field-forwarding (PUT body) ----------------------------------------

func TestSMTP_SetForwardsChangedOmitsUnset(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	var gotMethod string
	f.HandleFunc("PUT /api2/json/cluster/notifications/endpoints/smtp/s1", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "smtp", "set", "s1", "--server", "smtp2.example", "--disable"))
	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "smtp2.example", gotForm.Get("server"))
	require.Equal(t, "1", gotForm.Get("disable"))
	require.NotContains(t, gotForm, "port", "unset optionals must be omitted from the PUT body")
}

func TestSendmail_SetForwardsChanged(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/notifications/endpoints/sendmail/m1", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "sendmail", "set", "m1", "--author", "ops", "--comment", "c"))
	require.Equal(t, "ops", gotForm.Get("author"))
	require.Equal(t, "c", gotForm.Get("comment"))
	require.NotContains(t, gotForm, "from-address")
}

func TestWebhook_SetForwardsChanged(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/notifications/endpoints/webhook/w1", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "webhook", "set", "w1", "--method", "put", "--disable"))
	require.Equal(t, "put", gotForm.Get("method"))
	require.Equal(t, "1", gotForm.Get("disable"))
	require.NotContains(t, gotForm, "url")
}

func TestMatcher_SetForwardsChanged(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/notifications/matchers/match1", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "matcher", "set", "match1",
		"--mode", "all", "--notify-target", "mail-to-root"))
	require.Equal(t, "all", gotForm.Get("mode"))
	require.Equal(t, "mail-to-root", gotForm.Get("target"))
	require.NotContains(t, gotForm, "comment")
}

// --- get render path (non-gotify types share renderEndpointGet) --------------

func TestSMTP_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/notifications/endpoints/smtp/s1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		// PVE never returns the password on GET; renderEndpointGet must surface
		// only the non-secret config fields.
		testhelper.WriteData(w, map[string]any{
			"name": "s1", "server": "smtp.example", "from-address": "noc@example.com", "username": "noc",
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "smtp", "get", "s1"))
	require.Equal(t, "/api2/json/cluster/notifications/endpoints/smtp/s1", gotPath)
	out := buf.String()
	require.Contains(t, out, "smtp.example")
	require.NotContains(t, out, "password", "GET render must not surface a password field")
}

func TestWebhook_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/notifications/endpoints/webhook/w1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		// PVE returns webhook secrets as masked name entries (never the value);
		// the get render path must handle the secret-names field without error.
		testhelper.WriteData(w, map[string]any{
			"name": "w1", "url": "https://hook.example", "method": "post",
			"secret": []any{"name=apikey"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "webhook", "get", "w1"))
	require.Equal(t, "/api2/json/cluster/notifications/endpoints/webhook/w1", gotPath)
	require.Contains(t, buf.String(), "hook.example")
}

func TestMatcher_Get(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/notifications/matchers/match1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"name": "match1", "mode": "any", "target": []any{"mail-to-root"},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "matcher", "get", "match1"))
	require.Equal(t, "/api2/json/cluster/notifications/matchers/match1", gotPath)
	require.Contains(t, buf.String(), "mail-to-root")
}

func TestNotificationsCommandTree(t *testing.T) {
	root := Group(&cli.Deps{})
	notif := childCommands(root)["notifications"]
	require.NotNil(t, notif)
	nc := childCommands(notif)
	for _, sub := range []string{"targets", "endpoints", "gotify", "sendmail", "smtp", "webhook", "matcher"} {
		require.NotNil(t, nc[sub], "notifications must expose %q", sub)
	}
	for _, typ := range []string{"gotify", "sendmail", "smtp", "webhook", "matcher"} {
		tc := childCommands(nc[typ])
		for _, v := range []string{"list", "get", "create", "set", "delete"} {
			require.NotNil(t, tc[v], "%s must expose %q", typ, v)
		}
	}
}
