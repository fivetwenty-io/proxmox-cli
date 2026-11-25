package node_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

func accessLogPage(next string) map[string]any {
	page := map[string]any{
		"complete": 1, "invalid-lines": 0, "gaps": []any{},
		"entries": []any{
			map[string]any{
				"time": 1790000000, "client": "10.0.0.5", "auth-id": "root@pam", "method": "GET",
				"uri": "/api2/json/nodes", "status": 200, "size": 512,
			},
		},
	}
	if next != "" {
		page["next-cursor"] = next
	}
	return page
}

// TestNodeAccessLog_List verifies the list reads the access log, forwards every
// filter under the API's own name, and renders in table, json, and yaml.
func TestNodeAccessLog_List(t *testing.T) {
	for _, format := range sanFormats {
		t.Run(string(format), func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec recordedRequest
			recordForm(f, "GET /api2/json/nodes/pve1/access-log", &rec, accessLogPage(""))

			root, buf, prefix := newNodeRoot(t, f, format, exec.Fake())
			root.SetArgs(append(prefix, "--node", "pve1", "node", "access-log",
				"--limit", "20", "--client", "10.0.0.5", "--auth-id", "root@pam", "--auth-id-exact",
				"--method", "GET", "--path", "/nodes", "--path-group", "nodes", "--search", "root",
				"--min-status", "200", "--max-status", "299", "--since", "1790000000",
				"--until", "2026-11-01T00:00:00Z", "--cursor", "abc"))

			require.NoError(t, root.Execute())
			require.Equal(t, http.MethodGet, rec.method)
			require.Equal(t, "/api2/json/nodes/pve1/access-log", rec.path)
			for _, want := range []string{
				"limit=20", "client=10.0.0.5", "auth-id=root%40pam", "auth-id-exact=1", "method=GET",
				"path=%2Fnodes", "path-group=nodes", "search=root", "min-status=200", "max-status=299",
				"since=1790000000", "until=1793491200", "cursor=abc",
			} {
				require.Contains(t, rec.query, want)
			}
			require.Contains(t, buf.String(), "10.0.0.5")
			require.Contains(t, buf.String(), "/api2/json/nodes")
		})
	}
}

// TestNodeAccessLog_TableShowsTimeAndNextCursor verifies the table renders the
// time in UTC and prints the continuation cursor, and the json output does not
// add the hint.
func TestNodeAccessLog_TableShowsTimeAndNextCursor(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "GET /api2/json/nodes/pve1/access-log", &rec, accessLogPage("cursor-2"))

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	var stderr bytes.Buffer
	root.SetErr(&stderr)
	root.SetArgs(append(prefix, "--node", "pve1", "node", "access-log"))
	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), "2026-09-21T")
	require.NotContains(t, buf.String(), "--cursor", "the hint must stay off stdout")
	require.Contains(t, stderr.String(), "--cursor cursor-2")
	require.NotContains(t, buf.String(), "USER-AGENT", "PVE does not log a user agent")

	root, buf, prefix = newNodeRoot(t, f, output.FormatJSON, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "access-log"))
	require.NoError(t, root.Execute())
	require.Contains(t, buf.String(), `"next-cursor": "cursor-2"`)
	require.NotContains(t, buf.String(), "More requests are available")
}

// TestNodeAccessLog_Refusals verifies bad input fails before any request.
func TestNodeAccessLog_Refusals(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"after and cursor", []string{"--node", "pve1", "node", "access-log", "--after", "a", "--cursor", "b"}, "cannot be combined"},
		{"bad since", []string{"--node", "pve1", "node", "access-log", "--since", "yesterday"}, "invalid --since"},
		{"no node", []string{"node", "access-log"}, "no node specified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
			root.SetArgs(append(prefix, tc.args...))
			err := root.Execute()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestNodeAccessLog_WrapsServerError verifies a refusal names the node.
func TestNodeAccessLog_WrapsServerError(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("GET /api2/json/nodes/pve1/access-log", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "Permission check failed")
	})
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "access-log"))
	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), `read the access log of node "pve1"`)
}

