package pbs

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

var accessLogFormats = []output.Format{output.FormatTable, output.FormatJSON, output.FormatYAML}

func pbsAccessLogPage(next string) map[string]any {
	page := map[string]any{
		"complete": 1, "invalid-lines": 0, "gaps": []any{},
		"entries": []any{
			map[string]any{
				"time": 1790000000, "client": "10.0.0.9", "auth-id": "backup@pbs", "method": "POST",
				"uri": "/api2/json/backup", "status": 200, "size": 64, "user-agent": "proxmox-backup-client/4.0",
			},
		},
	}
	if next != "" {
		page["next-cursor"] = next
	}
	return page
}

// TestNodeAccessLog_List verifies the list forwards every filter, including the
// PBS-only user agent, and renders in table, json, and yaml.
func TestNodeAccessLog_List(t *testing.T) {
	for _, format := range accessLogFormats {
		t.Run(string(format), func(t *testing.T) {
			f, pc := newFakeClient(t)
			var rec recordedRequest
			recordJSON(f, "GET "+nodeAPIBase+"/access-log", &rec, pbsAccessLogPage(""))

			deps := depsFor(t, pc, format, false)
			var buf bytes.Buffer
			err := run(deps, &buf, newNodeCmd(), "node", "access-log",
				"--limit", "5", "--user-agent", "proxmox-backup-client", "--client", "10.0.0.9",
				"--method", "POST", "--min-status", "200", "--after", "head1", "--since", "1790000000")
			require.NoError(t, err)

			require.Equal(t, http.MethodGet, rec.method)
			require.Equal(t, nodeAPIBase+"/access-log", rec.path)
			require.Equal(t, "5", rec.query.Get("limit"))
			require.Equal(t, "proxmox-backup-client", rec.query.Get("user-agent"))
			require.Equal(t, "10.0.0.9", rec.query.Get("client"))
			require.Equal(t, "POST", rec.query.Get("method"))
			require.Equal(t, "200", rec.query.Get("min-status"))
			require.Equal(t, "head1", rec.query.Get("after"))
			require.Equal(t, "1790000000", rec.query.Get("since"))
			require.Contains(t, buf.String(), "proxmox-backup-client/4.0")
		})
	}
}

// TestNodeAccessLog_ShowsUserAgentColumnAndCursor verifies the table gains the
// user agent column and prints the continuation hint.
func TestNodeAccessLog_ShowsUserAgentColumnAndCursor(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/access-log", &rec, pbsAccessLogPage("cursor-2"))

	deps := depsFor(t, pc, output.FormatTable, false)
	stdout, stderr, err := runSplit(deps, newNodeCmd(), "node", "access-log")
	require.NoError(t, err)
	require.Contains(t, stdout, "USER-AGENT")
	require.NotContains(t, stdout, "--cursor", "the hint must stay off stdout")
	require.Contains(t, stderr, "--cursor cursor-2")
}

// runSplit runs sub with separate stdout and stderr buffers.
func runSplit(deps *cli.Deps, sub *cobra.Command, args ...string) (string, string, error) {
	var out, errBuf bytes.Buffer
	cmd := &cobra.Command{Use: "pbs"}
	cmd.AddCommand(sub)
	cmd.SetContext(cli.WithDeps(context.Background(), deps))
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errBuf.String(), err
}

// TestNodeAccessLog_ForwardsAfter verifies --after reaches the after query key.
func TestNodeAccessLog_ForwardsAfter(t *testing.T) {
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/access-log", &rec, pbsAccessLogPage(""))
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, newNodeCmd(), "node", "access-log", "--after", "head-1"))
	require.Equal(t, "head-1", rec.query.Get("after"))
	require.False(t, rec.query.Has("cursor"))
}

// TestNodeAccessLog_PrintsHeadCursorAndWarnings verifies the table output names
// the head cursor on stderr and warns about an incomplete page or gaps.
func TestNodeAccessLog_PrintsHeadCursorAndWarnings(t *testing.T) {
	f, pc := newFakeClient(t)
	page := pbsAccessLogPage("")
	page["head-cursor"] = "head-9"
	page["complete"] = 0
	page["gaps"] = []any{map[string]any{"from": 1, "to": 2}}
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/access-log", &rec, page)
	deps := depsFor(t, pc, output.FormatTable, false)
	stdout, stderr, err := runSplit(deps, newNodeCmd(), "node", "access-log")
	require.NoError(t, err)
	require.NotContains(t, stdout, "head-9")
	require.Contains(t, stderr, "--after head-9")
	require.Contains(t, stderr, "skipped 1 unreadable")
	require.Contains(t, stderr, "do not cover the whole log")
}

// TestNodeAccessLog_RejectsAfterWithCursor verifies the conflict is caught
// before any request.
func TestNodeAccessLog_RejectsAfterWithCursor(t *testing.T) {
	_, pc := newFakeClient(t)
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "access-log", "--after", "a", "--cursor", "b")
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be combined")
}

// TestNodeAccessLog_SurfacesAPIError verifies a refusal names the node.
func TestNodeAccessLog_SurfacesAPIError(t *testing.T) {
	f, pc := newFakeClient(t)
	f.HandleFunc("GET "+nodeAPIBase+"/access-log", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusForbidden, "denied")
	})
	deps := depsFor(t, pc, output.FormatTable, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), "node", "access-log")
	require.Error(t, err)
	require.Contains(t, err.Error(), "read the access log of node")
}

// TestNodeAccessLogStats verifies stats forwards its filters and renders the
// user agent ranking next to the others in every format.
func TestNodeAccessLogStats(t *testing.T) {
	for _, format := range accessLogFormats {
		t.Run(string(format), func(t *testing.T) {
			f, pc := newFakeClient(t)
			var rec recordedRequest
			recordJSON(f, "GET "+nodeAPIBase+"/access-log/stats", &rec, map[string]any{
				"since": 1790000000, "until": 1790003600, "complete": 1, "invalid-lines": 0,
				"total":       map[string]any{"requests": 7, "client-errors": 1},
				"user-agents": map[string]any{"rows": []any{map[string]any{"key": "proxmox-backup-client", "requests": 6}}},
				"clients":     map[string]any{"rows": []any{map[string]any{"key": "10.0.0.9", "requests": 7}}},
				"covered":     []any{}, "gaps": []any{}, "histogram": map[string]any{},
			})

			deps := depsFor(t, pc, format, false)
			var buf bytes.Buffer
			err := run(deps, &buf, newNodeCmd(), "node", "access-log", "stats",
				"--user-agent", "proxmox-backup-client", "--top", "3", "--bucket", "30")
			require.NoError(t, err)

			require.Equal(t, nodeAPIBase+"/access-log/stats", rec.path)
			require.Equal(t, "proxmox-backup-client", rec.query.Get("user-agent"))
			require.Equal(t, "3", rec.query.Get("top"))
			require.Equal(t, "30", rec.query.Get("bucket"))
			require.Empty(t, rec.query.Get("limit"))
			require.Contains(t, buf.String(), "proxmox-backup-client")
		})
	}
}
