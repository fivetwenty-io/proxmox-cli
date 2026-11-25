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

const testPVEMatcherExpression = `{"type":"severity","value":"error"}`

func TestMatcher_CreateForwardsExpression(t *testing.T) {
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
		"--expression", testPVEMatcherExpression, "--notify-target", "mail-to-root"))
	require.Equal(t, testPVEMatcherExpression, gotForm.Get("expression"))
	require.NotContains(t, gotForm, "mode")
	require.NotContains(t, gotForm, "match-severity")
}

func TestMatcher_CreateExpressionConflictsRejected(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("POST /api2/json/cluster/notifications/matchers", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	for _, extra := range [][]string{
		{"--match-field", "exact:type=gc"}, {"--match-calendar", "daily"}, {"--match-severity", "error"},
		{"--invert-match"}, {"--mode", "any"},
	} {
		var buf bytes.Buffer
		args := append([]string{"notifications", "matcher", "create", "match1",
			"--expression", testPVEMatcherExpression}, extra...)
		err := run(deps, &buf, args...)
		require.Error(t, err, "%v", extra)
		require.Contains(t, err.Error(), "--expression cannot be combined with "+extra[0])
	}
	require.False(t, called, "a rejected combination must not reach the server")
}

func TestMatcher_SetForwardsExpression(t *testing.T) {
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
		"--expression", testPVEMatcherExpression))
	require.Equal(t, testPVEMatcherExpression, gotForm.Get("expression"))
}

func TestMatcher_SetExpressionConflictsRejected(t *testing.T) {
	f, ac := newFakeClient(t)
	called := false
	f.HandleFunc("PUT /api2/json/cluster/notifications/matchers/match1", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatPlain}

	var buf bytes.Buffer
	err := run(deps, &buf, "notifications", "matcher", "set", "match1",
		"--expression", testPVEMatcherExpression, "--mode", "all", "--invert-match")
	require.EqualError(t, err, "--expression cannot be combined with --invert-match, --mode")
	require.False(t, called)
}

func TestMatcher_GetMigrateToExpressionQuery(t *testing.T) {
	f, ac := newFakeClient(t)
	var gotQuery url.Values
	f.HandleFunc("GET /api2/json/cluster/notifications/matchers/match1", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		testhelper.WriteData(w, map[string]any{"name": "match1", "expression": testPVEMatcherExpression})
	})
	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "notifications", "matcher", "get", "match1", "--migrate-to-expression"))
	require.Equal(t, "1", gotQuery.Get("migrate-to-expression"))
	require.Contains(t, buf.String(), "expression")

	gotQuery = nil
	buf.Reset()
	require.NoError(t, run(deps, &buf, "notifications", "matcher", "get", "match1"))
	require.False(t, gotQuery.Has("migrate-to-expression"), "an unset flag must not reach the query")
}
