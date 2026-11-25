package pbs

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

const testMatcherExpression = `{"type":"severity","value":"error"}`

func TestNotifMatcherAdd_ForwardsExpression(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+notifMatchersPath, &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "add", "expr-matcher",
		"--expression", testMatcherExpression, "--target", "target1")
	require.NoError(t, err)

	require.Equal(t, testMatcherExpression, rec.form.Get("expression"))
	require.False(t, rec.form.Has("mode"))
}

func TestNotifMatcherAdd_ExpressionConflictsRejected(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "POST "+notifMatchersPath, &rec, nil)

	for _, extra := range [][]string{
		{"--match-field", "type=gc"}, {"--match-calendar", "daily"}, {"--match-severity", "error"},
		{"--invert-match"}, {"--mode", "any"},
	} {
		deps := depsFor(t, pc, output.FormatTable, false)
		var buf bytes.Buffer
		args := append([]string{"matcher", "add", "expr-matcher", "--expression", testMatcherExpression}, extra...)
		err := run(deps, &buf, newNotifMatcherCmd(), args...)
		require.Error(t, err, "%v", extra)
		require.Contains(t, err.Error(), "--expression cannot be combined with "+extra[0])
	}
	require.Empty(t, rec.method, "a rejected combination must not reach the server")
}

func TestNotifMatcherUpdate_ForwardsExpression(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifMatchersPath+"/matcher-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "update", "matcher-a",
		"--expression", testMatcherExpression)
	require.NoError(t, err)
	require.Equal(t, testMatcherExpression, rec.form.Get("expression"))
}

func TestNotifMatcherUpdate_ExpressionConflictsRejected(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "PUT "+notifMatchersPath+"/matcher-a", &rec, nil)

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "update", "matcher-a",
		"--expression", testMatcherExpression, "--mode", "all", "--invert-match")
	require.EqualError(t, err, "--expression cannot be combined with --invert-match, --mode")
	require.Empty(t, rec.method)
}

func TestNotifMatcherShow_MigrateToExpressionQuery(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+notifMatchersPath+"/matcher-a", &rec, map[string]any{
		"name": "matcher-a", "expression": testMatcherExpression,
	})

	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNotifMatcherCmd(), "matcher", "show", "matcher-a", "--migrate-to-expression")
	require.NoError(t, err)
	require.Equal(t, "1", rec.query.Get("migrate-to-expression"))
	require.Contains(t, buf.String(), "expression")

	rec = recordedRequest{}
	buf.Reset()
	err = run(deps, &buf, newNotifMatcherCmd(), "matcher", "show", "matcher-a")
	require.NoError(t, err)
	require.False(t, rec.query.Has("migrate-to-expression"), "an unset flag must not reach the query")
}
