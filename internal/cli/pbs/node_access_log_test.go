package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

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
	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, newNodeCmd(), "node", "access-log"))
	require.Contains(t, buf.String(), "USER-AGENT")
	require.Contains(t, buf.String(), "--cursor cursor-2")
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
