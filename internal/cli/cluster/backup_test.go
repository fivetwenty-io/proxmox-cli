package cluster

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/testhelper"
)

// TestBackupList_Table verifies `pmx cluster backup list` queries GET
// /cluster/backup and renders the curated columns.
func TestBackupList_Table(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	f.HandleFunc("GET /api2/json/cluster/backup", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		testhelper.WriteData(w, []any{
			map[string]any{
				"id": "backup-pmxcli", "schedule": "02:30", "storage": "local",
				"mode": "snapshot", "enabled": 1, "vmid": "100,101", "comment": "nightly",
			},
			map[string]any{
				"id": "backup-all", "schedule": "sat 03:00", "storage": "pbs",
				"mode": "snapshot", "enabled": 0, "all": 1,
			},
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "backup", "list"))

	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "/api2/json/cluster/backup", gotPath)

	out := buf.String()
	require.Contains(t, out, "SCHEDULE")
	require.Contains(t, out, "backup-pmxcli")
	require.Contains(t, out, "02:30")
	require.Contains(t, out, "100,101")
	require.Contains(t, out, "nightly")
	// enabled=0 renders "no"; the all-scoped job renders "all" in the VMID column.
	require.Contains(t, out, "all")
	require.Contains(t, out, "no")
}

// TestBackupGet_Single verifies `pmx cluster backup get <id>` reads the per-job
// path and surfaces the job fields.
func TestBackupGet_Single(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotPath string
	f.HandleFunc("GET /api2/json/cluster/backup/backup-pmxcli", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		testhelper.WriteData(w, map[string]any{
			"id": "backup-pmxcli", "schedule": "02:30", "storage": "local", "mode": "snapshot",
		})
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "backup", "get", "backup-pmxcli"))

	require.Equal(t, "/api2/json/cluster/backup/backup-pmxcli", gotPath)
	out := buf.String()
	require.Contains(t, out, "backup-pmxcli")
	require.Contains(t, out, "02:30")
}

// TestBackupCreate_PostsFields verifies `pmx cluster backup create` POSTs the
// job attributes, including a caller-supplied id.
func TestBackupCreate_PostsFields(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod string
	var gotForm url.Values
	f.HandleFunc("POST /api2/json/cluster/backup", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "backup", "create",
		"--id", "backup-pmxcli", "--schedule", "02:30", "--storage", "local",
		"--vmid", "100", "--mode", "snapshot"))

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "backup-pmxcli", gotForm.Get("id"))
	require.Equal(t, "02:30", gotForm.Get("schedule"))
	require.Equal(t, "local", gotForm.Get("storage"))
	require.Equal(t, "100", gotForm.Get("vmid"))
	require.Equal(t, "snapshot", gotForm.Get("mode"))
	require.Contains(t, buf.String(), "backup-pmxcli")
}

// TestBackupSet_PutsChangedFields verifies `pmx cluster backup set <id>` issues a
// PUT carrying only the changed fields plus --delete.
func TestBackupSet_PutsChangedFields(t *testing.T) {
	f, ac := newFakeClient(t)

	var gotMethod, gotPath string
	var gotForm url.Values
	f.HandleFunc("PUT /api2/json/cluster/backup/backup-pmxcli", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = r.ParseForm()
		gotForm = r.Form
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.NoError(t, run(deps, &buf, "backup", "set", "backup-pmxcli",
		"--schedule", "04:00", "--delete", "comment"))

	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/api2/json/cluster/backup/backup-pmxcli", gotPath)
	require.Equal(t, "04:00", gotForm.Get("schedule"))
	require.Equal(t, "comment", gotForm.Get("delete"))
	// Unset attributes must not be sent.
	require.Empty(t, gotForm.Get("storage"))
	require.Contains(t, buf.String(), "updated")
}

// TestBackupDelete_RequiresYes verifies the delete guard refuses without --yes
// and issues a DELETE once confirmed.
func TestBackupDelete_RequiresYes(t *testing.T) {
	f, ac := newFakeClient(t)

	var called bool
	f.HandleFunc("DELETE /api2/json/cluster/backup/backup-pmxcli", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		testhelper.WriteData(w, nil)
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	err := run(deps, &buf, "backup", "delete", "backup-pmxcli")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
	require.False(t, called, "delete must not be issued without --yes")

	buf.Reset()
	require.NoError(t, run(deps, &buf, "backup", "delete", "backup-pmxcli", "--yes"))
	require.True(t, called)
	require.Contains(t, buf.String(), "deleted")
}

// TestBackupList_ServerError verifies a server failure on list surfaces an error.
func TestBackupList_ServerError(t *testing.T) {
	f, ac := newFakeClient(t)
	f.HandleFunc("GET /api2/json/cluster/backup", func(w http.ResponseWriter, _ *http.Request) {
		testhelper.WriteError(w, http.StatusInternalServerError, "boom")
	})

	deps := &cli.Deps{API: ac, Out: output.New(), Format: output.FormatTable}

	var buf bytes.Buffer
	require.Error(t, run(deps, &buf, "backup", "list"))
}

// TestBackupCommandTree verifies the backup sub-tree exposes the expected verbs.
func TestBackupCommandTree(t *testing.T) {
	root := Group(&cli.Deps{})
	var backup *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "backup" {
			backup = c
		}
	}
	require.NotNil(t, backup, "cluster must expose a backup sub-command")

	names := make(map[string]bool)
	for _, c := range backup.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"list", "get", "create", "set", "delete"} {
		require.True(t, names[want], "expected backup sub-command %q", want)
	}
	require.False(t, names["info"],
		"backup info was removed: GET /cluster/backup-info is a directory index; use backup-info not-backed-up")
}

// TestClusterBackup_NoLocalTargetFlag guards against shadowing the root's
// persistent -t/--target selector with a local --target anywhere in the cluster
// command tree.
func TestClusterBackup_NoLocalTargetFlag(t *testing.T) {
	root := Group(&cli.Deps{})
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
