package pbs

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
)

var aptFormats = []output.Format{output.FormatTable, output.FormatJSON, output.FormatYAML}

var pbsAptHistory = []any{
	map[string]any{
		"start-time": 1790000000, "start-index": 0, "end-time": 1790000060, "command-line": "apt-get upgrade",
		"packages": []any{
			map[string]any{"action": "upgrade", "package": "proxmox-backup-server", "old-version": "4.0.1", "new-version": "4.0.2"},
		},
	},
}

var pbsAptEntries = []any{
	map[string]any{
		"package": "proxmox-backup-server", "version": "4.0.2", "date": "Mon, 01 Sep 2026 10:00:00 +0000",
		"distributions": "trixie", "urgency": "medium", "changes": "\n  * fix the thing\n  * and another\n",
	},
}

var pbsAptGroups = []any{
	map[string]any{"packages": []any{"proxmox-backup-server", "proxmox-backup-client"}, "entries": pbsAptEntries},
}

func runApt(t *testing.T, format output.Format, path string, payload any, args ...string) (recordedRequest, string, error) {
	t.Helper()
	f, pc := newFakeClient(t)
	var rec recordedRequest
	recordJSON(f, "GET "+nodeAPIBase+"/apt/"+path, &rec, payload)
	deps := depsFor(t, pc, format, false)
	var buf bytes.Buffer
	err := run(deps, &buf, newNodeCmd(), append([]string{"node", "apt", path}, args...)...)
	return rec, buf.String(), err
}

func TestNodeAptHistory(t *testing.T) {
	for _, format := range aptFormats {
		t.Run(string(format), func(t *testing.T) {
			rec, out, err := runApt(t, format, "history", pbsAptHistory,
				"--limit", "5", "--package", "proxmox-backup-server", "--since", "1789000000", "--until", "2026-11-01T00:00:00Z")
			require.NoError(t, err)
			require.Equal(t, http.MethodGet, rec.method)
			require.Equal(t, nodeAPIBase+"/apt/history", rec.path)
			require.Equal(t, "5", rec.query.Get("limit"))
			require.Equal(t, "proxmox-backup-server", rec.query.Get("package"))
			require.Equal(t, "1789000000", rec.query.Get("since"))
			require.Equal(t, "1793491200", rec.query.Get("until"))
			require.Contains(t, out, "apt-get upgrade")
			if format == output.FormatTable {
				require.Contains(t, out, "1 upgrade")
			}
		})
	}
}

func TestNodeAptHistory_RejectsBadTime(t *testing.T) {
	_, _, err := runApt(t, output.FormatTable, "history", pbsAptHistory, "--until", "soon")
	require.ErrorContains(t, err, "--until")
}

func TestNodeAptTermlog(t *testing.T) {
	for _, format := range aptFormats {
		t.Run(string(format), func(t *testing.T) {
			rec, out, err := runApt(t, format, "termlog", "Reading package lists...\nDone\n",
				"--start-time", "1790000000", "--start-index", "2", "--format", "plain")
			require.NoError(t, err)
			require.Equal(t, nodeAPIBase+"/apt/termlog", rec.path)
			require.Equal(t, "1790000000", rec.query.Get("start-time"))
			require.Equal(t, "2", rec.query.Get("start-index"))
			require.Equal(t, "plain", rec.query.Get("format"))
			require.Contains(t, out, "Reading package lists")
		})
	}
}

func TestNodeAptTermlog_Validation(t *testing.T) {
	_, _, err := runApt(t, output.FormatTable, "termlog", "x")
	require.ErrorContains(t, err, "start-time")
	_, _, err = runApt(t, output.FormatTable, "termlog", "x", "--start-time", "1", "--format", "html")
	require.ErrorContains(t, err, "--format")
}

func TestNodeAptInstalledChangelog(t *testing.T) {
	for _, format := range aptFormats {
		t.Run(string(format), func(t *testing.T) {
			rec, out, err := runApt(t, format, "installed-changelog", pbsAptEntries,
				"--name", "proxmox-backup-server", "--since-version", "4.0.0", "--to-version", "4.0.2")
			require.NoError(t, err)
			require.Equal(t, nodeAPIBase+"/apt/installed-changelog", rec.path)
			require.Equal(t, "proxmox-backup-server", rec.query.Get("name"))
			require.Equal(t, "4.0.0", rec.query.Get("since-version"))
			require.Equal(t, "4.0.2", rec.query.Get("to-version"))
			require.Contains(t, out, "4.0.2")
			if format == output.FormatTable {
				require.Contains(t, out, "* fix the thing")
				require.NotContains(t, out, "and another")
			}
		})
	}
}

func TestNodeAptInstalledChangelog_RequiresName(t *testing.T) {
	_, _, err := runApt(t, output.FormatTable, "installed-changelog", pbsAptEntries)
	require.ErrorContains(t, err, "name")
}

func TestNodeAptTransactionChangelogs(t *testing.T) {
	for _, format := range aptFormats {
		t.Run(string(format), func(t *testing.T) {
			rec, out, err := runApt(t, format, "transaction-changelogs", pbsAptGroups,
				"--start-time", "2026-11-01T00:00:00Z", "--start-index", "1")
			require.NoError(t, err)
			require.Equal(t, nodeAPIBase+"/apt/transaction-changelogs", rec.path)
			require.Equal(t, "1793491200", rec.query.Get("start-time"))
			require.Equal(t, "1", rec.query.Get("start-index"))
			require.Contains(t, out, "proxmox-backup-server")
		})
	}
}

func TestNodeAptUpdateChangelogs(t *testing.T) {
	for _, format := range aptFormats {
		t.Run(string(format), func(t *testing.T) {
			rec, out, err := runApt(t, format, "update-changelogs", pbsAptGroups)
			require.NoError(t, err)
			require.Equal(t, nodeAPIBase+"/apt/update-changelogs", rec.path)
			require.Contains(t, out, "proxmox-backup-server")
		})
	}
}
