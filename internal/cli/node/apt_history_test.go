package node_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

var aptHistoryPayload = []any{
	map[string]any{
		"start-time": 1790000000, "start-index": 0, "end-time": 1790000060,
		"command-line": "apt-get dist-upgrade", "requested-by": "root@pam",
		"packages": []any{
			map[string]any{"action": "upgrade", "package": "pve-manager", "old-version": "9.0.1", "new-version": "9.0.2"},
			map[string]any{"action": "upgrade", "package": "pve-kernel", "old-version": "1", "new-version": "2"},
			map[string]any{"action": "install", "package": "htop", "new-version": "3"},
		},
	},
}

var aptEntries = []any{
	map[string]any{
		"package": "pve-manager", "version": "9.0.2", "date": "Mon, 01 Sep 2026 10:00:00 +0000",
		"distributions": "trixie", "urgency": "medium", "maintainer": "Proxmox <a@b>",
		"changes": "\n  * fix the thing\n  * and another\n",
	},
}

var aptGroups = []any{
	map[string]any{"packages": []any{"pve-manager", "libpve-common-perl"}, "entries": aptEntries},
}

// TestNodeAptHistory verifies the history reads its endpoint, forwards each
// filter under the API's name, and renders in table, json, and yaml.
func TestNodeAptHistory(t *testing.T) {
	for _, format := range sanFormats {
		t.Run(string(format), func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec recordedRequest
			recordForm(f, "GET /api2/json/nodes/pve1/apt/history", &rec, aptHistoryPayload)

			root, buf, prefix := newNodeRoot(t, f, format, exec.Fake())
			root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "history",
				"--limit", "5", "--package", "pve-manager", "--since", "1789000000", "--until", "2026-11-01T00:00:00Z"))
			require.NoError(t, root.Execute())

			require.Equal(t, http.MethodGet, rec.method)
			require.Equal(t, "/api2/json/nodes/pve1/apt/history", rec.path)
			for _, want := range []string{"limit=5", "package=pve-manager", "since=1789000000", "until=1793491200"} {
				require.Contains(t, rec.query, want)
			}
			out := buf.String()
			require.Contains(t, out, "apt-get dist-upgrade")
			if format == output.FormatTable {
				require.Contains(t, out, "1 install, 2 upgrade")
			} else {
				require.Contains(t, out, "pve-manager")
			}
		})
	}
}

func TestNodeAptHistory_RejectsBadTime(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "history", "--since", "yesterday"))
	err := root.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "--since")
}

// TestNodeAptTermlog verifies the terminal log prints as plain text, and that
// json and yaml carry the text.
func TestNodeAptTermlog(t *testing.T) {
	for _, format := range sanFormats {
		t.Run(string(format), func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec recordedRequest
			recordForm(f, "GET /api2/json/nodes/pve1/apt/termlog", &rec, "Reading package lists...\nDone\n")

			root, buf, prefix := newNodeRoot(t, f, format, exec.Fake())
			root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "termlog",
				"--start-time", "1790000000", "--start-index", "2", "--format", "styled"))
			require.NoError(t, root.Execute())

			require.Equal(t, "/api2/json/nodes/pve1/apt/termlog", rec.path)
			for _, want := range []string{"start-time=1790000000", "start-index=2", "format=styled"} {
				require.Contains(t, rec.query, want)
			}
			require.Contains(t, buf.String(), "Reading package lists")
		})
	}
}

func TestNodeAptTermlog_Validation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "termlog"))
	require.ErrorContains(t, root.Execute(), "start-time")

	root, _, prefix = newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "termlog", "--start-time", "1", "--format", "html"))
	require.ErrorContains(t, root.Execute(), "--format")
}

// TestNodeAptInstalledChangelog verifies the entries table and the filters.
func TestNodeAptInstalledChangelog(t *testing.T) {
	for _, format := range sanFormats {
		t.Run(string(format), func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec recordedRequest
			recordForm(f, "GET /api2/json/nodes/pve1/apt/installed-changelog", &rec, aptEntries)

			root, buf, prefix := newNodeRoot(t, f, format, exec.Fake())
			root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "installed-changelog",
				"--name", "pve-manager", "--since-version", "9.0.0", "--to-version", "9.0.2"))
			require.NoError(t, root.Execute())

			require.Equal(t, "/api2/json/nodes/pve1/apt/installed-changelog", rec.path)
			for _, want := range []string{"name=pve-manager", "since-version=9.0.0", "to-version=9.0.2"} {
				require.Contains(t, rec.query, want)
			}
			require.Contains(t, buf.String(), "9.0.2")
			if format == output.FormatTable {
				require.Contains(t, buf.String(), "* fix the thing")
				require.NotContains(t, buf.String(), "and another")
			}
		})
	}
}

func TestNodeAptInstalledChangelog_RequiresName(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	root, _, prefix := newNodeRoot(t, f, output.FormatTable, exec.Fake())
	root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "installed-changelog"))
	require.ErrorContains(t, root.Execute(), "name")
}

// TestNodeAptTransactionChangelogs verifies the grouped changelogs.
func TestNodeAptTransactionChangelogs(t *testing.T) {
	for _, format := range sanFormats {
		t.Run(string(format), func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec recordedRequest
			recordForm(f, "GET /api2/json/nodes/pve1/apt/transaction-changelogs", &rec, aptGroups)

			root, buf, prefix := newNodeRoot(t, f, format, exec.Fake())
			root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "transaction-changelogs",
				"--start-time", "2026-11-01T00:00:00Z", "--start-index", "1"))
			require.NoError(t, root.Execute())

			require.Equal(t, "/api2/json/nodes/pve1/apt/transaction-changelogs", rec.path)
			for _, want := range []string{"start-time=1793491200", "start-index=1"} {
				require.Contains(t, rec.query, want)
			}
			require.Contains(t, buf.String(), "pve-manager")
			if format == output.FormatTable {
				require.Contains(t, buf.String(), "pve-manager,libpve-common-perl")
			}
		})
	}
}

// TestNodeAptUpdateChangelogs verifies the pending-update changelogs.
func TestNodeAptUpdateChangelogs(t *testing.T) {
	for _, format := range sanFormats {
		t.Run(string(format), func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			var rec recordedRequest
			recordForm(f, "GET /api2/json/nodes/pve1/apt/update-changelogs", &rec, aptGroups)

			root, buf, prefix := newNodeRoot(t, f, format, exec.Fake())
			root.SetArgs(append(prefix, "--node", "pve1", "node", "apt", "update-changelogs"))
			require.NoError(t, root.Execute())

			require.Equal(t, "/api2/json/nodes/pve1/apt/update-changelogs", rec.path)
			require.Contains(t, buf.String(), "pve-manager")
		})
	}
}