// TestNodeAccessLogStats verifies stats forwards its filters and renders the
// totals and the leading values of each ranking in every format.
func TestNodeAccessLogStats(t *testing.T) {
	for _, format := range sanFormats {
		t.Run(string(format), func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec recordedRequest
			recordForm(f, "GET /api2/json/nodes/pve1/access-log/stats", &rec, map[string]any{
				"since": 1790000000, "until": 1790003600, "complete": 1, "invalid-lines": 0,
				"total":       map[string]any{"requests": 42, "client-errors": 3, "server-errors": 1, "unauthenticated": 2},
				"clients":     map[string]any{"rows": []any{map[string]any{"key": "10.0.0.5", "requests": 40}}},
				"auth-ids":    map[string]any{"rows": []any{map[string]any{"key": "root@pam", "requests": 41}}},
				"statuses":    map[string]any{"rows": []any{map[string]any{"key": "200", "requests": 38}}},
				"path-groups": map[string]any{"rows": []any{map[string]any{"key": "/nodes", "requests": 30}}},
				"histogram":   map[string]any{"bucket": 60},
				"covered":     []any{}, "gaps": []any{},
			})

			root, buf, prefix := newNodeRoot(t, f, format, exec.Fake())
			root.SetArgs(append(prefix, "--node", "pve1", "node", "access-log", "stats",
				"--top", "5", "--bucket", "60", "--client", "10.0.0.5", "--since", "1790000000"))

			require.NoError(t, root.Execute())
			require.Equal(t, "/api2/json/nodes/pve1/access-log/stats", rec.path)
			for _, want := range []string{"top=5", "bucket=60", "client=10.0.0.5", "since=1790000000"} {
				require.Contains(t, rec.query, want)
			}
			require.NotContains(t, rec.query, "limit", "stats has no --limit")
			require.Contains(t, buf.String(), "10.0.0.5")
			require.Contains(t, buf.String(), "42")
		})
	}
}

// TestNodeAccessLogStats_TableSummary verifies the key/value summary text.
func TestNodeAccessLogStats_TableSummary(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "GET /api2/json/nodes/pve1/access-log/stats", &rec, map[string]any{
		"since": 1790000000, "until": 1790003600, "complete": 0, "invalid-lines": 2, "stopped": "damaged copy",
		"total":   map[string]any{"requests": 42},
		"clients": map[string]any{"rows": []any{map[string]any{"key": "10.0.0.5", "requests": 40}}},
	})
	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "access-log", "stats"))
	require.NoError(t, root.Execute())
	for _, want := range []string{"total-requests", "10.0.0.5 (40)", "stopped", "damaged copy", "2026-09-21T"} {
		require.Contains(t, buf.String(), want)
	}
}

// TestNodeAccessLog_ForwardsAfter verifies --after reaches the after query key.
func TestNodeAccessLog_ForwardsAfter(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	recordForm(f, "GET /api2/json/nodes/pve1/access-log", &rec, accessLogPage(""))
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "access-log", "--after", "head-1"))
	require.NoError(t, root.Execute())
	require.Contains(t, rec.query, "after=head-1")
	require.NotContains(t, rec.query, "cursor=")
}

// TestNodeAccessLog_PrintsHeadCursorAndWarnings verifies the table output names
// the head cursor on stderr and warns about an incomplete page or gaps.
func TestNodeAccessLog_PrintsHeadCursorAndWarnings(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	page := accessLogPage("")
	page["head-cursor"] = "head-9"
	page["complete"] = 0
	page["gaps"] = []any{map[string]any{"from": 1, "to": 2}}
	var rec recordedRequest
	recordForm(f, "GET /api2/json/nodes/pve1/access-log", &rec, page)

	root, buf, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	var stderr bytes.Buffer
	root.SetErr(&stderr)
	root.SetArgs(append(prefix, "--node", "pve1", "node", "access-log"))
	require.NoError(t, root.Execute())
	require.NotContains(t, buf.String(), "head-9")
	require.Contains(t, stderr.String(), "--after head-9")
	require.Contains(t, stderr.String(), "skipped 1 unreadable")
	require.Contains(t, stderr.String(), "do not cover the whole log")
}
