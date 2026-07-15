package storage

import (
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestStoragePrune_DryRunListsPreview verifies `--dry-run` queries the
// prunebackups GET endpoint (a preview that deletes nothing) and renders the
// prune decisions.
func TestStoragePrune_DryRunListsPreview(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("GET /api2/json/nodes/pve1/storage/local/prunebackups", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		_ = r.ParseForm()
		rec.form = r.Form
		testhelper.WriteData(w, []map[string]any{
			{"volid": "local:backup/vzdump-qemu-100-2026.tar.zst", "mark": "keep", "type": "qemu", "vmid": 100, "ctime": 1700000000},
			{"volid": "local:backup/vzdump-qemu-100-2025.tar.zst", "mark": "remove", "type": "qemu", "vmid": 100, "ctime": 1690000000},
		})
	})

	out, err := run(t, f, "--node", "pve1", "prune", "local", "--vmid", "100", "--keep-last", "1", "--dry-run")
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/storage/local/prunebackups", rec.path)
	require.Equal(t, "keep-last=1", rec.form.Get("prune-backups"))
	require.Equal(t, "100", rec.form.Get("vmid"))

	require.Contains(t, out, "MARK")
	require.Contains(t, out, "keep")
	require.Contains(t, out, "remove")
	require.Contains(t, out, "vzdump-qemu-100-2026")
}

// TestStoragePrune_DeletesWithConfirmation verifies the real prune issues a
// DELETE with the assembled retention string once --yes is given.
func TestStoragePrune_DeletesWithConfirmation(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("DELETE /api2/json/nodes/pve1/storage/local/prunebackups", func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.Path
		_ = r.ParseForm()
		rec.form = r.Form
		testhelper.WriteData(w, []map[string]any{
			{"volid": "local:backup/vzdump-qemu-100-2025.tar.zst", "mark": "remove", "type": "qemu", "vmid": 100},
		})
	})

	out, err := run(t, f, "--node", "pve1", "prune", "local", "--vmid", "100", "--keep-last", "1", "--yes")
	require.NoError(t, err)

	require.Equal(t, http.MethodDelete, rec.method)
	require.Equal(t, "/api2/json/nodes/pve1/storage/local/prunebackups", rec.path)
	require.Equal(t, "keep-last=1", rec.form.Get("prune-backups"))
	require.Equal(t, "100", rec.form.Get("vmid"))
	require.Contains(t, out, "remove")
}

// TestStoragePrune_RequiresYes verifies the destructive prune refuses to run
// without --yes (and without --dry-run).
func TestStoragePrune_RequiresYes(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var called bool
	f.HandleFunc("DELETE /api2/json/nodes/pve1/storage/local/prunebackups", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, []any{})
	})

	_, err := run(t, f, "--node", "pve1", "prune", "local", "--keep-last", "1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "prune must not issue a DELETE without --yes")
}

// TestStoragePrune_RequiresRetention verifies prune refuses when no retention
// option is set, since the server rejects an empty prune-backups string.
func TestStoragePrune_RequiresRetention(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	_, err := run(t, f, "--node", "pve1", "prune", "local", "--yes")
	require.Error(t, err)
	require.Contains(t, err.Error(), "retention")
}

// TestStoragePrune_KeepAll verifies --keep-all assembles the keep-all retention
// directive.
func TestStoragePrune_KeepAll(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("GET /api2/json/nodes/pve1/storage/local/prunebackups", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		rec.form = r.Form
		testhelper.WriteData(w, []any{})
	})

	_, err := run(t, f, "--node", "pve1", "prune", "local", "--keep-all", "--dry-run")
	require.NoError(t, err)
	require.Equal(t, "keep-all=1", rec.form.Get("prune-backups"))
}

// TestStoragePrune_KeepLastZero verifies that an explicit --keep-last 0 is
// forwarded as keep-last=0 (prune everything) rather than being dropped as an
// unset zero value — the command distinguishes unset from explicit 0.
func TestStoragePrune_KeepLastZero(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	var rec recordedRequest
	f.HandleFunc("GET /api2/json/nodes/pve1/storage/local/prunebackups", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		rec.form = r.Form
		testhelper.WriteData(w, []any{})
	})

	_, err := run(t, f, "--node", "pve1", "prune", "local", "--vmid", "100", "--keep-last", "0", "--dry-run")
	require.NoError(t, err)
	require.Equal(t, "keep-last=0", rec.form.Get("prune-backups"))
}

// TestStoragePrune_NullResult verifies a DELETE that prunes nothing (server
// replies with a null data field) is normalised to an empty result rather than
// erroring, and renders as a header-only table with zero data rows — the real
// render path (rawMessageToEntries -> renderPrune) emits no synthetic "nothing
// pruned" message, since the table renderer falls back to headers whenever
// res.Headers is set, even with zero rows.
func TestStoragePrune_NullResult(t *testing.T) {
	f := testhelper.NewFakePVE(t)
	f.HandleFunc("DELETE /api2/json/nodes/pve1/storage/local/prunebackups", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteData(w, nil)
	})

	out, err := run(t, f, "--node", "pve1", "prune", "local", "--vmid", "100", "--keep-last", "1", "--yes")
	require.NoError(t, err)

	require.Contains(t, out, "VOLID")
	require.Contains(t, out, "MARK")
	require.Contains(t, out, "TYPE")
	require.Contains(t, out, "VMID")
	require.Contains(t, out, "CTIME")

	// tablewriter only draws a header/body separator ("├...┤") when at least one
	// data row follows the header, and only emits a "│"-bordered content line per
	// row. A null result must produce neither: exactly one "│" line (the header)
	// and no "├" separator, proving zero data rows were rendered.
	var contentLines, separatorLines int
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "│") {
			contentLines++
		}
		if strings.Contains(line, "├") {
			separatorLines++
		}
	}
	require.Equal(t, 1, contentLines, "null result must render only the header row, zero data rows: %q", out)
	require.Zero(t, separatorLines, "null result must omit the header/body separator emitted only when data rows exist: %q", out)
}

// TestStoragePrune_RequiresNode verifies prune fails clearly without a node.
func TestStoragePrune_RequiresNode(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "prune without node", args: []string{"prune", "local", "--keep-last", "1", "--dry-run"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := testhelper.NewFakePVE(t)
			_, err := run(t, f, tc.args...)
			require.Error(t, err)
			require.Contains(t, err.Error(), "no node specified")
		})
	}
}

// TestStoragePrune_InTree verifies the prune command is registered on the storage
// group.
func TestStoragePrune_InTree(t *testing.T) {
	root := Group(nil)
	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["prune"], "storage must expose a prune sub-command")
}

// TestStoragePrune_NoLocalTargetFlag guards against shadowing the root's
// persistent -t/--target selector with a local --target anywhere in the storage
// command tree.
func TestStoragePrune_NoLocalTargetFlag(t *testing.T) {
	root := Group(nil)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		require.Nil(t, c.Flags().Lookup("target"),
			"command %q must not define a local --target (collides with root -t/--target)", c.CommandPath())
		require.Nil(t, c.Flags().Lookup("node"),
			"command %q must not define a local --node (collides with root --node)", c.CommandPath())
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(root)
}
