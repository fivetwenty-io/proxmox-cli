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

func TestNotifications_Targets(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/cluster/notifications/targets", []any{
		map[string]any{"name": "mail-to-root", "type": "sendmail", "origin": "builtin"},
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "notifications", "targets"))
	out := buf.String()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "ORIGIN")
	require.Contains(t, out, "mail-to-root")
}

func TestGotify_List(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleJSON("GET /api2/json/cluster/notifications/endpoints/gotify", []any{
		map[string]any{"name": "g1", "server": "https://gotify.example", "disable": 0},
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "notifications", "gotify", "list"))
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
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "notifications", "gotify", "get", "g1"))
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
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "notifications", "gotify", "create", "g1",
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
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "notifications", "gotify", "set", "g1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes to set")
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
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "notifications", "gotify", "delete", "g1")
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
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "notifications", "gotify", "delete", "g1", "--yes"))
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
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "notifications", "smtp", "create", "s1",
		"--server", "smtp.example", "--from-address", "noc@example.com",
		"--username", "noc", "--password", secret, "--mailto", "root@example.com"))
	require.Equal(t, "smtp.example", gotForm.Get("server"))
	require.Equal(t, secret, gotForm.Get("password"), "password must reach the API")
	require.NotContains(t, buf.String(), secret, "password must never be echoed")
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
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "notifications", "webhook", "create", "w1",
		"--url", "https://hook.example", "--method", "post", "--secret", secret))
	require.Equal(t, "https://hook.example", gotForm.Get("url"))
	require.Equal(t, secret, gotForm.Get("secret"), "secret must reach the API")
	require.NotContains(t, buf.String(), "Zm9v", "secret value must never be echoed")
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
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "notifications", "sendmail", "create", "m1",
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
	defer withDeps(deps)()

	var buf bytes.Buffer
	require.NoError(t, run(&buf, "notifications", "matcher", "create", "match1",
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
	defer withDeps(deps)()

	var buf bytes.Buffer
	err := run(&buf, "notifications", "matcher", "set", "match1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no changes to set")
	require.False(t, called)
}

func TestNotificationsCommandTree(t *testing.T) {
	root := newClusterCmd(&cli.Deps{})
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
